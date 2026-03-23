package market

import (
	"math"
	"sync"
	"testing"
	"time"

	"nofx/provider/nofxos"
)

func TestComputeVolumeSpikeRawMTFResonance(t *testing.T) {
	resetHeatSeriesForTest()

	data := &Data{
		Symbol:       "SPIKEUSDT",
		CurrentPrice: 116.8,
		Indicators: IndicatorResult{
			Donchians: map[int]DonchianResult{
				72: {Upper: 115.5, Lower: 102.0, Mid: 108.75},
			},
		},
		TimeframeData: map[string]*TimeframeSeriesData{
			"5m":  buildVolumeSpikeTestSeries("5m", []float64{100, 102, 103, 105, 106, 107, 109, 110, 111, 112, 113, 116.8}, []float64{100, 102, 101, 103, 102, 104, 103, 105, 104, 106, 105, 420}),
			"15m": buildVolumeSpikeTestSeries("15m", []float64{100, 101, 102, 103, 104, 105, 105.5, 106, 107, 108, 109, 112.4}, []float64{140, 142, 141, 143, 144, 145, 146, 147, 148, 149, 150, 360}),
		},
	}

	raw, audit, context, ok := computeVolumeSpikeRaw(data)
	if !ok {
		t.Fatal("expected volume spike raw to be available")
	}
	if raw <= 0 {
		t.Fatalf("expected positive raw spike, got %.4f", raw)
	}
	if audit.VolumeZ <= 0 || audit.PriceZ <= 0 {
		t.Fatalf("expected positive z-scores, got volume=%.4f price=%.4f", audit.VolumeZ, audit.PriceZ)
	}

	donchianRaw, ok := computeDonchianPositionRaw(data)
	if !ok {
		t.Fatal("expected donchian position to be available")
	}
	if donchianRaw <= 1 {
		t.Fatalf("expected breakout-style donchian position > 1, got %.4f", donchianRaw)
	}

	resonanceRaw, ok := computeMTFResonanceRaw(context.short, context.medium, context.shortOK, context.mediumOK)
	if !ok {
		t.Fatal("expected mtf resonance to be available")
	}
	if resonanceRaw <= 0 {
		t.Fatalf("expected positive mtf resonance, got %.4f", resonanceRaw)
	}

	t.Logf("V3_AUDIT_SPIKE: Symbol=%s, Z_Vol=%.2f, Z_Price=%.2f, Final_Spike=%.1f",
		data.Symbol,
		audit.VolumeZ,
		audit.PriceZ,
		clamp(zScoreToPercent(raw), 0, 100),
	)
}

func TestBuildHeatScoreSuppressesBearishVolumeDump(t *testing.T) {
	resetHeatSeriesForTest()

	now := time.Now().UTC()
	seedHeatHistory("DUMPUSDT", "volume_spike", now, []float64{0.30, 0.33, 0.28, 0.36, 0.32, 0.29, 0.34, 0.31})
	seedHeatHistory("DUMPUSDT", "mtf_resonance", now, []float64{0.42, 0.45, 0.39, 0.48, 0.44, 0.41, 0.46, 0.43})
	seedHeatHistory("DUMPUSDT", "donchian_factor", now, []float64{0.62, 0.65, 0.61, 0.67, 0.64, 0.63, 0.66, 0.68})

	data := &Data{
		Symbol:       "DUMPUSDT",
		Sector:       "Meme",
		CurrentPrice: 92.4,
		Indicators: IndicatorResult{
			Donchians: map[int]DonchianResult{
				72: {Upper: 108.0, Lower: 94.0, Mid: 101.0},
			},
		},
		TimeframeData: map[string]*TimeframeSeriesData{
			"5m":  buildVolumeSpikeTestSeries("5m", []float64{100, 100.4, 100.8, 101.0, 101.3, 101.6, 101.8, 102.1, 102.4, 102.7, 103.0, 92.4}, []float64{110, 108, 111, 109, 112, 110, 113, 111, 114, 112, 115, 460}),
			"15m": buildVolumeSpikeTestSeries("15m", []float64{100, 100.5, 101.0, 101.4, 101.8, 102.1, 102.4, 102.7, 103.0, 103.2, 103.4, 93.1}, []float64{150, 151, 149, 152, 150, 153, 151, 154, 152, 155, 153, 520}),
		},
	}

	raw, audit, context, ok := computeVolumeSpikeRaw(data)
	if !ok {
		t.Fatal("expected bearish dump sample to still produce a raw reading")
	}
	if audit.DirectionSlopePct >= 0 {
		t.Fatalf("expected negative direction slope, got %.4f", audit.DirectionSlopePct)
	}
	if raw >= 0.30 {
		t.Fatalf("expected bearish dump raw to stay suppressed, got %.4f", raw)
	}
	resonanceRaw, ok := computeMTFResonanceRaw(context.short, context.medium, context.shortOK, context.mediumOK)
	if !ok {
		t.Fatal("expected mtf resonance evaluation")
	}
	if resonanceRaw != 0 {
		t.Fatalf("expected bearish dump resonance to be zero, got %.4f", resonanceRaw)
	}

	heat := buildHeatScore("spike-audit", data.Symbol, data, nil, now)
	if heat == nil {
		t.Fatal("expected heat score")
	}
	if heat.VolumeSpikeScore >= 45 {
		t.Fatalf("expected bearish dump score to stay muted, got %.2f", heat.VolumeSpikeScore)
	}
}

