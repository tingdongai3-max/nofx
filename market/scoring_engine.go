package market

import (
	"math"
	"nofx/logger"
	"nofx/provider/nofxos"
	"nofx/store"
	"sort"
	"sync"
	"time"
)

const (
	MinSocialMentionThreshold = 50
	HeatRollingWindow         = 24 * time.Hour
	volumeSpikeMinBars        = 12
	volumeSpikeShortWeight    = 0.55
	volumeSpikeMediumWeight   = 0.45
)

type heatSample struct {
	timestamp time.Time
	value     float64
}

type heatSeries struct {
	mu      sync.Mutex
	samples []heatSample
}

type volumeSpikeAudit struct {
	VolumeZ           float64
	PriceZ            float64
	DirectionSlopePct float64
	ShortVolumeZ      float64
	MediumVolumeZ     float64
	ShortPriceZ       float64
	MediumPriceZ      float64
	Raw               float64
}

type timeframeSpikeSnapshot struct {
	volumeZ  float64
	priceZ   float64
	slopePct float64
	raw      float64
}

type volumeSpikeContext struct {
	short    timeframeSpikeSnapshot
	medium   timeframeSpikeSnapshot
	shortOK  bool
	mediumOK bool
}

type nestedFactorScore struct {
	zScore float64
	score  float64
}

var heatSeriesMap sync.Map
var heatWarmupMap sync.Map

func getHeatSeries(key string) *heatSeries {
	value, _ := heatSeriesMap.LoadOrStore(key, &heatSeries{})
	return value.(*heatSeries)
}

func (s *heatSeries) history(now time.Time) []float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := now.Add(-HeatRollingWindow)
	trimmed := s.samples[:0]
	values := make([]float64, 0, len(s.samples))
	for _, sample := range s.samples {
		if sample.timestamp.Before(cutoff) {
			continue
		}
		trimmed = append(trimmed, sample)
		values = append(values, sample.value)
	}
	s.samples = trimmed
	return values
}

func (s *heatSeries) append(now time.Time, value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.samples = append(s.samples, heatSample{
		timestamp: now,
		value:     value,
	})

	cutoff := now.Add(-HeatRollingWindow)
	trimmed := s.samples[:0]
	for _, sample := range s.samples {
		if sample.timestamp.Before(cutoff) {
			continue
		}
		trimmed = append(trimmed, sample)
	}
	s.samples = trimmed
}

func (s *heatSeries) appendBatch(samples []heatSample) {
	if len(samples) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.samples = append(s.samples, samples...)
	latest := samples[len(samples)-1].timestamp
	cutoff := latest.Add(-HeatRollingWindow)
	trimmed := s.samples[:0]
	for _, sample := range s.samples {
		if sample.timestamp.Before(cutoff) {
			continue
		}
		trimmed = append(trimmed, sample)
	}
	s.samples = trimmed
}

func CalculateZScore(current float64, history []float64) float64 {
	if !isFinite(current) {
		return 0
	}
	if len(history) == 0 {
		return 0
	}

	alpha := 2.0 / float64(len(history)+1)
	mean := history[0]
	variance := 0.0

	for i := 1; i < len(history); i++ {
		diff := history[i] - mean
		mean = alpha*history[i] + (1-alpha)*mean
		variance = alpha*diff*diff + (1-alpha)*variance
	}

	std := math.Sqrt(variance)
	if std < 1e-9 {
		return 0
	}

	return (current - mean) / std
}

func buildHeatScore(traderID, symbol string, data *Data, quant *nofxos.QuantData, now time.Time) *HeatScoreData {
	return buildHeatScoreWithMode(traderID, symbol, data, quant, now, true)
}

func RecalculateHeatScore(traderID, symbol string, data *Data, quant *nofxos.QuantData) *HeatScoreData {
	return buildHeatScoreWithMode(traderID, symbol, data, quant, time.Now().UTC(), false)
}

