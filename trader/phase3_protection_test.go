package trader

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nofx/store"
	tradertypes "nofx/trader/types"
)

type fakeProtectionTrader struct {
	createStopLossCalls   []protectionOrderCall
	createTakeProfitCalls []protectionOrderCall
	cancelStopLossCalls   []string
	cancelTakeProfitCalls []string
}

type protectionOrderCall struct {
	Symbol       string
	PositionSide string
	Quantity     float64
	Price        float64
	ClientAlgoID string
}

func (f *fakeProtectionTrader) GetBalance() (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (f *fakeProtectionTrader) GetPositions() ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}
func (f *fakeProtectionTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (f *fakeProtectionTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (f *fakeProtectionTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (f *fakeProtectionTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (f *fakeProtectionTrader) SetLeverage(symbol string, leverage int) error         { return nil }
func (f *fakeProtectionTrader) SetMarginMode(symbol string, isCrossMargin bool) error { return nil }
func (f *fakeProtectionTrader) GetMarketPrice(symbol string) (float64, error)         { return 0, nil }
func (f *fakeProtectionTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	return nil
}
func (f *fakeProtectionTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	return nil
}
func (f *fakeProtectionTrader) CancelStopLossOrders(symbol string) error {
	f.cancelStopLossCalls = append(f.cancelStopLossCalls, symbol)
	return nil
}
func (f *fakeProtectionTrader) CancelTakeProfitOrders(symbol string) error {
	f.cancelTakeProfitCalls = append(f.cancelTakeProfitCalls, symbol)
	return nil
}
func (f *fakeProtectionTrader) CancelAllOrders(symbol string) error  { return nil }
func (f *fakeProtectionTrader) CancelStopOrders(symbol string) error { return nil }
func (f *fakeProtectionTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	return fmt.Sprintf("%.6f", quantity), nil
}
func (f *fakeProtectionTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (f *fakeProtectionTrader) GetClosedPnL(startTime time.Time, limit int) ([]tradertypes.ClosedPnLRecord, error) {
	return nil, nil
}
func (f *fakeProtectionTrader) GetOpenOrders(symbol string) ([]tradertypes.OpenOrder, error) {
	return nil, nil
}
func (f *fakeProtectionTrader) CreateStopLossOrder(symbol, positionSide string, quantity, stopPrice float64, clientAlgoID string) (string, error) {
	f.createStopLossCalls = append(f.createStopLossCalls, protectionOrderCall{
		Symbol:       symbol,
		PositionSide: positionSide,
		Quantity:     quantity,
		Price:        stopPrice,
		ClientAlgoID: clientAlgoID,
	})
	if clientAlgoID == "" {
		clientAlgoID = "stop-loss-test"
	}
	return clientAlgoID, nil
}
func (f *fakeProtectionTrader) CreateTakeProfitOrder(symbol, positionSide string, quantity, takeProfitPrice float64, clientAlgoID string) (string, error) {
	f.createTakeProfitCalls = append(f.createTakeProfitCalls, protectionOrderCall{
		Symbol:       symbol,
		PositionSide: positionSide,
		Quantity:     quantity,
		Price:        takeProfitPrice,
		ClientAlgoID: clientAlgoID,
	})
	if clientAlgoID == "" {
		clientAlgoID = "take-profit-test"
	}
	return clientAlgoID, nil
}

func newPhase3TraderStore(t *testing.T) *store.Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "phase3-trader.sqlite")
	st, err := store.NewWithConfig(store.DBConfig{Type: store.DBTypeSQLite, Path: dbPath})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func seedFixedProtectionDecision(t *testing.T, st *store.Store, traderID, symbol string, stopLoss, takeProfit float64) {
	t.Helper()

	if err := st.Decision().LogDecision(&store.DecisionRecord{
		TraderID:    traderID,
		CycleNumber: 7,
		Timestamp:   time.Now().UTC(),
		Success:     true,
		Decisions: []store.DecisionAction{
			{
				Action:     "open_long",
				Symbol:     symbol,
				StopLoss:   stopLoss,
				TakeProfit: takeProfit,
				Timestamp:  time.Now().UTC(),
				Success:    true,
			},
		},
	}); err != nil {
		t.Fatalf("seed decision failed: %v", err)
	}
}

func TestFixedProtectionManager_AttachesStopLossAndTakeProfit(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-1"
	symbol := "BTCUSDT"

	if err := st.StrategyProfile().Upsert(&store.StrategyProfile{
		TraderID:         traderID,
		Exchange:         "binance_usdm",
		Mode:             "one_way",
		ExecutionEnabled: true,
		ProtectionMode:   "fixed",
	}); err != nil {
		t.Fatalf("seed strategy profile failed: %v", err)
	}
	seedFixedProtectionDecision(t, st, traderID, symbol, 64000, 68000)

	fakeTrader := &fakeProtectionTrader{}
	manager := NewFixedProtectionManager(st, fakeTrader)
	group, err := manager.EnsureFixedProtectionForAggregate(&store.PositionAggregate{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              "LONG",
		TotalQty:          1,
		AvgEntryPrice:     65000,
		ExecutionEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("ensure fixed protection failed: %v", err)
	}
	if group == nil || group.Status != "armed" {
		t.Fatalf("expected armed protection group, got %#v", group)
	}

	if len(fakeTrader.createStopLossCalls) != 1 {
		t.Fatalf("expected 1 stop-loss attach call, got %d", len(fakeTrader.createStopLossCalls))
	}
	if len(fakeTrader.createTakeProfitCalls) != 1 {
		t.Fatalf("expected 1 take-profit attach call, got %d", len(fakeTrader.createTakeProfitCalls))
	}

	orders, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list registry rows failed: %v", err)
	}
	if len(orders) != 2 {
		t.Fatalf("expected 2 protection registry rows, got %d", len(orders))
	}

	summary, err := st.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, "LONG")
	if err != nil {
		t.Fatalf("summarize protection group failed: %v", err)
	}
	if !summary.HasProtection || summary.ProtectionGroupStatus != "armed" {
		t.Fatalf("expected armed protection summary, got %#v", summary)
	}
	if !summary.StopLossArmed || !summary.TakeProfitArmed {
		t.Fatalf("expected both protection legs armed, got %#v", summary)
	}

	orderEvents, err := st.OrderEventLog().ListRecentByTrader(traderID, 10)
	if err != nil {
		t.Fatalf("list order event logs failed: %v", err)
	}
	if len(orderEvents) != 2 {
		t.Fatalf("expected 2 order evidence rows, got %d", len(orderEvents))
	}

	protectionEvents, err := st.ProtectionEventLog().ListRecentByTrader(traderID, 10)
	if err != nil {
		t.Fatalf("list protection event logs failed: %v", err)
	}
	if len(protectionEvents) != 2 {
		t.Fatalf("expected 2 protection evidence rows, got %d", len(protectionEvents))
	}
}

func TestProtectionStateReconciler_CancelsSiblingWhenOneLegFills(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-1"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-test"
	positionKey := symbol + "|" + side

	if _, err := st.ProtectionGroupBuilder().ApplyProtectionEvent(&store.ProtectionGroupEventInput{
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
		EventTime:               time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed protection group failed: %v", err)
	}

	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              "SELL",
		PositionSideMode:  "one_way",
		OrderRole:         "stop_loss",
		LocalIntentID:     "sl-intent",
		LinkedGroupID:     groupID,
		LinkedPositionKey: positionKey,
		ExchangeOrderID:   "1001",
		ClientOrderID:     "sl-client",
		OrderType:         "STOP_MARKET",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     true,
		OrigQty:           1,
		ExecutedQty:       1,
		Status:            "FILLED",
		Source:            "user_stream",
		EventType:         "ORDER_TRADE_UPDATE",
		EventSource:       "unit_test",
		PayloadJSON:       `{"leg":"stop_loss"}`,
		EventTime:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed filled stop-loss row failed: %v", err)
	}
	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              "SELL",
		PositionSideMode:  "one_way",
		OrderRole:         "take_profit",
		LocalIntentID:     "tp-intent",
		LinkedGroupID:     groupID,
		LinkedPositionKey: positionKey,
		ExchangeOrderID:   "1002",
		ClientOrderID:     "tp-client",
		OrderType:         "TAKE_PROFIT_MARKET",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     true,
		OrigQty:           1,
		ExecutedQty:       0,
		Status:            "NEW",
		Source:            "user_stream",
		EventType:         "ORDER_TRADE_UPDATE",
		EventSource:       "unit_test",
		PayloadJSON:       `{"leg":"take_profit"}`,
		EventTime:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed sibling row failed: %v", err)
	}

	reconciler := NewProtectionStateReconciler(st, nil)
	previewGroup, err := st.ProtectionGroup().GetByKeys(traderID, groupID, positionKey)
	if err != nil {
		t.Fatalf("load protection group failed: %v", err)
	}
	fakeTrader := &fakeProtectionTrader{}
	err = reconciler.reconcileProtectionLegState(traderID, symbol, side, fakeTrader, &ProtectionReconcilePreview{
		TraderID:          traderID,
		SelectedSymbol:    symbol,
		ProtectionGroup:   previewGroup,
		HasProtection:     true,
		PositionAggregate: &store.PositionAggregate{TraderID: traderID, Symbol: symbol, Side: side, TotalQty: 1, ExecutionEligible: true},
	})
	if err != nil {
		t.Fatalf("reconcile protection leg state failed: %v", err)
	}

	if len(fakeTrader.cancelTakeProfitCalls) != 1 {
		t.Fatalf("expected one sibling cancel request, got %d", len(fakeTrader.cancelTakeProfitCalls))
	}
	if fakeTrader.cancelTakeProfitCalls[0] != symbol {
		t.Fatalf("expected sibling cancel symbol %s, got %s", symbol, fakeTrader.cancelTakeProfitCalls[0])
	}

	summary, err := st.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		t.Fatalf("summarize protection group failed: %v", err)
	}
	if summary.ProtectionGroupStatus != "cancel_pending" {
		t.Fatalf("expected cancel_pending group status, got %s", summary.ProtectionGroupStatus)
	}

	rows, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list registry rows failed: %v", err)
	}
	var sibling *store.OrderRegistry
	for _, row := range rows {
		if row != nil && row.OrderRole == "take_profit" {
			sibling = row
			break
		}
	}
	if sibling == nil {
		t.Fatalf("expected take_profit sibling row")
	}
	if sibling.Status != "PENDING_CANCEL" {
		t.Fatalf("expected sibling status PENDING_CANCEL, got %s", sibling.Status)
	}
}