func TestBuildHeatScoreUsesNestedQuantAndOnChainSubfactors(t *testing.T) {
	resetHeatSeriesForTest()

	now := time.Now().UTC()
	seedHeatHistory("NESTEDUSDT", "market", now, []float64{8, 9, 8.5, 9.2, 8.9, 9.4})
	seedHeatHistory("NESTEDUSDT", "trend", now, []float64{32, 33, 34, 35, 36, 37})
	seedHeatHistory("NESTEDUSDT", "quant_oi", now, []float64{0.8, 1.0, 1.1, 1.3, 1.5, 1.7})
	seedHeatHistory("NESTEDUSDT", "quant_imbalance", now, []float64{0.05, 0.06, 0.08, 0.09, 0.11, 0.12})
	seedHeatHistory("NESTEDUSDT", "quant_netflow", now, []float64{0.6, 0.8, 1.0, 1.1, 1.3, 1.5})
	seedHeatHistory("NESTEDUSDT", "onchain_ratio", now, []float64{0.4, 0.5, 0.6, 0.7, 0.8, 0.9})
	seedHeatHistory("NESTEDUSDT", "onchain_buy_ratio", now, []float64{0.48, 0.50, 0.52, 0.55, 0.57, 0.60})

	data := &Data{
		Symbol:       "NESTEDUSDT",
		Sector:       "AI",
		CurrentPrice: 125.6,
		Indicators: IndicatorResult{
			Donchians: map[int]DonchianResult{
				72: {Upper: 126.5, Lower: 111.0, Mid: 118.75},
			},
		},
		Orderbook: &OrderbookData{Imbalance: 0.22},
		DexScreener: &DexScreenerData{
			LiquidityUSD:      250000,
			BuyTxnsH1:         142,
			SellTxnsH1:        88,
			BuyRatio:          0.71,
			OnchainToCEXRatio: 1.45,
		},
	}
	quant := &nofxos.QuantData{
		OI: map[string]*nofxos.OIData{
			"binance": {
				Delta: map[string]*nofxos.OIDeltaData{
					"1h": {OIDeltaPercent: 4.8},
				},
			},
		},
		Netflow: &nofxos.NetflowData{
			Institution: &nofxos.FlowTypeData{
				Future: map[string]float64{"1h": 380000, "4h": 620000},
				Spot:   map[string]float64{"1h": 110000},
			},
		},
	}

	heat := buildHeatScore("nested-audit", data.Symbol, data, quant, now)
	if heat == nil {
		t.Fatal("expected heat score")
	}
	if heat.QuantFactorScore <= 50 {
		t.Fatalf("expected nested quant factor score above neutral, got %.2f", heat.QuantFactorScore)
	}
	if heat.OnChainScore <= 50 {
		t.Fatalf("expected nested onchain factor score above neutral, got %.2f", heat.OnChainScore)
	}
	if heat.QuantOIRaw <= 0 || heat.QuantImbalanceRaw <= 0 || heat.QuantNetflowRaw <= 0 {
		t.Fatalf("expected persisted quant raw fields, got oi=%.4f imbalance=%.4f netflow=%.4f",
			heat.QuantOIRaw, heat.QuantImbalanceRaw, heat.QuantNetflowRaw)
	}
	if heat.OnChainRatioRaw <= 0 || heat.OnChainBuyRaw <= 0 {
		t.Fatalf("expected persisted onchain raw fields, got ratio=%.4f buy=%.4f",
			heat.OnChainRatioRaw, heat.OnChainBuyRaw)
	}
}

