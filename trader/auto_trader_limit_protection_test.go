package trader

import (
	"testing"
	"time"

	"nofx/kernel"
	"nofx/trader/types"
)

type protectionCall struct {
	symbol string
	side   string
	qty    float64
	price  float64
}

type limitProtectionTestTrader struct {
	stopLossCalls         []protectionCall
	takeProfitCalls       []protectionCall
	cancelStopLossSymbols []string
	cancelTPSymbols       []string
	orderStatuses         map[string]map[string]interface{}
	positions             []map[string]interface{}
	stopLossErr           error
	takeProfitErr         error
}

func (m *limitProtectionTestTrader) GetBalance() (map[string]interface{}, error) { return nil, nil }
func (m *limitProtectionTestTrader) GetPositions() ([]map[string]interface{}, error) { return m.positions, nil }
func (m *limitProtectionTestTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return nil, nil
}
func (m *limitProtectionTestTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return nil, nil
}
func (m *limitProtectionTestTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return nil, nil
}
func (m *limitProtectionTestTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return nil, nil
}
func (m *limitProtectionTestTrader) SetLeverage(symbol string, leverage int) error { return nil }
func (m *limitProtectionTestTrader) SetMarginMode(symbol string, isCrossMargin bool) error { return nil }
func (m *limitProtectionTestTrader) GetMarketPrice(symbol string) (float64, error) { return 0, nil }
func (m *limitProtectionTestTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	m.stopLossCalls = append(m.stopLossCalls, protectionCall{symbol: symbol, side: positionSide, qty: quantity, price: stopPrice})
	return m.stopLossErr
}
func (m *limitProtectionTestTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	m.takeProfitCalls = append(m.takeProfitCalls, protectionCall{symbol: symbol, side: positionSide, qty: quantity, price: takeProfitPrice})
	return m.takeProfitErr
}
func (m *limitProtectionTestTrader) CancelStopLossOrders(symbol string) error {
	m.cancelStopLossSymbols = append(m.cancelStopLossSymbols, symbol)
	return nil
}
func (m *limitProtectionTestTrader) CancelTakeProfitOrders(symbol string) error {
	m.cancelTPSymbols = append(m.cancelTPSymbols, symbol)
	return nil
}
func (m *limitProtectionTestTrader) CancelAllOrders(symbol string) error { return nil }
func (m *limitProtectionTestTrader) CancelStopOrders(symbol string) error { return nil }
func (m *limitProtectionTestTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	return "", nil
}
func (m *limitProtectionTestTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	if status, ok := m.orderStatuses[orderID]; ok {
		return status, nil
	}
	return map[string]interface{}{"status": "NEW"}, nil
}
func (m *limitProtectionTestTrader) GetClosedPnL(startTime time.Time, limit int) ([]types.ClosedPnLRecord, error) {
	return nil, nil
}
func (m *limitProtectionTestTrader) GetOpenOrders(symbol string) ([]types.OpenOrder, error) { return nil, nil }

