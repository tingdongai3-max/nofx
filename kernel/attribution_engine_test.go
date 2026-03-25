package kernel

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nofx/store"
)

func TestCalculateRealFireWeightsHighlightsMicroAttribution(t *testing.T) {
	records := []*store.RealTradeResonanceRecord{
		{
			Status:            store.RealTradeResonanceStatusClosed,
			EntryGlobalEV:     0.020,
			EntrySectorEV:     0.040,
			EntrySymbolEV:     0.10,
			HoldAvgGlobalEV:   0.06,
			HoldAvgSectorEV:   0.12,
			HoldAvgSymbolEV:   0.08,
			HoldRetentionRate: 0.90,
			FinalPnL:          10,
		},
		{
			Status:            store.RealTradeResonanceStatusClosed,
			EntryGlobalEV:     0.021,
			EntrySectorEV:     0.010,
			EntrySymbolEV:     0.20,
			HoldAvgGlobalEV:   0.07,
			HoldAvgSectorEV:   0.12,
			HoldAvgSymbolEV:   0.08,
			HoldRetentionRate: 1.10,
			FinalPnL:          20,
		},
		{
			Status:            store.RealTradeResonanceStatusClosed,
			EntryGlobalEV:     0.019,
			EntrySectorEV:     0.030,
			EntrySymbolEV:     0.30,
			HoldAvgGlobalEV:   0.05,
			HoldAvgSectorEV:   0.12,
			HoldAvgSymbolEV:   0.08,
			HoldRetentionRate: 1.00,
			FinalPnL:          30,
		},
		{
			Status:            store.RealTradeResonanceStatusClosed,
			EntryGlobalEV:     0.020,
			EntrySectorEV:     0.020,
			EntrySymbolEV:     0.40,
			HoldAvgGlobalEV:   0.06,
			HoldAvgSectorEV:   0.12,
			HoldAvgSymbolEV:   0.08,
			HoldRetentionRate: 0.80,
			FinalPnL:          40,
		},
	}

	weights := CalculateRealFireWeights(records)
	if weights == nil {
		t.Fatal("expected attribution weights")
	}
	if weights.SampleCount != 4 {
		t.Fatalf("expected sample count 4, got %d", weights.SampleCount)
	}
	if weights.DominantDimension != RealFireDimensionSymbol {
		t.Fatalf("expected symbol to dominate realized attribution, got %s", weights.DominantDimension)
	}
	if weights.ShadowDominantDimension != RealFireDimensionSector {
		t.Fatalf("expected sector to dominate shadow attribution, got %s", weights.ShadowDominantDimension)
	}

	totalReal := 0.0
	totalShadow := 0.0
	var symbolWeight, sectorShadow float64
	for _, dimension := range weights.Dimensions {
		totalReal += dimension.RealWeight
		totalShadow += dimension.ShadowWeight
		if dimension.Name == RealFireDimensionSymbol {
			symbolWeight = dimension.RealWeight
		}
		if dimension.Name == RealFireDimensionSector {
			sectorShadow = dimension.ShadowWeight
		}
	}

	if math.Abs(totalReal-1) > 1e-6 {
		t.Fatalf("expected normalized realized weights, got %.6f", totalReal)
	}
	if math.Abs(totalShadow-1) > 1e-6 {
		t.Fatalf("expected normalized shadow weights, got %.6f", totalShadow)
	}
	if symbolWeight <= 0.50 {
		t.Fatalf("expected symbol realized weight above 50%%, got %.4f", symbolWeight)
	}
	if sectorShadow <= 0.40 {
		t.Fatalf("expected sector shadow weight above 40%%, got %.4f", sectorShadow)
	}
}

func TestCalculateRealFireWeightsHandlesEmptyInput(t *testing.T) {
	weights := CalculateRealFireWeights(nil)
	if weights == nil {
		t.Fatal("expected empty weights object")
	}
	if weights.SampleCount != 0 {
		t.Fatalf("expected sample count 0, got %d", weights.SampleCount)
	}
	if len(weights.Dimensions) != 3 {
		t.Fatalf("expected 3 default dimensions, got %d", len(weights.Dimensions))
	}
}

func TestGetLiveAttributionWeightsFallsBackToEqualWithoutStore(t *testing.T) {
	SetLiveAttributionStore(nil)

	weights, err := GetLiveAttributionWeights()
	if err != nil {
		t.Fatalf("expected no error without store, got %v", err)
	}
	if diff := math.Abs(weights[RealFireDimensionGlobal] - 1.0/3.0); diff > 1e-9 {
		t.Fatalf("expected equal fallback for global, got %.10f", weights[RealFireDimensionGlobal])
	}
	if diff := math.Abs(weights[RealFireDimensionSector] - 1.0/3.0); diff > 1e-9 {
		t.Fatalf("expected equal fallback for sector, got %.10f", weights[RealFireDimensionSector])
	}
	if diff := math.Abs(weights[RealFireDimensionSymbol] - 1.0/3.0); diff > 1e-9 {
		t.Fatalf("expected equal fallback for symbol, got %.10f", weights[RealFireDimensionSymbol])
	}
}

