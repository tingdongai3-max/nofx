package trader

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/market"
	"nofx/store"
)

func TestAutoTraderPersistShadowSnapshotsAudit(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_shadow_audit.db")
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

	at := &AutoTrader{
		id:    "shadow-audit",
		name:  "shadow-audit",
		store: st,
	}

	ctx := &kernel.Context{
		CandidateCoins: []kernel.CandidateCoin{
			{Symbol: "DOGEUSDT", Sources: []string{"ai500", "oi_top"}},
			{Symbol: "SOLUSDT", Sources: []string{"ai500"}},
			{Symbol: "LINKUSDT", Sources: []string{"oi_top"}},
		},
		MarketDataMap: map[string]*market.Data{
			"DOGEUSDT": {
				Symbol:       "DOGEUSDT",
				Sector:       "Meme",
				CurrentPrice: 0.1862,
				HeatScore: &market.HeatScoreData{
					CompositeScore:          92.0,
					TradingScore:            94.5,
					QuantScore:              81.2,
					MarketScore:             72.4,
					TrendScore:              68.8,
					DonchianFactorScore:     74.1,
					VolumeSpikeScore:        91.5,
					MTFResonanceFactorScore: 88.2,
					QuantFactorScore:        59.2,
					QuantOIRaw:              6.1,
					QuantImbalanceRaw:       0.27,
					QuantNetflowRaw:         4.3,
					SocialScore:             95.0,
					OnChainScore:            61.7,
					OnChainRatioRaw:         1.54,
					OnChainBuyRaw:           0.71,
					RawFactorScores: map[string]float64{
						"market":            0.9,
						"trend":             1.4,
						"donchian_factor":   1.1,
						"volume_spike":      2.2,
						"mtf_resonance":     1.8,
						"quant_oi":          0.7,
						"quant_imbalance":   0.4,
						"quant_netflow":     0.9,
						"social_rank":       2.4,
						"social_upvote":     1.9,
						"onchain_ratio":     0.8,
						"onchain_buy_ratio": 0.6,
					},
					RawFactorAvailable: map[string]bool{
						"market":            true,
						"trend":             true,
						"donchian_factor":   true,
						"volume_spike":      true,
						"mtf_resonance":     true,
						"quant_oi":          true,
						"quant_imbalance":   true,
						"quant_netflow":     true,
						"social_rank":       true,
						"social_upvote":     true,
						"onchain_ratio":     true,
						"onchain_buy_ratio": true,
					},
				},
				GeckoSentiment: &market.GeckoSentimentData{
					TrendingRank:               2,
					TrendingRankScore:          29.0 / 30.0,
					SentimentVotesUpPercentage: 91.0,
				},
				VolatilityUtilization: 0.88,
			},
			"SOLUSDT": {
				Symbol:       "SOLUSDT",
				Sector:       "MajorAlt",
				CurrentPrice: 178.42,
				HeatScore: &market.HeatScoreData{
					CompositeScore:          77.5,
					TradingScore:            79.1,
					QuantScore:              71.4,
					MarketScore:             55.4,
					TrendScore:              58.1,
					DonchianFactorScore:     57.7,
					VolumeSpikeScore:        73.4,
					MTFResonanceFactorScore: 69.9,
					QuantFactorScore:        62.3,
					QuantOIRaw:              4.2,
					QuantImbalanceRaw:       0.13,
					QuantNetflowRaw:         1.9,
					SocialScore:             81.6,
					OnChainScore:            64.4,
					OnChainRatioRaw:         1.03,
					OnChainBuyRaw:           0.62,
				},
				GeckoSentiment: &market.GeckoSentimentData{
					TrendingRank:               12,
					TrendingRankScore:          19.0 / 30.0,
					SentimentVotesUpPercentage: 74.0,
				},
				VolatilityUtilization: 0.66,
			},
			"LINKUSDT": {
				Symbol:       "LINKUSDT",
				Sector:       "MajorAlt",
				CurrentPrice: 23.41,
				HeatScore: &market.HeatScoreData{
					CompositeScore:          63.8,
					TradingScore:            60.4,
					QuantScore:              58.2,
					MarketScore:             52.1,
					TrendScore:              49.8,
					DonchianFactorScore:     48.7,
					VolumeSpikeScore:        57.9,
					MTFResonanceFactorScore: 52.4,
					QuantFactorScore:        54.5,
					QuantOIRaw:              2.6,
					QuantImbalanceRaw:       0.08,
					QuantNetflowRaw:         1.1,
					SocialScore:             57.6,
					OnChainScore:            50.3,
					OnChainRatioRaw:         0.74,
					OnChainBuyRaw:           0.54,
				},
				GeckoSentiment: &market.GeckoSentimentData{
					TrendingRank:               24,
					TrendingRankScore:          7.0 / 30.0,
					SentimentVotesUpPercentage: 57.0,
				},
				VolatilityUtilization: 0.57,
			},
		},
	}

	decisionTime := time.Now().UTC()
	if err := at.persistShadowSnapshots(decisionTime, ctx); err != nil {
		t.Fatalf("persist shadow snapshots: %v", err)
	}
	if err := at.markShadowActions(decisionTime, []kernel.Decision{
		{Symbol: "SOLUSDT", Action: "open_long"},
	}); err != nil {
		t.Fatalf("mark shadow actions: %v", err)
	}

	rows, err := st.Shadow().ListByTrader("shadow-audit", 10)
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	hasRawFactors := false
	for _, row := range rows {
		if row == nil || row.RawFactors == "" {
			continue
		}
		decoded, err := row.DecodedRawFactors()
		if err != nil {
			t.Fatalf("decode raw factors: %v", err)
		}
		if len(decoded.Scores) > 0 {
			hasRawFactors = true
			break
		}
	}
	if !hasRawFactors {
		t.Fatalf("expected persisted raw_factors payload, got rows=%+v", rows)
	}
	if rows[0].VolumeSpikeFactor <= 0 && rows[1].VolumeSpikeFactor <= 0 && rows[2].VolumeSpikeFactor <= 0 {
		t.Fatalf("expected persisted volume spike factor, got rows=%+v", rows)
	}
	if rows[0].DonchianFactor <= 0 && rows[1].DonchianFactor <= 0 && rows[2].DonchianFactor <= 0 {
		t.Fatalf("expected persisted donchian factor, got rows=%+v", rows)
	}
	if rows[0].MTFResonanceFactor <= 0 && rows[1].MTFResonanceFactor <= 0 && rows[2].MTFResonanceFactor <= 0 {
		t.Fatalf("expected persisted mtf resonance factor, got rows=%+v", rows)
	}
	if rows[0].SocialRankRaw <= 0 && rows[1].SocialRankRaw <= 0 && rows[2].SocialRankRaw <= 0 {
		t.Fatalf("expected persisted social rank raw, got rows=%+v", rows)
	}
	if rows[0].SocialUpvoteRaw <= 0 && rows[1].SocialUpvoteRaw <= 0 && rows[2].SocialUpvoteRaw <= 0 {
		t.Fatalf("expected persisted social upvote raw, got rows=%+v", rows)
	}
	if rows[0].QuantOIRaw <= 0 && rows[1].QuantOIRaw <= 0 && rows[2].QuantOIRaw <= 0 {
		t.Fatalf("expected persisted quant oi raw, got rows=%+v", rows)
	}
	if rows[0].QuantImbalanceRaw <= 0 && rows[1].QuantImbalanceRaw <= 0 && rows[2].QuantImbalanceRaw <= 0 {
		t.Fatalf("expected persisted quant imbalance raw, got rows=%+v", rows)
	}
	if rows[0].QuantNetflowRaw <= 0 && rows[1].QuantNetflowRaw <= 0 && rows[2].QuantNetflowRaw <= 0 {
		t.Fatalf("expected persisted quant netflow raw, got rows=%+v", rows)
	}
	if rows[0].OnChainRatioRaw <= 0 && rows[1].OnChainRatioRaw <= 0 && rows[2].OnChainRatioRaw <= 0 {
		t.Fatalf("expected persisted onchain ratio raw, got rows=%+v", rows)
	}
	if rows[0].OnChainBuyRaw <= 0 && rows[1].OnChainBuyRaw <= 0 && rows[2].OnChainBuyRaw <= 0 {
		t.Fatalf("expected persisted onchain buy raw, got rows=%+v", rows)
	}

	t.Logf("Audit verification DB: %s", dbPath)
}

