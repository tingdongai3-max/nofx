package market

import (
	"math"
	"nofx/store"
	"strings"
	"sync"
	"time"
)

const (
	DefaultWeightSocial      = 0.3
	DefaultWeightOnChain     = 0.3
	DefaultWeightMarket      = 0.1
	DefaultWeightTrend       = 0.1
	DefaultWeightVolumeSpike = 0.1
	DefaultWeightQuant       = 0.1
	DefaultNestedWeightCore  = 0.5
	DefaultNestedWeightSub   = 0.5

	AdaptiveSampleTarget   = 2000
	adaptiveCacheTTL       = 1 * time.Minute
	adaptiveSectorLimit    = 3000
	adaptiveCoinLimit      = AdaptiveSampleTarget
	ICHalfLife             = 1000.0
	adaptiveScopeGlobal    = "global"
	adaptiveSectorFallback = "Unclassified"
	empiricalWeightFloor   = 0.1
	empiricalWeightCeiling = 0.9
)

var adaptiveFactorOrder = []string{
	"market",
	"trend",
	"donchian_factor",
	"volume_spike",
	"mtf_resonance",
	"quant",
	"social",
	"onchain",
}

var adaptiveVisibleFactorOrder = []string{
	"market",
	"trend",
	"volume_spike",
	"quant",
	"social",
	"onchain",
}

var adaptiveHiddenFactorOrder = []string{
	"donchian_factor",
	"mtf_resonance",
}

var adaptiveNestedFactorGroups = map[string][]string{
	"trend":        []string{"trend", "donchian_factor"},
	"volume_spike": []string{"volume_spike", "mtf_resonance"},
}

type AdaptiveFactorState struct {
	Name            string  `json:"name"`
	IC              float64 `json:"ic"`
	SectorIC        float64 `json:"sector_ic"`
	CoinIC          float64 `json:"coin_ic"`
	FinalIC         float64 `json:"final_ic"`
	DefaultWeight   float64 `json:"default_weight"`
	EmpiricalWeight float64 `json:"empirical_weight"`
	FinalWeight     float64 `json:"final_weight"`
}

type AdaptiveWeightState struct {
	TraderID          string                `json:"trader_id,omitempty"`
	Symbol            string                `json:"symbol,omitempty"`
	Sector            string                `json:"sector,omitempty"`
	SampleCount       int                   `json:"sample_count"`
	SectorSampleCount int                   `json:"sector_sample_count"`
	CoinSampleCount   int                   `json:"coin_sample_count"`
	Alpha             float64               `json:"alpha"`
	SampleTarget      int                   `json:"sample_target"`
	BlendDefault      float64               `json:"blend_default"`
	BlendAdaptive     float64               `json:"blend_adaptive"`
	Factors           []AdaptiveFactorState `json:"factors"`
	HiddenFactors     []AdaptiveFactorState `json:"-"`
	NestedWeights     map[string]float64    `json:"-"`
	UpdatedAt         int64                 `json:"updated_at"`
}

type adaptiveCacheEntry struct {
	state     AdaptiveWeightState
	expiresAt time.Time
}

var (
	adaptiveStoreMu sync.RWMutex
	adaptiveStore   *store.Store
	adaptiveCache   sync.Map
)

func SetAdaptiveWeightStore(st *store.Store) {
	adaptiveStoreMu.Lock()
	adaptiveStore = st
	adaptiveStoreMu.Unlock()
	clearAdaptiveWeightCache()
}

func InvalidateAdaptiveWeightScope(traderID string) {
	if traderID == "" {
		clearAdaptiveWeightCache()
		return
	}

	prefix := normalizedAdaptiveTrader(traderID) + "|"
	adaptiveCache.Range(func(key, _ any) bool {
		keyString, ok := key.(string)
		if ok && strings.HasPrefix(keyString, prefix) {
			adaptiveCache.Delete(key)
		}
		return true
	})
}

func GetAdaptiveWeights(traderID, sector, symbol string) map[string]float64 {
	state := GetAdaptiveWeightState(traderID, sector, symbol)
	return adaptiveWeightsFromState(state)
}

func GetAdaptiveWeightState(traderID, sector, symbol string) AdaptiveWeightState {
	cacheKey := buildAdaptiveCacheKey(traderID, sector, symbol)
	if cached, ok := adaptiveCache.Load(cacheKey); ok {
		entry := cached.(adaptiveCacheEntry)
		if time.Now().UTC().Before(entry.expiresAt) {
			return entry.state
		}
		adaptiveCache.Delete(cacheKey)
	}

	state := computeAdaptiveWeightState(traderID, sector, symbol)
	adaptiveCache.Store(cacheKey, adaptiveCacheEntry{
		state:     state,
		expiresAt: time.Now().UTC().Add(adaptiveCacheTTL),
	})
	return state
}

