package kernel

import (
	"math"
	"testing"
	"time"

	"nofx/market"
	"nofx/store"
)

func TestBuildResonanceFeatureVectorFromMarketDataIncludesMicrostructureFeatures(t *testing.T) {
	data := &market.Data{
		HeatScore: &market.HeatScoreData{
			RawFactorScores: map[string]float64{
				"market": 1.25,
				"trend":  2.5,
			},
			RawFactorAvailable: map[string]bool{
				"market": true,
				"trend":  true,
			},
		},
		VolatilityUtilization: 0.12,
		FundingRate:           0.0004,
		Orderbook: &market.OrderbookData{
			Imbalance: 0.18,
		},
	}

	vector, ok := BuildResonanceFeatureVectorFromMarketData(data)
	if !ok {
		t.Fatal("expected feature vector to be built")
	}
	if len(vector) != len(resonanceFeatureNames) {
		t.Fatalf("expected %d-dimensional vector, got %d", len(resonanceFeatureNames), len(vector))
	}

	if idx := featureIndexForTest("market"); idx < 0 {
		t.Fatal("expected market feature to exist")
	} else if got := vector[idx]; math.Abs(got-1.25) > 1e-9 {
		t.Fatalf("expected market feature 1.25, got %.4f", got)
	}
	if idx := featureIndexForTest("vol_utilization"); idx < 0 {
		t.Fatal("expected vol_utilization feature to exist")
	} else if got := vector[idx]; math.Abs(got-12) > 1e-9 {
		t.Fatalf("expected vol_utilization feature 12, got %.4f", got)
	}
	if idx := featureIndexForTest("funding_rate"); idx < 0 {
		t.Fatal("expected funding_rate feature to exist")
	} else if got := vector[idx]; math.Abs(got-4) > 1e-9 {
		t.Fatalf("expected funding_rate feature 4, got %.4f", got)
	}
	if idx := featureIndexForTest("orderbook_imbalance"); idx < 0 {
		t.Fatal("expected orderbook_imbalance feature to exist")
	} else if got := vector[idx]; math.Abs(got-18) > 1e-9 {
		t.Fatalf("expected orderbook_imbalance feature 18, got %.4f", got)
	}
}

func TestBuildResonanceFeatureClusterFromSnapshotsUsesPositiveRows(t *testing.T) {
	baseTime := time.Now().UTC().UnixMilli()
	rawFactors := store.ShadowRawFactors{
		Scores: map[string]float64{
			"market":              1.2,
			"trend":               2.4,
			"vol_utilization":     11.0,
			"funding_rate":        3.0,
			"orderbook_imbalance": 17.0,
		},
		Available: map[string]bool{
			"market":              true,
			"trend":               true,
			"vol_utilization":     true,
			"funding_rate":        true,
			"orderbook_imbalance": true,
		},
	}.MarshalText()

	rows := make([]*store.ShadowSnapshot, 0, 21)
	for i := 0; i < 20; i++ {
		rows = append(rows, &store.ShadowSnapshot{
			ID:           uint(i + 1),
			DecisionTime: baseTime + int64(i),
			Filled:       true,
			ReturnPct:    0.03,
			RawFactors:   rawFactors,
		})
	}
	rows = append(rows, &store.ShadowSnapshot{
		ID:           99,
		DecisionTime: baseTime + 99,
		Filled:       true,
		ReturnPct:    -0.02,
		RawFactors:   rawFactors,
	})

	cluster := BuildResonanceFeatureClusterFromSnapshots(rows)
	if cluster == nil {
		t.Fatal("expected positive cluster to be built")
	}
	if cluster.PositiveSampleCount != 20 {
		t.Fatalf("expected 20 positive samples, got %d", cluster.PositiveSampleCount)
	}
	if cluster.SampleCount != 21 {
		t.Fatalf("expected 21 total samples, got %d", cluster.SampleCount)
	}
	if len(cluster.Mean) != len(resonanceFeatureNames) {
		t.Fatalf("expected mean length %d, got %d", len(resonanceFeatureNames), len(cluster.Mean))
	}
}

func TestIsStatisticallyResonantUsesMahalanobisGate(t *testing.T) {
	samples := make([][]float64, 0, 20)
	for i := 0; i < 20; i++ {
		samples = append(samples, []float64{10, 10, 10, 10, 10, 10, 10, 10})
	}

	cluster := BuildResonanceFeatureClusterFromVectors([]string{"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8"}, samples)
	if cluster == nil {
		t.Fatal("expected resonance feature cluster to be built")
	}

	if !IsStatisticallyResonant([]float64{10, 10, 10, 10, 10, 10, 10, 10}, cluster) {
		t.Fatal("expected center vector to be statistically resonant")
	}
	if IsStatisticallyResonant([]float64{120, 120, 120, 120, 120, 120, 120, 120}, cluster) {
		t.Fatal("expected far outlier vector to fail resonance gate")
	}
}

func featureIndexForTest(name string) int {
	for i, candidate := range resonanceFeatureNames {
		if candidate == name {
			return i
		}
	}
	return -1
}
