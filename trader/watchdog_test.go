package trader

import (
	"context"
	"testing"
	"time"

	"nofx/store"
	"nofx/trader/types"
)

type watchdogTestTrader struct{}

func (watchdogTestTrader) GetBalance() (map[string]interface{}, error) { return nil, nil }
func (watchdogTestTrader) GetPositions() ([]map[string]interface{}, error) { return nil, nil }
func (watchdogTestTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return nil, nil
}
func (watchdogTestTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return nil, nil
}
func (watchdogTestTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{"orderId": "close-long"}, nil
}
func (watchdogTestTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{"orderId": "close-short"}, nil
}
func (watchdogTestTrader) SetLeverage(symbol string, leverage int) error { return nil }
func (watchdogTestTrader) SetMarginMode(symbol string, isCrossMargin bool) error { return nil }
func (watchdogTestTrader) GetMarketPrice(symbol string) (float64, error) { return 0, nil }
func (watchdogTestTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	return nil
}
func (watchdogTestTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	return nil
}
func (watchdogTestTrader) CancelStopLossOrders(symbol string) error { return nil }
func (watchdogTestTrader) CancelTakeProfitOrders(symbol string) error { return nil }
func (watchdogTestTrader) CancelAllOrders(symbol string) error { return nil }
func (watchdogTestTrader) CancelStopOrders(symbol string) error { return nil }
func (watchdogTestTrader) FormatQuantity(symbol string, quantity float64) (string, error) { return "", nil }
func (watchdogTestTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	return nil, nil
}
func (watchdogTestTrader) GetClosedPnL(startTime time.Time, limit int) ([]types.ClosedPnLRecord, error) {
	return nil, nil
}
func (watchdogTestTrader) GetOpenOrders(symbol string) ([]types.OpenOrder, error) { return nil, nil }

func TestHandleWatchdogPriceEventDryRunLongHardTakeProfit(t *testing.T) {
	closed := 0
	pos := &watchdogPosition{
		Symbol:          "BTCUSDT",
		Side:            "long",
		Source:          "dry_run",
		Quantity:        1,
		EntryPrice:      100,
		TakeProfitPrice: 110,
		InitialStopLoss: 95,
	}
	hooks := &WatchdogHooks{
		ClosePosition: func(symbol, side string, quantity float64) (map[string]interface{}, error) {
			closed++
			return map[string]interface{}{"orderId": "dry-run-close"}, nil
		},
	}
	onCloseCalled := false
	triggered := handleWatchdogPriceEvent(context.Background(), watchdogTestTrader{}, func() *store.StrategyConfig {
		return &store.StrategyConfig{}
	}, pos, 111, func(symbol, action string, order map[string]interface{}, quantity, exitPrice, entryPrice float64) {
		onCloseCalled = true
	}, hooks)
	if !triggered {
		t.Fatalf("expected dry-run hard take profit to trigger close")
	}
	if closed != 1 {
		t.Fatalf("expected one close, got %d", closed)
	}
	if !onCloseCalled {
		t.Fatalf("expected onClose callback to run")
	}
}

func TestHandleWatchdogPriceEventDryRunShortHardStopLoss(t *testing.T) {
	closed := 0
	pos := &watchdogPosition{
		Symbol:          "ETHUSDT",
		Side:            "short",
		Source:          "dry_run",
		Quantity:        2,
		EntryPrice:      100,
		TakeProfitPrice: 90,
		InitialStopLoss: 105,
	}
	hooks := &WatchdogHooks{
		ClosePosition: func(symbol, side string, quantity float64) (map[string]interface{}, error) {
			closed++
			return map[string]interface{}{"orderId": "dry-run-close"}, nil
		},
	}
	triggered := handleWatchdogPriceEvent(context.Background(), watchdogTestTrader{}, func() *store.StrategyConfig {
		return &store.StrategyConfig{}
	}, pos, 106, nil, hooks)
	if !triggered {
		t.Fatalf("expected dry-run hard stop loss to trigger close")
	}
	if closed != 1 {
		t.Fatalf("expected one close, got %d", closed)
	}
}