func TestGetLiveAttributionWeightsUsesRecentClosedSamples(t *testing.T) {
	dbPath := filepath.Join(os.TempDir(), "nofx_live_attribution_weights.db")
	_ = os.Remove(dbPath)
	db, err := store.InitGorm(dbPath)
	if err != nil {
		t.Fatalf("open gorm db: %v", err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatalf("create store from gorm: %v", err)
	}
	defer st.Close()
	if err := st.RealTradeStats().InitTables(); err != nil {
		t.Fatalf("init real trade stats tables: %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		record := &store.RealTradeResonanceRecord{
			TraderID:      "GLOBAL_REAL_SNIPER",
			ExchangeID:    "acct-weights",
			OrderID:       string(rune('1' + i)),
			Symbol:        "BTCUSDT",
			Side:          "long",
			EntryTime:     now.Add(time.Duration(i) * time.Minute).UnixMilli(),
			EntryGlobalEV: 0.1,
			EntrySectorEV: 0.01 * float64(i+1),
			EntrySymbolEV: 0.1,
			FinalPnL:      float64(i + 1),
			Status:        store.RealTradeResonanceStatusOpen,
			CreatedAt:     now.Add(time.Duration(i) * time.Minute).UnixMilli(),
			UpdatedAt:     now.Add(time.Duration(i) * time.Minute).UnixMilli(),
		}
		if err := st.RealTradeStats().CreateOpenRecord(record); err != nil {
			t.Fatalf("create open record %d: %v", i, err)
		}
		if err := st.RealTradeStats().CloseRecordByOrderID(record.TraderID, record.ExchangeID, record.OrderID, record.FinalPnL, now.Add(time.Duration(i+5)*time.Minute).UnixMilli()); err != nil {
			t.Fatalf("close record %d: %v", i, err)
		}
	}

	SetLiveAttributionStore(st)
	defer SetLiveAttributionStore(nil)

	weights, err := GetLiveAttributionWeights()
	if err != nil {
		t.Fatalf("expected live attribution weights, got %v", err)
	}
	total := weights[RealFireDimensionGlobal] + weights[RealFireDimensionSector] + weights[RealFireDimensionSymbol]
	if math.Abs(total-1) > 1e-9 {
		t.Fatalf("expected weights to normalize to 1, got %.10f", total)
	}
	if weights[RealFireDimensionSector] <= weights[RealFireDimensionGlobal] || weights[RealFireDimensionSector] <= weights[RealFireDimensionSymbol] {
		t.Fatalf("expected sector to dominate live attribution weights, got %+v", weights)
	}
}

func setupSectorDominantLiveAttributionWeights(t *testing.T) func() {
	t.Helper()

	dbPath := filepath.Join(os.TempDir(), "nofx_live_attribution_sector_dominant.db")
	_ = os.Remove(dbPath)
	db, err := store.InitGorm(dbPath)
	if err != nil {
		t.Fatalf("open gorm db: %v", err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatalf("create store from gorm: %v", err)
	}
	if err := st.RealTradeStats().InitTables(); err != nil {
		st.Close()
		t.Fatalf("init real trade stats tables: %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		record := &store.RealTradeResonanceRecord{
			TraderID:      "GLOBAL_REAL_SNIPER",
			ExchangeID:    "acct-sector",
			OrderID:       fmt.Sprintf("sector-%d", i+1),
			Symbol:        "BTCUSDT",
			Side:          "long",
			EntryTime:     now.Add(time.Duration(i) * time.Minute).UnixMilli(),
			EntryGlobalEV: 0.1,
			EntrySectorEV: 0.02 * float64(i+1),
			EntrySymbolEV: 0.1,
			FinalPnL:      float64(i + 1),
			Status:        store.RealTradeResonanceStatusOpen,
			CreatedAt:     now.Add(time.Duration(i) * time.Minute).UnixMilli(),
			UpdatedAt:     now.Add(time.Duration(i) * time.Minute).UnixMilli(),
		}
		if err := st.RealTradeStats().CreateOpenRecord(record); err != nil {
			st.Close()
			t.Fatalf("create open record %d: %v", i, err)
		}
		if err := st.RealTradeStats().CloseRecordByOrderID(record.TraderID, record.ExchangeID, record.OrderID, record.FinalPnL, now.Add(time.Duration(i+5)*time.Minute).UnixMilli()); err != nil {
			st.Close()
			t.Fatalf("close record %d: %v", i, err)
		}
	}

	SetLiveAttributionStore(st)
	return func() {
		SetLiveAttributionStore(nil)
		st.Close()
	}
}
