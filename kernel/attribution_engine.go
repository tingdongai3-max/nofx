package kernel

import (
	"math"
	"nofx/store"
	"sync"
)

const (
	RealFireDimensionGlobal        = "global"
	RealFireDimensionSector        = "sector"
	RealFireDimensionSymbol        = "symbol"
	realFireSectorWeightMultiplier = 2.0
	liveAttributionTraderID        = "GLOBAL_REAL_SNIPER"
)

var (
	liveAttributionStoreMu sync.RWMutex
	liveAttributionStore   *store.Store
)

type RealFireDimensionWeight struct {
	Name             string  `json:"name"`
	Correlation      float64 `json:"correlation"`
	RealWeight       float64 `json:"real_weight"`
	ShadowWeight     float64 `json:"shadow_weight"`
	WeightDelta      float64 `json:"weight_delta"`
	EntryAverageEV   float64 `json:"entry_average_ev"`
	HoldAverageEV    float64 `json:"hold_average_ev"`
	AverageRetention float64 `json:"average_retention"`
	ClosedTradeCount int     `json:"closed_trade_count"`
}

type RealFireWeights struct {
	SampleCount             int                       `json:"sample_count"`
	Dimensions              []RealFireDimensionWeight `json:"dimensions"`
	DominantDimension       string                    `json:"dominant_dimension"`
	ShadowDominantDimension string                    `json:"shadow_dominant_dimension"`
}

type realFireDimensionAccumulator struct {
	entryValues      []float64
	pnls             []float64
	entrySum         float64
	holdSum          float64
	retentionSum     float64
	shadowStrength   float64
	closedTradeCount int
}

func SetLiveAttributionStore(st *store.Store) {
	liveAttributionStoreMu.Lock()
	liveAttributionStore = st
	liveAttributionStoreMu.Unlock()
}

func getLiveAttributionStore() *store.Store {
	liveAttributionStoreMu.RLock()
	defer liveAttributionStoreMu.RUnlock()
	return liveAttributionStore
}

func CalculateRealFireWeights(records []*store.RealTradeResonanceRecord) *RealFireWeights {
	result := &RealFireWeights{
		Dimensions: []RealFireDimensionWeight{
			{Name: RealFireDimensionGlobal},
			{Name: RealFireDimensionSector},
			{Name: RealFireDimensionSymbol},
		},
	}
	if len(records) == 0 {
		return result
	}

	closed := make([]*store.RealTradeResonanceRecord, 0, len(records))
	for _, record := range records {
		if record == nil || record.Status != store.RealTradeResonanceStatusClosed || !isFiniteAttributionValue(record.FinalPnL) {
			continue
		}
		closed = append(closed, record)
	}
	result.SampleCount = len(closed)
	if len(closed) == 0 {
		return result
	}

	accumulators := map[string]*realFireDimensionAccumulator{
		RealFireDimensionGlobal: {},
		RealFireDimensionSector: {},
		RealFireDimensionSymbol: {},
	}

	for _, record := range closed {
		pnl := record.FinalPnL
		accumulateDimension(accumulators[RealFireDimensionGlobal], record.EntryGlobalEV, record.HoldAvgGlobalEV, record.HoldRetentionRate, pnl)
		accumulateDimension(accumulators[RealFireDimensionSector], record.EntrySectorEV, record.HoldAvgSectorEV, record.HoldRetentionRate, pnl)
		accumulateDimension(accumulators[RealFireDimensionSymbol], record.EntrySymbolEV, record.HoldAvgSymbolEV, record.HoldRetentionRate, pnl)
	}

	realWeightBase := 0.0
	shadowWeightBase := 0.0
	for index := range result.Dimensions {
		dimension := &result.Dimensions[index]
		accumulator := accumulators[dimension.Name]
		if accumulator == nil || accumulator.closedTradeCount == 0 {
			continue
		}

		dimension.Correlation = pearsonCorrelation(accumulator.entryValues, accumulator.pnls)
		dimension.EntryAverageEV = accumulator.entrySum / float64(accumulator.closedTradeCount)
		dimension.HoldAverageEV = accumulator.holdSum / float64(accumulator.closedTradeCount)
		dimension.AverageRetention = accumulator.retentionSum / float64(accumulator.closedTradeCount)
		dimension.ClosedTradeCount = accumulator.closedTradeCount

		realWeightBase += math.Abs(dimension.Correlation)
		shadowWeightBase += accumulator.shadowStrength * realFireDimensionBoost(dimension.Name)
	}

	for index := range result.Dimensions {
		dimension := &result.Dimensions[index]
		accumulator := accumulators[dimension.Name]
		if accumulator == nil || accumulator.closedTradeCount == 0 {
			continue
		}
		if realWeightBase > 0 {
			dimension.RealWeight = math.Abs(dimension.Correlation) / realWeightBase
		}
		if shadowWeightBase > 0 {
			dimension.ShadowWeight = (accumulator.shadowStrength * realFireDimensionBoost(dimension.Name)) / shadowWeightBase
		}
		dimension.WeightDelta = dimension.RealWeight - dimension.ShadowWeight
	}

	result.DominantDimension = dominantRealFireDimension(result.Dimensions, func(d RealFireDimensionWeight) float64 {
		return d.RealWeight
	})
	result.ShadowDominantDimension = dominantRealFireDimension(result.Dimensions, func(d RealFireDimensionWeight) float64 {
		return d.ShadowWeight
	})
	return result
}

