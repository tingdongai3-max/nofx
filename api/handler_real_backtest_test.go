package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/store"

	"github.com/gin-gonic/gin"
)

func TestHandleRealBacktestPositionsIncludesAttribution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join("/tmp", "nofx_api_real_backtest_monitor_attribution_test.db")
	_ = os.Remove(dbPath)
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := st.SetRealBacktestEnabled(true); err != nil {
		t.Fatalf("enable real backtest: %v", err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	for index, record := range []*store.RealTradeResonanceRecord{
		{
			TraderID:          "GLOBAL_REAL_SNIPER",
			ExchangeID:        "acct-test",
			OrderID:           "rb-open-1",
			Symbol:            "BTCUSDT",
			Side:              "long",
			Status:            store.RealTradeResonanceStatusClosed,
			EntryTime:         nowMs - 3_600_000,
			EntryGlobalEV:     0.01,
			EntrySectorEV:     0.02,
			EntrySymbolEV:     0.10,
			HoldAvgGlobalEV:   0.04,
			HoldAvgSectorEV:   0.08,
			HoldAvgSymbolEV:   0.06,
			HoldRetentionRate: 0.9,
			FinalPnL:          12,
			ExitTime:          nowMs - 2_700_000,
			CreatedAt:         nowMs - 3_600_000,
			UpdatedAt:         nowMs - 2_700_000,
		},
		{
			TraderID:          "GLOBAL_REAL_SNIPER",
			ExchangeID:        "acct-test",
			OrderID:           "rb-open-2",
			Symbol:            "ETHUSDT",
			Side:              "long",
			Status:            store.RealTradeResonanceStatusClosed,
			EntryTime:         nowMs - 2_400_000,
			EntryGlobalEV:     0.01,
			EntrySectorEV:     0.03,
			EntrySymbolEV:     0.20,
			HoldAvgGlobalEV:   0.04,
			HoldAvgSectorEV:   0.08,
			HoldAvgSymbolEV:   0.06,
			HoldRetentionRate: 1.1,
			FinalPnL:          24,
			ExitTime:          nowMs - 1_500_000,
			CreatedAt:         nowMs - 2_400_000,
			UpdatedAt:         nowMs - 1_500_000,
		},
	} {
		record.OrderID = record.OrderID + "-" + string(rune('A'+index))
		if err := st.RealTradeStats().CreateOpenRecord(record); err != nil {
			t.Fatalf("seed real trade stats record %d: %v", index, err)
		}
	}

	srv := &Server{store: st}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/real-backtest/positions", nil)
	context.Set("user_id", "user-real-monitor")

	srv.handleRealBacktestPositions(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	var response realBacktestMonitorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v body=%s", err, recorder.Body.String())
	}

	if response.Attribution == nil {
		t.Fatal("expected attribution payload")
	}
	if response.Attribution.SampleCount != 2 {
		t.Fatalf("expected sample count 2, got %d", response.Attribution.SampleCount)
	}
	if len(response.Attribution.Dimensions) != 3 {
		t.Fatalf("expected 3 attribution dimensions, got %d", len(response.Attribution.Dimensions))
	}
}

func TestAlignRealBacktestMahalanobisMetaLocksThreshold(t *testing.T) {
	allowed := true
	threshold, resonant, entryAllowed, blockReason := alignRealBacktestMahalanobisMeta(2.29, &allowed, "")
	if threshold != kernel.DefaultMahalanobisThreshold {
		t.Fatalf("expected threshold %.2f, got %.2f", kernel.DefaultMahalanobisThreshold, threshold)
	}
	if resonant {
		t.Fatal("expected 2.29 to be outlier under the hard cap")
	}
	if entryAllowed == nil || *entryAllowed {
		t.Fatal("expected entry to be blocked after alignment")
	}
	if blockReason != "D > 2.0" {
		t.Fatalf("expected block reason to be hard-gated, got %q", blockReason)
	}
}
