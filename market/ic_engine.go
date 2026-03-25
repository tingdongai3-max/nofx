package market

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"nofx/logger"
	"nofx/store"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	ConfidenceThreshold    = 30.0
	ICHalfLife             = 2000.0
	adaptiveSectorFallback = "Unclassified"
	empiricalWeightFloor   = 0.01
	empiricalWeightCeiling = 0.90
)

var adaptiveFactorOrder = []string{
	"market",
	"trend",
	"donchian_factor",
	"volume_spike",
	"mtf_resonance",
	"quant",
	"quant_oi",
	"quant_imbalance",
	"quant_netflow",
	"social",
	"social_rank",
	"social_upvote",
	"onchain",
	"onchain_ratio",
	"onchain_buy_ratio",
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
	"quant_oi",
	"quant_imbalance",
	"quant_netflow",
	"social_rank",
	"social_upvote",
	"onchain_ratio",
	"onchain_buy_ratio",
}

var adaptiveNestedFactorGroups = map[string][]string{
	"trend":        []string{"trend", "donchian_factor"},
	"volume_spike": []string{"volume_spike", "mtf_resonance"},
	"quant":        []string{"quant_oi", "quant_imbalance", "quant_netflow"},
	"social":       []string{"social_rank", "social_upvote"},
	"onchain":      []string{"onchain_ratio", "onchain_buy_ratio"},
}

