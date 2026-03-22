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
			QuantFactor:        50,
			SocialFactor:       20 + progress*60,
			OnChainFactor:      50 + progress*2,
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
	if len(state.HiddenFactors) != 2 {
		t.Fatalf("expected 2 hidden factors, got %d", len(state.HiddenFactors))
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
}