func TestProtectionStateReconciler_DuplicateLegFillIsIdempotent(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-dup-fill"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-dup-fill"

	seedReplayProtectionScope(t, st, traderID, symbol, side, groupID, "FILLED", "NEW")

	reconciler := NewProtectionStateReconciler(st, nil)
	fakeTrader := &fakeProtectionTrader{}
	if _, err := reconciler.SyncTraderProtectionState(traderID, symbol, fakeTrader, true); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	eventsAfterFirst := countProtectionEventsByType(t, st, traderID)
	if eventsAfterFirst != 2 {
		t.Fatalf("expected 2 protection events after first reconcile, got %d", eventsAfterFirst)
	}

	if _, err := reconciler.SyncTraderProtectionState(traderID, symbol, fakeTrader, true); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}
	eventsAfterSecond := countProtectionEventsByType(t, st, traderID)
	if eventsAfterSecond != eventsAfterFirst {
		t.Fatalf("expected duplicate fill replay to be idempotent, got %d then %d protection events", eventsAfterFirst, eventsAfterSecond)
	}

	summary, err := st.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		t.Fatalf("summarize protection group failed: %v", err)
	}
	if summary.ProtectionGroupStatus != "cancel_pending" {
		t.Fatalf("expected cancel_pending after duplicate fill replay, got %s", summary.ProtectionGroupStatus)
	}
	if summary.ConsistencyStatus != "pending" {
		t.Fatalf("expected pending consistency after duplicate fill replay, got %s", summary.ConsistencyStatus)
	}
}

