package market

import "testing"

func TestAdaptiveWeightStateGetWeightSignatureStableAndSensitive(t *testing.T) {
	base := AdaptiveWeightState{
		Factors: []AdaptiveFactorState{
			{Name: "market", FinalWeight: 0.11, FinalIC: -0.25},
			{Name: "trend", FinalWeight: 0.12, FinalIC: 0.45},
			{Name: "volume_spike", FinalWeight: 0.13, FinalIC: 0.15},
			{Name: "quant", FinalWeight: 0.14, FinalIC: 0.22},
			{Name: "social", FinalWeight: 0.25, FinalIC: 0.31},
			{Name: "onchain", FinalWeight: 0.25, FinalIC: 0.18},
		},
		HiddenFactors: []AdaptiveFactorState{
			{Name: "donchian_factor", FinalWeight: 0.55, FinalIC: 0.21},
			{Name: "mtf_resonance", FinalWeight: 0.45, FinalIC: -0.11},
			{Name: "quant_oi", FinalWeight: 0.40, FinalIC: 0.09},
			{Name: "quant_imbalance", FinalWeight: 0.25, FinalIC: 0.07},
			{Name: "quant_netflow", FinalWeight: 0.35, FinalIC: 0.13},
			{Name: "social_rank", FinalWeight: 0.70, FinalIC: 0.17},
			{Name: "social_upvote", FinalWeight: 0.30, FinalIC: -0.05},
			{Name: "onchain_ratio", FinalWeight: 0.60, FinalIC: 0.04},
			{Name: "onchain_buy_ratio", FinalWeight: 0.40, FinalIC: 0.03},
		},
		NestedWeights: map[string]float64{
			"trend":             0.55,
			"donchian_factor":   0.45,
			"volume_spike":      0.52,
			"mtf_resonance":     0.48,
			"quant_oi":          0.40,
			"quant_imbalance":   0.25,
			"quant_netflow":     0.35,
			"social_rank":       0.70,
			"social_upvote":     0.30,
			"onchain_ratio":     0.60,
			"onchain_buy_ratio": 0.40,
		},
	}

	if base.GetWeightSignature() != base.GetWeightSignature() {
		t.Fatalf("expected identical state to produce stable signature")
	}

	changedVisible := base
	changedVisible.Factors = append([]AdaptiveFactorState(nil), base.Factors...)
	changedVisible.Factors[1].FinalWeight += 0.01
	if changedVisible.GetWeightSignature() == base.GetWeightSignature() {
		t.Fatalf("expected visible factor weight delta to change signature")
	}

	changedHidden := base
	changedHidden.HiddenFactors = append([]AdaptiveFactorState(nil), base.HiddenFactors...)
	changedHidden.HiddenFactors[0].FinalIC += 0.01
	if changedHidden.GetWeightSignature() == base.GetWeightSignature() {
		t.Fatalf("expected hidden factor IC delta to change signature")
	}

	changedNested := base
	changedNested.NestedWeights = make(map[string]float64, len(base.NestedWeights))
	for key, value := range base.NestedWeights {
		changedNested.NestedWeights[key] = value
	}
	changedNested.NestedWeights["quant_netflow"] += 0.01
	if changedNested.GetWeightSignature() == base.GetWeightSignature() {
		t.Fatalf("expected nested weight delta to change signature")
	}
}
