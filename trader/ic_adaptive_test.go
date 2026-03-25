package trader

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nofx/market"
	"nofx/store"
)

func TestAdaptiveWeightStateFromShadowSnapshots(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_ic_adaptive.db")
	_ = os.Remove(dbPath)
	gdb, err := store.InitGorm(dbPath)
	if err != nil {
		t.Fatalf("open gorm db: %v", err)
	}

	st, err := store.NewFromGorm(gdb)
	if err != nil {
		t.Fatalf("create store from gorm: %v", err)
	}
	defer st.Close()
	if err := st.GormDB().AutoMigrate(&store.ShadowSnapshot{}); err != nil {
		t.Fatalf("init shadow tables: %v", err)
	}

	_ = st.GormDB().Exec("DELETE FROM shadow_snapshots WHERE trader_id = ?", "ic-adapt").Error

	rows := make([]*store.ShadowSnapshot, 0, market.AdaptiveSampleTarget)
	baseTime := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < market.AdaptiveSampleTarget; i++ {
		progress := float64(i) / float64(market.AdaptiveSampleTarget-1)
		returnPct := -0.06 + float64(i)*(0.12/float64(market.AdaptiveSampleTarget-1))
		rows = append(rows, &store.ShadowSnapshot{
			TraderID:           "ic-adapt",
			DecisionTime:       baseTime.Add(time.Duration(i) * time.Minute).UnixMilli(),
			Symbol:             fmt.Sprintf("IC%02dUSDT", i),
			Sector:             "AI",
			ActionTaken:        i % 2,
			PriceT0:            100 + float64(i),
			HeatScore:          55 + float64(i%10),
			TradingSub:         50 + float64(i%7),
			QuantSub:           48 + float64(i%9),
			MarketFactor:       85 - progress*55,
			TrendFactor:        48 + progress*4,
			DonchianFactor:     35 + progress*40,
			VolumeSpikeFactor:  25 + progress*50,
			MTFResonanceFactor: 15 + progress*55,
			QuantFactor:        34 + progress*28,
			QuantOIRaw:         2 + progress*8,
			QuantImbalanceRaw:  0.05 + progress*0.35,
			QuantNetflowRaw:    1 + progress*5,
			SocialFactor:       20 + progress*60,
			SocialRankRaw:      0.2 + progress*0.6,
			SocialUpvoteRaw:    40 + progress*30,
			OnChainFactor:      50 + progress*2,
			OnChainRatioRaw:    0.5 + progress*1.2,
			OnChainBuyRaw:      0.45 + progress*0.2,
			VolUtilization:     0.35 + float64(i%5)*0.04,
			Filled:             true,
			PriceT1:            (100 + float64(i)) * (1 + returnPct),
			ReturnPct:          returnPct,
			FilledAt:           baseTime.Add(time.Duration(i+15) * time.Minute).UnixMilli(),
			CreatedAt:          baseTime.Add(time.Duration(i) * time.Minute).UnixMilli(),
			UpdatedAt:          baseTime.Add(time.Duration(i+15) * time.Minute).UnixMilli(),
		})
	}
	for start := 0; start < len(rows); start += 100 {
		end := start + 100
		if end > len(rows) {
			end = len(rows)
		}
		if err := st.Shadow().CreateBatch(rows[start:end]); err != nil {
			t.Fatalf("create adaptive rows batch %d-%d: %v", start, end, err)
		}
	}
	for _, row := range rows {
		if err := st.Shadow().MarkFilled(row.ID, row.PriceT1, row.ReturnPct, row.FilledAt); err != nil {
			t.Fatalf("mark filled: %v", err)
		}
	}

	market.SetAdaptiveWeightStore(st)
	defer market.SetAdaptiveWeightStore(nil)
	market.InvalidateAdaptiveWeightScope("ic-adapt")

	state := market.GetAdaptiveWeightState("ic-adapt", "AI", "IC29USDT")
	if state.SampleCount != market.AdaptiveSampleTarget {
		t.Fatalf("expected %d samples, got %d", market.AdaptiveSampleTarget, state.SampleCount)
	}
	if len(state.Factors) != 6 {
		t.Fatalf("expected 6 visible factors, got %d", len(state.Factors))
	}
	if len(state.HiddenFactors) != 9 {
		t.Fatalf("expected 9 hidden factors, got %d", len(state.HiddenFactors))
	}

	factors := make(map[string]market.AdaptiveFactorState, len(state.Factors))
	for _, factor := range state.Factors {
		factors[factor.Name] = factor
	}
	hiddenFactors := make(map[string]market.AdaptiveFactorState, len(state.HiddenFactors))
	for _, factor := range state.HiddenFactors {
		hiddenFactors[factor.Name] = factor
	}

	if factors["social"].IC <= 0 {
		t.Fatalf("expected positive social IC, got %.4f", factors["social"].IC)
	}
	if factors["volume_spike"].IC <= 0 {
		t.Fatalf("expected positive volume_spike IC, got %.4f", factors["volume_spike"].IC)
	}
	if hiddenFactors["donchian_factor"].IC <= 0 {
		t.Fatalf("expected positive donchian_factor IC, got %.4f", hiddenFactors["donchian_factor"].IC)
	}
	if hiddenFactors["mtf_resonance"].IC <= 0 {
		t.Fatalf("expected positive mtf_resonance IC, got %.4f", hiddenFactors["mtf_resonance"].IC)
	}
	if factors["quant"].IC <= 0 {
		t.Fatalf("expected positive quant IC, got %.4f", factors["quant"].IC)
	}
	if factors["onchain"].IC <= 0 {
		t.Fatalf("expected positive onchain IC, got %.4f", factors["onchain"].IC)
	}
	if hiddenFactors["quant_oi"].IC <= 0 || hiddenFactors["quant_imbalance"].IC <= 0 || hiddenFactors["quant_netflow"].IC <= 0 {
		t.Fatalf("expected positive quant hidden ICs, got oi=%.4f imbalance=%.4f netflow=%.4f",
			hiddenFactors["quant_oi"].IC,
			hiddenFactors["quant_imbalance"].IC,
			hiddenFactors["quant_netflow"].IC)
	}
	if hiddenFactors["onchain_ratio"].IC <= 0 || hiddenFactors["onchain_buy_ratio"].IC <= 0 {
		t.Fatalf("expected positive onchain hidden ICs, got ratio=%.4f buy=%.4f",
			hiddenFactors["onchain_ratio"].IC,
			hiddenFactors["onchain_buy_ratio"].IC)
	}
	if factors["market"].IC >= 0 {
		t.Fatalf("expected negative market IC, got %.4f", factors["market"].IC)
	}
	if factors["volume_spike"].FinalWeight <= factors["volume_spike"].DefaultWeight {
		t.Fatalf("expected volume_spike weight to increase, got default=%.4f final=%.4f",
			factors["volume_spike"].DefaultWeight, factors["volume_spike"].FinalWeight)
	}
	if factors["market"].EmpiricalWeight < 0.099 {
		t.Fatalf("expected market floor weight, got %.4f", factors["market"].EmpiricalWeight)
	}

	weightSum := 0.0
	for _, factor := range state.Factors {
		weightSum += factor.FinalWeight
	}
	if math.Abs(weightSum-1) > 1e-6 {
		t.Fatalf("expected normalized final weights, got sum=%.8f", weightSum)
	}
	trendNestedWeight := state.NestedWeights["trend"] + state.NestedWeights["donchian_factor"]
	if math.Abs(trendNestedWeight-1) > 1e-6 {
		t.Fatalf("expected normalized nested trend weights, got %.8f", trendNestedWeight)
	}
	spikeNestedWeight := state.NestedWeights["volume_spike"] + state.NestedWeights["mtf_resonance"]
	if math.Abs(spikeNestedWeight-1) > 1e-6 {
		t.Fatalf("expected normalized nested spike weights, got %.8f", spikeNestedWeight)
	}
	socialNestedWeight := state.NestedWeights["social_rank"] + state.NestedWeights["social_upvote"]
	if math.Abs(socialNestedWeight-1) > 1e-6 {
		t.Fatalf("expected normalized nested social weights, got %.8f", socialNestedWeight)
	}
	quantNestedWeight := state.NestedWeights["quant_oi"] + state.NestedWeights["quant_imbalance"] + state.NestedWeights["quant_netflow"]
	if math.Abs(quantNestedWeight-1) > 1e-6 {
		t.Fatalf("expected normalized nested quant weights, got %.8f", quantNestedWeight)
	}
	onchainNestedWeight := state.NestedWeights["onchain_ratio"] + state.NestedWeights["onchain_buy_ratio"]
	if math.Abs(onchainNestedWeight-1) > 1e-6 {
		t.Fatalf("expected normalized nested onchain weights, got %.8f", onchainNestedWeight)
	}

	t.Logf("V3_AUDIT_ADAPTIVE_SPIKE: trader=%s samples=%d sector_samples=%d coin_samples=%d volume_spike_ic=%.2f volume_spike_weight=%.2f social_ic=%.2f social_weight=%.2f market_ic=%.2f market_weight=%.2f",
		state.TraderID,
		state.SampleCount,
		state.SectorSampleCount,
		state.CoinSampleCount,
		factors["volume_spike"].IC,
		factors["volume_spike"].FinalWeight,
		factors["social"].IC,
		factors["social"].FinalWeight,
		factors["market"].IC,
		factors["market"].FinalWeight,
	)

	sectorState := market.GetAdaptiveWeightState("ic-adapt", "AI", "")
	if sectorState.Symbol != "" {
		t.Fatalf("expected empty symbol for sector scope, got %q", sectorState.Symbol)
	}
	if sectorState.CoinSampleCount != 0 {
		t.Fatalf("expected sector scope coin sample count 0, got %d", sectorState.CoinSampleCount)
	}
	if sectorState.SampleCount != market.AdaptiveSampleTarget {
		t.Fatalf("expected sector scope sample count %d, got %d", market.AdaptiveSampleTarget, sectorState.SampleCount)
	}
	if sectorState.BlendAdaptive <= 0 {
		t.Fatalf("expected sector scope adaptive blend to activate, got %.4f", sectorState.BlendAdaptive)
	}

	globalState := market.GetAdaptiveWeightState("ic-adapt", "", "")
	if globalState.Symbol != "" {
		t.Fatalf("expected empty symbol for global scope, got %q", globalState.Symbol)
	}
	if globalState.Sector != "" {
		t.Fatalf("expected empty sector label for global scope, got %q", globalState.Sector)
	}
	if globalState.SampleCount != market.AdaptiveSampleTarget {
		t.Fatalf("expected global scope sample count %d, got %d", market.AdaptiveSampleTarget, globalState.SampleCount)
	}
	if len(globalState.Factors) != 6 {
		t.Fatalf("expected 6 visible factors for global scope, got %d", len(globalState.Factors))
	}
}