func TestProtectionStateReconciler_DuplicateCancelReplayIsIdempotent(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-dup-cancel"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-dup-cancel"

	seedReplayProtectionScope(t, st, traderID, symbol, side, groupID, "FILLED", "PENDING_CANCEL")

	reconciler := NewProtectionStateReconciler(st, nil)
	fakeTrader := &fakeProtectionTrader{}
	if _, err := reconciler.SyncTraderProtectionState(traderID, symbol, fakeTrader, true); err != nil {
		t.Fatalf("first replay failed: %v", err)
	}
	eventsAfterFirst := countProtectionEventsByType(t, st, traderID)

	if _, err := reconciler.SyncTraderProtectionState(traderID, symbol, fakeTrader, true); err != nil {
		t.Fatalf("second replay failed: %v", err)
	}
	eventsAfterSecond := countProtectionEventsByType(t, st, traderID)
	if eventsAfterSecond != eventsAfterFirst {
		t.Fatalf("expected duplicate cancel replay to be idempotent, got %d then %d protection events", eventsAfterFirst, eventsAfterSecond)
	}

	summary, err := st.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		t.Fatalf("summarize protection group failed: %v", err)
	}
	if summary.ProtectionGroupStatus != "cancel_pending" {
		t.Fatalf("expected cancel_pending after duplicate cancel replay, got %s", summary.ProtectionGroupStatus)
	}
}

