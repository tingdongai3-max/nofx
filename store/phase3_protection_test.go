package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newPhase3Store(t *testing.T) *Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "phase3-store.sqlite")
	st, err := NewWithConfig(DBConfig{Type: DBTypeSQLite, Path: dbPath})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func TestPositionAggregateBuilder_ReadsProtectionGroupState(t *testing.T) {
	st := newPhase3Store(t)
	traderID := "trader-1"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-store"
	positionKey := symbol + "|" + side
	now := time.Now().UTC()

	if err := st.Position().Create(&TraderPosition{
		TraderID:           traderID,
		ExchangeID:         "exchange-1",
		ExchangeType:       "binance",
		ExchangePositionID: "pos-1",
		Symbol:             symbol,
		Side:               side,
		Quantity:           1,
		EntryPrice:         65000,
		EntryTime:          now.UnixMilli(),
		CreatedAt:          now.UnixMilli(),
		UpdatedAt:          now.UnixMilli(),
	}); err != nil {
		t.Fatalf("seed position failed: %v", err)
	}

	if _, err := st.ProtectionGroupBuilder().ApplyProtectionEvent(&ProtectionGroupEventInput{
		TraderID:                traderID,
		Symbol:                  symbol,
		Side:                    side,
		LinkedPositionKey:       positionKey,
		ProtectionGroupID:       groupID,
		ProtectionMode:          "fixed",
		StopLossOrderIntentID:   "sl-intent",
		TakeProfitOrderIntentID: "tp-intent",
		StopLossTriggerPrice:    64000,
		TakeProfitTriggerPrice:  68000,
		ProtectedQuantity:       1,
		Status:                  "armed",
		Source:                  "test",
		EventType:               "PROTECTION_GROUP_CREATED",
		EventSource:             "test",
		PayloadJSON:             "{}",
		EventTime:               now,
	}); err != nil {
		t.Fatalf("seed protection group failed: %v", err)
	}

	for _, order := range []struct {
		localIntentID string
		clientOrderID string
		orderRole     string
		exchangeID    string
	}{
		{localIntentID: "sl-intent", clientOrderID: "sl-client", orderRole: "stop_loss", exchangeID: "1001"},
		{localIntentID: "tp-intent", clientOrderID: "tp-client", orderRole: "take_profit", exchangeID: "1002"},
	} {
		if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&OrderRegistryEventInput{
			TraderID:          traderID,
			Symbol:            symbol,
			Side:              "SELL",
			PositionSideMode:  "one_way",
			OrderRole:         order.orderRole,
			LocalIntentID:     order.localIntentID,
			LinkedGroupID:     groupID,
			LinkedPositionKey: positionKey,
			ExchangeOrderID:   order.exchangeID,
			ClientOrderID:     order.clientOrderID,
			OrderType:         "STOP_MARKET",
			TimeInForce:       "GTC",
			ReduceOnly:        true,
			ClosePosition:     true,
			OrigQty:           1,
			ExecutedQty:       0,
			Status:            "NEW",
			Source:            "user_stream",
			EventType:         "ORDER_TRADE_UPDATE",
			EventSource:       "unit_test",
			PayloadJSON:       `{"leg":"protection"}`,
			EventTime:         now,
		}); err != nil {
			t.Fatalf("seed protection order failed: %v", err)
		}
	}

	aggregates, err := NewPositionAggregateBuilder(st).BuildForTrader(traderID, nil, nil)
	if err != nil {
		t.Fatalf("build aggregates failed: %v", err)
	}
	if len(aggregates) != 1 {
		t.Fatalf("expected 1 aggregate, got %d", len(aggregates))
	}

	agg := aggregates[0]
	if !agg.HasProtection {
		t.Fatalf("expected aggregate to read protection state")
	}
	if agg.ProtectionMode != "fixed" {
		t.Fatalf("expected fixed protection mode, got %s", agg.ProtectionMode)
	}
	if agg.ProtectionGroupID != groupID {
		t.Fatalf("expected protection group id %s, got %s", groupID, agg.ProtectionGroupID)
	}
	if !agg.StopLossArmed || !agg.TakeProfitArmed {
		t.Fatalf("expected both protection legs armed, got %#v", agg)
	}
	if agg.ProtectedQuantity != 1 {
		t.Fatalf("expected protected quantity 1, got %.6f", agg.ProtectedQuantity)
	}
	if agg.PendingAddQty != 0 {
		t.Fatalf("expected no pending add qty, got %.6f", agg.PendingAddQty)
	}
	if agg.PendingReduceQty <= 0 {
		t.Fatalf("expected pending reduce qty to reflect protection orders, got %.6f", agg.PendingReduceQty)
	}
	if !strings.Contains(agg.ProtectionStateJSON, `"has_protection":true`) {
		t.Fatalf("expected protection state JSON to reflect active protection, got %s", agg.ProtectionStateJSON)
	}
}