func EWMAPearsonCorrelation(x, y []float64, halfLife float64) float64 {
	if len(x) != len(y) || len(x) < 2 {
		return 0
	}
	if halfLife <= 0 {
		return 0
	}

	filteredX := make([]float64, 0, len(x))
	filteredY := make([]float64, 0, len(y))
	for i := range x {
		if !isFinite(x[i]) || !isFinite(y[i]) {
			continue
		}
		filteredX = append(filteredX, x[i])
		filteredY = append(filteredY, y[i])
	}
	if len(filteredX) < 2 {
		return 0
	}

	weights := make([]float64, len(filteredX))
	weightSum := 0.0
	for i := range filteredX {
		age := float64(len(filteredX)-1-i) / halfLife
		weight := math.Pow(2, -age)
		weights[i] = weight
		weightSum += weight
	}
	if weightSum <= 0 {
		return 0
	}

	meanX := 0.0
	meanY := 0.0
	for i := range filteredX {
		meanX += weights[i] * filteredX[i]
		meanY += weights[i] * filteredY[i]
	}
	meanX /= weightSum
	meanY /= weightSum

	var covariance float64
	var varianceX float64
	var varianceY float64
	for i := range filteredX {
		dx := filteredX[i] - meanX
		dy := filteredY[i] - meanY
		weight := weights[i]
		covariance += weight * dx * dy
		varianceX += weight * dx * dx
		varianceY += weight * dy * dy
	}
	covariance /= weightSum
	varianceX /= weightSum
	varianceY /= weightSum

	if varianceX <= 0 || varianceY <= 0 {
		return 0
	}

	return covariance / math.Sqrt(varianceX*varianceY)
}

func computeAdaptiveWeightState(traderID, sector, symbol string) AdaptiveWeightState {
	defaults := defaultAdaptiveWeights()
	hiddenDefaults := defaultHiddenAdaptiveWeights()
	sectorICs := zeroAdaptiveValues()
	coinICs := zeroAdaptiveValues()
	finalICs := zeroAdaptiveValues()
	empirical := copyWeightMap(defaults)
	finalWeights := copyWeightMap(defaults)
	hiddenEmpirical := copyWeightMap(hiddenDefaults)
	hiddenFinalWeights := copyWeightMap(hiddenDefaults)
	nestedWeights := defaultNestedAdaptiveWeights()

	normalizedTrader := normalizedAdaptiveTrader(traderID)
	normalizedSector := normalizeAdaptiveSector(sector)
	normalizedSymbol := Normalize(symbol)

	state := AdaptiveWeightState{
		TraderID:     normalizedTrader,
		Symbol:       normalizedSymbol,
		Sector:       normalizedSector,
		SampleTarget: AdaptiveSampleTarget,
		UpdatedAt:    time.Now().UTC().UnixMilli(),
	}

	st := getAdaptiveWeightStore()
	if st == nil || normalizedTrader == adaptiveScopeGlobal || normalizedSymbol == "" {
		state.Factors = buildAdaptiveFactorStatesForOrder(adaptiveVisibleFactorOrder, defaults, empirical, finalWeights, sectorICs, coinICs, finalICs)
		state.HiddenFactors = buildAdaptiveFactorStatesForOrder(adaptiveHiddenFactorOrder, hiddenDefaults, hiddenEmpirical, hiddenFinalWeights, sectorICs, coinICs, finalICs)
		state.NestedWeights = nestedWeights
		state.BlendDefault = 1
		return state
	}

	sectorRows, err := st.Shadow().ListFilledForSectorAdaptive(normalizedTrader, normalizedSector, adaptiveSectorLimit)
	if err != nil {
		state.Factors = buildAdaptiveFactorStatesForOrder(adaptiveVisibleFactorOrder, defaults, empirical, finalWeights, sectorICs, coinICs, finalICs)
		state.HiddenFactors = buildAdaptiveFactorStatesForOrder(adaptiveHiddenFactorOrder, hiddenDefaults, hiddenEmpirical, hiddenFinalWeights, sectorICs, coinICs, finalICs)
		state.NestedWeights = nestedWeights
		state.BlendDefault = 1
		return state
	}
	coinRows, err := st.Shadow().ListFilledForCoinAdaptive(normalizedTrader, normalizedSymbol, adaptiveCoinLimit)
	if err != nil {
		state.Factors = buildAdaptiveFactorStatesForOrder(adaptiveVisibleFactorOrder, defaults, empirical, finalWeights, sectorICs, coinICs, finalICs)
		state.HiddenFactors = buildAdaptiveFactorStatesForOrder(adaptiveHiddenFactorOrder, hiddenDefaults, hiddenEmpirical, hiddenFinalWeights, sectorICs, coinICs, finalICs)
		state.NestedWeights = nestedWeights
		state.BlendDefault = 1
		return state
	}

	sectorICs, state.SectorSampleCount = computeICsFromRows(sectorRows)
	coinICs, state.CoinSampleCount = computeICsFromRows(coinRows)
	state.SampleCount = state.SectorSampleCount
	state.Alpha = calculateBayesianShrinkageAlpha(state.CoinSampleCount)
	finalICs = shrinkFactorICs(sectorICs, coinICs, state.Alpha)

	if state.SectorSampleCount >= 2 {
		empirical = buildEmpiricalWeightsForOrder(adaptiveVisibleFactorOrder, defaults, finalICs)
		blendDefault := 0.0
		if state.SectorSampleCount < AdaptiveSampleTarget {
			blendDefault = float64(AdaptiveSampleTarget-state.SectorSampleCount) / float64(AdaptiveSampleTarget)
		}
		state.BlendDefault = clamp(blendDefault, 0, 1)
		state.BlendAdaptive = 1 - state.BlendDefault
		finalWeights = blendWeightMapsForOrder(adaptiveVisibleFactorOrder, defaults, empirical, state.BlendDefault, state.BlendAdaptive)
		finalWeights = normalizeWeightMapForOrder(adaptiveVisibleFactorOrder, finalWeights, defaults)
		nestedWeights, hiddenEmpirical, hiddenFinalWeights = buildNestedWeightState(finalICs, state.BlendDefault, state.BlendAdaptive)
	} else {
		state.BlendDefault = 1
		state.BlendAdaptive = 0
	}

	state.Factors = buildAdaptiveFactorStatesForOrder(adaptiveVisibleFactorOrder, defaults, empirical, finalWeights, sectorICs, coinICs, finalICs)
	state.HiddenFactors = buildAdaptiveFactorStatesForOrder(adaptiveHiddenFactorOrder, hiddenDefaults, hiddenEmpirical, hiddenFinalWeights, sectorICs, coinICs, finalICs)
	state.NestedWeights = nestedWeights
	return state
}