func buildHeatScoreWithMode(
	traderID,
	symbol string,
	data *Data,
	quant *nofxos.QuantData,
	now time.Time,
	commitHistory bool,
) *HeatScoreData {
	if data == nil {
		return nil
	}

	marketRaw := computeMarketHeatRaw(data)
	trendRaw := computeTrendHeatRaw(data)
	volumeSpikeRaw, volumeSpikeAuditState, volumeSpikeContext, volumeSpikeAvailable := computeVolumeSpikeRaw(data)
	donchianRaw, donchianAvailable := computeDonchianPositionRaw(data)
	mtfResonanceRaw, mtfResonanceAvailable := computeMTFResonanceRaw(
		volumeSpikeContext.short,
		volumeSpikeContext.medium,
		volumeSpikeContext.shortOK,
		volumeSpikeContext.mediumOK,
	)
	quantOIRaw, quantImbalanceRaw, quantNetflowRaw, quantOIAvailable, quantImbalanceAvailable, quantNetflowAvailable := computeQuantSubfactorRaw(data, quant)
	quantAvailable := quantOIAvailable || quantImbalanceAvailable || quantNetflowAvailable
	onChainRatioRaw, onChainBuyRaw, onChainRatioAvailable, onChainBuyAvailable := computeOnChainSubfactorRaw(data)
	onChainAvailable := onChainRatioAvailable || onChainBuyAvailable
	socialRankRaw, socialUpvoteRaw, socialRankAvailable, socialUpvoteAvailable := computeSocialSubfactorRaw(data)
	socialAvailable := socialRankAvailable || socialUpvoteAvailable

	marketZ := scoreSource(symbol, "market", marketRaw, now, commitHistory)
	trendCoreZ := scoreSource(symbol, "trend", trendRaw, now, commitHistory)
	donchianZ := 0.0
	if donchianAvailable {
		donchianZ = scoreSource(symbol, "donchian_factor", donchianRaw, now, commitHistory)
	}
	volumeSpikeCoreZ := 0.0
	if volumeSpikeAvailable {
		volumeSpikeCoreZ = scoreSource(symbol, "volume_spike", volumeSpikeRaw, now, commitHistory)
	}
	mtfResonanceZ := 0.0
	if mtfResonanceAvailable {
		mtfResonanceZ = scoreSource(symbol, "mtf_resonance", mtfResonanceRaw, now, commitHistory)
	}
	quantOIZ := 0.0
	if quantOIAvailable {
		quantOIZ = scoreSource(symbol, "quant_oi", quantOIRaw, now, commitHistory)
	}
	quantImbalanceZ := 0.0
	if quantImbalanceAvailable {
		quantImbalanceZ = scoreSource(symbol, "quant_imbalance", quantImbalanceRaw, now, commitHistory)
	}
	quantNetflowZ := 0.0
	if quantNetflowAvailable {
		quantNetflowZ = scoreSource(symbol, "quant_netflow", quantNetflowRaw, now, commitHistory)
	}
	onChainRatioZ := 0.0
	if onChainRatioAvailable {
		onChainRatioZ = scoreSource(symbol, "onchain_ratio", onChainRatioRaw, now, commitHistory)
	}
	onChainBuyZ := 0.0
	if onChainBuyAvailable {
		onChainBuyZ = scoreSource(symbol, "onchain_buy_ratio", onChainBuyRaw, now, commitHistory)
	}
	socialRankZ := 0.0
	if socialRankAvailable {
		socialRankZ = scoreSource(symbol, "social_rank", socialRankRaw, now, commitHistory)
	}
	socialUpvoteZ := 0.0
	if socialUpvoteAvailable {
		socialUpvoteZ = scoreSource(symbol, "social_upvote", socialUpvoteRaw, now, commitHistory)
	}

	adaptiveState := GetAdaptiveWeightState(traderID, data.Sector, symbol)
	adaptiveWeights := adaptiveWeightsFromState(adaptiveState)
	marketWeight := adaptiveWeights["market"]
	trendWeight := adaptiveWeights["trend"]
	volumeSpikeWeight := adaptiveWeights["volume_spike"]
	quantWeight := adaptiveWeights["quant"]
	onChainWeight := adaptiveWeights["onchain"]
	socialWeight := adaptiveWeights["social"]
	nestedWeights := adaptiveNestedWeightsFromState(adaptiveState)
	factorStates := adaptiveFactorStatesByName(adaptiveState.Factors, adaptiveState.HiddenFactors)
	quantFactorState := adaptiveFactorStateByName(adaptiveState.Factors, "quant")
	socialFactor := adaptiveFactorStateByName(adaptiveState.Factors, "social")
	onChainFactorState := adaptiveFactorStateByName(adaptiveState.Factors, "onchain")
	volumeSpikeFactorState := adaptiveFactorStateByName(adaptiveState.Factors, "volume_spike")
	donchianFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "donchian_factor")
	mtfResonanceFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "mtf_resonance")
	quantOIFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "quant_oi")
	quantImbalanceFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "quant_imbalance")
	quantNetflowFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "quant_netflow")
	socialRankFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "social_rank")
	socialUpvoteFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "social_upvote")
	onChainRatioFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "onchain_ratio")
	onChainBuyFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "onchain_buy_ratio")

	trendNestedWeights := normalizedNestedSubweights(
		nestedWeights,
		[]string{"trend", "donchian_factor"},
		map[string]bool{
			"trend":           true,
			"donchian_factor": donchianAvailable,
		},
	)
	trendNested := nestedFactorScoreFromZScores(trendNestedWeights, map[string]float64{
		"trend":           trendCoreZ,
		"donchian_factor": adjustedZScoreForAdaptiveFactor(symbol, "donchian_factor", donchianZ, factorStates),
	})

	volumeSpikeNestedWeights := normalizedNestedSubweights(
		nestedWeights,
		[]string{"volume_spike", "mtf_resonance"},
		map[string]bool{
			"volume_spike":  volumeSpikeAvailable,
			"mtf_resonance": mtfResonanceAvailable,
		},
	)
	volumeSpikeNested := nestedFactorScoreFromZScores(volumeSpikeNestedWeights, map[string]float64{
		"volume_spike":  volumeSpikeCoreZ,
		"mtf_resonance": adjustedZScoreForAdaptiveFactor(symbol, "mtf_resonance", mtfResonanceZ, factorStates),
	})
	quantNestedWeights := normalizedNestedSubweights(
		nestedWeights,
		[]string{"quant_oi", "quant_imbalance", "quant_netflow"},
		map[string]bool{
			"quant_oi":        quantOIAvailable,
			"quant_imbalance": quantImbalanceAvailable,
			"quant_netflow":   quantNetflowAvailable,
		},
	)
	quantNested := nestedFactorScoreFromZScores(quantNestedWeights, map[string]float64{
		"quant_oi":        adjustedZScoreForAdaptiveFactor(symbol, "quant_oi", quantOIZ, factorStates),
		"quant_imbalance": adjustedZScoreForAdaptiveFactor(symbol, "quant_imbalance", quantImbalanceZ, factorStates),
		"quant_netflow":   adjustedZScoreForAdaptiveFactor(symbol, "quant_netflow", quantNetflowZ, factorStates),
	})
	socialNestedWeights := normalizedNestedSubweights(
		nestedWeights,
		[]string{"social_rank", "social_upvote"},
		map[string]bool{
			"social_rank":   socialRankAvailable,
			"social_upvote": socialUpvoteAvailable,
		},
	)
	socialNested := nestedFactorScoreFromZScores(socialNestedWeights, map[string]float64{
		"social_rank":   adjustedZScoreForAdaptiveFactor(symbol, "social_rank", socialRankZ, factorStates),
		"social_upvote": adjustedZScoreForAdaptiveFactor(symbol, "social_upvote", socialUpvoteZ, factorStates),
	})
	onChainNestedWeights := normalizedNestedSubweights(
		nestedWeights,
		[]string{"onchain_ratio", "onchain_buy_ratio"},
		map[string]bool{
			"onchain_ratio":     onChainRatioAvailable,
			"onchain_buy_ratio": onChainBuyAvailable,
		},
	)
	onChainNested := nestedFactorScoreFromZScores(onChainNestedWeights, map[string]float64{
		"onchain_ratio":     adjustedZScoreForAdaptiveFactor(symbol, "onchain_ratio", onChainRatioZ, factorStates),
		"onchain_buy_ratio": adjustedZScoreForAdaptiveFactor(symbol, "onchain_buy_ratio", onChainBuyZ, factorStates),
	})

	tradingWeights := map[string]float64{
		"market": marketWeight,
		"trend":  trendWeight,
	}
	tradingScores := map[string]float64{
		"market": adjustedZScoreForAdaptiveFactor(symbol, "market", marketZ, factorStates),
		"trend":  adjustedZScoreForAdaptiveFactor(symbol, "trend", trendNested.zScore, factorStates),
	}
	tradingWeight := marketWeight + trendWeight
	if volumeSpikeAvailable {
		tradingWeights["volume_spike"] = volumeSpikeWeight
		tradingScores["volume_spike"] = adjustedZScoreForAdaptiveFactor(symbol, "volume_spike", volumeSpikeNested.zScore, factorStates)
		tradingWeight += volumeSpikeWeight
	}
	totalWeight := tradingWeight
	if quantAvailable {
		totalWeight += quantWeight
	}
	if onChainAvailable {
		totalWeight += onChainWeight
	}
	if socialAvailable {
		totalWeight += socialWeight
	}

	if totalWeight <= 0 {
		return &HeatScoreData{
			CompositeScore:          50,
			TradingScore:            50,
			QuantScore:              0,
			MarketScore:             50,
			TrendScore:              50,
			VolumeSpikeScore:        50,
			DonchianFactorScore:     50,
			MTFResonanceFactorScore: 50,
			QuantFactorScore:        50,
			SocialScore:             50,
			OnChainScore:            50,
			SourceWeights: map[string]float64{
				"social":       0,
				"onchain":      0,
				"market":       0,
				"trend":        0,
				"volume_spike": 0,
				"quant":        0,
			},
			RawFactorScores:    map[string]float64{},
			RawFactorAvailable: map[string]bool{},
		}
	}

	tradingScore := combineWeightedScores(tradingWeights, tradingScores)

	quantScore := 0.0
	quantWeights := map[string]float64{}
	quantScores := map[string]float64{}
	if quantAvailable {
		quantWeights["quant"] = quantWeight
		quantScores["quant"] = adjustedZScoreForAdaptiveFactor(symbol, "quant", quantNested.zScore, factorStates)
	}
	if onChainAvailable {
		quantWeights["onchain"] = onChainWeight
		quantScores["onchain"] = adjustedZScoreForAdaptiveFactor(symbol, "onchain", onChainNested.zScore, factorStates)
	}
	if socialAvailable {
		quantWeights["social"] = socialWeight
		quantScores["social"] = adjustedZScoreForAdaptiveFactor(symbol, "social", socialNested.zScore, factorStates)
	}
	if len(quantWeights) > 0 {
		quantScore = combineWeightedScores(quantWeights, quantScores)
	}

	composite := tradingScore
	if len(quantWeights) > 0 {
		composite = (tradingScore*tradingWeight + quantScore*(totalWeight-tradingWeight)) / totalWeight
	}
	if len(quantWeights) == 0 {
		composite = tradingScore
	}

	sourceWeights := map[string]float64{
		"social":       0,
		"onchain":      0,
		"market":       marketWeight / totalWeight,
		"trend":        trendWeight / totalWeight,
		"volume_spike": 0,
		"quant":        0,
	}
	if volumeSpikeAvailable {
		sourceWeights["volume_spike"] = volumeSpikeWeight / totalWeight
	}
	if quantAvailable {
		sourceWeights["quant"] = quantWeight / totalWeight
	}
	if socialAvailable {
		sourceWeights["social"] = socialWeight / totalWeight
	}
	if onChainAvailable {
		sourceWeights["onchain"] = onChainWeight / totalWeight
	}

	logger.Infof("V3_AUDIT_BAYES_IC: Symbol=%s, Sector=%s, Alpha=%.2f, BlendAdaptive=%.2f, SectorSamples=%d, CoinSamples=%d, Sector_Social_IC=%.2f, Coin_Social_IC=%.2f, Final_Social_W=%.2f",
		symbol,
		data.Sector,
		adaptiveState.Alpha,
		adaptiveState.BlendAdaptive,
		adaptiveState.SectorSampleCount,
		adaptiveState.CoinSampleCount,
		socialFactor.SectorIC,
		socialFactor.CoinIC,
		socialFactor.FinalWeight,
	)
	logger.Infof("V3_AUDIT_ADAPTIVE_SPIKE: Symbol=%s, Sector=%s, Alpha=%.2f, Sector_IC=%.2f, Coin_IC=%.2f, Final_IC=%.2f, Final_W=%.2f",
		symbol,
		data.Sector,
		adaptiveState.Alpha,
		volumeSpikeFactorState.SectorIC,
		volumeSpikeFactorState.CoinIC,
		volumeSpikeFactorState.FinalIC,
		volumeSpikeFactorState.FinalWeight,
	)
	logger.Infof("V3_AUDIT_SUBFACTOR: Symbol=%s, Trend_W=%.2f, Donchian_W=%.2f, Spike_W=%.2f, MTF_W=%.2f",
		symbol,
		trendNestedWeights["trend"],
		trendNestedWeights["donchian_factor"],
		volumeSpikeNestedWeights["volume_spike"],
		volumeSpikeNestedWeights["mtf_resonance"],
	)
	logger.Infof("V3_AUDIT_SUB_SOCIAL: Symbol=%s, Rank_W=%.2f (IC:%.2f), Upvote_W=%.2f (IC:%.2f)",
		symbol,
		socialNestedWeights["social_rank"],
		socialRankFactorState.FinalIC,
		socialNestedWeights["social_upvote"],
		socialUpvoteFactorState.FinalIC,
	)
	logger.Infof("V3_AUDIT_FULL_NESTED: Symbol=%s, Group=Trend, SubWeights=[Trend:%.2f, Donchian:%.2f]",
		symbol,
		trendNestedWeights["trend"],
		trendNestedWeights["donchian_factor"],
	)
	logger.Infof("V3_AUDIT_FULL_NESTED: Symbol=%s, Group=VolumeSpike, SubWeights=[Core:%.2f, Resonance:%.2f]",
		symbol,
		volumeSpikeNestedWeights["volume_spike"],
		volumeSpikeNestedWeights["mtf_resonance"],
	)
	logger.Infof("V3_AUDIT_FULL_NESTED: Symbol=%s, Group=Quant, SubWeights=[OI:%.2f, Imb:%.2f, Net:%.2f]",
		symbol,
		quantNestedWeights["quant_oi"],
		quantNestedWeights["quant_imbalance"],
		quantNestedWeights["quant_netflow"],
	)
	logger.Infof("V3_AUDIT_FULL_NESTED: Symbol=%s, Group=Social, SubWeights=[Rank:%.2f, Upvote:%.2f]",
		symbol,
		socialNestedWeights["social_rank"],
		socialNestedWeights["social_upvote"],
	)
	logger.Infof("V3_AUDIT_FULL_NESTED: Symbol=%s, Group=OnChain, SubWeights=[Ratio:%.2f, Buy:%.2f]",
		symbol,
		onChainNestedWeights["onchain_ratio"],
		onChainNestedWeights["onchain_buy_ratio"],
	)
	volumeSpikeScore := volumeSpikeNested.score
	if volumeSpikeAvailable {
		logger.Infof("V3_AUDIT_SPIKE: Symbol=%s, Z_Vol=%.2f, Z_Price=%.2f, Final_Spike=%.1f",
			symbol,
			volumeSpikeAuditState.VolumeZ,
			volumeSpikeAuditState.PriceZ,
			volumeSpikeScore,
		)
	}
	if donchianAvailable || mtfResonanceAvailable {
		logger.Debugf("V3_AUDIT_SUBFACTOR_IC: Symbol=%s, Donchian_IC=%.2f, Donchian_W=%.2f, MTF_IC=%.2f, MTF_W=%.2f",
			symbol,
			donchianFactorState.FinalIC,
			trendNestedWeights["donchian_factor"],
			mtfResonanceFactorState.FinalIC,
			volumeSpikeNestedWeights["mtf_resonance"],
		)
	}
	if quantAvailable {
		logger.Debugf("V3_AUDIT_SUBFACTOR_IC: Symbol=%s, Quant_IC=%.2f, OI_IC=%.2f, Imbalance_IC=%.2f, Netflow_IC=%.2f",
			symbol,
			quantFactorState.FinalIC,
			quantOIFactorState.FinalIC,
			quantImbalanceFactorState.FinalIC,
			quantNetflowFactorState.FinalIC,
		)
	}
	if onChainAvailable {
		logger.Debugf("V3_AUDIT_SUBFACTOR_IC: Symbol=%s, OnChain_IC=%.2f, Ratio_IC=%.2f, Buy_IC=%.2f",
			symbol,
			onChainFactorState.FinalIC,
			onChainRatioFactorState.FinalIC,
			onChainBuyFactorState.FinalIC,
		)
	}
	logger.Infof("V3_AUDIT_HEAT: %s, Raw_Market=%.2f, Z_Market=%.2f, Final_Score=%.1f", symbol, marketRaw, marketZ, composite)

	return &HeatScoreData{
		CompositeScore:          clamp(composite, 0, 100),
		TradingScore:            clamp(tradingScore, 0, 100),
		QuantScore:              clamp(quantScore, 0, 100),
		MarketScore:             clamp(zScoreToPercent(marketZ), 0, 100),
		TrendScore:              trendNested.score,
		VolumeSpikeScore:        volumeSpikeScore,
		DonchianFactorScore:     clamp(zScoreToPercent(donchianZ), 0, 100),
		MTFResonanceFactorScore: clamp(zScoreToPercent(mtfResonanceZ), 0, 100),
		QuantFactorScore:        quantNested.score,
		QuantOIRaw:              quantOIRaw,
		QuantImbalanceRaw:       quantImbalanceRaw,
		QuantNetflowRaw:         quantNetflowRaw,
		SocialScore:             socialNested.score,
		OnChainScore:            onChainNested.score,
		OnChainRatioRaw:         onChainRatioRaw,
		OnChainBuyRaw:           onChainBuyRaw,
		SourceWeights:           sourceWeights,
		RawFactorScores: map[string]float64{
			"market":             marketZ,
			"trend":              trendCoreZ,
			"trend_group":        trendNested.zScore,
			"donchian_factor":    donchianZ,
			"volume_spike":       volumeSpikeCoreZ,
			"volume_spike_group": volumeSpikeNested.zScore,
			"mtf_resonance":      mtfResonanceZ,
			"quant_group":        quantNested.zScore,
			"quant_oi":           quantOIZ,
			"quant_imbalance":    quantImbalanceZ,
			"quant_netflow":      quantNetflowZ,
			"social_group":       socialNested.zScore,
			"social_rank":        socialRankZ,
			"social_upvote":      socialUpvoteZ,
			"onchain_group":      onChainNested.zScore,
			"onchain_ratio":      onChainRatioZ,
			"onchain_buy_ratio":  onChainBuyZ,
		},
		RawFactorAvailable: map[string]bool{
			"market":             true,
			"trend":              true,
			"trend_group":        true,
			"donchian_factor":    donchianAvailable,
			"volume_spike":       volumeSpikeAvailable,
			"volume_spike_group": volumeSpikeAvailable || mtfResonanceAvailable,
			"mtf_resonance":      mtfResonanceAvailable,
			"quant_group":        quantAvailable,
			"quant_oi":           quantOIAvailable,
			"quant_imbalance":    quantImbalanceAvailable,
			"quant_netflow":      quantNetflowAvailable,
			"social_group":       socialAvailable,
			"social_rank":        socialRankAvailable,
			"social_upvote":      socialUpvoteAvailable,
			"onchain_group":      onChainAvailable,
			"onchain_ratio":      onChainRatioAvailable,
			"onchain_buy_ratio":  onChainBuyAvailable,
		},
	}
}

