package market

import (
	"math"
	"testing"

	"nofx/store"
)

func TestRankDataUsesAverageRanksForTies(t *testing.T) {
	got := RankData([]float64{10.5, 2.1, 8.8, 8.8})
	want := []float64{4, 1, 2.5, 2.5}

	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("expected rank %.2f at index %d, got %.2f", want[i], i, got[i])
		}
	}
}

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

func TestEWMASpearmanCorrelation(t *testing.T) {
	positive := EWMASpearmanCorrelation(
		[]float64{10, 20, 30, 40, 50},
		[]float64{1, 2, 3, 4, 5},
		ICHalfLife,
	)
	negative := EWMASpearmanCorrelation(
		[]float64{10, 20, 30, 40, 50},
		[]float64{5, 4, 3, 2, 1},
		ICHalfLife,
	)

	if positive < 0.99 {
		t.Fatalf("expected near-perfect positive Spearman IC, got %.6f", positive)
	}
	if negative > -0.99 {
		t.Fatalf("expected near-perfect negative Spearman IC, got %.6f", negative)
	}
}

func TestRecalculateHistoricalBinsUsesCurrentWeightsAndMedianEV(t *testing.T) {
	rawFactors := store.ShadowRawFactors{
		Scores: map[string]float64{
			"market":             0,
			"trend":              2,
			"donchian_factor":    2,
			"volume_spike":       0,
			"trend_group":        2,
			"volume_spike_group": 0,
		},
		Available: map[string]bool{
			"market":             true,
			"trend":              true,
			"donchian_factor":    true,
			"volume_spike":       false,
			"trend_group":        true,
			"volume_spike_group": false,
		},
	}.MarshalText()

	rows := []*store.ShadowSnapshot{
		{
			ID:         1,
			TraderID:   "recalc",
			Symbol:     "RIVERUSDT",
			HeatScore:  30,
			Filled:     true,
			ReturnPct:  0.30,
			RawFactors: rawFactors,
		},
		{
			ID:         2,
			TraderID:   "recalc",
			Symbol:     "RIVERUSDT",
			HeatScore:  30,
			Filled:     true,
			ReturnPct:  0.01,
			RawFactors: rawFactors,
		},
		{
			ID:         3,
			TraderID:   "recalc",
			Symbol:     "RIVERUSDT",
			HeatScore:  30,
			Filled:     true,
			ReturnPct:  0.01,
			RawFactors: rawFactors,
		},
	}

	state := AdaptiveWeightState{
		Factors: []AdaptiveFactorState{
			{Name: "market", FinalWeight: 0.2},
			{Name: "trend", FinalWeight: 0.8},
			{Name: "volume_spike", FinalWeight: 0.0},
			{Name: "quant", FinalWeight: 0.0},
			{Name: "social", FinalWeight: 0.0},
			{Name: "onchain", FinalWeight: 0.0},
		},
		NestedWeights: map[string]float64{
			"trend":           0.5,
			"donchian_factor": 0.5,
		},
	}

	scores := RecalculateHistoricalScores(rows, state)
	if len(scores) != 3 {
		t.Fatalf("expected 3 recalculated scores, got %d", len(scores))
	}
	if scores[1] <= 80 {
		t.Fatalf("expected recalculated score to move out of the stale 30 bin, got %.4f", scores[1])
	}

	bins := RecalculateHistoricalBins(rows, state)
	expectedMedianLong := math.Log(1.01)
	if len(bins) == 0 {
		t.Fatalf("expected at least one recalculated smoothed point, got %+v", bins)
	}
	for _, bin := range bins {
		if bin.BinStart < 80 {
			t.Fatalf("expected recalculated rows to move out of the stale 30 bucket, got %+v", bins)
		}
		if bin.TradeCount != 3 {
			t.Fatalf("expected each recalculated smoothed point to include all 3 rows, got %+v", bins)
		}
		if diff := math.Abs(bin.MedianExpectedValueLong - expectedMedianLong); diff > 1e-9 {
			t.Fatalf("expected median long EV %.10f, got %.10f", expectedMedianLong, bin.MedianExpectedValueLong)
		}
		if !(bin.ExpectedValueLong > bin.MedianExpectedValueLong) {
			t.Fatalf("expected mean long EV %.10f to exceed median %.10f when one outlier skews the bin", bin.ExpectedValueLong, bin.MedianExpectedValueLong)
		}
	}
}