func TestFixedProtectionManager_RestartRecoveryDoesNotCreateDuplicateGroup(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-restart"
	symbol := "BTCUSDT"
	side := "LONG"

	if err := st.StrategyProfile().Upsert(&store.StrategyProfile{
		TraderID:         traderID,
		Exchange:         "binance_usdm",
		Mode:             "one_way",
		ExecutionEnabled: true,
		ProtectionMode:   "fixed",
	}); err != nil {
		t.Fatalf("seed strategy profile failed: %v", err)
	}
	seedFixedProtectionDecision(t, st, traderID, symbol, 64000, 68000)

	fakeTrader := &fakeProtectionTrader{}
	manager := NewFixedProtectionManager(st, fakeTrader)
	group, err := manager.EnsureFixedProtectionForAggregate(&store.PositionAggregate{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		TotalQty:          1,
		AvgEntryPrice:     65000,
		ExecutionEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("initial protection attach failed: %v", err)
	}
	if group == nil {
		t.Fatalf("expected protection group on initial attach")
	}

	restartedManager := NewFixedProtectionManager(st, fakeTrader)
	restartedGroup, err := restartedManager.EnsureFixedProtectionForAggregate(&store.PositionAggregate{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		TotalQty:          1,
		AvgEntryPrice:     65000,
		ExecutionEligible: true,
	}, nil)
	if err != nil {
		t.Fatalf("recovery attach failed: %v", err)
	}
	if restartedGroup == nil {
		t.Fatalf("expected recovery attach to return existing group")
	}

	groups, err := st.ProtectionGroup().ListByTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		t.Fatalf("list protection groups failed: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected restart recovery to keep one protection group row, got %d", len(groups))
	}

	orders, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list protection orders failed: %v", err)
	}
	if len(orders) != 2 {
		t.Fatalf("expected restart recovery to keep one protection pair, got %d rows", len(orders))
	}
}

