package trader

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"nofx/store"
)

func TestAutoTraderRecordPositionTelemetryAudit(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_telemetry_audit.db")
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
	if err := st.Position().InitTables(); err != nil {
		t.Fatalf("init position tables: %v", err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	pos := &store.TraderPosition{
		TraderID:   "audit-trader",
		Symbol:     "ETHUSDT",
		Side:       "long",
		Quantity:   1.5,
		EntryPrice: 3010.0,
		EntryTime:  nowMs,
		CreatedAt:  nowMs,
		UpdatedAt:  nowMs,
	}
	if err := st.Position().CreateOpenPosition(pos); err != nil {
		t.Fatalf("create open position: %v", err)
	}

	at := &AutoTrader{
		id:    "audit-trader",
		store: st,
	}

	cycles := []struct {
		price   float64
		heat    float64
		trading float64
		quant   float64
	}{
		{price: 3012.4, heat: 46.0, trading: 49.3, quant: 39.2},
		{price: 3025.8, heat: 58.7, trading: 61.8, quant: 47.1},
		{price: 3004.6, heat: 41.9, trading: 44.2, quant: 35.7},
	}

	for _, cycle := range cycles {
		at.candidateTelemetry = map[string]candidateTelemetrySnapshot{
			"ETHUSDT": {
				Heat:       cycle.heat,
				TradingSub: cycle.trading,
				QuantSub:   cycle.quant,
			},
		}
		at.recordPositionTelemetry("ETHUSDT", "long", cycle.price)
	}

	stored, err := st.Position().GetOpenPositionBySymbol("audit-trader", "ETHUSDT", "long")
	if err != nil {
		t.Fatalf("get open position: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored position")
	}
	if got := len(stored.Telemetry); got != len(cycles) {
		t.Fatalf("expected %d telemetry points, got %d", len(cycles), got)
	}

	t.Logf("Audit verification DB: %s", dbPath)
}