func TestEWMASpearmanCorrelationResistsOutlierWeightDrift(t *testing.T) {
	baseFactor := make([]float64, 0, 100)
	baseReturns := make([]float64, 0, 100)
	for i := 1; i <= 100; i++ {
		baseFactor = append(baseFactor, float64(i))
		baseReturns = append(baseReturns, float64(i)/1000.0)
	}

	baselinePearson := EWMAPearsonCorrelation(baseFactor, baseReturns, ICHalfLife)
	baselineSpearman := EWMASpearmanCorrelation(baseFactor, baseReturns, ICHalfLife)

	outlierFactor := append(append([]float64{}, baseFactor...), 50.5)
	outlierReturns := append(append([]float64{}, baseReturns...), 5.0)

	outlierPearson := EWMAPearsonCorrelation(outlierFactor, outlierReturns, ICHalfLife)
	outlierSpearman := EWMASpearmanCorrelation(outlierFactor, outlierReturns, ICHalfLife)

	order := []string{"trend", "quant"}
	defaults := map[string]float64{
		"trend": 0.5,
		"quant": 0.5,
	}
	secondaryIC := 0.5

	baselinePearsonWeights := buildEmpiricalWeightsForOrder(order, defaults, map[string]float64{
		"trend": baselinePearson,
		"quant": secondaryIC,
	})
	outlierPearsonWeights := buildEmpiricalWeightsForOrder(order, defaults, map[string]float64{
		"trend": outlierPearson,
		"quant": secondaryIC,
	})
	baselineSpearmanWeights := buildEmpiricalWeightsForOrder(order, defaults, map[string]float64{
		"trend": baselineSpearman,
		"quant": secondaryIC,
	})
	outlierSpearmanWeights := buildEmpiricalWeightsForOrder(order, defaults, map[string]float64{
		"trend": outlierSpearman,
		"quant": secondaryIC,
	})

	pearsonWeightShift := math.Abs(outlierPearsonWeights["trend"] - baselinePearsonWeights["trend"])
	spearmanWeightShift := math.Abs(outlierSpearmanWeights["trend"] - baselineSpearmanWeights["trend"])

	if !(baselinePearson > 0.99 && baselineSpearman > 0.99) {
		t.Fatalf("expected clean baseline ICs near 1, got pearson=%.6f spearman=%.6f", baselinePearson, baselineSpearman)
	}
	if spearmanWeightShift >= pearsonWeightShift {
		t.Fatalf("expected Spearman weight drift %.6f to stay below Pearson %.6f", spearmanWeightShift, pearsonWeightShift)
	}
	if spearmanWeightShift >= pearsonWeightShift*0.5 {
		t.Fatalf("expected Spearman weight drift %.6f to be materially smaller than Pearson %.6f", spearmanWeightShift, pearsonWeightShift)
	}

	t.Logf("V3_AUDIT_SPEARMAN_OUTLIER: pearson_baseline=%.4f pearson_outlier=%.4f pearson_weight_shift=%.4f spearman_baseline=%.4f spearman_outlier=%.4f spearman_weight_shift=%.4f",
		baselinePearson,
		outlierPearson,
		pearsonWeightShift,
		baselineSpearman,
		outlierSpearman,
		spearmanWeightShift,
	)
}

func TestBuildEmpiricalWeightsForOrderUsesAbsoluteICMagnitude(t *testing.T) {
	order := []string{"trend", "quant"}
	defaults := map[string]float64{
		"trend": 0.5,
		"quant": 0.5,
	}

	positive := buildEmpiricalWeightsForOrder(order, defaults, map[string]float64{
		"trend": 0.30,
		"quant": 0.10,
	})
	negative := buildEmpiricalWeightsForOrder(order, defaults, map[string]float64{
		"trend": -0.30,
		"quant": 0.10,
	})

	if math.Abs(positive["trend"]-negative["trend"]) > 1e-9 {
		t.Fatalf("expected negative and positive ICs with equal magnitude to receive equal trend weight, got positive=%.6f negative=%.6f", positive["trend"], negative["trend"])
	}
	if math.Abs(positive["quant"]-negative["quant"]) > 1e-9 {
		t.Fatalf("expected negative and positive ICs with equal magnitude to receive equal quant weight, got positive=%.6f negative=%.6f", positive["quant"], negative["quant"])
	}
	if negative["trend"] <= negative["quant"] {
		t.Fatalf("expected larger absolute IC to receive larger weight, got trend=%.6f quant=%.6f", negative["trend"], negative["quant"])
	}
}

func TestMonitorSpearmanOutlierDriftDatasets(t *testing.T) {
	factorData := DefaultSpearmanMonitorFactorData()
	if len(factorData) != 2 {
		t.Fatalf("expected 2 default monitor datasets, got %d", len(factorData))
	}

	for index, series := range factorData {
		returns := computeReturns(series)
		if len(returns) != len(series) {
			t.Fatalf("expected returns length to match factor length for series %d", index)
		}
	}

	MonitorSpearmanOutlierDrift(factorData, 0.80)
}

