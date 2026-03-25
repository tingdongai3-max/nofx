package kernel

import (
	"math"
	"testing"
)

func TestCalculateResonanceScoreReturnsRawMean(t *testing.T) {
	cases := []struct {
		name    string
		factors []float64
		want    float64
	}{
		{
			name:    "compressed low-coherence signal",
			factors: []float64{100, 20, 20},
			want:    46.6666666667,
		},
		{
			name:    "high-mean coherent signal",
			factors: []float64{150, 100, 110},
			want:    120,
		},
		{
			name:    "flat coherent signal",
			factors: []float64{80, 80, 80},
			want:    80,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			score := CalculateResonanceScore(tc.factors, nil, 1.0)
			if diff := score - tc.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("unexpected raw score for %s: got %.10f want %.10f", tc.name, score, tc.want)
			}
		})
	}
}

func TestCalculateResonanceScoreReturnsZeroForNonPositiveMean(t *testing.T) {
	score := CalculateResonanceScore([]float64{-10, 0, 0}, nil, 1.0)
	if score != 0 {
		t.Fatalf("expected non-positive mean to return zero score, got %.4f", score)
	}
}

func TestCalculateResonanceScoreHardCapsAtMahalanobisThreshold(t *testing.T) {
	score := CalculateResonanceScore([]float64{100, 100, 100}, nil, 1.0, 2.01)
	if score != 0 {
		t.Fatalf("expected hard cap to zero out score above threshold, got %.4f", score)
	}

	score = CalculateResonanceScore([]float64{100, 100, 100}, nil, 1.0, 2.0)
	if diff := math.Abs(score - 100); diff > 1e-9 {
		t.Fatalf("expected boundary distance to remain allowed, got %.4f", score)
	}
}

func TestApplyResonanceMahalanobisPenaltyHardGatesOutlier(t *testing.T) {
	cluster := &ResonanceFeatureCluster{
		Mean:   []float64{0, 0},
		InvCov: [][]float64{{1, 0}, {0, 1}},
	}

	final, distance, blocked := ApplyResonanceMahalanobisPenalty(95, []float64{4, 0}, cluster, 0.5, 2.0)
	if !blocked {
		t.Fatal("expected outlier vector to be hard blocked")
	}
	if distance <= 2.0 {
		t.Fatalf("expected distance to exceed threshold, got %.4f", distance)
	}
	if final != 0 {
		t.Fatalf("expected hard-blocked score to be zero, got %.4f", final)
	}
}

func TestApplyResonanceMahalanobisPenaltyHardGatesAtThresholdBoundary(t *testing.T) {
	cluster := &ResonanceFeatureCluster{
		Mean:   []float64{0, 0},
		InvCov: [][]float64{{1, 0}, {0, 1}},
	}

	final, distance, blocked := ApplyResonanceMahalanobisPenalty(100, []float64{2.01, 0}, cluster, 0.5, 2.0)
	if !blocked {
		t.Fatal("expected boundary-crossing vector to be hard blocked")
	}
	if distance <= 2.0 {
		t.Fatalf("expected distance to exceed threshold, got %.4f", distance)
	}
	if final != 0 {
		t.Fatalf("expected hard-blocked score to be zero, got %.4f", final)
	}
}

func TestApplyResonanceMahalanobisPenaltyHardGatesVolatilitySpike(t *testing.T) {
	cluster := &ResonanceFeatureCluster{
		FeatureNames: []string{"market", "vol_utilization"},
		Mean:         []float64{10, 50},
		InvCov:       [][]float64{{1, 0}, {0, 0.0001}},
	}

	final, distance, blocked := ApplyResonanceMahalanobisPenalty(100, []float64{10, 110}, cluster, 0.5, 2.0)
	if !blocked {
		t.Fatal("expected volatility spike to be hard blocked")
	}
	if distance >= 2.0 {
		t.Fatalf("expected volatility spike to stay inside the mahalanobis threshold, got %.4f", distance)
	}
	if final != 0 {
		t.Fatalf("expected volatility spike score to be zero, got %.4f", final)
	}
}

func TestValidateResonanceFactorsAppliesFloor(t *testing.T) {
	ok, reason, minFactor := ValidateResonanceFactors([]float64{79, 79, 79}, 45)
	if !ok {
		t.Fatalf("expected coherent factor set to pass, reason=%s min=%.2f", reason, minFactor)
	}

	ok, reason, minFactor = ValidateResonanceFactors([]float64{20, 50, 167}, 45)
	if ok {
		t.Fatal("expected incoherent factor set to be rejected")
	}
	if reason != "Logic Incoherence" {
		t.Fatalf("expected logic incoherence, got %s", reason)
	}
	if minFactor != 20 {
		t.Fatalf("expected min factor 20, got %.2f", minFactor)
	}

	ok, reason, _ = ValidateResonanceFactors([]float64{-1, 50, 60}, 45)
	if ok {
		t.Fatal("expected negative factor violation")
	}
	if reason != "Negative Factor Violation" {
		t.Fatalf("expected negative factor violation, got %s", reason)
	}
}
