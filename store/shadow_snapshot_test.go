package store

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestShadowSnapshotStoreCreateAndFill(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	decisionTime := time.Now().UTC().Add(-20 * time.Minute).UnixMilli()
	rows := []*ShadowSnapshot{
		{
			TraderID:           "shadow-trader",
			DecisionTime:       decisionTime,
			Symbol:             "BTCUSDT",
			Sector:             "BlueChip",
			PriceT0:            102345.4,
			HeatScore:          81.3,
			TradingSub:         84.0,
			QuantSub:           72.6,
			MarketFactor:       78.1,
			TrendFactor:        74.3,
			DonchianFactor:     71.2,
			VolumeSpikeFactor:  83.6,
			MTFResonanceFactor: 79.4,
			QuantFactor:        67.4,
			QuantOIRaw:         5.8,
			QuantImbalanceRaw:  0.24,
			QuantNetflowRaw:    3.6,
			SocialFactor:       71.8,
			SocialRankRaw:      0.93,
			SocialUpvoteRaw:    82.4,
			OnChainFactor:      69.2,
			OnChainRatioRaw:    1.42,
			OnChainBuyRaw:      0.67,
			VolUtilization:     0.78,
			CreatedAt:          decisionTime,
			UpdatedAt:          decisionTime,
		},
		{
			TraderID:           "shadow-trader",
			DecisionTime:       decisionTime,
			Symbol:             "ETHUSDT",
			Sector:             "BlueChip",
			PriceT0:            3521.8,
			HeatScore:          69.4,
			TradingSub:         71.2,
			QuantSub:           64.8,
			MarketFactor:       66.2,
			TrendFactor:        68.1,
			DonchianFactor:     64.5,
			VolumeSpikeFactor:  58.4,
			MTFResonanceFactor: 55.7,
			QuantFactor:        61.4,
			QuantOIRaw:         2.1,
			QuantImbalanceRaw:  0.11,
			QuantNetflowRaw:    1.4,
			SocialFactor:       58.5,
			SocialRankRaw:      0.55,
			SocialUpvoteRaw:    64.2,
			OnChainFactor:      63.7,
			OnChainRatioRaw:    0.88,
			OnChainBuyRaw:      0.59,
			VolUtilization:     0.61,
			CreatedAt:          decisionTime,
			UpdatedAt:          decisionTime,
		},
	}
	if err := ss.CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if err := ss.MarkActionTaken("shadow-trader", decisionTime, []string{"ETHUSDT"}); err != nil {
		t.Fatalf("mark action taken: %v", err)
	}

	pending, err := ss.GetPendingFill(time.Now().UTC().Add(-15*time.Minute).UnixMilli(), 10)
	if err != nil {
		t.Fatalf("get pending fill: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending rows, got %d", len(pending))
	}

	if err := ss.MarkFilled(pending[0].ID, 104000.0, (104000.0-pending[0].PriceT0)/pending[0].PriceT0, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatalf("mark filled: %v", err)
	}

	listed, err := ss.ListByTrader("shadow-trader", 10)
	if err != nil {
		t.Fatalf("list by trader: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(listed))
	}
	consensusRows, err := ss.ListByTrader(GlobalConsensusTraderID, 10)
	if err != nil {
		t.Fatalf("list consensus rows: %v", err)
	}
	if len(consensusRows) != 2 {
		t.Fatalf("expected 2 consensus rows, got %d", len(consensusRows))
	}
	foundConsensusAction := false
	foundConsensusFilled := false
	for _, row := range consensusRows {
		if row == nil {
			continue
		}
		if row.Symbol == "ETHUSDT" && row.ActionTaken == 1 {
			foundConsensusAction = true
		}
		if row.Symbol == pending[0].Symbol && row.Filled {
			foundConsensusFilled = true
		}
	}
	if !foundConsensusAction {
		t.Fatalf("expected consensus pool action sync for ETHUSDT, got %+v", consensusRows)
	}
	if !foundConsensusFilled {
		t.Fatalf("expected consensus pool fill sync for %s, got %+v", pending[0].Symbol, consensusRows)
	}
	if listed[0].Symbol != "ETHUSDT" && listed[1].Symbol != "ETHUSDT" {
		t.Fatalf("expected ETHUSDT row in result set")
	}
	if listed[0].VolumeSpikeFactor <= 0 && listed[1].VolumeSpikeFactor <= 0 {
		t.Fatalf("expected persisted volume spike factor, got rows=%+v", listed)
	}
	if listed[0].DonchianFactor <= 0 && listed[1].DonchianFactor <= 0 {
		t.Fatalf("expected persisted donchian factor, got rows=%+v", listed)
	}
	if listed[0].MTFResonanceFactor <= 0 && listed[1].MTFResonanceFactor <= 0 {
		t.Fatalf("expected persisted mtf resonance factor, got rows=%+v", listed)
	}
	if listed[0].SocialRankRaw <= 0 && listed[1].SocialRankRaw <= 0 {
		t.Fatalf("expected persisted social rank raw, got rows=%+v", listed)
	}
	if listed[0].SocialUpvoteRaw <= 0 && listed[1].SocialUpvoteRaw <= 0 {
		t.Fatalf("expected persisted social upvote raw, got rows=%+v", listed)
	}
	if listed[0].QuantOIRaw <= 0 && listed[1].QuantOIRaw <= 0 {
		t.Fatalf("expected persisted quant oi raw, got rows=%+v", listed)
	}
	if listed[0].QuantImbalanceRaw <= 0 && listed[1].QuantImbalanceRaw <= 0 {
		t.Fatalf("expected persisted quant imbalance raw, got rows=%+v", listed)
	}
	if listed[0].QuantNetflowRaw <= 0 && listed[1].QuantNetflowRaw <= 0 {
		t.Fatalf("expected persisted quant netflow raw, got rows=%+v", listed)
	}
	if listed[0].OnChainRatioRaw <= 0 && listed[1].OnChainRatioRaw <= 0 {
		t.Fatalf("expected persisted onchain ratio raw, got rows=%+v", listed)
	}
	if listed[0].OnChainBuyRaw <= 0 && listed[1].OnChainBuyRaw <= 0 {
		t.Fatalf("expected persisted onchain buy raw, got rows=%+v", listed)
	}
}

func TestShadowSnapshotStoreMarksRebelSonOnLargeLoss(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shadow_rebel.db")
	db := openTelemetryTestDB(t, dbPath)
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	decisionTime := time.Now().UTC().Add(-18 * time.Minute).UnixMilli()
	if err := ss.CreateBatch([]*ShadowSnapshot{
		{
			TraderID:     "shadow-rebel",
			DecisionTime: decisionTime,
			Symbol:       "LUNAUSDT",
			Sector:       "Alt",
			PriceT0:      100,
			HeatScore:    88.0,
			CreatedAt:    decisionTime,
			UpdatedAt:    decisionTime,
		},
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	pending, err := ss.GetPendingFill(time.Now().UTC().Add(-15*time.Minute).UnixMilli(), 10)
	if err != nil {
		t.Fatalf("get pending fill: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending row, got %d", len(pending))
	}

	if err := ss.MarkFilled(pending[0].ID, 96.0, -0.04, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatalf("mark filled: %v", err)
	}

	listed, err := ss.ListByTrader("shadow-rebel", 10)
	if err != nil {
		t.Fatalf("list by trader: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 row, got %d", len(listed))
	}
	if listed[0].KnnAuditTag != "[REBEL_SON]" {
		t.Fatalf("expected rebel son tag, got %q", listed[0].KnnAuditTag)
	}
}

func TestShadowSnapshotStoreVerificationArtifact(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_shadow_verify.db")
	db := openTelemetryTestDB(t, dbPath)
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	_ = db.Exec("DELETE FROM shadow_snapshots WHERE trader_id = ?", "shadow-verify").Error

	now := time.Now().UTC()
	decisionTime := now.Add(-17 * time.Minute).UnixMilli()
	rows := []*ShadowSnapshot{
		{
			TraderID:           "shadow-verify",
			DecisionTime:       decisionTime,
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
			QuantOIRaw:         6.2,
			QuantImbalanceRaw:  0.28,
			QuantNetflowRaw:    4.8,
			SocialFactor:       95.0,
			SocialRankRaw:      0.96,
			SocialUpvoteRaw:    91.0,
			OnChainFactor:      61.7,
			OnChainRatioRaw:    1.66,
			OnChainBuyRaw:      0.72,
			VolUtilization:     0.88,
			CreatedAt:          decisionTime,
			UpdatedAt:          decisionTime,
		},
		{
			TraderID:           "shadow-verify",
			DecisionTime:       decisionTime,
			Symbol:             "SOLUSDT",
			Sector:             "MajorAlt",
			ActionTaken:        1,
			PriceT0:            178.42,
			HeatScore:          77.5,
			TradingSub:         79.1,
			QuantSub:           71.4,
			MarketFactor:       55.4,
			TrendFactor:        58.1,
			DonchianFactor:     57.7,
			VolumeSpikeFactor:  73.4,
			MTFResonanceFactor: 69.9,
			QuantFactor:        62.3,
			QuantOIRaw:         4.4,
			QuantImbalanceRaw:  0.14,
			QuantNetflowRaw:    2.1,
			SocialFactor:       81.6,
			SocialRankRaw:      0.61,
			SocialUpvoteRaw:    74.0,
			OnChainFactor:      64.4,
			OnChainRatioRaw:    1.08,
			OnChainBuyRaw:      0.61,
			VolUtilization:     0.66,
			CreatedAt:          decisionTime,
			UpdatedAt:          decisionTime,
		},
	}
	if err := ss.CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if err := ss.MarkActionTaken("shadow-verify", decisionTime, []string{"SOLUSDT"}); err != nil {
		t.Fatalf("mark action taken: %v", err)
	}

	pending, err := ss.GetPendingFill(now.Add(-15*time.Minute).UnixMilli(), 10)
	if err != nil {
		t.Fatalf("get pending fill: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("expected pending shadow rows")
	}
	if err := ss.MarkFilled(pending[0].ID, 0.2011, (0.2011-pending[0].PriceT0)/pending[0].PriceT0, now.UnixMilli()); err != nil {
		t.Fatalf("mark filled: %v", err)
	}

	type auditRow struct {
		Symbol             string  `gorm:"column:symbol"`
		Sector             string  `gorm:"column:sector"`
		ActionTaken        int     `gorm:"column:action_taken"`
		Filled             bool    `gorm:"column:filled"`
		PriceT0            float64 `gorm:"column:price_t0"`
		PriceT1            float64 `gorm:"column:price_t1"`
		ReturnPct          float64 `gorm:"column:return_pct"`
		MarketFactor       float64 `gorm:"column:market_factor"`
		DonchianFactor     float64 `gorm:"column:donchian_factor"`
		VolumeSpikeFactor  float64 `gorm:"column:volume_spike_factor"`
		MTFResonanceFactor float64 `gorm:"column:mtf_resonance_factor"`
		QuantOIRaw         float64 `gorm:"column:quant_oi_raw"`
		QuantImbalanceRaw  float64 `gorm:"column:quant_imbalance_raw"`
		QuantNetflowRaw    float64 `gorm:"column:quant_netflow_raw"`
		SocialFactor       float64 `gorm:"column:social_factor"`
		SocialRankRaw      float64 `gorm:"column:social_rank_raw"`
		SocialUpvoteRaw    float64 `gorm:"column:social_upvote_raw"`
		OnChainRatioRaw    float64 `gorm:"column:onchain_ratio_raw"`
		OnChainBuyRaw      float64 `gorm:"column:onchain_buy_raw"`
	}

	var auditRows []auditRow
	if err := db.Raw(`
		SELECT symbol, sector, action_taken, filled, price_t0, price_t1, return_pct, market_factor, donchian_factor, volume_spike_factor, mtf_resonance_factor, quant_oi_raw, quant_imbalance_raw, quant_netflow_raw, social_factor, social_rank_raw, social_upvote_raw, onchain_ratio_raw, onchain_buy_raw
		FROM shadow_snapshots
		WHERE trader_id = ?
		ORDER BY symbol ASC
	`, "shadow-verify").Scan(&auditRows).Error; err != nil {
		t.Fatalf("raw audit query: %v", err)
	}

	t.Logf("SQLite verification DB: %s", dbPath)
	t.Logf("SELECT symbol, sector, action_taken, filled, price_t0, price_t1, return_pct, market_factor, donchian_factor, volume_spike_factor, mtf_resonance_factor, quant_oi_raw, quant_imbalance_raw, quant_netflow_raw, social_factor, social_rank_raw, social_upvote_raw, onchain_ratio_raw, onchain_buy_raw FROM shadow_snapshots WHERE trader_id = 'shadow-verify' ORDER BY symbol ASC;")
	for _, row := range auditRows {
		t.Logf("row symbol=%s sector=%s action_taken=%d filled=%t price_t0=%.4f price_t1=%.4f return_pct=%.6f market_factor=%.1f donchian_factor=%.1f volume_spike_factor=%.1f mtf_resonance_factor=%.1f quant_oi_raw=%.2f quant_imbalance_raw=%.2f quant_netflow_raw=%.2f social_factor=%.1f social_rank_raw=%.2f social_upvote_raw=%.2f onchain_ratio_raw=%.2f onchain_buy_raw=%.2f",
			row.Symbol, row.Sector, row.ActionTaken, row.Filled, row.PriceT0, row.PriceT1, row.ReturnPct, row.MarketFactor, row.DonchianFactor, row.VolumeSpikeFactor, row.MTFResonanceFactor, row.QuantOIRaw, row.QuantImbalanceRaw, row.QuantNetflowRaw, row.SocialFactor, row.SocialRankRaw, row.SocialUpvoteRaw, row.OnChainRatioRaw, row.OnChainBuyRaw)
	}
}

func TestShadowRawFactorsRoundTrip(t *testing.T) {
	factors := ShadowRawFactors{
		Scores: map[string]float64{
			"trend":           1.8,
			"donchian_factor": 1.2,
			"quant_oi":        -0.6,
		},
		Available: map[string]bool{
			"trend":           true,
			"donchian_factor": true,
			"quant_oi":        false,
		},
	}

	raw := factors.MarshalText()
	if raw == "" {
		t.Fatal("expected raw_factors payload")
	}

	parsed, err := ParseShadowRawFactors(raw)
	if err != nil {
		t.Fatalf("parse raw_factors: %v", err)
	}
	if diff := absFloat64(parsed.Scores["trend"] - factors.Scores["trend"]); diff > 1e-9 {
		t.Fatalf("expected trend %.4f, got %.4f", factors.Scores["trend"], parsed.Scores["trend"])
	}
	if !parsed.Available["trend"] {
		t.Fatalf("expected trend availability to survive round-trip")
	}
	if parsed.Available["quant_oi"] {
		t.Fatalf("expected quant_oi availability=false after round-trip")
	}
}

func TestShadowSnapshotStoreCoinAdaptiveDefaultLimit(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	baseTime := time.Now().UTC().Add(-12 * time.Hour)
	rows := make([]*ShadowSnapshot, 0, 520)
	for i := 0; i < 520; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Minute).UnixMilli()
		rows = append(rows, &ShadowSnapshot{
			TraderID:           "coin-limit",
			DecisionTime:       ts,
			Symbol:             "BTCUSDT",
			Sector:             "BlueChip",
			ActionTaken:        i % 2,
			PriceT0:            100000 + float64(i),
			HeatScore:          60 + float64(i%7),
			TradingSub:         58 + float64(i%5),
			QuantSub:           55 + float64(i%6),
			MarketFactor:       50 + float64(i%8),
			TrendFactor:        52 + float64(i%9),
			DonchianFactor:     49 + float64(i%10),
			VolumeSpikeFactor:  48 + float64(i%11),
			MTFResonanceFactor: 47 + float64(i%12),
			QuantFactor:        51 + float64(i%13),
			SocialFactor:       53 + float64(i%14),
			OnChainFactor:      54 + float64(i%15),
			Filled:             true,
			PriceT1:            100050 + float64(i),
			ReturnPct:          float64(i%20) / 1000.0,
			FilledAt:           ts + 900000,
			CreatedAt:          ts,
			UpdatedAt:          ts + 900000,
		})
	}

	for start := 0; start < len(rows); start += 100 {
		end := start + 100
		if end > len(rows) {
			end = len(rows)
		}
		if err := ss.CreateBatch(rows[start:end]); err != nil {
			t.Fatalf("create batch %d-%d: %v", start, end, err)
		}
	}

	listed, err := ss.ListFilledForCoinAdaptive("coin-limit", "BTCUSDT", 0)
	if err != nil {
		t.Fatalf("list filled for coin adaptive: %v", err)
	}
	if len(listed) != 500 {
		t.Fatalf("expected default coin adaptive limit 500, got %d", len(listed))
	}
	if listed[0].DecisionTime <= listed[len(listed)-1].DecisionTime {
		t.Fatalf("expected descending decision times, got first=%d last=%d", listed[0].DecisionTime, listed[len(listed)-1].DecisionTime)
	}

	t.Logf("V3_AUDIT_COIN_LIMIT: trader=%s symbol=%s rows=%d first_decision_time=%d last_decision_time=%d",
		"coin-limit",
		"BTCUSDT",
		len(listed),
		listed[0].DecisionTime,
		listed[len(listed)-1].DecisionTime,
	)
	t.Logf("verification tag=%s", fmt.Sprintf("%s-%s-%d", "coin-limit", "BTCUSDT", len(listed)))
}

func TestRefreshCacheRunsRegisteredHooks(t *testing.T) {
	calls := 0
	unregister := RegisterCacheRefreshHook(func() error {
		calls++
		return nil
	})
	defer unregister()

	if err := RefreshCache(); err != nil {
		t.Fatalf("refresh cache: %v", err)
	}
	if err := RefreshCache(); err != nil {
		t.Fatalf("refresh cache second pass: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected refresh cache hook to run twice, got %d", calls)
	}

	NotifyMigration()
}

func TestShadowSnapshotStoreListPerformanceBinsByTrader(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	baseTime := time.Now().UTC().Add(-2 * time.Hour).UnixMilli()
	rows := []*ShadowSnapshot{
		{
			TraderID:     "binning-trader",
			DecisionTime: baseTime + 1,
			Symbol:       "A",
			Sector:       "Alpha",
			HeatScore:    72.4,
			PriceT0:      100,
			PriceT1:      110,
			ReturnPct:    0.10,
			Filled:       true,
			FilledAt:     baseTime + 900000,
			CreatedAt:    baseTime + 1,
			UpdatedAt:    baseTime + 900000,
		},
		{
			TraderID:     "binning-trader",
			DecisionTime: baseTime + 2,
			Symbol:       "B",
			Sector:       "Alpha",
			HeatScore:    74.9,
			PriceT0:      100,
			PriceT1:      96,
			ReturnPct:    -0.04,
			Filled:       true,
			FilledAt:     baseTime + 900001,
			CreatedAt:    baseTime + 2,
			UpdatedAt:    baseTime + 900001,
		},
		{
			TraderID:     "binning-trader",
			DecisionTime: baseTime + 3,
			Symbol:       "C",
			Sector:       "Alpha",
			HeatScore:    79.1,
			PriceT0:      100,
			PriceT1:      105,
			ReturnPct:    0.05,
			Filled:       true,
			FilledAt:     baseTime + 900002,
			CreatedAt:    baseTime + 3,
			UpdatedAt:    baseTime + 900002,
		},
		{
			TraderID:     "binning-trader",
			DecisionTime: baseTime + 4,
			Symbol:       "D",
			Sector:       "Beta",
			HeatScore:    80.0,
			PriceT0:      100,
			PriceT1:      103,
			ReturnPct:    0.03,
			Filled:       true,
			FilledAt:     baseTime + 900003,
			CreatedAt:    baseTime + 4,
			UpdatedAt:    baseTime + 900003,
		},
		{
			TraderID:     "binning-trader",
			DecisionTime: baseTime + 5,
			Symbol:       "E",
			Sector:       "Beta",
			HeatScore:    84.6,
			PriceT0:      100,
			PriceT1:      102,
			ReturnPct:    0.02,
			Filled:       true,
			FilledAt:     baseTime + 900004,
			CreatedAt:    baseTime + 5,
			UpdatedAt:    baseTime + 900004,
		},
		{
			TraderID:     "other-trader",
			DecisionTime: baseTime + 6,
			Symbol:       "Z",
			Sector:       "Alpha",
			HeatScore:    72.0,
			PriceT0:      100,
			PriceT1:      50,
			ReturnPct:    -0.50,
			Filled:       true,
			FilledAt:     baseTime + 900005,
			CreatedAt:    baseTime + 6,
			UpdatedAt:    baseTime + 900005,
		},
	}
	if err := ss.CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	bins, err := ss.ListPerformanceBinsByTrader("binning-trader", false)
	if err != nil {
		t.Fatalf("list performance bins: %v", err)
	}
	if len(bins) != 3 {
		t.Fatalf("expected 3 bins, got %d", len(bins))
	}

	if bins[0].BinStart != 70 || bins[0].BinLabel != "70-75" {
		t.Fatalf("unexpected first bin: %+v", bins[0])
	}
	if bins[0].TradeCount != 2 {
		t.Fatalf("expected 2 rows in 70-75 bin, got %d", bins[0].TradeCount)
	}
	if diff := absFloat64(bins[0].ExpectedValueLong - expectedValueLong(0.10, -0.04)); diff > 1e-9 {
		t.Fatalf("expected 70-75 long EV %.10f, got %.10f", expectedValueLong(0.10, -0.04), bins[0].ExpectedValueLong)
	}
	if diff := absFloat64(bins[0].MedianExpectedValueLong - medianLogLong(0.10, -0.04)); diff > 1e-9 {
		t.Fatalf("expected 70-75 long median EV %.10f, got %.10f", medianLogLong(0.10, -0.04), bins[0].MedianExpectedValueLong)
	}
	if diff := absFloat64(bins[0].ExpectedValueShort - expectedValueShort(0.10, -0.04)); diff > 1e-9 {
		t.Fatalf("expected 70-75 short EV %.10f, got %.10f", expectedValueShort(0.10, -0.04), bins[0].ExpectedValueShort)
	}
	if diff := absFloat64(bins[0].MedianExpectedValueShort - medianLogShort(0.10, -0.04)); diff > 1e-9 {
		t.Fatalf("expected 70-75 short median EV %.10f, got %.10f", medianLogShort(0.10, -0.04), bins[0].MedianExpectedValueShort)
	}
	if diff := absFloat64(bins[0].ProfitFactorLong - 2.5); diff > 1e-9 {
		t.Fatalf("expected 70-75 long PF 2.5, got %.10f", bins[0].ProfitFactorLong)
	}
	if diff := absFloat64(bins[0].ProfitFactorShort - 0.4); diff > 1e-9 {
		t.Fatalf("expected 70-75 short PF 0.4, got %.10f", bins[0].ProfitFactorShort)
	}

	if bins[1].BinStart != 75 || bins[1].BinLabel != "75-80" {
		t.Fatalf("unexpected second bin: %+v", bins[1])
	}
	if bins[1].TradeCount != 1 {
		t.Fatalf("expected 1 row in 75-80 bin, got %d", bins[1].TradeCount)
	}
	if diff := absFloat64(bins[1].ExpectedValueLong - expectedValueLong(0.05)); diff > 1e-9 {
		t.Fatalf("expected 75-80 long EV %.10f, got %.10f", expectedValueLong(0.05), bins[1].ExpectedValueLong)
	}
	if diff := absFloat64(bins[1].MedianExpectedValueLong - medianLogLong(0.05)); diff > 1e-9 {
		t.Fatalf("expected 75-80 long median EV %.10f, got %.10f", medianLogLong(0.05), bins[1].MedianExpectedValueLong)
	}
	if diff := absFloat64(bins[1].ExpectedValueShort - expectedValueShort(0.05)); diff > 1e-9 {
		t.Fatalf("expected 75-80 short EV %.10f, got %.10f", expectedValueShort(0.05), bins[1].ExpectedValueShort)
	}
	if diff := absFloat64(bins[1].MedianExpectedValueShort - medianLogShort(0.05)); diff > 1e-9 {
		t.Fatalf("expected 75-80 short median EV %.10f, got %.10f", medianLogShort(0.05), bins[1].MedianExpectedValueShort)
	}
	if diff := absFloat64(bins[1].ProfitFactorLong - 99.9); diff > 1e-9 {
		t.Fatalf("expected capped 75-80 long PF 99.9, got %.10f", bins[1].ProfitFactorLong)
	}
	if diff := absFloat64(bins[1].ProfitFactorShort - 0); diff > 1e-9 {
		t.Fatalf("expected capped 75-80 short PF 0, got %.10f", bins[1].ProfitFactorShort)
	}

	if bins[2].BinStart != 80 || bins[2].BinLabel != "80-85" {
		t.Fatalf("unexpected third bin: %+v", bins[2])
	}
	if bins[2].TradeCount != 2 {
		t.Fatalf("expected 2 rows in 80-85 bin, got %d", bins[2].TradeCount)
	}
	if diff := absFloat64(bins[2].ExpectedValueLong - expectedValueLong(0.03, 0.02)); diff > 1e-9 {
		t.Fatalf("expected 80-85 long EV %.10f, got %.10f", expectedValueLong(0.03, 0.02), bins[2].ExpectedValueLong)
	}
	if diff := absFloat64(bins[2].MedianExpectedValueLong - medianLogLong(0.03, 0.02)); diff > 1e-9 {
		t.Fatalf("expected 80-85 long median EV %.10f, got %.10f", medianLogLong(0.03, 0.02), bins[2].MedianExpectedValueLong)
	}
	if diff := absFloat64(bins[2].ExpectedValueShort - expectedValueShort(0.03, 0.02)); diff > 1e-9 {
		t.Fatalf("expected 80-85 short EV %.10f, got %.10f", expectedValueShort(0.03, 0.02), bins[2].ExpectedValueShort)
	}
	if diff := absFloat64(bins[2].MedianExpectedValueShort - medianLogShort(0.03, 0.02)); diff > 1e-9 {
		t.Fatalf("expected 80-85 short median EV %.10f, got %.10f", medianLogShort(0.03, 0.02), bins[2].MedianExpectedValueShort)
	}
	if diff := absFloat64(bins[2].ProfitFactorLong - 99.9); diff > 1e-9 {
		t.Fatalf("expected capped 80-85 long PF 99.9, got %.10f", bins[2].ProfitFactorLong)
	}
	if diff := absFloat64(bins[2].ProfitFactorShort - 0); diff > 1e-9 {
		t.Fatalf("expected capped 80-85 short PF 0, got %.10f", bins[2].ProfitFactorShort)
	}

	alphaBins, err := ss.ListPerformanceBinsBySector("binning-trader", "Alpha", false)
	if err != nil {
		t.Fatalf("list sector performance bins: %v", err)
	}
	if len(alphaBins) != 2 {
		t.Fatalf("expected 2 alpha sector bins, got %d", len(alphaBins))
	}
	if alphaBins[0].BinStart != 70 || alphaBins[1].BinStart != 75 {
		t.Fatalf("unexpected alpha sector bins: %+v", alphaBins)
	}

	symbolBins, err := ss.ListPerformanceBinsBySymbol("binning-trader", "D", false)
	if err != nil {
		t.Fatalf("list symbol performance bins: %v", err)
	}
	if len(symbolBins) != 1 || symbolBins[0].BinStart != 80 {
		t.Fatalf("unexpected symbol bins: %+v", symbolBins)
	}
}

func TestShadowSnapshotStoreListPerformanceBinsAlignsShortBiasDirectionality(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	baseTime := time.Now().UTC().Add(-45 * time.Minute).UnixMilli()
	rows := []*ShadowSnapshot{
		{
			TraderID:     "short-bias",
			DecisionTime: baseTime + 1,
			Symbol:       "SHORTAUSDT",
			Sector:       "AI",
			HeatScore:    32.0,
			PriceT0:      100,
			PriceT1:      96,
			ReturnPct:    -0.04,
			Filled:       true,
			FilledAt:     baseTime + 900001,
			CreatedAt:    baseTime + 1,
			UpdatedAt:    baseTime + 900001,
		},
		{
			TraderID:     "short-bias",
			DecisionTime: baseTime + 2,
			Symbol:       "SHORTBUSDT",
			Sector:       "AI",
			HeatScore:    34.0,
			PriceT0:      100,
			PriceT1:      98,
			ReturnPct:    -0.02,
			Filled:       true,
			FilledAt:     baseTime + 900002,
			CreatedAt:    baseTime + 2,
			UpdatedAt:    baseTime + 900002,
		},
		{
			TraderID:     "short-bias",
			DecisionTime: baseTime + 3,
			Symbol:       "SHORTCUSDT",
			Sector:       "AI",
			HeatScore:    31.0,
			PriceT0:      100,
			PriceT1:      101,
			ReturnPct:    0.01,
			Filled:       true,
			FilledAt:     baseTime + 900003,
			CreatedAt:    baseTime + 3,
			UpdatedAt:    baseTime + 900003,
		},
		{
			TraderID:     "short-bias",
			DecisionTime: baseTime + 4,
			Symbol:       "WRONGAUSDT",
			Sector:       "AI",
			HeatScore:    37.0,
			PriceT0:      100,
			PriceT1:      103,
			ReturnPct:    0.03,
			Filled:       true,
			FilledAt:     baseTime + 900004,
			CreatedAt:    baseTime + 4,
			UpdatedAt:    baseTime + 900004,
		},
		{
			TraderID:     "short-bias",
			DecisionTime: baseTime + 5,
			Symbol:       "WRONGBUSDT",
			Sector:       "AI",
			HeatScore:    36.0,
			PriceT0:      100,
			PriceT1:      102,
			ReturnPct:    0.02,
			Filled:       true,
			FilledAt:     baseTime + 900005,
			CreatedAt:    baseTime + 5,
			UpdatedAt:    baseTime + 900005,
		},
		{
			TraderID:     "short-bias",
			DecisionTime: baseTime + 6,
			Symbol:       "WRONGCUSDT",
			Sector:       "AI",
			HeatScore:    39.0,
			PriceT0:      100,
			PriceT1:      99,
			ReturnPct:    -0.01,
			Filled:       true,
			FilledAt:     baseTime + 900006,
			CreatedAt:    baseTime + 6,
			UpdatedAt:    baseTime + 900006,
		},
	}
	if err := ss.CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	bins, err := ss.ListPerformanceBinsByTrader("short-bias", false)
	if err != nil {
		t.Fatalf("list performance bins: %v", err)
	}
	if len(bins) != 2 {
		t.Fatalf("expected 2 short-biased bins, got %+v", bins)
	}

	if bins[0].BinStart != 30 {
		t.Fatalf("expected first short bin to start at 30, got %+v", bins[0])
	}
	if diff := absFloat64(bins[0].ExpectedValueLong - expectedValueLong(-0.04, -0.02, 0.01)); diff > 1e-9 {
		t.Fatalf("expected 30-35 long EV %.10f, got %.10f", expectedValueLong(-0.04, -0.02, 0.01), bins[0].ExpectedValueLong)
	}
	if diff := absFloat64(bins[0].ProfitFactorLong - (1.0 / 6.0)); diff > 1e-9 {
		t.Fatalf("expected 30-35 long profit factor %.10f, got %.10f", 1.0/6.0, bins[0].ProfitFactorLong)
	}
	if diff := absFloat64(bins[0].ExpectedValueShort - expectedValueShort(-0.04, -0.02, 0.01)); diff > 1e-9 {
		t.Fatalf("expected 30-35 short EV %.10f, got %.10f", expectedValueShort(-0.04, -0.02, 0.01), bins[0].ExpectedValueShort)
	}
	if diff := absFloat64(bins[0].ProfitFactorShort - 6.0); diff > 1e-9 {
		t.Fatalf("expected 30-35 short profit factor 6.0, got %.10f", bins[0].ProfitFactorShort)
	}

	if bins[1].BinStart != 35 {
		t.Fatalf("expected second short bin to start at 35, got %+v", bins[1])
	}
	if diff := absFloat64(bins[1].ExpectedValueLong - expectedValueLong(0.03, 0.02, -0.01)); diff > 1e-9 {
		t.Fatalf("expected 35-40 long EV %.10f, got %.10f", expectedValueLong(0.03, 0.02, -0.01), bins[1].ExpectedValueLong)
	}
	if diff := absFloat64(bins[1].ProfitFactorLong - 5.0); diff > 1e-9 {
		t.Fatalf("expected 35-40 long profit factor 5.0, got %.10f", bins[1].ProfitFactorLong)
	}
	if diff := absFloat64(bins[1].ExpectedValueShort - expectedValueShort(0.03, 0.02, -0.01)); diff > 1e-9 {
		t.Fatalf("expected 35-40 short EV %.10f, got %.10f", expectedValueShort(0.03, 0.02, -0.01), bins[1].ExpectedValueShort)
	}
	if diff := absFloat64(bins[1].ProfitFactorShort - 0.2); diff > 1e-9 {
		t.Fatalf("expected 35-40 short profit factor 0.2, got %.10f", bins[1].ProfitFactorShort)
	}
}

func TestShadowSnapshotStoreListPerformanceBinsBySymbolShrinksSparseBinTowardSector(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	baseTime := time.Now().UTC().Add(-90 * time.Minute).UnixMilli()
	rows := make([]*ShadowSnapshot, 0, 20)
	for i := 0; i < 19; i++ {
		returnPct := -0.01
		if i < 10 {
			returnPct = 0.02
		}
		ts := baseTime + int64(i+1)
		rows = append(rows, &ShadowSnapshot{
			TraderID:     "binning-shrink",
			DecisionTime: ts,
			Symbol:       fmt.Sprintf("AI%02dUSDT", i),
			Sector:       "AI",
			HeatScore:    72.0,
			PriceT0:      100,
			PriceT1:      100 * (1 + returnPct),
			ReturnPct:    returnPct,
			Filled:       true,
			FilledAt:     ts + 900000,
			CreatedAt:    ts,
			UpdatedAt:    ts + 900000,
		})
	}
	rows = append(rows, &ShadowSnapshot{
		TraderID:     "binning-shrink",
		DecisionTime: baseTime + 25,
		Symbol:       "RIVERUSDT",
		Sector:       "AI",
		HeatScore:    72.0,
		PriceT0:      100,
		PriceT1:      102,
		ReturnPct:    0.02,
		Filled:       true,
		FilledAt:     baseTime + 900025,
		CreatedAt:    baseTime + 25,
		UpdatedAt:    baseTime + 900025,
	})
	if err := ss.CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	bins, err := ss.ListPerformanceBinsBySymbol("binning-shrink", "RIVERUSDT", false)
	if err != nil {
		t.Fatalf("list symbol performance bins: %v", err)
	}
	if len(bins) != 1 {
		t.Fatalf("expected 1 symbol bin, got %d", len(bins))
	}

	expectedLongEV := (1.0/30.0)*expectedValueLong(0.02) + (29.0/30.0)*expectedValueLong(append(repeatReturns(0.02, 11), repeatReturns(-0.01, 9)...)...)
	expectedLongProfitFactor := (1.0/30.0)*99.9 + (29.0/30.0)*(0.22/0.09)
	expectedShortEV := (1.0/30.0)*expectedValueShort(0.02) + (29.0/30.0)*expectedValueShort(append(repeatReturns(0.02, 11), repeatReturns(-0.01, 9)...)...)
	expectedShortProfitFactor := (29.0 / 30.0) * (0.09 / 0.22)
	if bins[0].TradeCount != 1 {
		t.Fatalf("expected sparse symbol bin trade count 1, got %d", bins[0].TradeCount)
	}
	if !bins[0].Smoothed || bins[0].SmoothedBy != "sector" {
		t.Fatalf("expected sector-smoothed symbol bin, got %+v", bins[0])
	}
	if diff := absFloat64(bins[0].ExpectedValueLong - expectedLongEV); diff > 1e-9 {
		t.Fatalf("expected smoothed long EV %.10f, got %.10f", expectedLongEV, bins[0].ExpectedValueLong)
	}
	if diff := absFloat64(bins[0].ProfitFactorLong - expectedLongProfitFactor); diff > 1e-9 {
		t.Fatalf("expected smoothed long profit factor %.10f, got %.10f", expectedLongProfitFactor, bins[0].ProfitFactorLong)
	}
	if diff := absFloat64(bins[0].ExpectedValueShort - expectedShortEV); diff > 1e-9 {
		t.Fatalf("expected smoothed short EV %.10f, got %.10f", expectedShortEV, bins[0].ExpectedValueShort)
	}
	if diff := absFloat64(bins[0].ProfitFactorShort - expectedShortProfitFactor); diff > 1e-9 {
		t.Fatalf("expected smoothed short profit factor %.10f, got %.10f", expectedShortProfitFactor, bins[0].ProfitFactorShort)
	}
}

func TestShadowSnapshotStoreListPerformanceBinsBySectorShrinksSparseBinTowardGlobal(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	baseTime := time.Now().UTC().Add(-70 * time.Minute).UnixMilli()
	rows := make([]*ShadowSnapshot, 0, 20)
	for i := 0; i < 19; i++ {
		returnPct := -0.01
		if i < 8 {
			returnPct = 0.02
		}
		ts := baseTime + int64(i+1)
		rows = append(rows, &ShadowSnapshot{
			TraderID:     "sector-shrink",
			DecisionTime: ts,
			Symbol:       fmt.Sprintf("GLOBAL%02dUSDT", i),
			Sector:       "Meme",
			HeatScore:    82.0,
			PriceT0:      100,
			PriceT1:      100 * (1 + returnPct),
			ReturnPct:    returnPct,
			Filled:       true,
			FilledAt:     ts + 900000,
			CreatedAt:    ts,
			UpdatedAt:    ts + 900000,
		})
	}
	rows = append(rows, &ShadowSnapshot{
		TraderID:     "sector-shrink",
		DecisionTime: baseTime + 25,
		Symbol:       "SECTORONEUSDT",
		Sector:       "AI",
		HeatScore:    82.0,
		PriceT0:      100,
		PriceT1:      103,
		ReturnPct:    0.03,
		Filled:       true,
		FilledAt:     baseTime + 900025,
		CreatedAt:    baseTime + 25,
		UpdatedAt:    baseTime + 900025,
	})
	if err := ss.CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	bins, err := ss.ListPerformanceBinsBySector("sector-shrink", "AI", false)
	if err != nil {
		t.Fatalf("list sector performance bins: %v", err)
	}
	if len(bins) != 1 {
		t.Fatalf("expected 1 sector bin, got %d", len(bins))
	}

	expectedLongEV := (1.0/30.0)*expectedValueLong(0.03) + (29.0/30.0)*expectedValueLong(append(append(repeatReturns(0.02, 8), repeatReturns(-0.01, 11)...), 0.03)...)
	expectedLongProfitFactor := (1.0/30.0)*99.9 + (29.0/30.0)*(0.19/0.11)
	expectedShortEV := (1.0/30.0)*expectedValueShort(0.03) + (29.0/30.0)*expectedValueShort(append(append(repeatReturns(0.02, 8), repeatReturns(-0.01, 11)...), 0.03)...)
	expectedShortProfitFactor := (29.0 / 30.0) * (0.11 / 0.19)
	if bins[0].TradeCount != 1 {
		t.Fatalf("expected sparse sector bin trade count 1, got %d", bins[0].TradeCount)
	}
	if !bins[0].Smoothed || bins[0].SmoothedBy != "global" {
		t.Fatalf("expected global-smoothed sector bin, got %+v", bins[0])
	}
	if diff := absFloat64(bins[0].ExpectedValueLong - expectedLongEV); diff > 1e-9 {
		t.Fatalf("expected smoothed sector long EV %.10f, got %.10f", expectedLongEV, bins[0].ExpectedValueLong)
	}
	if diff := absFloat64(bins[0].ProfitFactorLong - expectedLongProfitFactor); diff > 1e-9 {
		t.Fatalf("expected smoothed sector long profit factor %.10f, got %.10f", expectedLongProfitFactor, bins[0].ProfitFactorLong)
	}
	if diff := absFloat64(bins[0].ExpectedValueShort - expectedShortEV); diff > 1e-9 {
		t.Fatalf("expected smoothed sector short EV %.10f, got %.10f", expectedShortEV, bins[0].ExpectedValueShort)
	}
	if diff := absFloat64(bins[0].ProfitFactorShort - expectedShortProfitFactor); diff > 1e-9 {
		t.Fatalf("expected smoothed sector short profit factor %.10f, got %.10f", expectedShortProfitFactor, bins[0].ProfitFactorShort)
	}
}

func TestShadowSnapshotStoreListPerformanceBinsBySymbolFallsBackToGlobalWhenSectorBinMissing(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	baseTime := time.Now().UTC().Add(-40 * time.Minute).UnixMilli()
	rows := []*ShadowSnapshot{
		{
			TraderID:     "symbol-global-fallback",
			DecisionTime: baseTime + 1,
			Symbol:       "RIVERUSDT",
			Sector:       "",
			HeatScore:    86.0,
			PriceT0:      100,
			PriceT1:      103,
			ReturnPct:    0.03,
			Filled:       true,
			FilledAt:     baseTime + 900001,
			CreatedAt:    baseTime + 1,
			UpdatedAt:    baseTime + 900001,
		},
		{
			TraderID:     "symbol-global-fallback",
			DecisionTime: baseTime + 2,
			Symbol:       "RIVERUSDT",
			Sector:       "AI",
			HeatScore:    52.0,
			PriceT0:      100,
			PriceT1:      101,
			ReturnPct:    0.01,
			Filled:       true,
			FilledAt:     baseTime + 900002,
			CreatedAt:    baseTime + 2,
			UpdatedAt:    baseTime + 900002,
		},
		{
			TraderID:     "symbol-global-fallback",
			DecisionTime: baseTime + 3,
			Symbol:       "MEME01USDT",
			Sector:       "Meme",
			HeatScore:    86.0,
			PriceT0:      100,
			PriceT1:      99,
			ReturnPct:    -0.01,
			Filled:       true,
			FilledAt:     baseTime + 900003,
			CreatedAt:    baseTime + 3,
			UpdatedAt:    baseTime + 900003,
		},
		{
			TraderID:     "symbol-global-fallback",
			DecisionTime: baseTime + 4,
			Symbol:       "MEME02USDT",
			Sector:       "Meme",
			HeatScore:    86.0,
			PriceT0:      100,
			PriceT1:      99,
			ReturnPct:    -0.01,
			Filled:       true,
			FilledAt:     baseTime + 900004,
			CreatedAt:    baseTime + 4,
			UpdatedAt:    baseTime + 900004,
		},
	}
	if err := ss.CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	bins, err := ss.ListPerformanceBinsBySymbol("symbol-global-fallback", "RIVERUSDT", false)
	if err != nil {
		t.Fatalf("list symbol performance bins: %v", err)
	}
	if len(bins) != 2 {
		t.Fatalf("expected 2 symbol bins, got %d", len(bins))
	}

	expectedLongProfitFactor := (1.0/30.0)*99.9 + (29.0/30.0)*(0.03/0.02)
	expectedLongEV := (1.0/30.0)*expectedValueLong(0.03) + (29.0/30.0)*expectedValueLong(0.03, -0.01, -0.01)
	expectedShortProfitFactor := (29.0 / 30.0) * (2.0 / 3.0)
	expectedShortEV := (1.0/30.0)*expectedValueShort(0.03) + (29.0/30.0)*expectedValueShort(0.03, -0.01, -0.01)
	var targetBin *ScoreBinPerformance
	for _, bin := range bins {
		if bin.BinStart == 85 {
			targetBin = bin
			break
		}
	}
	if targetBin == nil {
		t.Fatalf("expected 85-90 symbol bin, got %+v", bins)
	}
	if !targetBin.Smoothed || targetBin.SmoothedBy != "global" {
		t.Fatalf("expected symbol bin to fall back to global bin smoothing, got %+v", targetBin)
	}
	if diff := absFloat64(targetBin.ProfitFactorLong - expectedLongProfitFactor); diff > 1e-9 {
		t.Fatalf("expected global-smoothed long profit factor %.10f, got %.10f", expectedLongProfitFactor, targetBin.ProfitFactorLong)
	}
	if diff := absFloat64(targetBin.ExpectedValueLong - expectedLongEV); diff > 1e-9 {
		t.Fatalf("expected global-smoothed long EV %.10f, got %.10f", expectedLongEV, targetBin.ExpectedValueLong)
	}
	if diff := absFloat64(targetBin.ProfitFactorShort - expectedShortProfitFactor); diff > 1e-9 {
		t.Fatalf("expected global-smoothed short profit factor %.10f, got %.10f", expectedShortProfitFactor, targetBin.ProfitFactorShort)
	}
	if diff := absFloat64(targetBin.ExpectedValueShort - expectedShortEV); diff > 1e-9 {
		t.Fatalf("expected global-smoothed short EV %.10f, got %.10f", expectedShortEV, targetBin.ExpectedValueShort)
	}
}

func TestShadowSnapshotStoreListPerformanceBinsUsesSevenDayTimeWindow(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	baseTime := time.Now().UTC().Add(-10 * 24 * time.Hour)
	rows := make([]*ShadowSnapshot, 0, 20)
	for i := 0; i < 20; i++ {
		heatScore := 90.0
		if i >= 12 {
			heatScore = 50.0
		}
		ts := baseTime.Add(time.Duration(i) * time.Minute).UnixMilli()
		rows = append(rows, &ShadowSnapshot{
			TraderID:     "time-window",
			DecisionTime: ts,
			Symbol:       fmt.Sprintf("TIME%04dUSDT", i),
			Sector:       "Window",
			HeatScore:    heatScore,
			PriceT0:      100,
			PriceT1:      101,
			ReturnPct:    0.01,
			Filled:       true,
			FilledAt:     ts + 900000,
			CreatedAt:    ts,
			UpdatedAt:    ts + 900000,
		})
	}
	for i := 0; i < 8; i++ {
		rows[i].DecisionTime = time.Now().UTC().Add(-8 * 24 * time.Hour).Add(time.Duration(i) * time.Minute).UnixMilli()
		rows[i].FilledAt = rows[i].DecisionTime + 900000
		rows[i].CreatedAt = rows[i].DecisionTime
		rows[i].UpdatedAt = rows[i].FilledAt
	}
	for i := 8; i < len(rows); i++ {
		rows[i].DecisionTime = time.Now().UTC().Add(-6 * 24 * time.Hour).Add(time.Duration(i) * time.Minute).UnixMilli()
		rows[i].FilledAt = rows[i].DecisionTime + 900000
		rows[i].CreatedAt = rows[i].DecisionTime
		rows[i].UpdatedAt = rows[i].FilledAt
	}
	if err := ss.CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	bins, err := ss.ListPerformanceBinsByTrader("time-window", false)
	if err != nil {
		t.Fatalf("list performance bins: %v", err)
	}
	if len(bins) != 2 {
		t.Fatalf("expected only two in-window bins to remain, got %+v", bins)
	}
	if bins[0].BinStart != 50 || bins[1].BinStart != 90 {
		t.Fatalf("expected in-window bins 50 and 90, got %+v", bins)
	}
	if bins[0].TradeCount != 8 || bins[1].TradeCount != 4 {
		t.Fatalf("expected seven-day time window to keep 8 rows in 50-55 and 4 rows in 90-95, got %+v", bins)
	}
}

func TestShadowSnapshotStoreListPerformanceBinsBySymbolUses5000SampleLimit(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	baseTime := time.Now().UTC().Add(-14 * 24 * time.Hour)
	rows := make([]*ShadowSnapshot, 0, 5105)
	for i := 0; i < 5105; i++ {
		heatScore := 90.0
		if i >= 105 {
			heatScore = 50.0
		}
		ts := baseTime.Add(time.Duration(i) * time.Minute).UnixMilli()
		rows = append(rows, &ShadowSnapshot{
			TraderID:     "symbol-window",
			DecisionTime: ts,
			Symbol:       "RIVERUSDT",
			Sector:       "AI",
			HeatScore:    heatScore,
			PriceT0:      100,
			PriceT1:      101,
			ReturnPct:    0.01,
			Filled:       true,
			FilledAt:     ts + 900000,
			CreatedAt:    ts,
			UpdatedAt:    ts + 900000,
		})
	}
	for start := 0; start < len(rows); start += 250 {
		end := start + 250
		if end > len(rows) {
			end = len(rows)
		}
		if err := ss.CreateBatch(rows[start:end]); err != nil {
			t.Fatalf("create batch %d-%d: %v", start, end, err)
		}
	}

	bins, err := ss.ListPerformanceBinsBySymbol("symbol-window", "RIVERUSDT", false)
	if err != nil {
		t.Fatalf("list symbol performance bins: %v", err)
	}
	if len(bins) != 1 {
		t.Fatalf("expected only 50-55 bin after symbol 5000 cap, got %+v", bins)
	}
	if bins[0].BinStart != 50 {
		t.Fatalf("expected capped symbol bin 50, got %+v", bins[0])
	}
	if bins[0].TradeCount != 5000 {
		t.Fatalf("expected symbol performance cap 5000, got %d", bins[0].TradeCount)
	}
}

func TestShadowSnapshotStoreListSmoothedPerformanceBinsKeepsSharpBuckets(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ss := NewShadowSnapshotStore(db)
	if err := ss.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	baseTime := time.Now().UTC().Add(-30 * time.Minute).UnixMilli()
	rows := []*ShadowSnapshot{
		{
			TraderID:     "smoothed-window",
			DecisionTime: baseTime + 1,
			Symbol:       "EDGEAUSDT",
			Sector:       "AI",
			HeatScore:    39.0,
			PriceT0:      100,
			PriceT1:      104,
			ReturnPct:    0.04,
			Filled:       true,
			FilledAt:     baseTime + 900001,
			CreatedAt:    baseTime + 1,
			UpdatedAt:    baseTime + 900001,
		},
		{
			TraderID:     "smoothed-window",
			DecisionTime: baseTime + 2,
			Symbol:       "EDGEBUSDT",
			Sector:       "AI",
			HeatScore:    41.0,
			PriceT0:      100,
			PriceT1:      98,
			ReturnPct:    -0.02,
			Filled:       true,
			FilledAt:     baseTime + 900002,
			CreatedAt:    baseTime + 2,
			UpdatedAt:    baseTime + 900002,
		},
	}
	if err := ss.CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	bins, err := ss.ListSmoothedPerformanceBinsByTrader("smoothed-window", PERFORMANCE_WINDOW_SIZE_DEFAULT, false)
	if err != nil {
		t.Fatalf("list smoothed performance bins: %v", err)
	}
	if len(bins) != 2 {
		t.Fatalf("expected two raw buckets without neighbor drag, got %+v", bins)
	}

	expected := map[int]struct {
		long  float64
		short float64
	}{
		39: {long: expectedValueLong(0.04), short: expectedValueShort(0.04)},
		41: {long: expectedValueLong(-0.02), short: expectedValueShort(-0.02)},
	}
	for _, bin := range bins {
		want, ok := expected[bin.BinStart]
		if !ok {
			t.Fatalf("unexpected raw bucket %+v", bin)
		}
		if bin.TradeCount != 1 {
			t.Fatalf("expected raw bucket at %d to contain exactly one sample, got %+v", bin.BinStart, bin)
		}
		if bin.Smoothed {
			t.Fatalf("expected raw bucket at %d to remain unsmoothed, got %+v", bin.BinStart, bin)
		}
		if diff := absFloat64(bin.ExpectedValueLong - want.long); diff > 1e-9 {
			t.Fatalf("expected raw long EV %.10f at %d, got %.10f", want.long, bin.BinStart, bin.ExpectedValueLong)
		}
		if diff := absFloat64(bin.ExpectedValueShort - want.short); diff > 1e-9 {
			t.Fatalf("expected raw short EV %.10f at %d, got %.10f", want.short, bin.BinStart, bin.ExpectedValueShort)
		}
	}
}

func TestResolvePerformanceBiasRequiresPositiveEVAndPFEdge(t *testing.T) {
	longBin := &ScoreBinPerformance{
		ExpectedValueLong:  0.012,
		ProfitFactorLong:   2.10,
		ExpectedValueShort: 0.003,
		ProfitFactorShort:  1.05,
	}
	bias, expectedEV := ResolvePerformanceBias(longBin)
	if bias != PerformanceBiasLong {
		t.Fatalf("expected long bias, got %s", bias)
	}
	if diff := absFloat64(expectedEV - longBin.ExpectedValueLong); diff > 1e-9 {
		t.Fatalf("expected long EV %.10f, got %.10f", longBin.ExpectedValueLong, expectedEV)
	}

	shortBin := &ScoreBinPerformance{
		ExpectedValueLong:  -0.004,
		ProfitFactorLong:   0.82,
		ExpectedValueShort: 0.011,
		ProfitFactorShort:  2.25,
	}
	bias, expectedEV = ResolvePerformanceBias(shortBin)
	if bias != PerformanceBiasShort {
		t.Fatalf("expected short bias, got %s", bias)
	}
	if diff := absFloat64(expectedEV - shortBin.ExpectedValueShort); diff > 1e-9 {
		t.Fatalf("expected short EV %.10f, got %.10f", shortBin.ExpectedValueShort, expectedEV)
	}

	waitBin := &ScoreBinPerformance{
		ExpectedValueLong:  0.006,
		ProfitFactorLong:   1.05,
		ExpectedValueShort: -0.002,
		ProfitFactorShort:  0.91,
	}
	bias, _ = ResolvePerformanceBias(waitBin)
	if bias != PerformanceBiasWait {
		t.Fatalf("expected wait bias when PF edge is missing, got %s", bias)
	}
}

func absFloat64(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func expectedValueLong(returns ...float64) float64 {
	if len(returns) == 0 {
		return 0
	}

	total := 0.0
	for _, returnPct := range returns {
		base := 1 + returnPct
		if base <= 1e-9 {
			base = 1e-9
		}
		total += math.Log(base)
	}
	return total / float64(len(returns))
}

func expectedValueShort(returns ...float64) float64 {
	if len(returns) == 0 {
		return 0
	}

	total := 0.0
	for _, returnPct := range returns {
		base := 1 - returnPct
		if base <= 1e-9 {
			base = 1e-9
		}
		total += math.Log(base)
	}
	return total / float64(len(returns))
}

func medianLogLong(returns ...float64) float64 {
	if len(returns) == 0 {
		return 0
	}

	values := make([]float64, 0, len(returns))
	for _, returnPct := range returns {
		base := 1 + returnPct
		if base <= 1e-9 {
			base = 1e-9
		}
		values = append(values, math.Log(base))
	}
	return medianFloat64(values)
}

func medianLogShort(returns ...float64) float64 {
	if len(returns) == 0 {
		return 0
	}

	values := make([]float64, 0, len(returns))
	for _, returnPct := range returns {
		base := 1 - returnPct
		if base <= 1e-9 {
			base = 1e-9
		}
		values = append(values, math.Log(base))
	}
	return medianFloat64(values)
}

func repeatReturns(value float64, count int) []float64 {
	if count <= 0 {
		return nil
	}

	values := make([]float64, count)
	for i := range values {
		values[i] = value
	}
	return values
}