func TestHierarchicalBayesianICShrinkage(t *testing.T) {
	globalICs := zeroAdaptiveValues()
	sectorICs := zeroAdaptiveValues()
	coinICs := zeroAdaptiveValues()
	globalICs["social"] = 0.0
	sectorICs["social"] = 0.5
	coinICs["social"] = -0.5

	finalICs := shrinkFactorICs(globalICs, sectorICs, coinICs, 200, 5)
	expected := 0.35714285714285715
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
		{samples: 25, want: 0.02},
		{samples: 125, want: 0.1},
		{samples: 500, want: 0.4},
		{samples: 800, want: 0.4},
	}

	for _, tc := range testCases {
		got := calculateBayesianShrinkageAlpha(tc.samples)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("expected alpha=%.6f for %d coin samples, got %.6f", tc.want, tc.samples, got)
		}
	}
}

func TestShrinkFactorICsFallsBackToGlobalWhenSectorSamplesThin(t *testing.T) {
	globalICs := zeroAdaptiveValues()
	sectorICs := zeroAdaptiveValues()
	coinICs := zeroAdaptiveValues()

	globalICs["social"] = 0.4
	sectorICs["social"] = -0.2
	finalICs := shrinkFactorICs(globalICs, sectorICs, coinICs, 10, 0)
	expected := 0.25
	if math.Abs(finalICs["social"]-expected) > 1e-9 {
		t.Fatalf("expected sector-thin social IC %.6f, got %.6f", expected, finalICs["social"])
	}
}

func TestShrinkFactorICsAppliesConfidenceToHiddenFactors(t *testing.T) {
	globalICs := zeroAdaptiveValues()
	sectorICs := zeroAdaptiveValues()
	coinICs := zeroAdaptiveValues()

	globalICs["social_rank"] = 0.2
	sectorICs["social_rank"] = 0.6
	coinICs["social_rank"] = -0.4
	globalICs["social_upvote"] = 0.2
	sectorICs["social_upvote"] = 0.6
	coinICs["social_upvote"] = -0.4

	finalICs := shrinkFactorICs(globalICs, sectorICs, coinICs, 15, 5)
	expected := 0.22857142857142856
	if math.Abs(finalICs["social_rank"]-expected) > 1e-9 {
		t.Fatalf("expected social_rank shrunk IC %.6f, got %.6f", expected, finalICs["social_rank"])
	}
	if math.Abs(finalICs["social_upvote"]-expected) > 1e-9 {
		t.Fatalf("expected social_upvote shrunk IC %.6f, got %.6f", expected, finalICs["social_upvote"])
	}
}

func TestBuildNestedWeightStateClampsSocialSubfactors(t *testing.T) {
	finalICs := zeroAdaptiveValues()
	finalICs["social_rank"] = -0.35
	finalICs["social_upvote"] = 0.72
	finalICs["quant_oi"] = 0.81
	finalICs["quant_imbalance"] = -0.20
	finalICs["quant_netflow"] = 0.64
	finalICs["onchain_ratio"] = 0.77
	finalICs["onchain_buy_ratio"] = -0.18

	nestedWeights, hiddenEmpirical, hiddenFinal := buildNestedWeightState(finalICs, 0, 1)
	if math.Abs(nestedWeights["social_rank"]+nestedWeights["social_upvote"]-1) > 1e-9 {
		t.Fatalf("expected normalized social nested weights, got rank=%.6f upvote=%.6f", nestedWeights["social_rank"], nestedWeights["social_upvote"])
	}
	if math.Abs(nestedWeights["quant_oi"]+nestedWeights["quant_imbalance"]+nestedWeights["quant_netflow"]-1) > 1e-9 {
		t.Fatalf("expected normalized quant nested weights, got oi=%.6f imbalance=%.6f netflow=%.6f", nestedWeights["quant_oi"], nestedWeights["quant_imbalance"], nestedWeights["quant_netflow"])
	}
	if math.Abs(nestedWeights["onchain_ratio"]+nestedWeights["onchain_buy_ratio"]-1) > 1e-9 {
		t.Fatalf("expected normalized onchain nested weights, got ratio=%.6f buy=%.6f", nestedWeights["onchain_ratio"], nestedWeights["onchain_buy_ratio"])
	}
	if hiddenEmpirical["social_upvote"] <= hiddenEmpirical["social_rank"] {
		t.Fatalf("expected empirical social_upvote weight to outrank social_rank, got rank=%.6f upvote=%.6f", hiddenEmpirical["social_rank"], hiddenEmpirical["social_upvote"])
	}
	if hiddenFinal["social_rank"] != nestedWeights["social_rank"] || hiddenFinal["social_upvote"] != nestedWeights["social_upvote"] {
		t.Fatalf("expected hidden social final weights to match nested weights, got hidden=%v nested=%v", hiddenFinal, nestedWeights)
	}

	assertWithinAdaptiveNestedBounds(t, "social", "social_rank", nestedWeights["social_rank"])
	assertWithinAdaptiveNestedBounds(t, "social", "social_upvote", nestedWeights["social_upvote"])
	assertWithinAdaptiveNestedBounds(t, "quant", "quant_oi", nestedWeights["quant_oi"])
	assertWithinAdaptiveNestedBounds(t, "quant", "quant_imbalance", nestedWeights["quant_imbalance"])
	assertWithinAdaptiveNestedBounds(t, "quant", "quant_netflow", nestedWeights["quant_netflow"])
	assertWithinAdaptiveNestedBounds(t, "onchain", "onchain_ratio", nestedWeights["onchain_ratio"])
	assertWithinAdaptiveNestedBounds(t, "onchain", "onchain_buy_ratio", nestedWeights["onchain_buy_ratio"])
}

