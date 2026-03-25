package store

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyShadowRoundTripFeeMigrationIsIdempotent(t *testing.T) {
	db := openTelemetryTestDB(t, filepath.Join(t.TempDir(), "shadow_fee_migration.db"))
	st, err := NewFromGorm(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := st.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	decisionTime := time.Now().UTC().Add(-30 * time.Minute).UnixMilli()
	rows := []*ShadowSnapshot{
		{
			TraderID:     GlobalConsensusTraderID,
			DecisionTime: decisionTime,
			Symbol:       "BTCUSDT",
			Sector:       "Major",
			PriceT0:      100,
			PriceT1:      100.2,
			ReturnPct:    0.002,
			Filled:       true,
			FilledAt:     decisionTime + int64(time.Minute/time.Millisecond),
			CreatedAt:    decisionTime,
			UpdatedAt:    decisionTime,
		},
	}
	if err := st.Shadow().CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	migrated, err := ApplyShadowRoundTripFeeMigration(st)
	if err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("expected 1 migrated row, got %d", migrated)
	}

	listed, err := st.Shadow().ListByTrader(GlobalConsensusTraderID, 10)
	if err != nil {
		t.Fatalf("list migrated rows: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 migrated row, got %d", len(listed))
	}

	expected := 0.002 - ShadowRoundTripFeeRate
	if math.Abs(listed[0].ReturnPct-expected) > 1e-9 {
		t.Fatalf("expected migrated return_pct %.6f, got %.6f", expected, listed[0].ReturnPct)
	}

	migratedAgain, err := ApplyShadowRoundTripFeeMigration(st)
	if err != nil {
		t.Fatalf("apply migration second time: %v", err)
	}
	if migratedAgain != 0 {
		t.Fatalf("expected second migration to be skipped, got %d affected rows", migratedAgain)
	}

	listedAgain, err := st.Shadow().ListByTrader(GlobalConsensusTraderID, 10)
	if err != nil {
		t.Fatalf("list rows after second migration: %v", err)
	}
	if len(listedAgain) != 1 {
		t.Fatalf("expected 1 row after second migration, got %d", len(listedAgain))
	}
	if math.Abs(listedAgain[0].ReturnPct-expected) > 1e-9 {
		t.Fatalf("expected idempotent migrated return_pct %.6f, got %.6f", expected, listedAgain[0].ReturnPct)
	}
}
