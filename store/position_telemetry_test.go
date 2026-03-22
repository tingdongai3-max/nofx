package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func openTelemetryTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func TestPositionStoreAppendTelemetryPoint(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ps := NewPositionStore(db)
	if err := ps.InitTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	pos := &TraderPosition{
		TraderID:   "trader-test",
		Symbol:     "BTCUSDT",
		Side:       "long",
		Quantity:   1,
		EntryPrice: 100000,
		EntryTime:  nowMs,
		CreatedAt:  nowMs,
		UpdatedAt:  nowMs,
	}
	if err := ps.CreateOpenPosition(pos); err != nil {
		t.Fatalf("create position: %v", err)
	}

	points := []FactorTelemetry{
		{Timestamp: nowMs, Price: 100100, HeatScore: 54.2, TradingSub: 58.1, QuantSub: 41.3},
		{Timestamp: nowMs + 60_000, Price: 100250, HeatScore: 61.5, TradingSub: 63.8, QuantSub: 48.2},
		{Timestamp: nowMs + 120_000, Price: 100050, HeatScore: 57.8, TradingSub: 55.4, QuantSub: 52.7},
	}

	for _, point := range points {
		if err := ps.AppendTelemetryPoint(pos.ID, point); err != nil {
			t.Fatalf("append telemetry: %v", err)
		}
	}

	stored, err := ps.GetOpenPositionBySymbol("trader-test", "BTCUSDT", "long")
	if err != nil {
		t.Fatalf("get open position: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored position")
	}
	if got := len(stored.Telemetry); got != len(points) {
		t.Fatalf("expected %d telemetry points, got %d", len(points), got)
	}
	if stored.Telemetry[2].HeatScore != points[2].HeatScore {
		t.Fatalf("expected latest heat %.1f, got %.1f", points[2].HeatScore, stored.Telemetry[2].HeatScore)
	}

	var raw string
	if err := db.Raw("SELECT telemetry FROM trader_positions WHERE id = ?", pos.ID).Scan(&raw).Error; err != nil {
		t.Fatalf("raw telemetry query: %v", err)
	}
	if !strings.Contains(raw, "\"heat_score\":57.8") {
		t.Fatalf("expected serialized telemetry JSON, got %s", raw)
	}
}

func TestPositionStoreTelemetryVerificationArtifact(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_telemetry_verify.db")
	db := openTelemetryTestDB(t, dbPath)
	ps := NewPositionStore(db)
	if err := ps.InitTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	_ = db.Exec("DELETE FROM trader_positions WHERE trader_id = ?", "verify-trader").Error

	nowMs := time.Now().UTC().UnixMilli()
	pos := &TraderPosition{
		TraderID:   "verify-trader",
		Symbol:     "ETHUSDT",
		Side:       "long",
		Quantity:   2,
		EntryPrice: 3050.25,
		EntryTime:  nowMs,
		CreatedAt:  nowMs,
		UpdatedAt:  nowMs,
	}
	if err := ps.CreateOpenPosition(pos); err != nil {
		t.Fatalf("create position: %v", err)
	}

	for idx, point := range []FactorTelemetry{
		{Timestamp: nowMs, Price: 3055.10, HeatScore: 49.5, TradingSub: 52.4, QuantSub: 43.1},
		{Timestamp: nowMs + 60_000, Price: 3062.40, HeatScore: 58.2, TradingSub: 61.7, QuantSub: 47.3},
		{Timestamp: nowMs + 120_000, Price: 3071.80, HeatScore: 64.9, TradingSub: 68.2, QuantSub: 53.8},
	} {
		if err := ps.AppendTelemetryPoint(pos.ID, point); err != nil {
			t.Fatalf("append telemetry %d: %v", idx, err)
		}
	}

	var raw string
	if err := db.Raw("SELECT telemetry FROM trader_positions WHERE id = ?", pos.ID).Scan(&raw).Error; err != nil {
		t.Fatalf("raw telemetry query: %v", err)
	}

	t.Logf("SQLite verification DB: %s", dbPath)
	t.Logf("SELECT telemetry FROM trader_positions WHERE id = %d;", pos.ID)
	t.Logf("telemetry=%s", raw)
}