func scoreSource(symbol, source string, raw float64, now time.Time, commitHistory bool) float64 {
	if !isFinite(raw) {
		raw = 0
	}
	key := symbol + ":" + source
	series := getHeatSeries(key)
	history := series.history(now)
	z := CalculateZScore(raw, history)
	if commitHistory {
		series.append(now, raw)
	}
	return z
}

func adaptiveFactorStateByName(factors []AdaptiveFactorState, name string) AdaptiveFactorState {
	for _, factor := range factors {
		if factor.Name == name {
			return factor
		}
	}
	return AdaptiveFactorState{Name: name}
}

func adaptiveFactorStatesByName(groups ...[]AdaptiveFactorState) map[string]AdaptiveFactorState {
	states := make(map[string]AdaptiveFactorState)
	for _, factors := range groups {
		for _, factor := range factors {
			states[factor.Name] = factor
		}
	}
	return states
}

func adjustedZScoreForAdaptiveFactor(symbol, factor string, originalZ float64, states map[string]AdaptiveFactorState) float64 {
	state := states[factor]
	if state.Name == "" {
		state.Name = factor
	}
	if state.FinalIC >= 0 || math.Abs(originalZ) <= 1e-12 {
		return originalZ
	}

	invertedZ := -originalZ
	logger.Infof("V3_AUDIT_INVERSION: Symbol=%s, Factor=%s, IC=%.2f, OriginalZ=%.2f, InvertedZ=%.2f",
		symbol,
		state.Name,
		state.FinalIC,
		originalZ,
		invertedZ,
	)
	return invertedZ
}