func TestComputeICsFromRowsIncludesVolumeSpike(t *testing.T) {
	rows := []*store.ShadowSnapshot{
		{Filled: true, ReturnPct: -0.04, VolumeSpikeFactor: 10, DonchianFactor: 12, MTFResonanceFactor: 8, QuantOIRaw: 1, QuantImbalanceRaw: 0.05, QuantNetflowRaw: 0.8, SocialRankRaw: 0.2, SocialUpvoteRaw: 40, OnChainRatioRaw: 0.5, OnChainBuyRaw: 0.48},
		{Filled: true, ReturnPct: -0.01, VolumeSpikeFactor: 20, DonchianFactor: 22, MTFResonanceFactor: 18, QuantOIRaw: 2, QuantImbalanceRaw: 0.10, QuantNetflowRaw: 1.6, SocialRankRaw: 0.4, SocialUpvoteRaw: 50, OnChainRatioRaw: 0.7, OnChainBuyRaw: 0.52},
		{Filled: true, ReturnPct: 0.02, VolumeSpikeFactor: 30, DonchianFactor: 32, MTFResonanceFactor: 28, QuantOIRaw: 3, QuantImbalanceRaw: 0.15, QuantNetflowRaw: 2.4, SocialRankRaw: 0.6, SocialUpvoteRaw: 60, OnChainRatioRaw: 0.9, OnChainBuyRaw: 0.56},
		{Filled: true, ReturnPct: 0.05, VolumeSpikeFactor: 40, DonchianFactor: 42, MTFResonanceFactor: 38, QuantOIRaw: 4, QuantImbalanceRaw: 0.20, QuantNetflowRaw: 3.2, SocialRankRaw: 0.8, SocialUpvoteRaw: 70, OnChainRatioRaw: 1.1, OnChainBuyRaw: 0.60},
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
	if ics["quant_oi"] < 0.99 || ics["quant_imbalance"] < 0.99 || ics["quant_netflow"] < 0.99 {
		t.Fatalf("expected strong positive quant hidden ICs, got oi=%.6f imbalance=%.6f netflow=%.6f", ics["quant_oi"], ics["quant_imbalance"], ics["quant_netflow"])
	}
	if ics["onchain_ratio"] < 0.99 || ics["onchain_buy_ratio"] < 0.99 {
		t.Fatalf("expected strong positive onchain hidden ICs, got ratio=%.6f buy=%.6f", ics["onchain_ratio"], ics["onchain_buy_ratio"])
	}
	if ics["social_rank"] < 0.99 || ics["social_upvote"] < 0.99 {
		t.Fatalf("expected strong positive social hidden ICs, got rank=%.6f upvote=%.6f", ics["social_rank"], ics["social_upvote"])
	}
	if ics["market"] != 0 {
		t.Fatalf("expected zero market IC for empty factor series, got %.6f", ics["market"])
	}
}

func assertWithinAdaptiveNestedBounds(t *testing.T, group, factor string, value float64) {
	t.Helper()

	minValue := empiricalWeightFloor
	maxValue := 0.99
	if value < minValue-1e-9 || value > maxValue+1e-9 {
		t.Fatalf("expected %s/%s weight %.6f within [%.6f, %.6f]", group, factor, value, minValue, maxValue)
	}
}
