package trader

import (
	"path/filepath"
	"testing"

	"nofx/store"
	tradertypes "nofx/trader/types"
)

type staticOpenOrderReader struct {
	orders []tradertypes.OpenOrder
}

func (r staticOpenOrderReader) GetOpenOrders(symbol string) ([]tradertypes.OpenOrder, error) {
	return r.orders, nil
}

func newPhase2TraderStore(t *testing.T) *store.Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "phase2-trader.sqlite")
	st, err := store.NewWithConfig(store.DBConfig{Type: store.DBTypeSQLite, Path: dbPath})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func TestBinanceOrderReconcileManager_IngestsUserStreamAndDetectsMismatch(t *testing.T) {
	st := newPhase2TraderStore(t)
	manager := NewBinanceOrderReconcileManager(st)

	payload := []byte(`{
		"e":"ORDER_TRADE_UPDATE",
		"E":1710000000000,
		"T":1710000000000,
		"o":{
			"s":"BTCUSDT",
			"c":"client-1",
			"S":"BUY",
			"o":"LIMIT",
			"f":"GTC",
			"q":"1.5",
			"p":"65000",
			"ap":"65000",
			"sp":"0",
			"z":"0.4",
			"X":"PARTIALLY_FILLED",
			"i":12345,
			"R":false,
			"cp":false,
			"ps":"BOTH",
			"AP":"0",
			"cr":"0"
		}
	}`)

	row, err := manager.HandleOrderTradeUpdate("trader-1", payload)
	if err != nil {
		t.Fatalf("handle user stream update failed: %v", err)
	}
	if !manager.UserStreamReady() {
		t.Fatalf("expected user stream to be marked ready after ingest")
	}
	if row.Side != "LONG" {
		t.Fatalf("expected order truth side LONG, got %s", row.Side)
	}
	if row.RemainingQty != 1.1 {
		t.Fatalf("expected remaining qty 1.1, got %.12f", row.RemainingQty)
	}

	logs, err := st.OrderEventLog().ListRecentByTrader("trader-1", 10)
	if err != nil {
		t.Fatalf("list event logs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 event log row, got %d", len(logs))
	}

	preview, err := manager.PreviewOrderState("trader-1", "BTCUSDT", staticOpenOrderReader{})
	if err != nil {
		t.Fatalf("preview reconcile failed: %v", err)
	}
	if !preview.HasStateMismatch {
		t.Fatalf("expected reconcile mismatch to be detected")
	}
	if preview.ReconcileStatus != "mismatch" {
		t.Fatalf("expected reconcile status mismatch, got %s", preview.ReconcileStatus)
	}
	if len(preview.MismatchReasons) == 0 {
		t.Fatalf("expected mismatch reasons")
	}
	if preview.MismatchReasons[0].Category != "order_state" {
		t.Fatalf("expected order_state mismatch reason, got %s", preview.MismatchReasons[0].Category)
	}
}

func TestBuildRuntimeCapabilityPreview_BlocksByOrderState(t *testing.T) {
	st := newPhase2TraderStore(t)
	userID := "user-1"
	traderID := "trader-1"

	if err := st.AIModel().Create(userID, "model-1", "Test Model", "openai", true, "test-key", ""); err != nil {
		t.Fatalf("seed ai model failed: %v", err)
	}
	exchangeID, err := st.Exchange().Create(userID, "binance", "Default", true, "", "", "", false, "", true, "", "", "", "", "", "", 0)
	if err != nil {
		t.Fatalf("seed exchange failed: %v", err)
	}
	if err := st.Trader().Create(&store.Trader{
		ID:                  traderID,
		UserID:              userID,
		Name:                "Test Trader",
		AIModelID:           "model-1",
		ExchangeID:          exchangeID,
		InitialBalance:      1000,
		ScanIntervalMinutes: 3,
		IsRunning:           true,
	}); err != nil {
		t.Fatalf("seed trader failed: %v", err)
	}

	orderPreview := &OrderReconcilePreview{
		TraderID:                traderID,
		SelectedSymbol:          "BTCUSDT",
		Exchange:                "binance_usdm",
		Mode:                    "one_way",
		HasWorkingOrders:        true,
		HasPendingCancelReplace: true,
		HasStateMismatch:        true,
		PendingAddQty:           0.25,
		PendingReduceQty:        0.30,
		MismatchReasons: []OrderStateMismatchReason{
			{
				Category: "order_state",
				Reason:   "registry and exchange disagree",
				Symbol:   "BTCUSDT",
			},
		},
		UserStreamReady: true,
	}

	preview, err := BuildRuntimeCapabilityPreview(
		st,
		userID,
		traderID,
		"BTCUSDT",
		"live",
		nil,
		nil,
		orderPreview,
		nil,
		nil,
		true,
		nil,
	)
	if err != nil {
		t.Fatalf("build runtime capability preview failed: %v", err)
	}

	if !preview.HasStateMismatch {
		t.Fatalf("expected state mismatch to propagate into capability preview")
	}
	if !preview.HasWorkingOrders {
		t.Fatalf("expected working orders flag to propagate into capability preview")
	}
	if !containsString(preview.BlockedActions, "close_position") {
		t.Fatalf("expected close_position to be blocked by order state")
	}
	if !containsCapabilityReasonCategory(preview.BlockReasons, "order_state") {
		t.Fatalf("expected order_state block reason in capability preview")
	}
}

func containsCapabilityReasonCategory(reasons []CapabilityReason, target string) bool {
	for _, reason := range reasons {
		if reason.Category == target {
			return true
		}
	}
	return false
}
