package kernel

import (
	"sort"
	"sync"

	"nofx/store"
)

type DirectionalResonance string

const (
	DirectionalResonanceLong           DirectionalResonance = "LONG"
	DirectionalResonanceShort          DirectionalResonance = "SHORT"
	DirectionalResonanceNoSignal       DirectionalResonance = "NO_SIGNAL"
	liveResonanceEntryFloor                                 = 0.0
	sparseNeighborhoodRiskDiscount                          = 0.50
	highNoiseRiskDiscount                                   = 0.10
	pureGreenNeighborhoodWinRateFloor                       = 0.80
	wolfPackNeighborhoodLossRateFloor                       = 0.80
	highNoiseNeighborhoodLossRateFloor                      = 0.60
)

type FeatureArchetypeMatch struct {
	Archetype *FeatureArchetype
	Distance  float64
	Threshold float64
	Known     bool
	Reason    string
	Distances map[string]float64
}

type ResonanceResult struct {
	Signal                 DirectionalResonance
	LogicScore             float64
	BinStart               int
	FinalEntryEV           float64
	LiveAttributionWeights map[string]float64
	GlobalBin              *store.ScoreBinPerformance
	SectorBin              *store.ScoreBinPerformance
	SymbolBin              *store.ScoreBinPerformance
}

type NeighborhoodRiskAdjustment struct {
	RawEntryEV            float64
	RiskDiscountFactor    float64
	RiskAdjustedEV        float64
	Label                 string
	BlockReason           string
	Blocked               bool
	ShadowMonitorRequired bool
}

func (r ResonanceResult) HasSignal() bool {
	return r.Signal != DirectionalResonanceNoSignal
}

func EvaluateNeighborhoodRiskAdjustment(rawEntryEV float64, audit NeighborhoodAuditResult) NeighborhoodRiskAdjustment {
	adjustment := NeighborhoodRiskAdjustment{
		RawEntryEV:            sanitizeFinite(rawEntryEV),
		RiskDiscountFactor:    1,
		RiskAdjustedEV:        sanitizeFinite(rawEntryEV),
		Label:                 audit.Label,
		ShadowMonitorRequired: audit.ShadowMonitorRequired,
	}

	switch {
	case audit.SparseZone || audit.SampleCount < sparseNeighborhoodSampleFloor:
		adjustment.Label = "Sparse Zone"
		adjustment.RiskDiscountFactor = sparseNeighborhoodRiskDiscount
		adjustment.ShadowMonitorRequired = true
	case audit.LossRate > wolfPackNeighborhoodLossRateFloor:
		adjustment.Label = "Wolf Pack Detected"
		adjustment.BlockReason = "[Wolf Pack Detected]"
		adjustment.Blocked = true
		adjustment.RiskDiscountFactor = 0
	case audit.WinRate > pureGreenNeighborhoodWinRateFloor:
		adjustment.Label = "Pure Green Community"
		adjustment.RiskDiscountFactor = 1
	case audit.LossRate >= highNoiseNeighborhoodLossRateFloor:
		adjustment.Label = "高噪诱多区"
		adjustment.BlockReason = "[High Noise Zone]"
		adjustment.RiskDiscountFactor = highNoiseRiskDiscount
	case audit.WinRate > audit.LossRate && audit.WinRate > 0:
		adjustment.Label = "Positive Mixed Zone"
		adjustment.RiskDiscountFactor = audit.WinRate
		if adjustment.RiskDiscountFactor < sparseNeighborhoodRiskDiscount {
			adjustment.RiskDiscountFactor = sparseNeighborhoodRiskDiscount
		}
	default:
		adjustment.Label = "Neutral Mixed Zone"
		adjustment.RiskDiscountFactor = sparseNeighborhoodRiskDiscount
	}

	adjustment.RiskAdjustedEV = RiskAdjustEntryEV(adjustment.RawEntryEV, adjustment.RiskDiscountFactor)
	if adjustment.Blocked {
		adjustment.RiskAdjustedEV = 0
	}
	return adjustment
}

func RiskAdjustEntryEV(rawEntryEV, riskDiscountFactor float64) float64 {
	rawEntryEV = sanitizeFinite(rawEntryEV)
	riskDiscountFactor = sanitizeFinite(riskDiscountFactor)
	if rawEntryEV <= 0 || riskDiscountFactor <= 0 {
		return 0
	}
	if riskDiscountFactor > 1 {
		riskDiscountFactor = 1
	}
	return rawEntryEV * riskDiscountFactor
}

func AdaptiveEntryEVFloor(adaptiveEntryFloor float64) float64 {
	floor := sanitizeFinite(adaptiveEntryFloor)
	if floor <= 0 {
		return 0
	}
	return floor / 10000.0
}

func ClearsRiskAdjustedEntryFloor(riskAdjustedEV, adaptiveEntryFloor float64) bool {
	return sanitizeFinite(riskAdjustedEV) >= AdaptiveEntryEVFloor(adaptiveEntryFloor)
}