func TestAdaptiveWeightStateUses2000SectorBlendTarget(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_ic_blend_target.db")
	_ = os.Remove(dbPath)
	gdb, err := store.InitGorm(dbPath)
	if err != nil {
		t.Fatalf("open gorm db: %v", err)
	}

	st, err := store.NewFromGorm(gdb)
	if err != nil {
		t.Fatalf("create store from gorm: %v", err)
	}
	defer st.Close()
	if err := st.GormDB().AutoMigrate(&store.ShadowSnapshot{}); err != nil {
		t.Fatalf("init shadow tables: %v", err)
	}

	const sectorSamples = 166
	baseTime := time.Now().UTC().Add(-4 * time.Hour)
	rows := make([]*store.ShadowSnapshot, 0, sectorSamples)
	for i := 0; i < sectorSamples; i++ {
		progress := float64(i) / float64(sectorSamples-1)
		returnPct := -0.03 + float64(i)*(0.06/float64(sectorSamples-1))
		rows = append(rows, &store.ShadowSnapshot{
			TraderID:           "ic-blend",
			DecisionTime:       baseTime.Add(time.Duration(i) * time.Minute).UnixMilli(),
			Symbol:             "BLENDUSDT",
			Sector:             "AI",
			ActionTaken:        i % 2,
			PriceT0:            100 + float64(i)/10,
			HeatScore:          55 + float64(i%5),
			TradingSub:         53 + float64(i%4),
			QuantSub:           49 + float64(i%3),
			MarketFactor:       70 - progress*25,
			TrendFactor:        42 + progress*18,
			DonchianFactor:     38 + progress*20,
			VolumeSpikeFactor:  30 + progress*25,
			MTFResonanceFactor: 28 + progress*22,
			QuantFactor:        42 + progress*12,
			QuantOIRaw:         2 + progress*6,
			QuantImbalanceRaw:  0.04 + progress*0.22,
			QuantNetflowRaw:    1 + progress*3,
			SocialFactor:       24 + progress*30,
			SocialRankRaw:      0.25 + progress*0.5,
			SocialUpvoteRaw:    45 + progress*20,
			OnChainFactor:      47 + progress*6,
			OnChainRatioRaw:    0.55 + progress*0.9,
			OnChainBuyRaw:      0.47 + progress*0.15,
			Filled:             true,
			PriceT1:            (100 + float64(i)/10) * (1 + returnPct),
			ReturnPct:          returnPct,
			FilledAt:           baseTime.Add(time.Duration(i+15) * time.Minute).UnixMilli(),
			CreatedAt:          baseTime.Add(time.Duration(i) * time.Minute).UnixMilli(),
			UpdatedAt:          baseTime.Add(time.Duration(i+15) * time.Minute).UnixMilli(),
		})
	}

	for start := 0; start < len(rows); start += 100 {
		end := start + 100
		if end > len(rows) {
			end = len(rows)
		}
		if err := st.Shadow().CreateBatch(rows[start:end]); err != nil {
			t.Fatalf("create blend rows batch %d-%d: %v", start, end, err)
		}
	}
	for _, row := range rows {
		if err := st.Shadow().MarkFilled(row.ID, row.PriceT1, row.ReturnPct, row.FilledAt); err != nil {
			t.Fatalf("mark filled: %v", err)
		}
	}

	market.SetAdaptiveWeightStore(st)
	defer market.SetAdaptiveWeightStore(nil)
	market.InvalidateAdaptiveWeightScope("ic-blend")

	state := market.GetAdaptiveWeightState("ic-blend", "AI", "BLENDUSDT")
	expectedBlendAdaptive := float64(sectorSamples) / float64(market.AdaptiveSampleTarget)
	if math.Abs(state.BlendAdaptive-expectedBlendAdaptive) > 1e-9 {
		t.Fatalf("expected blend adaptive %.6f for %d sector samples, got %.6f", expectedBlendAdaptive, sectorSamples, state.BlendAdaptive)
	}
	if state.CoinSampleCount != sectorSamples {
		t.Fatalf("expected %d coin samples, got %d", sectorSamples, state.CoinSampleCount)
	}

	t.Logf("V3_AUDIT_BAYES_IC: Symbol=%s, Sector=%s, Alpha=%.2f, BlendAdaptive=%.4f, SectorSamples=%d, CoinSamples=%d",
		state.Symbol,
		state.Sector,
		state.Alpha,
		state.BlendAdaptive,
		state.SectorSampleCount,
		state.CoinSampleCount,
	)
}