func TestAdjustedZScoreForAdaptiveFactorInvertsNegativeIC(t *testing.T) {
	states := map[string]AdaptiveFactorState{
		"social_rank": {Name: "social_rank", FinalIC: -0.42},
		"trend":       {Name: "trend", FinalIC: 0.18},
	}

	inverted := adjustedZScoreForAdaptiveFactor("ANTIUSDT", "social_rank", 2.5, states)
	if inverted != -2.5 {
		t.Fatalf("expected negative IC to invert z-score, got %.2f", inverted)
	}

	unchanged := adjustedZScoreForAdaptiveFactor("ANTIUSDT", "trend", 1.8, states)
	if unchanged != 1.8 {
		t.Fatalf("expected positive IC to keep z-score unchanged, got %.2f", unchanged)
	}
}

func TestNestedFactorScoreUsesInvertedSubfactorSignal(t *testing.T) {
	states := map[string]AdaptiveFactorState{
		"social_rank":   {Name: "social_rank", FinalIC: -0.5},
		"social_upvote": {Name: "social_upvote", FinalIC: 0.4},
	}
	weights := map[string]float64{
		"social_rank":   0.7,
		"social_upvote": 0.3,
	}
	zScores := map[string]float64{
		"social_rank":   adjustedZScoreForAdaptiveFactor("ANTIUSDT", "social_rank", 2.0, states),
		"social_upvote": adjustedZScoreForAdaptiveFactor("ANTIUSDT", "social_upvote", 1.0, states),
	}

	nested := nestedFactorScoreFromZScores(weights, zScores)
	expectedZ := -2.0*0.7 + 1.0*0.3
	if math.Abs(nested.zScore-expectedZ) > 1e-9 {
		t.Fatalf("expected nested z-score %.6f after anti-factor inversion, got %.6f", expectedZ, nested.zScore)
	}
}

func TestCoreSubfactorDoesNotDoubleInvertParentTrend(t *testing.T) {
	states := map[string]AdaptiveFactorState{
		"trend": {Name: "trend", FinalIC: -0.4},
	}
	weights := map[string]float64{
		"trend":           1.0,
		"donchian_factor": 0.0,
	}
	nested := nestedFactorScoreFromZScores(weights, map[string]float64{
		"trend":           2.0,
		"donchian_factor": 0.0,
	})

	finalZ := adjustedZScoreForAdaptiveFactor("ANTIUSDT", "trend", nested.zScore, states)
	if finalZ != -2.0 {
		t.Fatalf("expected parent trend inversion to happen exactly once, got %.2f", finalZ)
	}
}

func TestCoreSubfactorDoesNotDoubleInvertParentVolumeSpike(t *testing.T) {
	states := map[string]AdaptiveFactorState{
		"volume_spike": {Name: "volume_spike", FinalIC: -0.35},
	}
	weights := map[string]float64{
		"volume_spike":  1.0,
		"mtf_resonance": 0.0,
	}
	nested := nestedFactorScoreFromZScores(weights, map[string]float64{
		"volume_spike":  1.5,
		"mtf_resonance": 0.0,
	})

	finalZ := adjustedZScoreForAdaptiveFactor("ANTIUSDT", "volume_spike", nested.zScore, states)
	if finalZ != -1.5 {
		t.Fatalf("expected parent volume_spike inversion to happen exactly once, got %.2f", finalZ)
	}
}

func buildVolumeSpikeTestSeries(timeframe string, closes []float64, volumes []float64) *TimeframeSeriesData {
	klines := make([]KlineBar, 0, len(closes))
	for i := range closes {
		open := closes[i]
		if i > 0 {
			open = closes[i-1]
		}
		high := closes[i]
		if open > high {
			high = open
		}
		low := closes[i]
		if open < low {
			low = open
		}
		klines = append(klines, KlineBar{
			Time:   int64(i+1) * 60_000,
			Open:   open,
			High:   high * 1.002,
			Low:    low * 0.998,
			Close:  closes[i],
			Volume: volumes[i],
		})
	}

	return &TimeframeSeriesData{
		Timeframe: timeframe,
		Klines:    klines,
	}
}

func seedHeatHistory(symbol, source string, now time.Time, values []float64) {
	series := getHeatSeries(symbol + ":" + source)
	samples := make([]heatSample, 0, len(values))
	for i, value := range values {
		samples = append(samples, heatSample{
			timestamp: now.Add(time.Duration(i-len(values)) * time.Minute),
			value:     value,
		})
	}
	series.appendBatch(samples)
}

func resetHeatSeriesForTest() {
	heatSeriesMap = sync.Map{}
	heatWarmupMap = sync.Map{}
}