func computeICsFromRows(rows []*store.ShadowSnapshot) (map[string]float64, int) {
	factorSamples, returns := buildAdaptiveSampleSet(rows)
	ics := zeroAdaptiveValues()
	if len(returns) < 2 {
		return ics, len(returns)
	}

	for _, factor := range adaptiveFactorOrder {
		ics[factor] = EWMAPearsonCorrelation(factorSamples[factor], returns, ICHalfLife)
	}
	return ics, len(returns)
}

func buildAdaptiveSampleSet(rows []*store.ShadowSnapshot) (map[string][]float64, []float64) {
	factorSamples := map[string][]float64{
		"market":          {},
		"trend":           {},
		"donchian_factor": {},
		"volume_spike":    {},
		"mtf_resonance":   {},
		"quant":           {},
		"social":          {},
		"onchain":         {},
	}
	returns := make([]float64, 0, len(rows))
	for _, row := range rows {
		if row == nil || !row.Filled || !isFinite(row.ReturnPct) {
			continue
		}
		if row.MarketFactor == 0 &&
			row.TrendFactor == 0 &&
			row.DonchianFactor == 0 &&
			row.VolumeSpikeFactor == 0 &&
			row.MTFResonanceFactor == 0 &&
			row.QuantFactor == 0 &&
			row.SocialFactor == 0 &&
			row.OnChainFactor == 0 {
			continue
		}

		values := map[string]float64{
			"market":          row.MarketFactor,
			"trend":           row.TrendFactor,
			"donchian_factor": row.DonchianFactor,
			"volume_spike":    row.VolumeSpikeFactor,
			"mtf_resonance":   row.MTFResonanceFactor,
			"quant":           row.QuantFactor,
			"social":          row.SocialFactor,
			"onchain":         row.OnChainFactor,
		}
		valid := true
		for _, factor := range adaptiveFactorOrder {
			if !isFinite(values[factor]) {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}

		returns = append(returns, row.ReturnPct)
		for _, factor := range adaptiveFactorOrder {
			factorSamples[factor] = append(factorSamples[factor], values[factor])
		}
	}

	reverseFloat64s(returns)
	for _, factor := range adaptiveFactorOrder {
		reverseFloat64s(factorSamples[factor])
	}

	return factorSamples, returns
}

func reverseFloat64s(values []float64) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

// calculateBayesianShrinkageAlpha stretches the coin-level shrinkage curve so
// alpha only reaches the 0.4 cap after 2000 coin samples.
func calculateBayesianShrinkageAlpha(coinSampleCount int) float64 {
	return math.Min(float64(coinSampleCount)/5000.0, 0.4)
}

func shrinkFactorICs(sectorICs, coinICs map[string]float64, alpha float64) map[string]float64 {
	finalICs := make(map[string]float64, len(adaptiveFactorOrder))
	for _, factor := range adaptiveFactorOrder {
		finalICs[factor] = sectorICs[factor]*(1-alpha) + coinICs[factor]*alpha
	}
	return finalICs
}

func buildEmpiricalWeightsForOrder(order []string, defaults map[string]float64, ics map[string]float64) map[string]float64 {
	empirical := make(map[string]float64, len(defaults))
	positiveICSum := 0.0
	negativeCount := 0

	for _, factor := range order {
		if ics[factor] > 0 {
			positiveICSum += ics[factor]
			continue
		}
		empirical[factor] = empiricalWeightFloor
		negativeCount++
	}

	if positiveICSum <= 0 {
		return copyWeightMap(defaults)
	}

	remainingBudget := 1 - float64(negativeCount)*empiricalWeightFloor
	if remainingBudget <= 0 {
		return copyWeightMap(defaults)
	}

	active := make([]string, 0, len(order))
	for _, factor := range order {
		if ics[factor] > 0 {
			active = append(active, factor)
		}
	}

	remainingIC := positiveICSum
	for len(active) > 0 && remainingBudget > 0 {
		next := make([]string, 0, len(active))
		capped := false
		for _, factor := range active {
			if remainingIC <= 0 {
				break
			}
			tentative := remainingBudget * (ics[factor] / remainingIC)
			if tentative > empiricalWeightCeiling {
				empirical[factor] = empiricalWeightCeiling
				remainingBudget -= empiricalWeightCeiling
				remainingIC -= ics[factor]
				capped = true
				continue
			}
			next = append(next, factor)
		}

		if !capped {
			for _, factor := range active {
				if remainingIC <= 0 {
					empirical[factor] = remainingBudget / float64(len(active))
					continue
				}
				empirical[factor] = remainingBudget * (ics[factor] / remainingIC)
			}
			break
		}
		active = next
	}

	return stabilizeUnitSumForOrder(order, empirical, defaults)
}

func buildAdaptiveFactorStatesForOrder(order []string, defaults, empirical, finalWeights, sectorICs, coinICs, finalICs map[string]float64) []AdaptiveFactorState {
	factors := make([]AdaptiveFactorState, 0, len(order))
	for _, factor := range order {
		factors = append(factors, AdaptiveFactorState{
			Name:            factor,
			IC:              finalICs[factor],
			SectorIC:        sectorICs[factor],
			CoinIC:          coinICs[factor],
			FinalIC:         finalICs[factor],
			DefaultWeight:   defaults[factor],
			EmpiricalWeight: empirical[factor],
			FinalWeight:     finalWeights[factor],
		})
	}
	return factors
}

func adaptiveWeightsFromState(state AdaptiveWeightState) map[string]float64 {
	weights := defaultAdaptiveWeights()
	for _, factor := range state.Factors {
		weights[factor.Name] = factor.FinalWeight
	}
	return weights
}

func adaptiveNestedWeightsFromState(state AdaptiveWeightState) map[string]float64 {
	weights := defaultNestedAdaptiveWeights()
	for key, value := range state.NestedWeights {
		weights[key] = value
	}
	return weights
}

func defaultAdaptiveWeights() map[string]float64 {
	return map[string]float64{
		"market":       DefaultWeightMarket,
		"trend":        DefaultWeightTrend,
		"volume_spike": DefaultWeightVolumeSpike,
		"quant":        DefaultWeightQuant,
		"social":       DefaultWeightSocial,
		"onchain":      DefaultWeightOnChain,
	}
}

func defaultHiddenAdaptiveWeights() map[string]float64 {
	return map[string]float64{
		"donchian_factor": DefaultNestedWeightSub,
		"mtf_resonance":   DefaultNestedWeightSub,
	}
}

func defaultNestedAdaptiveWeights() map[string]float64 {
	return map[string]float64{
		"trend":           DefaultNestedWeightCore,
		"donchian_factor": DefaultNestedWeightSub,
		"volume_spike":    DefaultNestedWeightCore,
		"mtf_resonance":   DefaultNestedWeightSub,
	}
}

func zeroAdaptiveValues() map[string]float64 {
	return map[string]float64{
		"market":          0,
		"trend":           0,
		"donchian_factor": 0,
		"volume_spike":    0,
		"mtf_resonance":   0,
		"quant":           0,
		"social":          0,
		"onchain":         0,
	}
}

func blendWeightMapsForOrder(order []string, defaults, empirical map[string]float64, blendDefault, blendAdaptive float64) map[string]float64 {
	weights := make(map[string]float64, len(defaults))
	for _, factor := range order {
		weights[factor] = defaults[factor]*blendDefault + empirical[factor]*blendAdaptive
	}
	return weights
}

func normalizeWeightMapForOrder(order []string, weights, fallback map[string]float64) map[string]float64 {
	sum := 0.0
	normalized := make(map[string]float64, len(order))
	for _, factor := range order {
		value := weights[factor]
		if value < 0 {
			value = 0
		}
		normalized[factor] = value
		sum += value
	}

	if sum <= 0 {
		return copyWeightMap(fallback)
	}

	for _, factor := range order {
		normalized[factor] /= sum
	}
	return normalized
}

func stabilizeUnitSumForOrder(order []string, weights, fallback map[string]float64) map[string]float64 {
	stable := make(map[string]float64, len(order))
	sum := 0.0
	largestFactor := ""
	largestValue := -1.0
	for _, factor := range order {
		value := weights[factor]
		if value < 0 {
			value = 0
		}
		stable[factor] = value
		sum += value
		if value > largestValue {
			largestValue = value
			largestFactor = factor
		}
	}

	if sum <= 0 || largestFactor == "" {
		return copyWeightMap(fallback)
	}

	stable[largestFactor] += 1 - sum
	if stable[largestFactor] < 0 {
		return normalizeWeightMapForOrder(order, stable, fallback)
	}
	return stable
}

func buildNestedWeightState(finalICs map[string]float64, blendDefault, blendAdaptive float64) (map[string]float64, map[string]float64, map[string]float64) {
	nestedWeights := defaultNestedAdaptiveWeights()
	hiddenDefaults := defaultHiddenAdaptiveWeights()
	hiddenEmpirical := copyWeightMap(hiddenDefaults)
	hiddenFinal := copyWeightMap(hiddenDefaults)

	for parent, order := range adaptiveNestedFactorGroups {
		groupDefaults := defaultNestedGroupWeights(parent)
		groupEmpirical := buildEmpiricalWeightsForOrder(order, groupDefaults, finalICs)
		groupFinal := blendWeightMapsForOrder(order, groupDefaults, groupEmpirical, blendDefault, blendAdaptive)
		groupFinal = normalizeWeightMapForOrder(order, groupFinal, groupDefaults)

		for _, factor := range order {
			nestedWeights[factor] = groupFinal[factor]
			if factor == parent {
				continue
			}
			hiddenEmpirical[factor] = groupEmpirical[factor]
			hiddenFinal[factor] = groupFinal[factor]
		}
	}

	return nestedWeights, hiddenEmpirical, hiddenFinal
}

func defaultNestedGroupWeights(parent string) map[string]float64 {
	group := map[string]float64{
		parent: DefaultNestedWeightCore,
	}
	for _, factor := range adaptiveNestedFactorGroups[parent] {
		if factor == parent {
			continue
		}
		group[factor] = DefaultNestedWeightSub
	}
	return group
}

func copyWeightMap(src map[string]float64) map[string]float64 {
	dst := make(map[string]float64, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func buildAdaptiveCacheKey(traderID, sector, symbol string) string {
	return strings.Join([]string{
		normalizedAdaptiveTrader(traderID),
		normalizeAdaptiveSector(sector),
		Normalize(symbol),
	}, "|")
}

func normalizedAdaptiveTrader(traderID string) string {
	if traderID == "" {
		return adaptiveScopeGlobal
	}
	return traderID
}

func normalizeAdaptiveSector(sector string) string {
	sector = strings.TrimSpace(sector)
	if sector == "" {
		return adaptiveSectorFallback
	}
	return sector
}

func getAdaptiveWeightStore() *store.Store {
	adaptiveStoreMu.RLock()
	defer adaptiveStoreMu.RUnlock()
	return adaptiveStore
}

func clearAdaptiveWeightCache() {
	adaptiveCache.Range(func(key, _ any) bool {
		adaptiveCache.Delete(key)
		return true
	})
}