func TestProtectionStateReconciler_UserStreamDelayReplayDoesNotJitter(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-delay"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-delay"

	seedReplayProtectionScope(t, st, traderID, symbol, side, groupID, "FILLED", "NEW")

	reconciler := NewProtectionStateReconciler(st, nil)
	fakeTrader := &fakeProtectionTrader{}
	previewBefore, err := reconciler.SyncTraderProtectionState(traderID, symbol, fakeTrader, false)
	if err != nil {
		t.Fatalf("first delayed sync failed: %v", err)
	}
	if previewBefore == nil || previewBefore.ProtectionGroupStatus != "cancel_pending" {
		t.Fatalf("expected cancel_pending after delayed sync, got %#v", previewBefore)
	}
	eventsAfterFirst := countProtectionEventsByType(t, st, traderID)

	previewAfter, err := reconciler.SyncTraderProtectionState(traderID, symbol, fakeTrader, true)
	if err != nil {
		t.Fatalf("second replay sync failed: %v", err)
	}
	if previewAfter == nil || previewAfter.ProtectionGroupStatus != "cancel_pending" {
		t.Fatalf("expected cancel_pending after replay sync, got %#v", previewAfter)
	}
	eventsAfterSecond := countProtectionEventsByType(t, st, traderID)
	if eventsAfterSecond != eventsAfterFirst {
		t.Fatalf("expected delayed replay to remain stable, got %d then %d protection events", eventsAfterFirst, eventsAfterSecond)
	}
	if previewBefore.ProtectionConsistency != previewAfter.ProtectionConsistency {
		t.Fatalf("expected replay consistency to remain stable, got %s then %s", previewBefore.ProtectionConsistency, previewAfter.ProtectionConsistency)
	}
}

func TestRuntimeCapabilityResolver_EnablesSetProtectionOnlyWhenEligible(t *testing.T) {
	resolver := NewRuntimeCapabilityResolver()
	preview, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:            "trader-1",
		ExecutionMode:       "live",
		AllowOrderPlacement: true,
		UserStreamReady:     true,
		StrategyProfile: &store.StrategyProfile{
			TraderID:         "trader-1",
			Exchange:         "binance_usdm",
			Mode:             "one_way",
			ExecutionEnabled: true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-1",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvailableQty:      1,
			ExecutionEligible: true,
		},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if !containsString(preview.AllowedActions, "set_protection") {
		t.Fatalf("expected set_protection to be allowed when eligible")
	}
	if containsString(preview.BlockedActions, "set_protection") {
		t.Fatalf("did not expect set_protection to be blocked when eligible")
	}
}