func TestAdaptiveWeightStateIsSharedAcrossTraders(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_ic_shared_global.db")
	_ = os.Remove(dbPath)
	gdb, err := store.InitGorm(dbPath)
	if err != nil {
		t.Fatalf("open gorm db: %v", err)
	}

	st, err := store.NewFromGorm(gdb)
	if err != nil {
		t.Fatalf("create store from gorm: %v", err)
	}
	defer st.Close()
	if err := st.GormDB().AutoMigrate(&store.ShadowSnapshot{}); err != nil {
		t.Fatalf("init shadow tables: %v", err)
	}

	const sampleCount = 64
	baseTime := time.Now().UTC().Add(-3 * time.Hour)
	rows := make([]*store.ShadowSnapshot, 0, sampleCount)
	for i := 0; i < sampleCount; i++ {
		progress := float64(i) / float64(sampleCount-1)
		returnPct := -0.04 + progress*0.08
		rows = append(rows, &store.ShadowSnapshot{
			TraderID:           "shared-ai-a",
			DecisionTime:       baseTime.Add(time.Duration(i) * time.Minute).UnixMilli(),
			Symbol:             "SYNCUSDT",
			Sector:             "AI",
			ActionTaken:        i % 2,
			PriceT0:            100 + float64(i)/10,
			HeatScore:          52 + progress*18,
			MarketFactor:       80 - progress*50,
			TrendFactor:        30 + progress*45,
			DonchianFactor:     34 + progress*38,
			VolumeSpikeFactor:  25 + progress*42,
			MTFResonanceFactor: 22 + progress*44,
			QuantFactor:        38 + progress*20,
			QuantOIRaw:         1 + progress*5,
			QuantImbalanceRaw:  0.04 + progress*0.18,
			QuantNetflowRaw:    0.8 + progress*2.5,
			SocialFactor:       24 + progress*36,
			SocialRankRaw:      0.2 + progress*0.55,
			SocialUpvoteRaw:    40 + progress*22,
			OnChainFactor:      45 + progress*10,
			OnChainRatioRaw:    0.5 + progress*0.8,
			OnChainBuyRaw:      0.44 + progress*0.16,
			Filled:             true,
			PriceT1:            (100 + float64(i)/10) * (1 + returnPct),
			ReturnPct:          returnPct,
			FilledAt:           baseTime.Add(time.Duration(i+15) * time.Minute).UnixMilli(),
			CreatedAt:          baseTime.Add(time.Duration(i) * time.Minute).UnixMilli(),
			UpdatedAt:          baseTime.Add(time.Duration(i+15) * time.Minute).UnixMilli(),
		})
	}
	if err := st.Shadow().CreateBatch(rows); err != nil {
		t.Fatalf("create shared adaptive rows: %v", err)
	}

	market.SetAdaptiveWeightStore(st)
	defer market.SetAdaptiveWeightStore(nil)
	market.InvalidateAdaptiveWeightScope("")

	stateA := market.GetAdaptiveWeightState("shared-ai-a", "AI", "SYNCUSDT")
	stateB := market.GetAdaptiveWeightState("shared-ai-b", "AI", "SYNCUSDT")

	if stateA.TraderID != store.GlobalConsensusTraderID {
		t.Fatalf("expected shared pool trader id %q, got %q", store.GlobalConsensusTraderID, stateA.TraderID)
	}
	if stateB.TraderID != store.GlobalConsensusTraderID {
		t.Fatalf("expected shared pool trader id %q, got %q", store.GlobalConsensusTraderID, stateB.TraderID)
	}
	if stateA.SampleCount != sampleCount || stateB.SampleCount != sampleCount {
		t.Fatalf("expected shared sample count %d, got A=%d B=%d", sampleCount, stateA.SampleCount, stateB.SampleCount)
	}
	if stateA.CoinSampleCount != sampleCount || stateB.CoinSampleCount != sampleCount {
		t.Fatalf("expected shared coin sample count %d, got A=%d B=%d", sampleCount, stateA.CoinSampleCount, stateB.CoinSampleCount)
	}
	if stateA.GetWeightSignature() != stateB.GetWeightSignature() {
		t.Fatalf("expected identical shared DNA signature, got A=%s B=%s", stateA.GetWeightSignature(), stateB.GetWeightSignature())
	}
	if math.Abs(stateA.BlendAdaptive-stateB.BlendAdaptive) > 1e-9 {
		t.Fatalf("expected identical adaptive blend, got A=%.8f B=%.8f", stateA.BlendAdaptive, stateB.BlendAdaptive)
	}
}
