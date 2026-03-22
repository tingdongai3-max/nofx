package market

import (
	"sync"
	"testing"
	"time"
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
