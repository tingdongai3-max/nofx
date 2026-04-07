package store

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func newPhase2TestStore(t *testing.T) *Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "phase2.sqlite")
	st, err := NewWithConfig(DBConfig{Type: DBTypeSQLite, Path: dbPath})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func TestOrderRegistryBuilder_PartialFillUpdatesRemainingQty(t *testing.T) {
	st := newPhase2TestStore(t)

	row, err := st.OrderRegistryBuilder().ApplyOrderEvent(&OrderRegistryEventInput{
		TraderID:         "trader-1",
		Symbol:           "BTCUSDT",
		Side:             "BUY",
		PositionSideMode: "one_way",
		OrderRole:        "entry",
		LocalIntentID:    "intent-1",
		ExchangeOrderID:  "1001",
		ClientOrderID:    "client-1",
		OrderType:        "LIMIT",
		TimeInForce:      "GTC",
		OrigQty:          1.5,
		ExecutedQty:      0.4,
		Status:           "PARTIALLY_FILLED",
		Source:           "local_submit",
		EventType:        "ORDER_TRADE_UPDATE",
		EventSource:      "unit_test",
		PayloadJSON:      `{"mock":true}`,
		EventTime:        time.UnixMilli(1710000000000),
	})
	if err != nil {
		t.Fatalf("apply order event failed: %v", err)
	}

	if row.Side != "LONG" {
		t.Fatalf("expected truth side LONG, got %s", row.Side)
	}
	if diff := math.Abs(row.RemainingQty - 1.1); diff > 1e-9 {
		t.Fatalf("expected remaining qty 1.1, got %.12f", row.RemainingQty)
	}
	if !row.IsWorking {
		t.Fatalf("expected partially filled order to remain working")
	}

	logs, err := st.OrderEventLog().ListRecentByTrader("trader-1", 10)
	if err != nil {
		t.Fatalf("list event logs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 event log row, got %d", len(logs))
	}
	if logs[0].EventType != "ORDER_TRADE_UPDATE" {
		t.Fatalf("expected ORDER_TRADE_UPDATE event log, got %s", logs[0].EventType)
	}
}

func TestPositionAggregateBuilder_UsesOrderRegistryPendingQuantities(t *testing.T) {
	st := newPhase2TestStore(t)
	now := time.Now().UTC()

	if err := st.Position().Create(&TraderPosition{
		TraderID:           "trader-1",
		ExchangeID:         "exchange-1",
		ExchangeType:       "binance",
		ExchangePositionID: "pos-1",
		Symbol:             "BTCUSDT",
		Side:               "LONG",
		Quantity:           1,
		EntryPrice:         65000,
		EntryTime:          now.UnixMilli(),
		CreatedAt:          now.UnixMilli(),
		UpdatedAt:          now.UnixMilli(),
	}); err != nil {
		t.Fatalf("create position failed: %v", err)
	}

	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&OrderRegistryEventInput{
		TraderID:         "trader-1",
		Symbol:           "BTCUSDT",
		Side:             "BUY",
		PositionSideMode: "one_way",
		OrderRole:        "entry",
		LocalIntentID:    "intent-entry",
		ExchangeOrderID:  "2001",
		ClientOrderID:    "client-entry",
		OrderType:        "LIMIT",
		TimeInForce:      "GTC",
		OrigQty:          0.25,
		ExecutedQty:      0,
		Status:           "NEW",
		Source:           "local_submit",
		EventType:        "ORDER_NEW",
		EventSource:      "unit_test",
		PayloadJSON:      `{"role":"entry"}`,
		EventTime:        now,
	}); err != nil {
		t.Fatalf("apply add order failed: %v", err)
	}

	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&OrderRegistryEventInput{
		TraderID:         "trader-1",
		Symbol:           "BTCUSDT",
		Side:             "SELL",
		PositionSideMode: "one_way",
		OrderRole:        "reduce",
		ReduceOnly:       true,
		LocalIntentID:    "intent-reduce",
		ExchangeOrderID:  "2002",
		ClientOrderID:    "client-reduce",
		OrderType:        "STOP_MARKET",
		TimeInForce:      "GTC",
		OrigQty:          0.4,
		ExecutedQty:      0.1,
		Status:           "PARTIALLY_FILLED",
		Source:           "user_stream",
		EventType:        "ORDER_TRADE_UPDATE",
		EventSource:      "unit_test",
		PayloadJSON:      `{"role":"reduce"}`,
		EventTime:        now.Add(time.Second),
	}); err != nil {
		t.Fatalf("apply reduce order failed: %v", err)
	}

	aggregates, err := NewPositionAggregateBuilder(st).BuildForTrader("trader-1", nil, nil)
	if err != nil {
		t.Fatalf("build aggregates failed: %v", err)
	}
	if len(aggregates) != 1 {
		t.Fatalf("expected 1 aggregate, got %d", len(aggregates))
	}

	aggregate := aggregates[0]
	if diff := math.Abs(aggregate.PendingAddQty - 0.25); diff > 1e-9 {
		t.Fatalf("expected pending add qty 0.25, got %.12f", aggregate.PendingAddQty)
	}
	if diff := math.Abs(aggregate.PendingReduceQty - 0.3); diff > 1e-9 {
		t.Fatalf("expected pending reduce qty 0.3, got %.12f", aggregate.PendingReduceQty)
	}
	if diff := math.Abs(aggregate.AvailableQty - 0.7); diff > 1e-9 {
		t.Fatalf("expected available qty 0.7, got %.12f", aggregate.AvailableQty)
	}
}

func TestProtectionGroupSummary_EmptyScopeUsesUnknownConsistency(t *testing.T) {
	st := newPhase2TestStore(t)

	summary, err := st.ProtectionGroup().SummarizeForTraderSymbolSide("trader-1", "BTCUSDT", "LONG")
	if err != nil {
		t.Fatalf("summarize protection group failed: %v", err)
	}
	if summary == nil {
		t.Fatalf("expected summary for empty scope")
	}
	if summary.ConsistencyStatus != "unknown" {
		t.Fatalf("expected unknown consistency for empty scope, got %s", summary.ConsistencyStatus)
	}
	if summary.HasProtection {
		t.Fatalf("expected no protection in empty scope summary")
	}
}