func EnsureHeatHistoryPreloaded(symbol string, primaryTimeframe string) {
	onceValue, _ := heatWarmupMap.LoadOrStore(symbol, &sync.Once{})
	onceValue.(*sync.Once).Do(func() {
		preloadHeatHistory(symbol, primaryTimeframe)
	})
}

func combineWeightedScores(weights map[string]float64, zScores map[string]float64) float64 {
	totalWeight := 0.0
	accumulator := 0.0
	for key, weight := range weights {
		totalWeight += weight
		accumulator += zScoreToPercent(zScores[key]) * weight
	}
	if totalWeight <= 0 {
		return 0
	}
	return accumulator / totalWeight
}

func zScoreToPercent(z float64) float64 {
	return 50 + 50*math.Tanh(z/2)
}

func combineWeightedZScores(weights map[string]float64, zScores map[string]float64) float64 {
	totalWeight := 0.0
	accumulator := 0.0
	for key, weight := range weights {
		totalWeight += weight
		accumulator += zScores[key] * weight
	}
	if totalWeight <= 0 {
		return 0
	}
	return accumulator / totalWeight
}

func normalizedNestedSubweights(rawWeights map[string]float64, order []string, available map[string]bool) map[string]float64 {
	weights := make(map[string]float64, len(order))
	sum := 0.0
	availableCount := 0

	for _, factor := range order {
		if !available[factor] {
			weights[factor] = 0
			continue
		}
		availableCount++
		value := rawWeights[factor]
		if value < 0 {
			value = 0
		}
		weights[factor] = value
		sum += value
	}

	if availableCount == 0 {
		return weights
	}
	if sum <= 0 {
		equalWeight := 1.0 / float64(availableCount)
		for _, factor := range order {
			if available[factor] {
				weights[factor] = equalWeight
			}
		}
		return weights
	}

	for _, factor := range order {
		if available[factor] {
			weights[factor] /= sum
		}
	}
	return weights
}

