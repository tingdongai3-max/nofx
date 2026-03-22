package market

import (
	"math"
	"nofx/logger"
	"nofx/provider/nofxos"
	"nofx/store"
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
	quantRaw, quantAvailable := computeQuantHeatRaw(quant)
	onChainRaw, onChainAvailable := computeOnChainHeatRaw(data)
	socialRaw, socialAvailable := computeSocialHeatRaw(data)

	marketZ := scoreSource(symbol, "market", marketRaw, now)
	trendCoreZ := scoreSource(symbol, "trend", trendRaw, now)
	donchianZ := 0.0
	if donchianAvailable {
		donchianZ = scoreSource(symbol, "donchian_factor", donchianRaw, now)
	}
	volumeSpikeCoreZ := 0.0
	if volumeSpikeAvailable {
		volumeSpikeCoreZ = scoreSource(symbol, "volume_spike", volumeSpikeRaw, now)
	}
	mtfResonanceZ := 0.0
	if mtfResonanceAvailable {
		mtfResonanceZ = scoreSource(symbol, "mtf_resonance", mtfResonanceRaw, now)
	}
	quantZ := 0.0
	if quantAvailable {
		quantZ = scoreSource(symbol, "quant", quantRaw, now)
	}
	onChainZ := 0.0
	if onChainAvailable {
		onChainZ = scoreSource(symbol, "onchain", onChainRaw, now)
	}
	socialZ := 0.0
	if socialAvailable {
		socialZ = scoreSource(symbol, "social", socialRaw, now)
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
	socialFactor := adaptiveFactorStateByName(adaptiveState.Factors, "social")
	volumeSpikeFactorState := adaptiveFactorStateByName(adaptiveState.Factors, "volume_spike")
	donchianFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "donchian_factor")
	mtfResonanceFactorState := adaptiveFactorStateByName(adaptiveState.HiddenFactors, "mtf_resonance")

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
		"donchian_factor": donchianZ,
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
		"mtf_resonance": mtfResonanceZ,
	})

	tradingWeights := map[string]float64{
		"market": marketWeight,
		"trend":  trendWeight,
	}
	tradingScores := map[string]float64{
		"market": marketZ,
		"trend":  trendNested.zScore,
	}
	tradingWeight := marketWeight + trendWeight
	if volumeSpikeAvailable {
		tradingWeights["volume_spike"] = volumeSpikeWeight
		tradingScores["volume_spike"] = volumeSpikeNested.zScore
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
			SourceWeights: map[string]float64{
				"social":       0,
				"onchain":      0,
				"market":       0,
				"trend":        0,
				"volume_spike": 0,
				"quant":        0,
			},
		}
	}

	tradingScore := combineWeightedScores(tradingWeights, tradingScores)

	quantScore := 0.0
	quantWeights := map[string]float64{}
	quantScores := map[string]float64{}
	if quantAvailable {
		quantWeights["quant"] = quantWeight
		quantScores["quant"] = quantZ
	}
	if onChainAvailable {
		quantWeights["onchain"] = onChainWeight
		quantScores["onchain"] = onChainZ
	}
	if socialAvailable {
		quantWeights["social"] = socialWeight
		quantScores["social"] = socialZ
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

	logger.Infof("V3_AUDIT_BAYES_IC: Symbol=%s, Sector=%s, Alpha=%.2f, Sector_Social_IC=%.2f, Coin_Social_IC=%.2f, Final_Social_W=%.2f",
		symbol,
		data.Sector,
		adaptiveState.Alpha,
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
		QuantFactorScore:        clamp(zScoreToPercent(quantZ), 0, 100),
		SocialScore:             clamp(zScoreToPercent(socialZ), 0, 100),
		OnChainScore:            clamp(zScoreToPercent(onChainZ), 0, 100),
		SourceWeights:           sourceWeights,
	}
}

func scoreSource(symbol, source string, raw float64, now time.Time) float64 {
	if !isFinite(raw) {
		raw = 0
	}
	key := symbol + ":" + source
	series := getHeatSeries(key)
	history := series.history(now)
	z := CalculateZScore(raw, history)
	series.append(now, raw)
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

func computeQuantHeatRaw(quant *nofxos.QuantData) (float64, bool) {
	if quant == nil {
		return 0, false
	}

	raw := 0.0
	available := false

	if change, ok := quant.PriceChange["1h"]; ok {
		raw += math.Abs(change) * 100 * 0.35
		available = true
	}
	if change, ok := quant.PriceChange["4h"]; ok {
		raw += math.Abs(change) * 100 * 0.2
		available = true
	}

	for _, oi := range quant.OI {
		if oi == nil || oi.Delta == nil {
			continue
		}
		if delta, ok := oi.Delta["1h"]; ok && delta != nil {
			raw += math.Abs(delta.OIDeltaPercent) * 0.25
			available = true
			break
		}
	}

	if quant.Netflow != nil {
		netflowMagnitude := 0.0
		if quant.Netflow.Institution != nil {
			netflowMagnitude += sumAbsoluteFlow(quant.Netflow.Institution.Future, "1h", "4h")
			netflowMagnitude += sumAbsoluteFlow(quant.Netflow.Institution.Spot, "1h", "4h")
		}
		if quant.Netflow.Personal != nil {
			netflowMagnitude += sumAbsoluteFlow(quant.Netflow.Personal.Future, "1h", "4h")
			netflowMagnitude += sumAbsoluteFlow(quant.Netflow.Personal.Spot, "1h", "4h")
		}
		if netflowMagnitude > 0 {
			raw += math.Log10(1+netflowMagnitude) * 10 * 0.2
			available = true
		}
	}

	return raw, available
}

func computeOnChainHeatRaw(data *Data) (float64, bool) {
	if data == nil || data.DexScreener == nil {
		return 0, false
	}

	dex := data.DexScreener
	if dex.LiquidityUSD < MinDexLiquidityUSD {
		return 0, false
	}

	raw := 0.0
	available := false

	if dex.OnchainToCEXRatio > 0 {
		raw += math.Min(dex.OnchainToCEXRatio, 3.0) * 40
		available = true
	}

	if dex.BuyTxnsH1+dex.SellTxnsH1 > 0 {
		buyBias := dex.BuyRatio - 0.5
		if buyBias > 0 {
			raw += buyBias * 120
		}
		available = true
	}

	return raw, available
}

func computeSocialHeatRaw(data *Data) (float64, bool) {
	if data == nil || data.GeckoSentiment == nil {
		return 0, false
	}

	interest := data.GeckoSentiment.PublicInterestScore
	sentimentUp := data.GeckoSentiment.SentimentVotesUpPercentage
	if interest <= 0 && sentimentUp <= 0 {
		return 0, false
	}

	raw := (interest * 0.7) + (sentimentUp * 0.3)
	return raw, socialSignalAvailable(interest, sentimentUp)
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

func sumAbsoluteFlow(values map[string]float64, keys ...string) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, key := range keys {
		if value, ok := values[key]; ok {
			total += math.Abs(value)
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