func GetLiveAttributionWeights() (map[string]float64, error) {
	st := getLiveAttributionStore()
	if st == nil || st.RealTradeStats() == nil {
		return NormalizeLiveAttributionWeights(nil), nil
	}

	records, err := st.RealTradeStats().ListClosedByTrader(liveAttributionTraderID, store.PERFORMANCE_RECENT_SAMPLE_LIMIT)
	if err != nil {
		return NormalizeLiveAttributionWeights(nil), err
	}

	weights := CalculateRealFireWeights(records)
	if weights == nil || weights.SampleCount == 0 {
		return NormalizeLiveAttributionWeights(nil), nil
	}

	liveWeights := map[string]float64{}
	for _, dimension := range weights.Dimensions {
		liveWeights[dimension.Name] = dimension.RealWeight
	}
	return NormalizeLiveAttributionWeights(liveWeights), nil
}

func NormalizeLiveAttributionWeights(weights map[string]float64) map[string]float64 {
	normalized := map[string]float64{
		RealFireDimensionGlobal: 1.0 / 3.0,
		RealFireDimensionSector: 1.0 / 3.0,
		RealFireDimensionSymbol: 1.0 / 3.0,
	}
	if len(weights) == 0 {
		return normalized
	}

	globalWeight := liveAttributionWeight(weights, RealFireDimensionGlobal, "macro")
	sectorWeight := liveAttributionWeight(weights, RealFireDimensionSector)
	symbolWeight := liveAttributionWeight(weights, RealFireDimensionSymbol)
	if !isFiniteAttributionValue(globalWeight) || !isFiniteAttributionValue(sectorWeight) || !isFiniteAttributionValue(symbolWeight) {
		return normalized
	}

	globalWeight = math.Max(globalWeight, 0)
	sectorWeight = math.Max(sectorWeight, 0)
	symbolWeight = math.Max(symbolWeight, 0)
	total := globalWeight + sectorWeight + symbolWeight
	if total <= 0 {
		return normalized
	}

	normalized[RealFireDimensionGlobal] = globalWeight / total
	normalized[RealFireDimensionSector] = sectorWeight / total
	normalized[RealFireDimensionSymbol] = symbolWeight / total
	return normalized
}

func liveAttributionWeight(weights map[string]float64, names ...string) float64 {
	for _, name := range names {
		if value, ok := weights[name]; ok && isFiniteAttributionValue(value) {
			return value
		}
	}
	return 0
}

func accumulateDimension(accumulator *realFireDimensionAccumulator, entryEV, holdAvgEV, retention, pnl float64) {
	if accumulator == nil || !isFiniteAttributionValue(pnl) {
		return
	}

	entry := sanitizeAttributionValue(entryEV)
	hold := sanitizeAttributionValue(holdAvgEV)
	accumulator.entryValues = append(accumulator.entryValues, entry)
	accumulator.pnls = append(accumulator.pnls, pnl)
	accumulator.entrySum += entry
	accumulator.holdSum += hold
	accumulator.retentionSum += sanitizeRetentionValue(retention)
	accumulator.shadowStrength += math.Abs(hold)
	accumulator.closedTradeCount++
}

func realFireDimensionBoost(name string) float64 {
	if name == RealFireDimensionSector {
		return realFireSectorWeightMultiplier
	}
	return 1
}

func dominantRealFireDimension(dimensions []RealFireDimensionWeight, selector func(RealFireDimensionWeight) float64) string {
	bestName := ""
	bestValue := -1.0
	for _, dimension := range dimensions {
		value := selector(dimension)
		if value > bestValue {
			bestValue = value
			bestName = dimension.Name
		}
	}
	if bestValue <= 0 {
		return ""
	}
	return bestName
}

func pearsonCorrelation(x, y []float64) float64 {
	if len(x) != len(y) || len(x) < 2 {
		return 0
	}

	meanX := 0.0
	meanY := 0.0
	for index := range x {
		meanX += x[index]
		meanY += y[index]
	}
	meanX /= float64(len(x))
	meanY /= float64(len(y))

	numerator := 0.0
	denomX := 0.0
	denomY := 0.0
	for index := range x {
		dx := x[index] - meanX
		dy := y[index] - meanY
		numerator += dx * dy
		denomX += dx * dx
		denomY += dy * dy
	}

	if denomX <= 0 || denomY <= 0 {
		return 0
	}

	return numerator / math.Sqrt(denomX*denomY)
}

func sanitizeAttributionValue(value float64) float64 {
	if !isFiniteAttributionValue(value) {
		return 0
	}
	return value
}

func sanitizeRetentionValue(value float64) float64 {
	if !isFiniteAttributionValue(value) {
		return 0
	}
	if value < 0 {
		return 0
	}
	return value
}

func isFiniteAttributionValue(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