func nestedFactorScoreFromZScores(weights map[string]float64, zScores map[string]float64) nestedFactorScore {
	zScore := combineWeightedZScores(weights, zScores)
	return nestedFactorScore{
		zScore: zScore,
		score:  clamp(zScoreToPercent(zScore), 0, 100),
	}
}

func computeVolumeSpikeRaw(data *Data) (float64, volumeSpikeAudit, volumeSpikeContext, bool) {
	if data == nil || len(data.TimeframeData) == 0 {
		return 0, volumeSpikeAudit{}, volumeSpikeContext{}, false
	}

	shortSnapshot, shortOK := computeTimeframeSpikeSnapshot(data.TimeframeData["5m"])
	mediumSnapshot, mediumOK := computeTimeframeSpikeSnapshot(data.TimeframeData["15m"])
	if !shortOK && !mediumOK {
		return 0, volumeSpikeAudit{}, volumeSpikeContext{}, false
	}

	volumeZ := weightedTimeframeAverage(shortSnapshot.volumeZ, mediumSnapshot.volumeZ, shortOK, mediumOK)
	priceZ := weightedTimeframeAverage(shortSnapshot.priceZ, mediumSnapshot.priceZ, shortOK, mediumOK)
	directionSlope := weightedTimeframeAverage(shortSnapshot.slopePct, mediumSnapshot.slopePct, shortOK, mediumOK)
	raw := weightedTimeframeAverage(shortSnapshot.raw, mediumSnapshot.raw, shortOK, mediumOK)

	return raw, volumeSpikeAudit{
			VolumeZ:           volumeZ,
			PriceZ:            priceZ,
			DirectionSlopePct: directionSlope,
			ShortVolumeZ:      shortSnapshot.volumeZ,
			MediumVolumeZ:     mediumSnapshot.volumeZ,
			ShortPriceZ:       shortSnapshot.priceZ,
			MediumPriceZ:      mediumSnapshot.priceZ,
			Raw:               raw,
		}, volumeSpikeContext{
			short:    shortSnapshot,
			medium:   mediumSnapshot,
			shortOK:  shortOK,
			mediumOK: mediumOK,
		}, true
}

