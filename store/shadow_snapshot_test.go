package store

import (
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
			SocialFactor:       71.8,
			OnChainFactor:      69.2,
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
			SocialFactor:       58.5,
			OnChainFactor:      63.7,
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
			SocialFactor:       95.0,
			OnChainFactor:      61.7,
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
			SocialFactor:       81.6,
			OnChainFactor:      64.4,
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
		SocialFactor       float64 `gorm:"column:social_factor"`
	}

	var auditRows []auditRow
	if err := db.Raw(`
		SELECT symbol, sector, action_taken, filled, price_t0, price_t1, return_pct, market_factor, donchian_factor, volume_spike_factor, mtf_resonance_factor, social_factor
		FROM shadow_snapshots
		WHERE trader_id = ?
		ORDER BY symbol ASC
	`, "shadow-verify").Scan(&auditRows).Error; err != nil {
		t.Fatalf("raw audit query: %v", err)
	}

	t.Logf("SQLite verification DB: %s", dbPath)
	t.Logf("SELECT symbol, sector, action_taken, filled, price_t0, price_t1, return_pct, market_factor, donchian_factor, volume_spike_factor, mtf_resonance_factor, social_factor FROM shadow_snapshots WHERE trader_id = 'shadow-verify' ORDER BY symbol ASC;")
	for _, row := range auditRows {
		t.Logf("row symbol=%s sector=%s action_taken=%d filled=%t price_t0=%.4f price_t1=%.4f return_pct=%.6f market_factor=%.1f donchian_factor=%.1f volume_spike_factor=%.1f mtf_resonance_factor=%.1f social_factor=%.1f",
			row.Symbol, row.Sector, row.ActionTaken, row.Filled, row.PriceT0, row.PriceT1, row.ReturnPct, row.MarketFactor, row.DonchianFactor, row.VolumeSpikeFactor, row.MTFResonanceFactor, row.SocialFactor)
	}
}