var adaptiveNestedPriors = map[string]map[string]float64{
	"trend": {
		"trend":           0.5,
		"donchian_factor": 0.5,
	},
	"volume_spike": {
		"volume_spike":  0.5,
		"mtf_resonance": 0.5,
	},
	"quant": {
		"quant_oi":        0.4,
		"quant_imbalance": 0.25,
		"quant_netflow":   0.35,
	},
	"social": {
		"social_rank":   0.7,
		"social_upvote": 0.3,
	},
	"onchain": {
		"onchain_ratio":     0.6,
		"onchain_buy_ratio": 0.4,
	},
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
	HiddenFactors     []AdaptiveFactorState `json:"hidden_factors"`
	NestedWeights     map[string]float64    `json:"nested_weights"`
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

func init() {
	store.RegisterCacheRefreshHook(func() error {
		clearAdaptiveWeightCache()
		return nil
	})
}

func SetAdaptiveWeightStore(st *store.Store) {
	adaptiveStoreMu.Lock()
	adaptiveStore = st
	adaptiveStoreMu.Unlock()
	clearAdaptiveWeightCache()
}

func InvalidateAdaptiveWeightScope(traderID string) {
	clearAdaptiveWeightCache()
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

func RecalculateHistoricalScores(rows []*store.ShadowSnapshot, state AdaptiveWeightState) map[uint]float64 {
	recalculated := make(map[uint]float64, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		score, ok := recalculateHistoricalScore(row, state)
		if !ok {
			continue
		}
		recalculated[row.ID] = score
	}
	return recalculated
}

func RecalculateHistoricalBins(rows []*store.ShadowSnapshot, state AdaptiveWeightState) []*store.ScoreBinPerformance {
	return RecalculateHistoricalBinsWithWindow(rows, state, store.PERFORMANCE_WINDOW_SIZE_DEFAULT)
}

// RecalculateHistoricalFactors keeps the historical recalc entry point explicit at the
// call sites that need a sector-aware rebuild before bin aggregation.
func RecalculateHistoricalFactors(
	rows []*store.ShadowSnapshot,
	state AdaptiveWeightState,
	windowSize int,
	filters ...func(*store.ShadowSnapshot, float64) bool,
) []*store.ScoreBinPerformance {
	return RecalculateHistoricalBinsWithWindow(rows, state, windowSize, filters...)
}

func RecalculateHistoricalBinsWithWindow(
	rows []*store.ShadowSnapshot,
	state AdaptiveWeightState,
	windowSize int,
	filters ...func(*store.ShadowSnapshot, float64) bool,
) []*store.ScoreBinPerformance {
	var filter func(*store.ShadowSnapshot, float64) bool
	if len(filters) > 0 {
		filter = filters[0]
	}

	recalculated := RecalculateHistoricalScores(rows, state)
	return store.AggregateSmoothedPerformanceBins(rows, func(row *store.ShadowSnapshot) (float64, bool) {
		if row == nil {
			return 0, false
		}
		score, ok := recalculated[row.ID]
		if ok && filter != nil && !filter(row, score) {
			return 0, false
		}
		return score, ok
	}, windowSize)
}

func recalculateHistoricalScore(row *store.ShadowSnapshot, state AdaptiveWeightState) (float64, bool) {
	rawFactors := decodeHistoricalRawFactors(row)
	if len(rawFactors.Scores) == 0 {
		return 0, false
	}

	weights := adaptiveWeightsFromState(state)
	nestedWeights := adaptiveNestedWeightsFromState(state)
	factorStates := adaptiveFactorStatesByName(state.Factors, state.HiddenFactors)

	marketZ, marketAvailable := shadowRawFactorZ(rawFactors, "market")
	trendGroupZ, trendAvailable := recalculateNestedGroupZ(row.Symbol, rawFactors, factorStates, nestedWeights, "trend_group", []string{"trend", "donchian_factor"})
	volumeSpikeGroupZ, volumeSpikeAvailable := recalculateNestedGroupZ(row.Symbol, rawFactors, factorStates, nestedWeights, "volume_spike_group", []string{"volume_spike", "mtf_resonance"})
	quantGroupZ, quantAvailable := recalculateNestedGroupZ(row.Symbol, rawFactors, factorStates, nestedWeights, "quant_group", []string{"quant_oi", "quant_imbalance", "quant_netflow"})
	socialGroupZ, socialAvailable := recalculateNestedGroupZ(row.Symbol, rawFactors, factorStates, nestedWeights, "social_group", []string{"social_rank", "social_upvote"})
	onChainGroupZ, onChainAvailable := recalculateNestedGroupZ(row.Symbol, rawFactors, factorStates, nestedWeights, "onchain_group", []string{"onchain_ratio", "onchain_buy_ratio"})

	tradingWeights := map[string]float64{}
	tradingScores := map[string]float64{}
	tradingWeight := 0.0
	if marketAvailable {
		tradingWeights["market"] = weights["market"]
		tradingScores["market"] = adjustedZScoreForAdaptiveFactor(row.Symbol, "market", marketZ, factorStates)
		tradingWeight += weights["market"]
	}
	if trendAvailable {
		tradingWeights["trend"] = weights["trend"]
		tradingScores["trend"] = adjustedZScoreForAdaptiveFactor(row.Symbol, "trend", trendGroupZ, factorStates)
		tradingWeight += weights["trend"]
	}
	if volumeSpikeAvailable {
		tradingWeights["volume_spike"] = weights["volume_spike"]
		tradingScores["volume_spike"] = adjustedZScoreForAdaptiveFactor(row.Symbol, "volume_spike", volumeSpikeGroupZ, factorStates)
		tradingWeight += weights["volume_spike"]
	}
	if tradingWeight <= 0 {
		return 0, false
	}

	tradingScore := combineWeightedScores(tradingWeights, tradingScores)

	quantWeights := map[string]float64{}
	quantScores := map[string]float64{}
	quantWeightTotal := 0.0
	if quantAvailable {
		quantWeights["quant"] = weights["quant"]
		quantScores["quant"] = adjustedZScoreForAdaptiveFactor(row.Symbol, "quant", quantGroupZ, factorStates)
		quantWeightTotal += weights["quant"]
	}
	if onChainAvailable {
		quantWeights["onchain"] = weights["onchain"]
		quantScores["onchain"] = adjustedZScoreForAdaptiveFactor(row.Symbol, "onchain", onChainGroupZ, factorStates)
		quantWeightTotal += weights["onchain"]
	}
	if socialAvailable {
		quantWeights["social"] = weights["social"]
		quantScores["social"] = adjustedZScoreForAdaptiveFactor(row.Symbol, "social", socialGroupZ, factorStates)
		quantWeightTotal += weights["social"]
	}

	if quantWeightTotal <= 0 {
		return clamp(tradingScore, 0, 100), true
	}

	quantScore := combineWeightedScores(quantWeights, quantScores)
	totalWeight := tradingWeight + quantWeightTotal
	if totalWeight <= 0 {
		return 0, false
	}

	composite := (tradingScore*tradingWeight + quantScore*quantWeightTotal) / totalWeight
	return clamp(composite, 0, 100), true
}

func recalculateNestedGroupZ(
	symbol string,
	rawFactors store.ShadowRawFactors,
	factorStates map[string]AdaptiveFactorState,
	nestedWeights map[string]float64,
	fallbackKey string,
	order []string,
) (float64, bool) {
	available := make(map[string]bool, len(order))
	zScores := make(map[string]float64, len(order))
	hasAvailable := false

	for _, factor := range order {
		value, ok := shadowRawFactorZ(rawFactors, factor)
		available[factor] = ok
		if !ok {
			continue
		}
		hasAvailable = true
		zScores[factor] = adjustedZScoreForAdaptiveFactor(symbol, factor, value, factorStates)
	}

	if hasAvailable {
		groupWeights := normalizedNestedSubweights(nestedWeights, order, available)
		return combineWeightedZScores(groupWeights, zScores), true
	}

	return shadowRawFactorZ(rawFactors, fallbackKey)
}

func decodeHistoricalRawFactors(row *store.ShadowSnapshot) store.ShadowRawFactors {
	if row == nil {
		return store.ShadowRawFactors{}
	}

	rawFactors, err := row.DecodedRawFactors()
	if err == nil && len(rawFactors.Scores) > 0 {
		return rawFactors
	}
	return recoverLegacyRawFactors(row)
}

func recoverLegacyRawFactors(row *store.ShadowSnapshot) store.ShadowRawFactors {
	if row == nil {
		return store.ShadowRawFactors{}
	}

	hasAnyTelemetry := row.MarketFactor != 0 ||
		row.TrendFactor != 0 ||
		row.VolumeSpikeFactor != 0 ||
		row.QuantFactor != 0 ||
		row.SocialFactor != 0 ||
		row.OnChainFactor != 0 ||
		row.VolUtilization != 0 ||
		row.FundingRate != 0 ||
		row.QuantImbalanceRaw != 0
	if !hasAnyTelemetry {
		return store.ShadowRawFactors{}
	}

	scores := map[string]float64{
		"market":              percentScoreToZ(row.MarketFactor),
		"trend":               percentScoreToZ(row.TrendFactor),
		"trend_group":         percentScoreToZ(row.TrendFactor),
		"donchian_factor":     percentScoreToZ(row.DonchianFactor),
		"volume_spike":        percentScoreToZ(row.VolumeSpikeFactor),
		"volume_spike_group":  percentScoreToZ(row.VolumeSpikeFactor),
		"mtf_resonance":       percentScoreToZ(row.MTFResonanceFactor),
		"quant_group":         percentScoreToZ(row.QuantFactor),
		"social_group":        percentScoreToZ(row.SocialFactor),
		"onchain_group":       percentScoreToZ(row.OnChainFactor),
		"onchain_ratio":       percentScoreToZ(row.OnChainFactor),
		"onchain_buy_ratio":   percentScoreToZ(row.OnChainFactor),
		"vol_utilization":     row.VolUtilization * 100,
		"funding_rate":        row.FundingRate * 10000,
		"orderbook_imbalance": row.QuantImbalanceRaw * 100,
	}

	available := map[string]bool{
		"market":              true,
		"trend":               true,
		"trend_group":         true,
		"donchian_factor":     row.DonchianFactor != 0,
		"volume_spike":        row.VolumeSpikeFactor != 0,
		"volume_spike_group":  row.VolumeSpikeFactor != 0 || row.MTFResonanceFactor != 0,
		"mtf_resonance":       row.MTFResonanceFactor != 0,
		"quant_group":         row.QuantFactor != 0,
		"social_group":        row.SocialFactor != 0,
		"onchain_group":       row.OnChainFactor != 0,
		"onchain_ratio":       row.OnChainFactor != 0,
		"onchain_buy_ratio":   row.OnChainFactor != 0,
		"vol_utilization":     row.VolUtilization > 0,
		"funding_rate":        row.FundingRate != 0,
		"orderbook_imbalance": row.QuantImbalanceRaw != 0,
	}

	return store.ShadowRawFactors{
		Scores:    scores,
		Available: available,
	}
}

func shadowRawFactorZ(rawFactors store.ShadowRawFactors, name string) (float64, bool) {
	value, exists := rawFactors.Scores[name]
	if !exists || !isFinite(value) {
		return 0, false
	}

	if len(rawFactors.Available) == 0 {
		return value, true
	}
	available, ok := rawFactors.Available[name]
	if !ok {
		return value, true
	}
	return value, available
}

func percentScoreToZ(score float64) float64 {
	if !isFinite(score) {
		return 0
	}
	normalized := (clamp(score, 0, 100) - 50) / 50
	normalized = clamp(normalized, -0.999999, 0.999999)
	return 2 * math.Atanh(normalized)
}

// RankData converts raw numeric values into 1-based average ranks so tied values
// share the mean rank across the tied span.
func RankData(data []float64) []float64 {
	ranks := make([]float64, len(data))
	if len(data) == 0 {
		return ranks
	}

	type rankPoint struct {
		index int
		value float64
	}

	points := make([]rankPoint, 0, len(data))
	for index, value := range data {
		points = append(points, rankPoint{
			index: index,
			value: value,
		})
	}

	sort.Slice(points, func(i, j int) bool {
		if points[i].value == points[j].value {
			return points[i].index < points[j].index
		}
		return points[i].value < points[j].value
	})

	for start := 0; start < len(points); {
		end := start + 1
		for end < len(points) && points[end].value == points[start].value {
			end++
		}

		averageRank := (float64(start+1) + float64(end)) / 2.0
		for index := start; index < end; index++ {
			ranks[points[index].index] = averageRank
		}
		start = end
	}

	return ranks
}

func EWMAPearsonCorrelation(x, y []float64, halfLife float64) float64 {
	filteredX, filteredY := filterFiniteCorrelationPairs(x, y)
	return ewmaWeightedPearson(filteredX, filteredY, halfLife)
}

// EWMASpearmanCorrelation computes Spearman rank IC by ranking both inputs first
// and then applying the EWMA-weighted Pearson correlation over the ranked series.
func EWMASpearmanCorrelation(x, y []float64, halfLife float64) float64 {
	filteredX, filteredY := filterFiniteCorrelationPairs(x, y)
	if len(filteredX) < 2 {
		return 0
	}

	rankedX := RankData(filteredX)
	rankedY := RankData(filteredY)
	return ewmaWeightedPearson(rankedX, rankedY, halfLife)
}

// TemporalWeightedSpearmanCorrelation computes Spearman rank IC using
// DecisionTime-derived exponential decay so recent observations dominate.
func TemporalWeightedSpearmanCorrelation(x, y []float64, decisionTimes []int64, halfLife float64) float64 {
	filteredX, filteredY, filteredTimes := filterFiniteTemporalCorrelationTriples(x, y, decisionTimes)
	if len(filteredX) < 2 {
		return 0
	}

	rankedX := RankData(filteredX)
	rankedY := RankData(filteredY)
	return temporalWeightedPearson(rankedX, rankedY, filteredTimes, halfLife)
}

func filterFiniteCorrelationPairs(x, y []float64) ([]float64, []float64) {
	if len(x) != len(y) || len(x) < 2 {
		return nil, nil
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
	return filteredX, filteredY
}

func filterFiniteTemporalCorrelationTriples(x, y []float64, decisionTimes []int64) ([]float64, []float64, []int64) {
	if len(x) != len(y) || len(x) != len(decisionTimes) || len(x) < 2 {
		return nil, nil, nil
	}

	filteredX := make([]float64, 0, len(x))
	filteredY := make([]float64, 0, len(y))
	filteredTimes := make([]int64, 0, len(x))
	for i := range x {
		if !isFinite(x[i]) || !isFinite(y[i]) {
			continue
		}
		filteredX = append(filteredX, x[i])
		filteredY = append(filteredY, y[i])
		filteredTimes = append(filteredTimes, decisionTimes[i])
	}
	return filteredX, filteredY, filteredTimes
}

func ewmaWeightedPearson(x, y []float64, halfLife float64) float64 {
	if len(x) != len(y) || len(x) < 2 {
		return 0
	}
	return weightedPearson(x, y, ewmaIndexWeights(len(x), halfLife))
}

func temporalWeightedPearson(x, y []float64, decisionTimes []int64, halfLife float64) float64 {
	if len(x) != len(y) || len(x) != len(decisionTimes) || len(x) < 2 {
		return 0
	}
	return weightedPearson(x, y, temporalDecayWeights(decisionTimes, halfLife))
}

func weightedPearson(x, y, weights []float64) float64 {
	if len(x) != len(y) || len(x) != len(weights) || len(x) < 2 {
		return 0
	}

	validWeights := make([]float64, len(weights))
	weightSum := 0.0
	validCount := 0
	for i, weight := range weights {
		if !isFinite(weight) || weight <= 0 {
			continue
		}
		validWeights[i] = weight
		weightSum += weight
		validCount++
	}
	if weightSum <= 0 || validCount < 2 {
		return 0
	}

	meanX := 0.0
	meanY := 0.0
	for i := range x {
		weight := validWeights[i]
		if weight <= 0 {
			continue
		}
		meanX += weight * x[i]
		meanY += weight * y[i]
	}
	meanX /= weightSum
	meanY /= weightSum

	var covariance float64
	var varianceX float64
	var varianceY float64
	for i := range x {
		weight := validWeights[i]
		if weight <= 0 {
			continue
		}
		dx := x[i] - meanX
		dy := y[i] - meanY
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

func ewmaIndexWeights(length int, halfLife float64) []float64 {
	if length < 2 || halfLife <= 0 {
		return nil
	}

	weights := make([]float64, length)
	for i := 0; i < length; i++ {
		age := float64(length-1-i) / halfLife
		weights[i] = math.Pow(2, -age)
	}
	return weights
}

func temporalDecayWeights(decisionTimes []int64, halfLife float64) []float64 {
	if len(decisionTimes) < 2 || halfLife <= 0 {
		return nil
	}

	fallbackWeights := ewmaIndexWeights(len(decisionTimes), halfLife)
	validTimes := make([]int64, 0, len(decisionTimes))
	latestDecisionTime := int64(0)
	for _, decisionTime := range decisionTimes {
		if decisionTime <= 0 {
			continue
		}
		validTimes = append(validTimes, decisionTime)
		if decisionTime > latestDecisionTime {
			latestDecisionTime = decisionTime
		}
	}
	if latestDecisionTime <= 0 {
		return fallbackWeights
	}

	sort.Slice(validTimes, func(i, j int) bool {
		return validTimes[i] < validTimes[j]
	})

	intervals := make([]int64, 0, len(validTimes)-1)
	for i := 1; i < len(validTimes); i++ {
		delta := validTimes[i] - validTimes[i-1]
		if delta > 0 {
			intervals = append(intervals, delta)
		}
	}

	referenceIntervalMs := medianPositiveInterval(intervals)
	if referenceIntervalMs <= 0 {
		return fallbackWeights
	}

	lambda := math.Ln2 / (halfLife * float64(referenceIntervalMs))
	weights := make([]float64, len(decisionTimes))
	for i, decisionTime := range decisionTimes {
		if decisionTime <= 0 {
			weights[i] = fallbackWeights[i]
			continue
		}
		deltaT := float64(latestDecisionTime - decisionTime)
		if deltaT < 0 {
			deltaT = 0
		}
		weights[i] = math.Exp(-lambda * deltaT)
	}
	return weights
}

func medianPositiveInterval(intervals []int64) int64 {
	if len(intervals) == 0 {
		return 0
	}

	values := append([]int64(nil), intervals...)
	sort.Slice(values, func(i, j int) bool {
		return values[i] < values[j]
	})

	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

func computeAdaptiveWeightState(traderID, sector, symbol string) AdaptiveWeightState {
	defaults := defaultAdaptiveWeights()
	hiddenDefaults := defaultHiddenAdaptiveWeights()
	globalICs := zeroAdaptiveValues()
	sectorICs := zeroAdaptiveValues()
	coinICs := zeroAdaptiveValues()
	finalICs := zeroAdaptiveValues()
	empirical := copyWeightMap(defaults)
	finalWeights := copyWeightMap(defaults)
	hiddenEmpirical := copyWeightMap(hiddenDefaults)
	hiddenFinalWeights := copyWeightMap(hiddenDefaults)
	nestedWeights := defaultNestedAdaptiveWeights()
	st := getAdaptiveWeightStore()
	adaptiveMemory := loadAdaptiveMemoryConfig(st)

	rawSector := strings.TrimSpace(sector)
	normalizedTrader := normalizedAdaptiveTrader(traderID)
	normalizedSector := normalizeAdaptiveSector(rawSector)
	normalizedSymbol := normalizeAdaptiveSymbol(symbol)

	state := AdaptiveWeightState{
		TraderID:     normalizedTrader,
		Symbol:       normalizedSymbol,
		Sector:       rawSector,
		SampleTarget: adaptiveMemory.GlobalSamples,
		UpdatedAt:    time.Now().UTC().UnixMilli(),
	}

	finalizeState := func() AdaptiveWeightState {
		state.Factors = buildAdaptiveFactorStatesForOrder(adaptiveVisibleFactorOrder, defaults, empirical, finalWeights, sectorICs, coinICs, finalICs)
		state.HiddenFactors = buildAdaptiveFactorStatesForOrder(adaptiveHiddenFactorOrder, hiddenDefaults, hiddenEmpirical, hiddenFinalWeights, sectorICs, coinICs, finalICs)
		state.NestedWeights = nestedWeights
		return state
	}

	if st == nil {
		state.BlendDefault = 1
		return finalizeState()
	}

	scopeLabel := "global"
	switch {
	case normalizedSymbol != "":
		scopeLabel = "symbol"
	case rawSector != "":
		scopeLabel = "sector"
	}
	logger.Infof(
		"V3_AUDIT_ADAPTIVE_MEMORY: Scope=%s, Symbol=%s, Sector=%s, GlobalSamples=%d, SectorSamples=%d, SymbolSamples=%d",
		scopeLabel,
		normalizedSymbol,
		rawSector,
		adaptiveMemory.GlobalSamples,
		adaptiveMemory.SectorSamples,
		adaptiveMemory.SymbolSamples,
	)

	switch {
	case normalizedSymbol != "":
		globalRows, err := st.Shadow().ListFilledSharedForAdaptive(adaptiveMemory.GlobalSamples)
		if err != nil {
			state.BlendDefault = 1
			return finalizeState()
		}
		sectorRows, err := st.Shadow().ListFilledSharedForSectorAdaptive(normalizedSector, adaptiveMemory.SectorSamples)
		if err != nil {
			state.BlendDefault = 1
			return finalizeState()
		}
		coinRows, err := st.Shadow().ListFilledSharedForCoinAdaptive(normalizedSymbol, adaptiveMemory.SymbolSamples)
		if err != nil {
			state.BlendDefault = 1
			return finalizeState()
		}

		globalICs, _ = computeICsFromRows(globalRows, adaptiveMemory.GlobalSamples, "global")
		sectorICs, state.SectorSampleCount = computeICsFromRows(sectorRows, adaptiveMemory.SectorSamples, "sector")
		coinICs, state.CoinSampleCount = computeICsFromRows(coinRows, adaptiveMemory.SymbolSamples, "symbol")
		state.SampleCount = state.SectorSampleCount
		state.Alpha = calculateBayesianShrinkageAlpha(state.CoinSampleCount)
		finalICs = shrinkFactorICs(globalICs, sectorICs, coinICs, state.SectorSampleCount, state.CoinSampleCount)
		logAdaptiveShrinkage("symbol", normalizedSymbol, rawSector, globalICs, sectorICs, coinICs, finalICs, state.SectorSampleCount, state.CoinSampleCount)
	case rawSector != "":
		globalRows, err := st.Shadow().ListFilledSharedForAdaptive(adaptiveMemory.GlobalSamples)
		if err != nil {
			state.BlendDefault = 1
			return finalizeState()
		}
		sectorRows, err := st.Shadow().ListFilledSharedForSectorAdaptive(normalizedSector, adaptiveMemory.SectorSamples)
		if err != nil {
			state.BlendDefault = 1
			return finalizeState()
		}

		globalICs, _ = computeICsFromRows(globalRows, adaptiveMemory.GlobalSamples, "global")
		sectorICs, state.SectorSampleCount = computeICsFromRows(sectorRows, adaptiveMemory.SectorSamples, "sector")
		state.SampleCount = state.SectorSampleCount
		finalICs = smoothFactorICs(sectorICs, globalICs, state.SectorSampleCount, false)
		logAdaptiveShrinkage("sector", normalizedSymbol, rawSector, globalICs, sectorICs, coinICs, finalICs, state.SectorSampleCount, 0)
	default:
		globalRows, err := st.Shadow().ListFilledSharedForAdaptive(adaptiveMemory.GlobalSamples)
		if err != nil {
			state.BlendDefault = 1
			return finalizeState()
		}

		globalICs, state.SampleCount = computeICsFromRows(globalRows, adaptiveMemory.GlobalSamples, "global")
		finalICs = copyWeightMap(globalICs)
		logAdaptiveShrinkage("global", normalizedSymbol, rawSector, globalICs, sectorICs, coinICs, finalICs, 0, 0)
	}

	if state.SampleCount >= 2 {
		empirical = buildEmpiricalWeightsForOrder(adaptiveVisibleFactorOrder, defaults, finalICs)
		blendDefault := 0.0
		if state.SampleCount < AdaptiveSampleTarget {
			blendDefault = float64(AdaptiveSampleTarget-state.SampleCount) / float64(AdaptiveSampleTarget)
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

	return finalizeState()
}

func computeICsFromRows(rows []*store.ShadowSnapshot, sampleLimit int, scope string) (map[string]float64, int) {
	samples := buildAdaptiveSampleSet(rows)
	sortAdaptiveSampleSetByDecisionTimeDesc(&samples)
	ics := zeroAdaptiveValues()
	if len(samples.Returns) < 2 {
		return ics, len(samples.Returns)
	}

	effectiveLimit := sampleLimit
	if effectiveLimit <= 0 {
		effectiveLimit = len(samples.Returns)
	}

	weights, lambda := dynamicExponentialDecayWeights(len(samples.Returns), effectiveLimit)
	if len(weights) == len(samples.Returns) {
		logger.Infof("V3_AUDIT_ADAPTIVE_DECAY: Scope=%s, QueryLimit=%d, Samples=%d, Lambda=%.6f", scope, effectiveLimit, len(samples.Returns), lambda)
	}

	for _, factor := range adaptiveFactorOrder {
		ics[factor] = weightedSpearmanCorrelationWithWeights(
			samples.FactorSamples[factor],
			samples.Returns,
			weights,
		)
	}
	return ics, len(samples.Returns)
}

type adaptiveSampleSet struct {
	FactorSamples map[string][]float64
	Returns       []float64
	DecisionTimes []int64
}

func buildAdaptiveSampleSet(rows []*store.ShadowSnapshot) adaptiveSampleSet {
	factorSamples := map[string][]float64{
		"market":            {},
		"trend":             {},
		"donchian_factor":   {},
		"volume_spike":      {},
		"mtf_resonance":     {},
		"quant":             {},
		"quant_oi":          {},
		"quant_imbalance":   {},
		"quant_netflow":     {},
		"social":            {},
		"social_rank":       {},
		"social_upvote":     {},
		"onchain":           {},
		"onchain_ratio":     {},
		"onchain_buy_ratio": {},
	}
	returns := make([]float64, 0, len(rows))
	decisionTimes := make([]int64, 0, len(rows))
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
			"market":            row.MarketFactor,
			"trend":             row.TrendFactor,
			"donchian_factor":   row.DonchianFactor,
			"volume_spike":      row.VolumeSpikeFactor,
			"mtf_resonance":     row.MTFResonanceFactor,
			"quant":             row.QuantFactor,
			"quant_oi":          row.QuantOIRaw,
			"quant_imbalance":   row.QuantImbalanceRaw,
			"quant_netflow":     row.QuantNetflowRaw,
			"social":            row.SocialFactor,
			"social_rank":       row.SocialRankRaw,
			"social_upvote":     row.SocialUpvoteRaw,
			"onchain":           row.OnChainFactor,
			"onchain_ratio":     row.OnChainRatioRaw,
			"onchain_buy_ratio": row.OnChainBuyRaw,
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
		decisionTimes = append(decisionTimes, row.DecisionTime)
		for _, factor := range adaptiveFactorOrder {
			factorSamples[factor] = append(factorSamples[factor], values[factor])
		}
	}

	return adaptiveSampleSet{
		FactorSamples: factorSamples,
		Returns:       returns,
		DecisionTimes: decisionTimes,
	}
}

func sortAdaptiveSampleSetByDecisionTimeDesc(samples *adaptiveSampleSet) {
	if samples == nil || len(samples.Returns) < 2 || len(samples.DecisionTimes) != len(samples.Returns) {
		return
	}

	indices := make([]int, len(samples.Returns))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		left := samples.DecisionTimes[indices[i]]
		right := samples.DecisionTimes[indices[j]]
		if left == right {
			return indices[i] < indices[j]
		}
		return left > right
	})

	reorderedReturns := make([]float64, len(samples.Returns))
	reorderedDecisionTimes := make([]int64, len(samples.DecisionTimes))
	for factor := range samples.FactorSamples {
		reordered := make([]float64, len(samples.FactorSamples[factor]))
		for destIdx, srcIdx := range indices {
			reordered[destIdx] = samples.FactorSamples[factor][srcIdx]
		}
		samples.FactorSamples[factor] = reordered
	}
	for destIdx, srcIdx := range indices {
		reorderedReturns[destIdx] = samples.Returns[srcIdx]
		reorderedDecisionTimes[destIdx] = samples.DecisionTimes[srcIdx]
	}
	samples.Returns = reorderedReturns
	samples.DecisionTimes = reorderedDecisionTimes
}

func dynamicExponentialDecayWeights(sampleCount, sampleLimit int) ([]float64, float64) {
	if sampleCount < 2 {
		return nil, 0
	}
	if sampleLimit <= 0 {
		sampleLimit = sampleCount
	}

	lambda := (2.0 * math.Ln2) / float64(sampleLimit)
	weights := make([]float64, sampleCount)
	sum := 0.0
	for i := 0; i < sampleCount; i++ {
		weight := math.Exp(-lambda * float64(i))
		weights[i] = weight
		sum += weight
	}
	if sum <= 0 {
		return nil, lambda
	}
	for i := range weights {
		weights[i] /= sum
	}
	return weights, lambda
}

func weightedSpearmanCorrelationWithWeights(x, y, weights []float64) float64 {
	filteredX, filteredY := filterFiniteCorrelationPairs(x, y)
	if len(filteredX) < 2 || len(filteredX) != len(weights) {
		return 0
	}

	rankedX := RankData(filteredX)
	rankedY := RankData(filteredY)
	return weightedPearson(rankedX, rankedY, weights)
}

func loadAdaptiveMemoryConfig(st *store.Store) store.AdaptiveMemorySystemConfig {
	if st == nil {
		return store.DefaultAdaptiveMemoryConfig()
	}

	cfg, err := st.GetAdaptiveMemoryConfig()
	if err != nil {
		logger.Warnf("⚠️ Failed to load adaptive memory config, using defaults: %v", err)
		return store.DefaultAdaptiveMemoryConfig()
	}
	return cfg
}

func reverseFloat64s(values []float64) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseInt64s(values []int64) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

// calculateBayesianShrinkageAlpha stretches the coin-level shrinkage curve so
// alpha only reaches the 0.4 cap after 500 coin samples.
func calculateBayesianShrinkageAlpha(coinSampleCount int) float64 {
	return math.Min(float64(coinSampleCount)/1250.0, 0.4)
}

func calculateConfidenceFactor(sampleCount int) float64 {
	if sampleCount <= 0 {
		return 0
	}
	return float64(sampleCount) / (float64(sampleCount) + ConfidenceThreshold)
}

func smoothFactorICs(primaryICs, fallbackICs map[string]float64, sampleCount int, alwaysSmooth bool) map[string]float64 {
	if !alwaysSmooth && float64(sampleCount) >= ConfidenceThreshold {
		return copyWeightMap(primaryICs)
	}

	confidenceFactor := calculateConfidenceFactor(sampleCount)
	smoothed := make(map[string]float64, len(adaptiveFactorOrder))
	for _, factor := range adaptiveFactorOrder {
		smoothed[factor] = primaryICs[factor]*confidenceFactor + fallbackICs[factor]*(1-confidenceFactor)
	}
	return smoothed
}

func shrinkFactorICs(globalICs, sectorICs, coinICs map[string]float64, sectorSampleCount, coinSampleCount int) map[string]float64 {
	smoothedSectorICs := smoothFactorICs(sectorICs, globalICs, sectorSampleCount, false)
	return smoothFactorICs(coinICs, smoothedSectorICs, coinSampleCount, true)
}

func logAdaptiveShrinkage(scope, symbol, sector string, globalICs, sectorICs, coinICs, finalICs map[string]float64, sectorSampleCount, coinSampleCount int) {
	sectorConfidence := calculateConfidenceFactor(sectorSampleCount)
	symbolConfidence := calculateConfidenceFactor(coinSampleCount)
	if float64(sectorSampleCount) >= ConfidenceThreshold {
		sectorConfidence = 1
	}
	if scope != "symbol" {
		symbolConfidence = 0
	}

	for _, factor := range adaptiveVisibleFactorOrder {
		logger.Infof("V3_AUDIT_ADAPTIVE: Scope=%s, Symbol=%s, Sector=%s, Factor=%s, Global_IC=%.2f, Sector_IC=%.2f, Coin_IC=%.2f, Final_IC=%.2f, SectorCF=%.2f, SymbolCF=%.2f",
			scope,
			symbol,
			sector,
			factor,
			globalICs[factor],
			sectorICs[factor],
			coinICs[factor],
			finalICs[factor],
			sectorConfidence,
			symbolConfidence,
		)
	}
}

func buildEmpiricalWeightsForOrder(order []string, defaults map[string]float64, ics map[string]float64) map[string]float64 {
	empirical := make(map[string]float64, len(defaults))
	absoluteICSum := 0.0
	zeroCount := 0

	for _, factor := range order {
		magnitude := math.Abs(ics[factor])
		if magnitude > 0 {
			absoluteICSum += magnitude
			continue
		}
		empirical[factor] = empiricalWeightFloor
		zeroCount++
	}

	if absoluteICSum <= 0 {
		return copyWeightMap(defaults)
	}

	remainingBudget := 1 - float64(zeroCount)*empiricalWeightFloor
	if remainingBudget <= 0 {
		return copyWeightMap(defaults)
	}

	active := make([]string, 0, len(order))
	for _, factor := range order {
		if math.Abs(ics[factor]) > 0 {
			active = append(active, factor)
		}
	}

	remainingIC := absoluteICSum
	for len(active) > 0 && remainingBudget > 0 {
		next := make([]string, 0, len(active))
		capped := false
		for _, factor := range active {
			if remainingIC <= 0 {
				break
			}
			magnitude := math.Abs(ics[factor])
			tentative := remainingBudget * (magnitude / remainingIC)
			if tentative > empiricalWeightCeiling {
				empirical[factor] = empiricalWeightCeiling
				remainingBudget -= empiricalWeightCeiling
				remainingIC -= magnitude
				capped = true
				continue
			}
			next = append(next, factor)
		}

		if !capped {
			for _, factor := range active {
				magnitude := math.Abs(ics[factor])
				if remainingIC <= 0 {
					empirical[factor] = remainingBudget / float64(len(active))
					continue
				}
				empirical[factor] = remainingBudget * (magnitude / remainingIC)
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
	weights := map[string]float64{
		"donchian_factor": DefaultNestedWeightSub,
		"mtf_resonance":   DefaultNestedWeightSub,
	}
	for group, priors := range adaptiveNestedPriors {
		for factor, weight := range priors {
			if factor == group {
				continue
			}
			weights[factor] = weight
		}
	}
	return weights
}

func defaultNestedAdaptiveWeights() map[string]float64 {
	weights := make(map[string]float64, len(adaptiveFactorOrder))
	for _, priors := range adaptiveNestedPriors {
		for factor, weight := range priors {
			weights[factor] = weight
		}
	}
	return weights
}

func zeroAdaptiveValues() map[string]float64 {
	return map[string]float64{
		"market":            0,
		"trend":             0,
		"donchian_factor":   0,
		"volume_spike":      0,
		"mtf_resonance":     0,
		"quant":             0,
		"quant_oi":          0,
		"quant_imbalance":   0,
		"quant_netflow":     0,
		"social":            0,
		"social_rank":       0,
		"social_upvote":     0,
		"onchain":           0,
		"onchain_ratio":     0,
		"onchain_buy_ratio": 0,
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
		groupFinal = clampNestedGroupWeights(order, groupFinal, groupDefaults)

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
	priors, ok := adaptiveNestedPriors[parent]
	if !ok {
		return map[string]float64{
			parent: 1,
		}
	}

	group := make(map[string]float64, len(priors))
	for factor, weight := range priors {
		group[factor] = weight
	}
	return group
}

func clampNestedGroupWeights(order []string, weights, priors map[string]float64) map[string]float64 {
	minBounds := make(map[string]float64, len(order))
	maxBounds := make(map[string]float64, len(order))
	for _, factor := range order {
		minBounds[factor] = empiricalWeightFloor
		maxBounds[factor] = 0.99
	}

	return projectWeightsIntoBounds(order, weights, priors, minBounds, maxBounds)
}

func projectWeightsIntoBounds(order []string, weights, fallback, minBounds, maxBounds map[string]float64) map[string]float64 {
	projected := normalizeWeightMapForOrder(order, weights, fallback)
	const maxIterations = 8
	for i := 0; i < maxIterations; i++ {
		sum := 0.0
		for _, factor := range order {
			projected[factor] = clamp(projected[factor], minBounds[factor], maxBounds[factor])
			sum += projected[factor]
		}

		diff := 1 - sum
		if math.Abs(diff) <= 1e-9 {
			break
		}

		if diff > 0 {
			slackSum := 0.0
			for _, factor := range order {
				slack := maxBounds[factor] - projected[factor]
				if slack > 0 {
					slackSum += slack
				}
			}
			if slackSum <= 0 {
				break
			}
			for _, factor := range order {
				slack := maxBounds[factor] - projected[factor]
				if slack <= 0 {
					continue
				}
				projected[factor] += diff * (slack / slackSum)
			}
			continue
		}

		slackSum := 0.0
		for _, factor := range order {
			slack := projected[factor] - minBounds[factor]
			if slack > 0 {
				slackSum += slack
			}
		}
		if slackSum <= 0 {
			break
		}
		for _, factor := range order {
			slack := projected[factor] - minBounds[factor]
			if slack <= 0 {
				continue
			}
			projected[factor] += diff * (slack / slackSum)
		}
	}

	return stabilizeUnitSumForOrder(order, projected, fallback)
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
		normalizeAdaptiveSymbol(symbol),
	}, "|")
}

func normalizedAdaptiveTrader(traderID string) string {
	return store.GlobalConsensusTraderID
}

func normalizeAdaptiveSector(sector string) string {
	sector = strings.TrimSpace(sector)
	if sector == "" {
		return adaptiveSectorFallback
	}
	return sector
}

func normalizeAdaptiveSymbol(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}
	return Normalize(symbol)
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

// RunFullRegression executes the repository's full Go regression suite using the
// repo toolchain wrapper so the command is safe for repeated CI/CD invocation.
func RunFullRegression() error {
	projectRoot := resolveProjectRoot()
	cmd := exec.Command(filepath.Join(projectRoot, "scripts", "with_go_env.sh"), "go", "test", "./...")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// FullStackHealthCheck ensures backend (8080) and frontend (3000) are online,
// performs HTTP 200 validation, refreshes caches, and notifies DB migration completion.
func FullStackHealthCheck() (err error) {
	logger.Info("FullStackHealthCheck invoked")
	defer func() {
		if refreshErr := store.RefreshCache(); refreshErr != nil {
			if err == nil {
				err = fmt.Errorf("refresh cache: %w", refreshErr)
			} else {
				err = fmt.Errorf("%w; cache refresh failed: %v", err, refreshErr)
			}
		}
		store.NotifyMigration()
		if err != nil {
			logger.Warnf("FullStackHealthCheck completed with error: %v", err)
			return
		}
		logger.Info("FullStackHealthCheck completed successfully")
	}()

	out, cmdErr := exec.Command("ss", "-tlnp").Output()
	if cmdErr != nil {
		err = fmt.Errorf("inspect listening ports: %w", cmdErr)
		return err
	}
	listeners := string(out)
	if !strings.Contains(listeners, ":8080") {
		err = errors.New("backend port 8080 not listening")
		return err
	}
	if !strings.Contains(listeners, ":3000") {
		err = errors.New("frontend port 3000 not listening")
		return err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, httpErr := client.Head("http://localhost:3000")
	if httpErr != nil {
		err = fmt.Errorf("frontend HTTP check failed: %w", httpErr)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("frontend HTTP check failed: status=%d", resp.StatusCode)
		return err
	}

	return nil
}

// MonitorSpearmanOutlierDrift evaluates ranked EWMA IC drift over multiple factor
// series and logs both normal samples and threshold breaches for repeated CI/CD runs.
func MonitorSpearmanOutlierDrift(factorData [][]float64, threshold float64) {
	for index, data := range factorData {
		returns := computeReturns(data)
		ic := EWMASpearmanCorrelation(data, returns, ICHalfLife)
		if math.Abs(ic) > threshold {
			logger.Warnf("Spearman IC outlier drift detected: series=%d ic=%.4f threshold=%.4f", index, ic, threshold)
			continue
		}
		logger.Infof("Spearman IC outlier drift monitored: series=%d ic=%.4f threshold=%.4f status=ok", index, ic, threshold)
	}
}

func computeReturns(data []float64) []float64 {
	returns := make([]float64, len(data))
	if len(data) == 0 {
		return returns
	}

	for i := 1; i < len(data); i++ {
		prev := data[i-1]
		current := data[i]
		if !isFinite(prev) || !isFinite(current) {
			continue
		}
		if prev == 0 {
			returns[i] = current
			continue
		}
		returns[i] = (current - prev) / math.Abs(prev)
	}

	return returns
}

// DefaultSpearmanMonitorFactorData returns deterministic stable and outlier-perturbed
// factor series so monitor_spearman can run without external market dependencies.
func DefaultSpearmanMonitorFactorData() [][]float64 {
	stable := make([]float64, 0, 24)
	for i := 0; i < 24; i++ {
		stable = append(stable, 100+math.Sin(float64(i)/3.0)*2+float64(i%3))
	}

	outlier := append([]float64{}, stable...)
	outlier = append(outlier, 180, 181, 182, 400, 183, 184)

	return [][]float64{stable, outlier}
}

func resolveProjectRoot() string {
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir
		}
	}

	if wd, err := os.Getwd(); err == nil {
		if fileExists(filepath.Join(wd, "go.mod")) {
			return wd
		}
	}

	return "."
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