func MatchFeatureArchetype(vector []float64, library *FeatureArchetypeLibrary, threshold float64) FeatureArchetypeMatch {
	match := FeatureArchetypeMatch{
		Threshold: threshold,
		Distances: make(map[string]float64),
	}
	if threshold <= 0 {
		match.Threshold = DefaultMahalanobisThreshold
	}
	if library == nil || len(library.Archetypes) == 0 || len(vector) == 0 {
		match.Known = true
		return match
	}

	type distanceResult struct {
		archetype *FeatureArchetype
		distance  float64
		ok        bool
	}

	results := make(chan distanceResult, len(library.Archetypes))
	var wg sync.WaitGroup
	for _, archetype := range library.Archetypes {
		if archetype == nil {
			continue
		}
		wg.Add(1)
		go func(candidate *FeatureArchetype) {
			defer wg.Done()
			distance, ok := candidate.Distance(vector)
			results <- distanceResult{
				archetype: candidate,
				distance:  distance,
				ok:        ok,
			}
		}(archetype)
	}

	wg.Wait()
	close(results)

	bestSet := false
	for result := range results {
		if result.archetype == nil || !result.ok {
			continue
		}
		match.Distances[result.archetype.ID] = result.distance
		if !bestSet || result.distance < match.Distance {
			bestSet = true
			match.Distance = result.distance
			match.Archetype = result.archetype
		}
	}
	if !bestSet {
		match.Known = true
		return match
	}

	if resonanceVolatilityAccelerationBlocked(vector, match.Archetype.Cluster()) {
		match.Known = false
		match.Reason = "Volatility acceleration > 100%"
		return match
	}
	if match.Distance > match.Threshold {
		match.Known = false
		match.Reason = "Outlier"
		return match
	}

	match.Known = true
	return match
}

func CheckDirectionalResonance(logicScore float64, matrices *PerformanceBinMatrices) ResonanceResult {
	result := ResonanceResult{
		Signal:     DirectionalResonanceNoSignal,
		LogicScore: logicScore,
		BinStart:   int(logicScore),
	}
	if matrices == nil {
		return result
	}

	globalBin := store.FindPerformanceBinForScore(matrices.Global, logicScore)
	sectorBin := store.FindPerformanceBinForScore(matrices.Sector, logicScore)
	symbolBin := store.FindPerformanceBinForScore(matrices.Symbol, logicScore)
	if globalBin == nil || sectorBin == nil || symbolBin == nil {
		return result
	}

	result.BinStart = globalBin.BinStart
	result.GlobalBin = cloneResonanceBin(globalBin)
	result.SectorBin = cloneResonanceBin(sectorBin)
	result.SymbolBin = cloneResonanceBin(symbolBin)

	weights, _ := GetLiveAttributionWeights()
	weights = NormalizeLiveAttributionWeights(weights)
	result.LiveAttributionWeights = weights

	longEV := weightedResonanceEntryEV(weights, globalBin, sectorBin, symbolBin, DirectionalResonanceLong)
	shortEV := weightedResonanceEntryEV(weights, globalBin, sectorBin, symbolBin, DirectionalResonanceShort)
	if longEV <= liveResonanceEntryFloor && shortEV <= liveResonanceEntryFloor {
		return result
	}

	result.FinalEntryEV = longEV
	result.Signal = DirectionalResonanceLong
	if shortEV > longEV {
		result.FinalEntryEV = shortEV
		result.Signal = DirectionalResonanceShort
	}
	if result.FinalEntryEV <= liveResonanceEntryFloor {
		result.Signal = DirectionalResonanceNoSignal
	}
	return result
}

func isLongResonance(bin *store.ScoreBinPerformance) bool {
	return bin != nil &&
		bin.ExpectedValueLong > 0 &&
		bin.MedianExpectedValueLong > 0
}

func isShortResonance(bin *store.ScoreBinPerformance) bool {
	return bin != nil &&
		bin.ExpectedValueShort > 0 &&
		bin.MedianExpectedValueShort > 0
}

func weightedResonanceEntryEV(weights map[string]float64, globalBin, sectorBin, symbolBin *store.ScoreBinPerformance, signal DirectionalResonance) float64 {
	globalWeight := liveAttributionWeight(weights, RealFireDimensionGlobal, "macro")
	sectorWeight := liveAttributionWeight(weights, RealFireDimensionSector)
	symbolWeight := liveAttributionWeight(weights, RealFireDimensionSymbol)
	totalWeight := globalWeight + sectorWeight + symbolWeight
	if totalWeight <= 0 {
		globalWeight = 1.0 / 3.0
		sectorWeight = 1.0 / 3.0
		symbolWeight = 1.0 / 3.0
	}

	return directionalExpectedValue(globalBin, signal)*globalWeight +
		directionalExpectedValue(sectorBin, signal)*sectorWeight +
		directionalExpectedValue(symbolBin, signal)*symbolWeight
}

func directionalExpectedValue(bin *store.ScoreBinPerformance, signal DirectionalResonance) float64 {
	if bin == nil {
		return 0
	}
	switch signal {
	case DirectionalResonanceShort:
		return bin.ExpectedValueShort
	case DirectionalResonanceLong:
		fallthrough
	default:
		return bin.ExpectedValueLong
	}
}

func cloneResonanceBin(bin *store.ScoreBinPerformance) *store.ScoreBinPerformance {
	if bin == nil {
		return nil
	}
	cloned := *bin
	return &cloned
}

func sortedArchetypeIDs(match FeatureArchetypeMatch) []string {
	if len(match.Distances) == 0 {
		return nil
	}
	ids := make([]string, 0, len(match.Distances))
	for id := range match.Distances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