func TestProtectionPreview_ReturnsConsistentProtectionState(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-1"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-preview"
	positionKey := symbol + "|" + side

	if err := st.Position().Create(&store.TraderPosition{
		TraderID:           traderID,
		ExchangeID:         "exchange-1",
		ExchangeType:       "binance",
		ExchangePositionID: "pos-1",
		Symbol:             symbol,
		Side:               side,
		Quantity:           1,
		EntryPrice:         65000,
		EntryTime:          time.Now().UTC().UnixMilli(),
		CreatedAt:          time.Now().UTC().UnixMilli(),
		UpdatedAt:          time.Now().UTC().UnixMilli(),
	}); err != nil {
		t.Fatalf("seed position failed: %v", err)
	}

	if _, err := st.ProtectionGroupBuilder().ApplyProtectionEvent(&store.ProtectionGroupEventInput{
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
		EventTime:               time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed protection group failed: %v", err)
	}
	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              "SELL",
		PositionSideMode:  "one_way",
		OrderRole:         "stop_loss",
		LocalIntentID:     "sl-intent",
		LinkedGroupID:     groupID,
		LinkedPositionKey: positionKey,
		ExchangeOrderID:   "2001",
		ClientOrderID:     "sl-client",
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
		PayloadJSON:       `{"leg":"stop_loss"}`,
		EventTime:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stop-loss row failed: %v", err)
	}
	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              "SELL",
		PositionSideMode:  "one_way",
		OrderRole:         "take_profit",
		LocalIntentID:     "tp-intent",
		LinkedGroupID:     groupID,
		LinkedPositionKey: positionKey,
		ExchangeOrderID:   "2002",
		ClientOrderID:     "tp-client",
		OrderType:         "TAKE_PROFIT_MARKET",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     true,
		OrigQty:           1,
		ExecutedQty:       0,
		Status:            "NEW",
		Source:            "user_stream",
		EventType:         "ORDER_TRADE_UPDATE",
		EventSource:       "unit_test",
		PayloadJSON:       `{"leg":"take_profit"}`,
		EventTime:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed take-profit row failed: %v", err)
	}

	if _, err := store.NewPositionAggregateBuilder(st).BuildForTrader(traderID, nil, nil); err != nil {
		t.Fatalf("refresh aggregates failed: %v", err)
	}

	preview, err := BuildRuntimeProtectionPreview(st, traderID, symbol, true)
	if err != nil {
		t.Fatalf("build runtime protection preview failed: %v", err)
	}
	if !preview.HasProtection {
		t.Fatalf("expected protection preview to show active protection")
	}
	if preview.ProtectionGroupStatus != "armed" {
		t.Fatalf("expected armed protection status, got %s", preview.ProtectionGroupStatus)
	}
	if preview.ProtectionConsistency != "pending" {
		t.Fatalf("expected pending protection consistency, got %s", preview.ProtectionConsistency)
	}
	if preview.HasStateMismatch {
		t.Fatalf("did not expect a protection state mismatch")
	}
	if preview.ProtectionGroup == nil || preview.OrderRegistrySummary == nil {
		t.Fatalf("expected protection group and order summary in preview")
	}
	if preview.PositionAggregate == nil {
		t.Fatalf("expected protection preview to include refreshed position aggregate")
	}
}

func seedReplayProtectionScope(t *testing.T, st *store.Store, traderID, symbol, side, groupID, stopLossStatus, takeProfitStatus string) {
	t.Helper()

	positionKey := symbol + "|" + side
	now := time.Now().UTC()

	if err := st.Position().Create(&store.TraderPosition{
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

	if _, err := st.ProtectionGroupBuilder().ApplyProtectionEvent(&store.ProtectionGroupEventInput{
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
		orderRole  string
		intentID   string
		exchangeID string
		status     string
	}{
		{orderRole: "stop_loss", intentID: "sl-intent", exchangeID: "1001", status: stopLossStatus},
		{orderRole: "take_profit", intentID: "tp-intent", exchangeID: "1002", status: takeProfitStatus},
	} {
		executedQty := 0.0
		remainingQty := 1.0
		if strings.EqualFold(order.status, "FILLED") {
			executedQty = 1
			remainingQty = 0
		}
		if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
			TraderID:          traderID,
			Symbol:            symbol,
			Side:              "SELL",
			PositionSideMode:  "one_way",
			OrderRole:         order.orderRole,
			LocalIntentID:     order.intentID,
			LinkedGroupID:     groupID,
			LinkedPositionKey: positionKey,
			ExchangeOrderID:   order.exchangeID,
			ClientOrderID:     order.intentID + "-client",
			OrderType:         "STOP_MARKET",
			TimeInForce:       "GTC",
			ReduceOnly:        true,
			ClosePosition:     true,
			OrigQty:           1,
			ExecutedQty:       executedQty,
			Status:            order.status,
			Source:            "user_stream",
			EventType:         "ORDER_TRADE_UPDATE",
			EventSource:       "unit_test",
			PayloadJSON:       `{"leg":"protection"}`,
			EventTime:         now,
		}); err != nil {
			t.Fatalf("seed protection order failed: %v", err)
		}
		_ = remainingQty
	}
}

func countProtectionEventsByType(t *testing.T, st *store.Store, traderID string) int {
	t.Helper()

	events, err := st.ProtectionEventLog().ListRecentByTrader(traderID, 100)
	if err != nil {
		t.Fatalf("list protection events failed: %v", err)
	}
	return len(events)
}
