package market

import (
	"math"
	"testing"

	"nofx/store"
)

func TestEWMAPearsonCorrelation(t *testing.T) {
	xRecent := []float64{-3, -2, -1, 1, 2, 3}
	yRecent := []float64{3, 2, 1, 1, 2, 3}
	xDistant := []float64{1, 2, 3, -3, -2, -1}
	yDistant := []float64{1, 2, 3, 3, 2, 1}

	recentIC := EWMAPearsonCorrelation(xRecent, yRecent, 1)
	distantIC := EWMAPearsonCorrelation(xDistant, yDistant, 1)

	if recentIC <= distantIC {
		t.Fatalf("expected recent correlation %.6f to exceed distant correlation %.6f", recentIC, distantIC)
	}
	if recentIC <= 0 {
		t.Fatalf("expected recent-weighted IC to stay positive, got %.6f", recentIC)
	}
	if distantIC >= 0 {
		t.Fatalf("expected distant-weighted IC to flip negative after EWMA decay, got %.6f", distantIC)
	}
}

func TestHierarchicalBayesianICShrinkage(t *testing.T) {
	sectorICs := zeroAdaptiveValues()
	coinICs := zeroAdaptiveValues()
	sectorICs["social"] = 0.5
	coinICs["social"] = -0.5

	alpha := calculateBayesianShrinkageAlpha(2000)
	if math.Abs(alpha-0.4) > 1e-9 {
		t.Fatalf("expected alpha=0.4 for 2000 coin samples, got %.6f", alpha)
	}

	finalICs := shrinkFactorICs(sectorICs, coinICs, alpha)
	expected := 0.1
	if math.Abs(finalICs["social"]-expected) > 1e-9 {
		t.Fatalf("expected shrunk social IC=%.2f, got %.6f", expected, finalICs["social"])
	}
	if !(finalICs["social"] < sectorICs["social"] && finalICs["social"] > coinICs["social"]) {
		t.Fatalf("expected final IC %.4f to shrink toward sector IC %.4f and away from coin IC %.4f",
			finalICs["social"], sectorICs["social"], coinICs["social"])
	}
}

func TestCalculateBayesianShrinkageAlphaCurve(t *testing.T) {
	testCases := []struct {
		samples int
		want    float64
	}{
		{samples: 0, want: 0},
		{samples: 25, want: 0.005},
		{samples: 2000, want: 0.4},
		{samples: 2500, want: 0.4},
	}

	for _, tc := range testCases {
		got := calculateBayesianShrinkageAlpha(tc.samples)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("expected alpha=%.6f for %d coin samples, got %.6f", tc.want, tc.samples, got)
		}
	}
}

func TestComputeICsFromRowsIncludesVolumeSpike(t *testing.T) {
	rows := []*store.ShadowSnapshot{
		{Filled: true, ReturnPct: -0.04, VolumeSpikeFactor: 10, DonchianFactor: 12, MTFResonanceFactor: 8},
		{Filled: true, ReturnPct: -0.01, VolumeSpikeFactor: 20, DonchianFactor: 22, MTFResonanceFactor: 18},
		{Filled: true, ReturnPct: 0.02, VolumeSpikeFactor: 30, DonchianFactor: 32, MTFResonanceFactor: 28},
		{Filled: true, ReturnPct: 0.05, VolumeSpikeFactor: 40, DonchianFactor: 42, MTFResonanceFactor: 38},
	}

	ics, sampleCount := computeICsFromRows(rows)
	if sampleCount != 4 {
		t.Fatalf("expected 4 samples, got %d", sampleCount)
	}
	if ics["volume_spike"] < 0.99 {
		t.Fatalf("expected strong positive volume_spike IC, got %.6f", ics["volume_spike"])
	}
	if ics["donchian_factor"] < 0.99 {
		t.Fatalf("expected strong positive donchian_factor IC, got %.6f", ics["donchian_factor"])
	}
	if ics["mtf_resonance"] < 0.99 {
		t.Fatalf("expected strong positive mtf_resonance IC, got %.6f", ics["mtf_resonance"])
	}
	if ics["market"] != 0 {
		t.Fatalf("expected zero market IC for empty factor series, got %.6f", ics["market"])
	}
}