func computeTimeframeSpikeSnapshot(tfData *TimeframeSeriesData) (timeframeSpikeSnapshot, bool) {
	if tfData == nil || len(tfData.Klines) < volumeSpikeMinBars {
		return timeframeSpikeSnapshot{}, false
	}

	currentVolume := tfData.Klines[len(tfData.Klines)-1].Volume
	if !isFinite(currentVolume) || currentVolume <= 0 {
		return timeframeSpikeSnapshot{}, false
	}

	volumeHistory := make([]float64, 0, len(tfData.Klines)-1)
	priceHistory := make([]float64, 0, len(tfData.Klines)-2)
	for i := 0; i < len(tfData.Klines)-1; i++ {
		if isFinite(tfData.Klines[i].Volume) && tfData.Klines[i].Volume >= 0 {
			volumeHistory = append(volumeHistory, tfData.Klines[i].Volume)
		}
	}
	for i := 1; i < len(tfData.Klines)-1; i++ {
		priceChange, ok := closeChangePct(tfData.Klines[i-1].Close, tfData.Klines[i].Close)
		if ok {
			priceHistory = append(priceHistory, priceChange)
		}
	}
	if len(volumeHistory) < 3 || len(priceHistory) < 3 {
		return timeframeSpikeSnapshot{}, false
	}

	currentSlope, ok := closeChangePct(tfData.Klines[len(tfData.Klines)-2].Close, tfData.Klines[len(tfData.Klines)-1].Close)
	if !ok {
		return timeframeSpikeSnapshot{}, false
	}

	volumeZ := CalculateZScore(currentVolume, volumeHistory)
	priceZ := CalculateZScore(currentSlope, priceHistory)
	if currentSlope <= 0 {
		return timeframeSpikeSnapshot{
			volumeZ:  volumeZ,
			priceZ:   priceZ,
			slopePct: currentSlope,
			raw:      0,
		}, true
	}

	positiveVolumeZ := math.Max(volumeZ, 0)
	positivePriceZ := math.Max(priceZ, 0)
	raw := positiveVolumeZ*0.6 + positivePriceZ*0.4

	return timeframeSpikeSnapshot{
		volumeZ:  volumeZ,
		priceZ:   priceZ,
		slopePct: currentSlope,
		raw:      raw,
	}, true
}

func closeChangePct(prevClose, currentClose float64) (float64, bool) {
	if !isFinite(prevClose) || !isFinite(currentClose) || prevClose <= 0 {
		return 0, false
	}
	return ((currentClose - prevClose) / prevClose) * 100, true
}

func weightedTimeframeAverage(shortValue, mediumValue float64, shortOK, mediumOK bool) float64 {
	switch {
	case shortOK && mediumOK:
		return shortValue*volumeSpikeShortWeight + mediumValue*volumeSpikeMediumWeight
	case shortOK:
		return shortValue
	case mediumOK:
		return mediumValue
	default:
		return 0
	}
}

func computeDonchianPositionRaw(data *Data) (float64, bool) {
	if data == nil {
		return 0, false
	}

	selected, ok := selectPrimaryDonchian(data.Indicators.Donchians)
	if !ok {
		return 0, false
	}
	span := selected.Upper - selected.Lower
	if span <= 0 {
		return 0, false
	}
	return (data.CurrentPrice - selected.Lower) / span, true
}

func computeMTFResonanceRaw(shortSnapshot, mediumSnapshot timeframeSpikeSnapshot, shortOK, mediumOK bool) (float64, bool) {
	if !shortOK || !mediumOK {
		return 0, false
	}
	if shortSnapshot.slopePct <= 0 || mediumSnapshot.slopePct <= 0 {
		return 0, true
	}
	if shortSnapshot.priceZ <= 0 || mediumSnapshot.priceZ <= 0 {
		return 0, true
	}

	shortVolumeZ := math.Max(shortSnapshot.volumeZ, 0)
	mediumVolumeZ := math.Max(mediumSnapshot.volumeZ, 0)
	if shortVolumeZ <= 0 || mediumVolumeZ <= 0 {
		return 0, true
	}

	return shortVolumeZ * mediumVolumeZ, true
}

func selectPrimaryDonchian(boxes map[int]DonchianResult) (DonchianResult, bool) {
	if len(boxes) == 0 {
		return DonchianResult{}, false
	}

	period := 0
	selected := DonchianResult{}
	for candidatePeriod, box := range boxes {
		if candidatePeriod <= 0 {
			continue
		}
		if period == 0 || candidatePeriod < period {
			period = candidatePeriod
			selected = box
		}
	}
	if period == 0 || selected.Upper <= 0 || selected.Lower <= 0 {
		return DonchianResult{}, false
	}
	return selected, true
}