func TestAutoTraderShadowFillAuditDecoupledFromCurrentCandidates(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_shadow_fill_audit.db")
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

	decisionTime := time.Now().UTC().Add(-20 * time.Minute)
	row := &store.ShadowSnapshot{
		TraderID:           "shadow-fill",
		DecisionTime:       decisionTime.UnixMilli(),
		Symbol:             "DOGEUSDT",
		Sector:             "Meme",
		ActionTaken:        0,
		PriceT0:            0.1862,
		HeatScore:          92.0,
		TradingSub:         94.5,
		QuantSub:           81.2,
		MarketFactor:       72.4,
		TrendFactor:        68.8,
		DonchianFactor:     74.1,
		VolumeSpikeFactor:  91.5,
		MTFResonanceFactor: 88.2,
		QuantFactor:        59.2,
		SocialFactor:       95.0,
		OnChainFactor:      61.7,
		QuantOIRaw:         6.1,
		QuantImbalanceRaw:  0.27,
		QuantNetflowRaw:    4.3,
		OnChainRatioRaw:    1.54,
		OnChainBuyRaw:      0.71,
		VolUtilization:     0.88,
		CreatedAt:          decisionTime.UnixMilli(),
		UpdatedAt:          decisionTime.UnixMilli(),
	}
	if err := st.Shadow().CreateBatch([]*store.ShadowSnapshot{row}); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	at := &AutoTrader{
		id:                 "shadow-fill",
		store:              st,
		shadowTargetWindow: 15 * time.Minute,
		shadowPriceFetcher: func(symbol string, target time.Time) (float64, error) {
			if symbol != "DOGEUSDT" {
				t.Fatalf("unexpected symbol lookup: %s", symbol)
			}
			return 0.1921, nil
		},
		candidateSnapshot: CandidateSnapshot{
			TraderID:    "shadow-fill",
			TraderName:  "shadow-fill",
			UpdatedAt:   time.Now().UTC(),
			ScoreEngine: "recalculated_backcast",
			Candidates:  nil,
		},
	}

	fillNow := decisionTime.Add(16 * time.Minute)
	if err := at.processShadowFillCycle(fillNow); err != nil {
		t.Fatalf("process shadow fill cycle: %v", err)
	}

	rows, err := st.Shadow().ListByTrader("shadow-fill", 10)
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !rows[0].Filled {
		t.Fatal("expected row to be filled")
	}
	if rows[0].PriceT1 <= 0 {
		t.Fatalf("expected filled price, got %.4f", rows[0].PriceT1)
	}
	expectedGross := (0.1921 - 0.1862) / 0.1862
	expectedNet := expectedGross - ShadowRoundTripFeeRate
	if math.Abs(rows[0].ReturnPct-expectedNet) > 1e-9 {
		t.Fatalf("expected net return_pct %.6f, got %.6f", expectedNet, rows[0].ReturnPct)
	}

	t.Logf("Decoupled fill verified with empty current candidate snapshot for %s", rows[0].Symbol)
}
