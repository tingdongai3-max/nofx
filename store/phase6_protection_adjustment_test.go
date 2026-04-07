package store

import (
	"testing"
	"time"
)

func TestPositionAggregateBuilder_ReadsProtectionAdjustmentState(t *testing.T) {
	st := newPhase3Store(t)
	traderID := "trader-phase6"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-phase6"
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

	if _, err := st.ProtectionAdjustmentBuilder().ApplyProtectionAdjustmentEvent(&ProtectionAdjustmentEventInput{
		TraderID:                      traderID,
		Symbol:                        symbol,
		Side:                          side,
		LinkedPositionKey:             positionKey,
		ProtectionGroupID:             groupID,
		ProtectionMode:                "trailing_segmented",
		StopLossOrderIntentID:         "sl-intent",
		TakeProfitOrderIntentID:       "tp-intent",
		StopLossExchangeOrderID:       "sl-exchange",
		TakeProfitExchangeOrderID:     "tp-exchange",
		StopLossInitialTriggerPrice:   64000,
		StopLossCurrentTriggerPrice:   65500,
		TakeProfitInitialTriggerPrice: 68000,
		TakeProfitCurrentTriggerPrice: 70000,
		BreakEvenArmed:                true,
		TrailingArmed:                 true,
		TrailingRuleID:                "rule-phase6",
		TrailingAnchorPrice:           66500,
		TrailingLastMoveAt:            now,
		TrailingMoveCount:             2,
		ProtectionRevision:            3,
		LastProtectionAction:          "move_stop_loss",
		LastProtectionActionAt:        now,
		ProtectedQuantity:             1,
		Status:                        "armed",
		Source:                        "unit_test",
		EventType:                     "TRAILING_STEP_TRIGGERED",
		EventSource:                   "unit_test",
		PayloadJSON:                   `{"phase":"phase6"}`,
		EventTime:                     now,
	}); err != nil {
		t.Fatalf("seed protection adjustment failed: %v", err)
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
			EventType:         "ORDER_NEW",
			EventSource:       "unit_test",
			PayloadJSON:       `{"phase":"phase6"}`,
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
		t.Fatalf("expected aggregate to reflect protection state")
	}
	if agg.ProtectionMode != "trailing_segmented" {
		t.Fatalf("expected trailing_segmented protection mode, got %s", agg.ProtectionMode)
	}
	if agg.ProtectionGroupID != groupID {
		t.Fatalf("expected protection group id %s, got %s", groupID, agg.ProtectionGroupID)
	}
	if agg.CurrentStopLossPrice != 65500 {
		t.Fatalf("expected current stop-loss price 65500, got %.6f", agg.CurrentStopLossPrice)
	}
	if agg.InitialStopLossPrice != 64000 {
		t.Fatalf("expected initial stop-loss price 64000, got %.6f", agg.InitialStopLossPrice)
	}
	if !agg.BreakEvenArmed {
		t.Fatalf("expected break-even armed state")
	}
	if !agg.TrailingArmed {
		t.Fatalf("expected trailing armed state")
	}
	if agg.TrailingRuleID != "rule-phase6" {
		t.Fatalf("expected trailing rule id rule-phase6, got %s", agg.TrailingRuleID)
	}
	if agg.ProtectionRevision != 3 {
		t.Fatalf("expected protection revision 3, got %d", agg.ProtectionRevision)
	}
	if agg.ProtectionMoveCount != 3 {
		t.Fatalf("expected protection move count 3, got %d", agg.ProtectionMoveCount)
	}
	if agg.LastProtectionMoveAt.IsZero() {
		t.Fatalf("expected last protection move timestamp")
	}
}