func computeMarketHeatRaw(data *Data) float64 {
	raw := math.Abs(data.PriceChange1h)*0.4 + math.Abs(data.PriceChange4h)*0.25

	if data.OpenInterest != nil && data.OpenInterest.Average > 0 {
		raw += math.Abs((data.OpenInterest.Latest-data.OpenInterest.Average)/data.OpenInterest.Average) * 100 * 0.2
	}

	if data.Orderbook != nil {
		raw += math.Abs(data.Orderbook.Imbalance) * 100 * 0.1
	}

	if data.VolatilityUtilization > 0 {
		raw += data.VolatilityUtilization * 100 * 0.05
	}

	return raw
}

func computeTrendHeatRaw(data *Data) float64 {
	raw := data.VolatilityUtilization * 100
	if raw < 0 {
		raw = 0
	}

	if len(data.Indicators.EMAs) >= 2 {
		minEMA := math.MaxFloat64
		maxEMA := -math.MaxFloat64
		for _, value := range data.Indicators.EMAs {
			if value <= 0 {
				continue
			}
			if value < minEMA {
				minEMA = value
			}
			if value > maxEMA {
				maxEMA = value
			}
		}
		if maxEMA > 0 && minEMA < math.MaxFloat64 {
			raw += math.Abs((maxEMA-minEMA)/maxEMA) * 100 * 10
		}
	}

	return raw
}

func computeQuantSubfactorRaw(data *Data, quant *nofxos.QuantData) (float64, float64, float64, bool, bool, bool) {
	oiRaw, oiAvailable := computeQuantOIRaw(quant)
	imbalanceRaw, imbalanceAvailable := computeQuantImbalanceRaw(data)
	netflowRaw, netflowAvailable := computeQuantNetflowRaw(quant)
	return oiRaw, imbalanceRaw, netflowRaw, oiAvailable, imbalanceAvailable, netflowAvailable
}

func computeQuantOIRaw(quant *nofxos.QuantData) (float64, bool) {
	if quant == nil || len(quant.OI) == 0 {
		return 0, false
	}

	exchanges := make([]string, 0, len(quant.OI))
	for exchange := range quant.OI {
		exchanges = append(exchanges, exchange)
	}
	sort.Strings(exchanges)

	total := 0.0
	used := 0
	for _, exchange := range exchanges {
		oi := quant.OI[exchange]
		if oi == nil || oi.Delta == nil {
			continue
		}
		delta := oi.Delta["1h"]
		if delta == nil || !isFinite(delta.OIDeltaPercent) {
			continue
		}
		total += delta.OIDeltaPercent
		used++
	}
	if used == 0 {
		return 0, false
	}
	return total / float64(used), true
}

func computeQuantImbalanceRaw(data *Data) (float64, bool) {
	if data == nil || data.Orderbook == nil || !isFinite(data.Orderbook.Imbalance) {
		return 0, false
	}
	return data.Orderbook.Imbalance, true
}

func computeQuantNetflowRaw(quant *nofxos.QuantData) (float64, bool) {
	if quant == nil || quant.Netflow == nil {
		return 0, false
	}

	netflow := 0.0
	if quant.Netflow.Institution != nil {
		netflow += sumFlow(quant.Netflow.Institution.Future, "1h", "4h")
		netflow += sumFlow(quant.Netflow.Institution.Spot, "1h", "4h")
	}
	if quant.Netflow.Personal != nil {
		netflow += sumFlow(quant.Netflow.Personal.Future, "1h", "4h")
		netflow += sumFlow(quant.Netflow.Personal.Spot, "1h", "4h")
	}
	if !isFinite(netflow) || netflow == 0 {
		return 0, false
	}

	return math.Copysign(math.Log10(1+math.Abs(netflow)), netflow), true
}

func computeOnChainSubfactorRaw(data *Data) (float64, float64, bool, bool) {
	if data == nil || data.DexScreener == nil {
		return 0, 0, false, false
	}

	dex := data.DexScreener
	if dex.LiquidityUSD < MinDexLiquidityUSD {
		return 0, 0, false, false
	}

	ratioRaw := 0.0
	ratioAvailable := false
	if isFinite(dex.OnchainToCEXRatio) && dex.OnchainToCEXRatio > 0 {
		ratioRaw = math.Min(dex.OnchainToCEXRatio, 3.0)
		ratioAvailable = true
	}

	buyRaw := 0.0
	buyAvailable := false
	if dex.BuyTxnsH1+dex.SellTxnsH1 > 0 && isFinite(dex.BuyRatio) {
		buyRaw = dex.BuyRatio
		buyAvailable = true
	}

	return ratioRaw, buyRaw, ratioAvailable, buyAvailable
}

func SocialSubfactorRawValues(sentiment *GeckoSentimentData) (float64, float64) {
	if sentiment == nil {
		return 0, 0
	}

	rankRaw := sentiment.TrendingRankScore
	if !isFinite(rankRaw) || rankRaw < 0 {
		rankRaw = 0
	}

	upvoteRaw := sentiment.SentimentVotesUpPercentage
	if !isFinite(upvoteRaw) || upvoteRaw < 0 {
		upvoteRaw = 0
	}

	return rankRaw, upvoteRaw
}

func computeSocialSubfactorRaw(data *Data) (float64, float64, bool, bool) {
	if data == nil || data.GeckoSentiment == nil {
		return 0, 0, false, false
	}

	rankRaw, upvoteRaw := SocialSubfactorRawValues(data.GeckoSentiment)
	return rankRaw, upvoteRaw, isFinite(rankRaw), isFinite(upvoteRaw)
}