func TestApplyProtectionForFilledEntryUsesStagedTakeProfit(t *testing.T) {
	mockTrader := &limitProtectionTestTrader{}
	at := &AutoTrader{
		trader:            mockTrader,
		takeProfitTargets: make(map[string]float64),
		stopLossTargets:   make(map[string]float64),
	}

	order := &PendingEntryOrder{
		OrderID:   "entry-1",
		Symbol:    "BTCUSDT",
		Side:      "LONG",
		StopLoss:  95,
		Leverage:  5,
		TakeProfitStages: []kernel.TakeProfitStage{
			{Price: 110, ClosePct: 50},
			{Price: 120, ClosePct: 50},
		},
	}
	if err := at.applyProtectionForFilledEntry(order, 2); err != nil {
		t.Fatalf("expected cached protection apply to succeed, got %v", err)
	}

	if len(mockTrader.cancelStopLossSymbols) != 1 || mockTrader.cancelStopLossSymbols[0] != "BTCUSDT" {
		t.Fatalf("expected stop-loss cancel before reapply, got %#v", mockTrader.cancelStopLossSymbols)
	}
	if len(mockTrader.stopLossCalls) != 1 {
		t.Fatalf("expected 1 stop-loss order, got %d", len(mockTrader.stopLossCalls))
	}
	if got := mockTrader.stopLossCalls[0]; got.qty != 2 || got.price != 95 || got.side != "LONG" {
		t.Fatalf("unexpected stop-loss call: %#v", got)
	}
	if len(mockTrader.cancelTPSymbols) != 1 || mockTrader.cancelTPSymbols[0] != "BTCUSDT" {
		t.Fatalf("expected take-profit cancel before staged placement, got %#v", mockTrader.cancelTPSymbols)
	}
	if len(mockTrader.takeProfitCalls) != 2 {
		t.Fatalf("expected 2 staged take-profit orders, got %d", len(mockTrader.takeProfitCalls))
	}
	if got := mockTrader.takeProfitCalls[0]; got.qty != 1 || got.price != 110 || got.side != "LONG" {
		t.Fatalf("unexpected first TP call: %#v", got)
	}
	if got := mockTrader.takeProfitCalls[1]; got.qty != 1 || got.price != 120 || got.side != "LONG" {
		t.Fatalf("unexpected second TP call: %#v", got)
	}
}

func TestCheckPendingEntryProtectionsAppliesCachedProtectionOnFill(t *testing.T) {
	mockTrader := &limitProtectionTestTrader{
		orderStatuses: map[string]map[string]interface{}{
			"entry-2": {"status": "FILLED", "executedQty": 3.0},
		},
	}
	at := &AutoTrader{
		id:                "trader-1",
		trader:            mockTrader,
		takeProfitTargets: make(map[string]float64),
		stopLossTargets:   make(map[string]float64),
		pendingEntryOrders: map[string]*PendingEntryOrder{
			"entry-2": {
				OrderID:     "entry-2",
				Symbol:      "ETHUSDT",
				Side:        "SHORT",
				StopLoss:    2500,
				TakeProfit:  2300,
				Leverage:    10,
			},
		},
	}

	at.checkPendingEntryProtections()

	if len(mockTrader.stopLossCalls) != 1 {
		t.Fatalf("expected stop-loss to be applied after fill, got %d", len(mockTrader.stopLossCalls))
	}
	if len(mockTrader.takeProfitCalls) != 1 {
		t.Fatalf("expected take-profit to be applied after fill, got %d", len(mockTrader.takeProfitCalls))
	}
	if got := len(at.snapshotPendingEntryOrders()); got != 0 {
		t.Fatalf("expected filled pending entry to be removed, still have %d", got)
	}
}

func TestCheckPendingEntryProtectionsKeepsPendingWhenProtectionApplyFails(t *testing.T) {
	mockTrader := &limitProtectionTestTrader{
		orderStatuses: map[string]map[string]interface{}{
			"entry-3": {"status": "FILLED", "executedQty": 2.0},
		},
		takeProfitErr: assertErr("position not visible yet"),
	}
	at := &AutoTrader{
		id:                "trader-1",
		trader:            mockTrader,
		takeProfitTargets: make(map[string]float64),
		stopLossTargets:   make(map[string]float64),
		pendingEntryOrders: map[string]*PendingEntryOrder{
			"entry-3": {
				OrderID:    "entry-3",
				Symbol:     "ETHUSDT",
				Side:       "SHORT",
				StopLoss:   2500,
				TakeProfit: 2300,
				Leverage:   10,
			},
		},
	}

	at.checkPendingEntryProtections()

	if got := len(at.snapshotPendingEntryOrders()); got != 1 {
		t.Fatalf("expected failed protection apply to keep pending entry, got %d", got)
	}
}

type staticErr string

func (e staticErr) Error() string { return string(e) }

func assertErr(msg string) error { return staticErr(msg) }
