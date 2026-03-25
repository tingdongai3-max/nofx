package kernel

import (
	"testing"
	"time"

	"nofx/market"
	"nofx/store"
)

func TestCheckDirectionalResonanceUsesLiveAttributionWeights(t *testing.T) {
	cleanup := setupSectorDominantLiveAttributionWeights(t)
	defer cleanup()

	longBin := &store.ScoreBinPerformance{
		BinStart:                80,
		TradeCount:              20,
		ExpectedValueLong:       0.007,
		MedianExpectedValueLong: 0.005,
		ExpectedValueShort:      -0.003,
	}
	shortBin := &store.ScoreBinPerformance{
		BinStart:                 20,
		TradeCount:               20,
		ExpectedValueLong:        -0.004,
		ExpectedValueShort:       0.006,
		MedianExpectedValueShort: 0.004,
	}

	longResult := CheckDirectionalResonance(82, &PerformanceBinMatrices{
		Global: []*store.ScoreBinPerformance{longBin},
		Sector: []*store.ScoreBinPerformance{longBin},
		Symbol: []*store.ScoreBinPerformance{longBin},
	})
	if longResult.Signal != DirectionalResonanceLong {
		t.Fatalf("expected long resonance, got %s", longResult.Signal)
	}
	if longResult.BinStart != 80 {
		t.Fatalf("expected long resonance bin 80, got %d", longResult.BinStart)
	}
	if longResult.FinalEntryEV <= 0 {
		t.Fatalf("expected positive long weighted EV, got %.6f", longResult.FinalEntryEV)
	}
	if longResult.LiveAttributionWeights[RealFireDimensionSector] <= longResult.LiveAttributionWeights[RealFireDimensionGlobal] {
		t.Fatalf("expected sector weight to dominate, got %+v", longResult.LiveAttributionWeights)
	}

	shortResult := CheckDirectionalResonance(18, &PerformanceBinMatrices{
		Global: []*store.ScoreBinPerformance{shortBin},
		Sector: []*store.ScoreBinPerformance{shortBin},
		Symbol: []*store.ScoreBinPerformance{shortBin},
	})
	if shortResult.Signal != DirectionalResonanceShort {
		t.Fatalf("expected short resonance, got %s", shortResult.Signal)
	}
	if shortResult.FinalEntryEV <= 0 {
		t.Fatalf("expected positive short weighted EV, got %.6f", shortResult.FinalEntryEV)
	}

	noSignal := CheckDirectionalResonance(55, &PerformanceBinMatrices{
		Global: []*store.ScoreBinPerformance{{
			BinStart:                55,
			TradeCount:              10,
			ExpectedValueLong:       -0.01,
			MedianExpectedValueLong: -0.02,
			ExpectedValueShort:      -0.01,
		}},
		Sector: []*store.ScoreBinPerformance{{
			BinStart:                55,
			TradeCount:              10,
			ExpectedValueLong:       -0.02,
			MedianExpectedValueLong: -0.01,
			ExpectedValueShort:      -0.02,
		}},
		Symbol: []*store.ScoreBinPerformance{{
			BinStart:                55,
			TradeCount:              10,
			ExpectedValueLong:       -0.03,
			MedianExpectedValueLong: -0.01,
			ExpectedValueShort:      -0.03,
		}},
	})
	if noSignal.Signal != DirectionalResonanceNoSignal {
		t.Fatalf("expected no signal, got %s", noSignal.Signal)
	}
}

func TestMatchFeatureArchetypeSelectsMomentumSpike(t *testing.T) {
	baseTime := time.Now().UTC().UnixMilli()
	rows := make([]*store.ShadowSnapshot, 0, 30)
	for i := 0; i < 10; i++ {
		rows = append(rows,
			testArchetypeSnapshotRow(uint(i+1), baseTime+int64(i), "RANGEUSDT", 0.03, map[string]float64{
				"market":              0.2,
				"trend":               0.1,
				"donchian_factor":     0.1,
				"volume_spike":        0.3,
				"mtf_resonance":       0.9,
				"quant_netflow":       0.1,
				"vol_utilization":     10,
				"funding_rate":        1,
				"orderbook_imbalance": 6,
			}),
			testArchetypeSnapshotRow(uint(i+101), baseTime+int64(100+i), "PULSEUSDT", 0.08, map[string]float64{
				"market":              1.4,
				"trend":               2.2,
				"donchian_factor":     1.8,
				"volume_spike":        3.2,
				"mtf_resonance":       1.7,
				"quant_netflow":       1.4,
				"vol_utilization":     55,
				"funding_rate":        3,
				"orderbook_imbalance": 18,
			}),
			testArchetypeSnapshotRow(uint(i+201), baseTime+int64(200+i), "DUMPUSDT", 0.05, map[string]float64{
				"market":              -2.4,
				"trend":               -2.0,
				"donchian_factor":     -2.1,
				"volume_spike":        -0.2,
				"mtf_resonance":       0.5,
				"quant_netflow":       -1.5,
				"vol_utilization":     8,
				"funding_rate":        -2,
				"orderbook_imbalance": -5,
			}),
		)
	}

	library := BuildFeatureArchetypeLibraryFromSnapshots(rows)
	if library == nil {
		t.Fatal("expected feature archetype library to build")
	}

	vector, ok := BuildResonanceFeatureVectorFromMarketData(&market.Data{
		HeatScore: &market.HeatScoreData{
			RawFactorScores: map[string]float64{
				"market":          1.5,
				"trend":           2.1,
				"donchian_factor": 1.7,
				"volume_spike":    3.1,
				"mtf_resonance":   1.6,
				"quant_netflow":   1.5,
			},
			RawFactorAvailable: map[string]bool{
				"market":          true,
				"trend":           true,
				"donchian_factor": true,
				"volume_spike":    true,
				"mtf_resonance":   true,
				"quant_netflow":   true,
			},
		},
		VolatilityUtilization: 0.56,
		FundingRate:           0.0003,
		Orderbook: &market.OrderbookData{
			Imbalance: 0.19,
		},
	})
	if !ok {
		t.Fatal("expected live feature vector")
	}

	match := MatchFeatureArchetype(vector, library, 2.0)
	if !match.Known {
		t.Fatalf("expected known archetype match, got %+v", match)
	}
	if match.Archetype == nil || match.Archetype.ID != "Type_B" {
		t.Fatalf("expected Type_B archetype, got %+v", match.Archetype)
	}
	if match.Distance > 2.0 {
		t.Fatalf("expected Type_B distance <= 2.0, got %.4f", match.Distance)
	}
}

func testArchetypeSnapshotRow(id uint, decisionTime int64, symbol string, returnPct float64, scores map[string]float64) *store.ShadowSnapshot {
	available := make(map[string]bool, len(scores))
	for key := range scores {
		available[key] = true
	}
	return &store.ShadowSnapshot{
		ID:           id,
		DecisionTime: decisionTime,
		Symbol:       symbol,
		Filled:       true,
		ReturnPct:    returnPct,
		RawFactors: store.ShadowRawFactors{
			Scores:    scores,
			Available: available,
		}.MarshalText(),
	}
}