func preloadHeatHistory(symbol string, primaryTimeframe string) {
	now := time.Now().UTC()
	end := now
	start := end.Add(-HeatRollingWindow).Add(-4 * time.Hour)

	timeframe := "1h"

	klines, err := GetKlinesRange(symbol, timeframe, start, end)
	if err != nil || len(klines) < VolUtilLookback+5 {
		logger.Infof("V3_AUDIT_WARMUP: Symbol=%s, InjectedSamples=%d, InitialZ=%.2f", symbol, 0, 0.0)
		return
	}

	normTF, err := NormalizeTimeframe(timeframe)
	if err == nil {
		timeframe = normTF
	}

	oiSeries, _ := fetchOpenInterestHistorySeries(symbol, normalizeOIPeriod(timeframe), 30)

	marketSamples := make([]heatSample, 0, 24)
	trendSamples := make([]heatSample, 0, 24)
	donchianSamples := make([]heatSample, 0, 24)
	quantSamples := make([]heatSample, 0, 24)

	startIdx := max(VolUtilLookback+4, len(klines)-24)
	for i := startIdx; i < len(klines); i++ {
		window := klines[:i+1]
		current := window[len(window)-1]

		synthetic := &Data{
			Symbol:        symbol,
			CurrentPrice:  current.Close,
			PriceChange1h: historicalPriceChange(window, 1),
			PriceChange4h: historicalPriceChange(window, 4),
			Indicators:    buildIndicatorSnapshot(window, defaultHeatWarmupIndicatorConfig()),
			OpenInterest:  syntheticOIData(oiSeries, i-startIdx),
		}

		atr := calculateATR(window, VolUtilLookback)
		synthetic.VolatilityUtilization = CalculateVolatilityUtilization(symbol, window, atr)

		timestamp := time.UnixMilli(current.CloseTime).UTC()
		marketSamples = append(marketSamples, heatSample{
			timestamp: timestamp,
			value:     computeMarketHeatRaw(synthetic),
		})
		trendSamples = append(trendSamples, heatSample{
			timestamp: timestamp,
			value:     computeTrendHeatRaw(synthetic),
		})
		if donchianRaw, ok := computeDonchianPositionRaw(synthetic); ok {
			donchianSamples = append(donchianSamples, heatSample{
				timestamp: timestamp,
				value:     donchianRaw,
			})
		}

		if oi := synthetic.OpenInterest; oi != nil && oi.Average > 0 {
			quantRaw := math.Abs(synthetic.PriceChange1h)*0.35 +
				math.Abs(synthetic.PriceChange4h)*0.2 +
				math.Abs((oi.Latest-oi.Average)/oi.Average)*100*0.25
			quantSamples = append(quantSamples, heatSample{
				timestamp: timestamp,
				value:     quantRaw,
			})
		}
	}

	getHeatSeries(symbol + ":market").appendBatch(applyEWMAWarmupDecay(marketSamples))
	getHeatSeries(symbol + ":trend").appendBatch(applyEWMAWarmupDecay(trendSamples))
	if len(donchianSamples) > 0 {
		getHeatSeries(symbol + ":donchian_factor").appendBatch(applyEWMAWarmupDecay(donchianSamples))
	}
	if len(quantSamples) > 0 {
		getHeatSeries(symbol + ":quant").appendBatch(applyEWMAWarmupDecay(quantSamples))
	}

	initialZ := 0.0
	if len(marketSamples) > 1 {
		history := make([]float64, 0, len(marketSamples)-1)
		for _, sample := range marketSamples[:len(marketSamples)-1] {
			history = append(history, sample.value)
		}
		initialZ = CalculateZScore(marketSamples[len(marketSamples)-1].value, history)
	}

	logger.Infof("V3_AUDIT_WARMUP: Symbol=%s, InjectedSamples=%d, InitialZ=%.2f", symbol, len(marketSamples), initialZ)
}

func applyEWMAWarmupDecay(samples []heatSample) []heatSample {
	if len(samples) == 0 {
		return nil
	}

	alpha := 2.0 / float64(len(samples)+1)
	weighted := make([]heatSample, 0, len(samples))
	weight := math.Pow(1-alpha, float64(len(samples)-1))
	for _, sample := range samples {
		weighted = append(weighted, heatSample{
			timestamp: sample.timestamp,
			value:     sample.value * weight,
		})
		weight /= (1 - alpha)
	}
	return weighted
}

func defaultHeatWarmupIndicatorConfig() store.IndicatorConfig {
	return store.IndicatorConfig{
		EnableEMA:         true,
		EnableRSI:         true,
		EnableATR:         true,
		EnableDonchianBox: true,
		EMAPeriods:        []int{20, 50},
		RSIPeriods:        []int{14},
		ATRPeriods:        []int{14},
		DonchianPeriods:   []int{72, 240, 500},
	}
}

func historicalPriceChange(series []Kline, bars int) float64 {
	if len(series) <= bars || bars <= 0 {
		return 0
	}
	current := series[len(series)-1].Close
	base := series[len(series)-1-bars].Close
	if base == 0 {
		return 0
	}
	return ((current - base) / base) * 100
}

func syntheticOIData(series []float64, idx int) *OIData {
	if len(series) == 0 || idx < 0 || idx >= len(series) {
		return nil
	}
	lookbackStart := max(0, idx-13)
	sum := 0.0
	used := 0
	for _, value := range series[lookbackStart : idx+1] {
		if value <= 0 {
			continue
		}
		sum += value
		used++
	}
	if used == 0 {
		return nil
	}
	return &OIData{
		Latest:  series[idx],
		Average: sum / float64(used),
	}
}

func sumFlow(values map[string]float64, keys ...string) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, key := range keys {
		if value, ok := values[key]; ok {
			total += value
		}
	}
	return total
}

func clamp(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func socialSignalAvailable(values ...float64) bool {
	for _, value := range values {
		if value > 0 {
			return true
		}
	}
	return false
}
