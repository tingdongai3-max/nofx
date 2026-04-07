package trader

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"nofx/store"
	tradertypes "nofx/trader/types"
)

type phase4ReduceCall struct {
	Symbol   string
	Quantity float64
}

type phase4AddCall struct {
	Symbol   string
	Quantity float64
	Leverage int
}

type phase4ScaleOutTrader struct {
	positions []map[string]interface{}

	limitOrderCalls      []tradertypes.LimitOrderRequest
	addLongCalls         []phase4AddCall
	addShortCalls        []phase4AddCall
	reduceLongCalls      []phase4ReduceCall
	reduceShortCalls     []phase4ReduceCall
	createStopLossCalls  []protectionOrderCall
	createTakeProfitCalls []protectionOrderCall
	cancelStopLossCalls  []string
	cancelTakeProfitCalls []string
	cancelAllOrdersCalls  []string
	cancelOrderCalls      []string
}

func (f *phase4ScaleOutTrader) GetBalance() (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (f *phase4ScaleOutTrader) GetPositions() ([]map[string]interface{}, error) {
	return f.positions, nil
}

func (f *phase4ScaleOutTrader) setPosition(symbol, side string, quantity, entryPrice, markPrice float64) {
	f.positions = []map[string]interface{}{
		{
			"symbol":           symbol,
			"side":             strings.ToUpper(strings.TrimSpace(side)),
			"entry_price":      entryPrice,
			"mark_price":       markPrice,
			"quantity":         quantity,
			"unrealized_pnl":   0,
			"leverage":         5,
			"liquidation_price": 0,
		},
	}
}

func (f *phase4ScaleOutTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (f *phase4ScaleOutTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (f *phase4ScaleOutTrader) AddLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	f.addLongCalls = append(f.addLongCalls, phase4AddCall{Symbol: symbol, Quantity: quantity, Leverage: leverage})
	id := fmt.Sprintf("add-long-%d", len(f.addLongCalls))
	return map[string]interface{}{
		"orderId":       id,
		"clientOrderId": id,
		"status":        "NEW",
	}, nil
}

func (f *phase4ScaleOutTrader) AddShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	f.addShortCalls = append(f.addShortCalls, phase4AddCall{Symbol: symbol, Quantity: quantity, Leverage: leverage})
	id := fmt.Sprintf("add-short-%d", len(f.addShortCalls))
	return map[string]interface{}{
		"orderId":       id,
		"clientOrderId": id,
		"status":        "NEW",
	}, nil
}

func (f *phase4ScaleOutTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (f *phase4ScaleOutTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (f *phase4ScaleOutTrader) SetLeverage(symbol string, leverage int) error         { return nil }
func (f *phase4ScaleOutTrader) SetMarginMode(symbol string, isCrossMargin bool) error { return nil }
func (f *phase4ScaleOutTrader) GetMarketPrice(symbol string) (float64, error)         { return 0, nil }
func (f *phase4ScaleOutTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	return nil
}
func (f *phase4ScaleOutTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	return nil
}
func (f *phase4ScaleOutTrader) CancelStopLossOrders(symbol string) error {
	f.cancelStopLossCalls = append(f.cancelStopLossCalls, symbol)
	return nil
}
func (f *phase4ScaleOutTrader) CancelTakeProfitOrders(symbol string) error {
	f.cancelTakeProfitCalls = append(f.cancelTakeProfitCalls, symbol)
	return nil
}
func (f *phase4ScaleOutTrader) CancelAllOrders(symbol string) error {
	f.cancelAllOrdersCalls = append(f.cancelAllOrdersCalls, symbol)
	return nil
}
func (f *phase4ScaleOutTrader) CancelStopOrders(symbol string) error { return nil }
func (f *phase4ScaleOutTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	return fmt.Sprintf("%.6f", quantity), nil
}
func (f *phase4ScaleOutTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (f *phase4ScaleOutTrader) GetClosedPnL(startTime time.Time, limit int) ([]tradertypes.ClosedPnLRecord, error) {
	return nil, nil
}
func (f *phase4ScaleOutTrader) GetOpenOrders(symbol string) ([]tradertypes.OpenOrder, error) {
	return nil, nil
}

func (f *phase4ScaleOutTrader) PlaceLimitOrder(req *tradertypes.LimitOrderRequest) (*tradertypes.LimitOrderResult, error) {
	if req == nil {
		return nil, fmt.Errorf("limit order request is required")
	}
	f.limitOrderCalls = append(f.limitOrderCalls, *req)
	orderID := req.ClientID
	if strings.TrimSpace(orderID) == "" {
		orderID = fmt.Sprintf("limit-%d", len(f.limitOrderCalls))
	}
	return &tradertypes.LimitOrderResult{
		OrderID:      orderID,
		ClientID:     orderID,
		Symbol:       req.Symbol,
		Side:         req.Side,
		PositionSide: req.PositionSide,
		Price:        req.Price,
		Quantity:     req.Quantity,
		Status:       "NEW",
	}, nil
}

func (f *phase4ScaleOutTrader) CancelOrder(symbol, orderID string) error {
	f.cancelOrderCalls = append(f.cancelOrderCalls, orderID)
	return nil
}

func (f *phase4ScaleOutTrader) GetOrderBook(symbol string, depth int) (bids, asks [][]float64, err error) {
	return nil, nil, nil
}

func (f *phase4ScaleOutTrader) ReduceLong(symbol string, quantity float64) (map[string]interface{}, error) {
	f.reduceLongCalls = append(f.reduceLongCalls, phase4ReduceCall{Symbol: symbol, Quantity: quantity})
	id := fmt.Sprintf("reduce-long-%d", len(f.reduceLongCalls))
	return map[string]interface{}{
		"orderId":       id,
		"clientOrderId": id,
		"status":        "NEW",
	}, nil
}

func (f *phase4ScaleOutTrader) ReduceShort(symbol string, quantity float64) (map[string]interface{}, error) {
	f.reduceShortCalls = append(f.reduceShortCalls, phase4ReduceCall{Symbol: symbol, Quantity: quantity})
	id := fmt.Sprintf("reduce-short-%d", len(f.reduceShortCalls))
	return map[string]interface{}{
		"orderId":       id,
		"clientOrderId": id,
		"status":        "NEW",
	}, nil
}

func (f *phase4ScaleOutTrader) CreateStopLossOrder(symbol, positionSide string, quantity, stopPrice float64, clientAlgoID string) (string, error) {
	f.createStopLossCalls = append(f.createStopLossCalls, protectionOrderCall{
		Symbol:       symbol,
		PositionSide: positionSide,
		Quantity:     quantity,
		Price:        stopPrice,
		ClientAlgoID: clientAlgoID,
	})
	if strings.TrimSpace(clientAlgoID) == "" {
		clientAlgoID = fmt.Sprintf("stop-loss-%d", len(f.createStopLossCalls))
	}
	return clientAlgoID, nil
}

func (f *phase4ScaleOutTrader) CreateTakeProfitOrder(symbol, positionSide string, quantity, takeProfitPrice float64, clientAlgoID string) (string, error) {
	f.createTakeProfitCalls = append(f.createTakeProfitCalls, protectionOrderCall{
		Symbol:       symbol,
		PositionSide: positionSide,
		Quantity:     quantity,
		Price:        takeProfitPrice,
		ClientAlgoID: clientAlgoID,
	})
	if strings.TrimSpace(clientAlgoID) == "" {
		clientAlgoID = fmt.Sprintf("take-profit-%d", len(f.createTakeProfitCalls))
	}
	return clientAlgoID, nil
}

func TestScaleOutManager_CreatesPlanAndReduceOrders(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-out"
	symbol := "BTCUSDT"
	side := "LONG"

	if err := st.StrategyProfile().Upsert(&store.StrategyProfile{
		TraderID:         traderID,
		Exchange:         "binance_usdm",
		Mode:             "one_way",
		ExecutionEnabled: true,
		ProtectionMode:   "fixed",
		DecisionStyle:    "ai_trading",
	}); err != nil {
		t.Fatalf("seed strategy profile failed: %v", err)
	}

	fake := &phase4ScaleOutTrader{}
	manager := NewScaleOutManager(st, fake)
	aggregate := &store.PositionAggregate{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		TotalQty:          1,
		AvgEntryPrice:     65000,
		ExecutionEligible: true,
	}

	plan, err := manager.EnsureScaleOutPlanForAggregate(aggregate, nil, &ScaleOutPlanConfig{
		LevelRatios:   []float64{0.5, 0.5},
		TargetPercents: []float64{0.03, 0.06},
		TargetType:    "limit_price",
	})
	if err != nil {
		t.Fatalf("create scale-out plan failed: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected scale-out plan")
	}
	if plan.Status != "armed" {
		t.Fatalf("expected armed scale-out plan, got %s", plan.Status)
	}
	if len(fake.limitOrderCalls) != 2 {
		t.Fatalf("expected two reduce orders, got %d", len(fake.limitOrderCalls))
	}
	for _, req := range fake.limitOrderCalls {
		if !req.ReduceOnly {
			t.Fatalf("expected reduce-only order request")
		}
		if req.PositionSide != "LONG" {
			t.Fatalf("expected LONG position side, got %s", req.PositionSide)
		}
		if req.Side != "SELL" {
			t.Fatalf("expected SELL side for long scale-out, got %s", req.Side)
		}
	}

	levels, err := st.ScaleOutPlanLevel().ListByPlanID(plan.ScaleOutPlanID)
	if err != nil {
		t.Fatalf("list plan levels failed: %v", err)
	}
	if len(levels) != 2 {
		t.Fatalf("expected two scale-out levels, got %d", len(levels))
	}
	for _, level := range levels {
		if level.Status != "armed" {
			t.Fatalf("expected armed level status, got %s", level.Status)
		}
	}

	rows, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list registry rows failed: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two registry rows, got %d", len(rows))
	}
	for _, row := range rows {
		if row.OrderRole != "reduce" {
			t.Fatalf("expected reduce order role, got %s", row.OrderRole)
		}
		if !row.IsWorking {
			t.Fatalf("expected working reduce order, got %#v", row)
		}
	}

	reused, err := manager.EnsureScaleOutPlanForAggregate(aggregate, nil, nil)
	if err != nil {
		t.Fatalf("reuse scale-out plan failed: %v", err)
	}
	if reused == nil || reused.ScaleOutPlanID != plan.ScaleOutPlanID {
		t.Fatalf("expected plan reuse, got %#v", reused)
	}
	if len(fake.limitOrderCalls) != 2 {
		t.Fatalf("expected idempotent reuse to avoid duplicate orders, got %d calls", len(fake.limitOrderCalls))
	}
}

func TestProtectionRebalanceManager_AdjustsProtectedQuantityAfterPartialFill(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-protection-rebalance"
	symbol := "BTCUSDT"
	side := "LONG"

	if err := st.StrategyProfile().Upsert(&store.StrategyProfile{
		TraderID:         traderID,
		Exchange:         "binance_usdm",
		Mode:             "one_way",
		ExecutionEnabled: true,
		ProtectionMode:   "fixed",
		DecisionStyle:    "ai_trading",
	}); err != nil {
		t.Fatalf("seed strategy profile failed: %v", err)
	}
	seedFixedProtectionDecision(t, st, traderID, symbol, 64000, 68000)

	fake := &phase4ScaleOutTrader{}
	fixedManager := NewFixedProtectionManager(st, fake)
	initialAggregate := &store.PositionAggregate{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		TotalQty:          1,
		AvgEntryPrice:     65000,
		ExecutionEligible: true,
	}
	if _, err := fixedManager.EnsureFixedProtectionForAggregate(initialAggregate, nil); err != nil {
		t.Fatalf("seed fixed protection failed: %v", err)
	}

	rebalanceManager := NewProtectionRebalanceManager(st, fixedManager)
	rebalancedGroup, err := rebalanceManager.RebalanceForAggregate(&store.PositionAggregate{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		TotalQty:          0.75,
		AvgEntryPrice:     65000,
		ExecutionEligible: true,
	})
	if err != nil {
		t.Fatalf("rebalance protection failed: %v", err)
	}
	if rebalancedGroup == nil {
		t.Fatalf("expected rebalanced protection group")
	}
	if diff := math.Abs(rebalancedGroup.ProtectedQuantity - 0.75); diff > 1e-9 {
		t.Fatalf("expected protected quantity 0.75, got %.12f", rebalancedGroup.ProtectedQuantity)
	}
	if len(fake.cancelStopLossCalls) != 1 || len(fake.cancelTakeProfitCalls) != 1 {
		t.Fatalf("expected sibling protection cancel + recreate, got stop=%d take=%d", len(fake.cancelStopLossCalls), len(fake.cancelTakeProfitCalls))
	}
	if len(fake.createStopLossCalls) != 2 || len(fake.createTakeProfitCalls) != 2 {
		t.Fatalf("expected initial + rebalanced protection orders, got stop=%d take=%d", len(fake.createStopLossCalls), len(fake.createTakeProfitCalls))
	}

	summary, err := st.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		t.Fatalf("summarize protection group failed: %v", err)
	}
	if diff := math.Abs(summary.ProtectedQuantity - 0.75); diff > 1e-9 {
		t.Fatalf("expected summary protected quantity 0.75, got %.12f", summary.ProtectedQuantity)
	}
	if summary.ProtectionGroupStatus != "armed" {
		t.Fatalf("expected armed protection group after rebalance, got %s", summary.ProtectionGroupStatus)
	}
}

func TestScaleOutStateReconciler_PartialFillAdvancesPlan(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-out-fill"
	symbol := "BTCUSDT"
	side := "LONG"

	if err := st.StrategyProfile().Upsert(&store.StrategyProfile{
		TraderID:         traderID,
		Exchange:         "binance_usdm",
		Mode:             "one_way",
		ExecutionEnabled: true,
		ProtectionMode:   "fixed",
		DecisionStyle:    "ai_trading",
	}); err != nil {
		t.Fatalf("seed strategy profile failed: %v", err)
	}
	seedFixedProtectionDecision(t, st, traderID, symbol, 64000, 68000)

	if err := st.Position().Create(&store.TraderPosition{
		TraderID:           traderID,
		ExchangeID:         "exchange-1",
		ExchangeType:       "binance",
		ExchangePositionID: "pos-1",
		Symbol:             symbol,
		Side:               side,
		Quantity:           0.8,
		EntryPrice:         65000,
		EntryTime:          time.Now().UTC().UnixMilli(),
		CreatedAt:          time.Now().UTC().UnixMilli(),
		UpdatedAt:          time.Now().UTC().UnixMilli(),
	}); err != nil {
		t.Fatalf("seed position failed: %v", err)
	}

	fake := &phase4ScaleOutTrader{}
	fixedManager := NewFixedProtectionManager(st, fake)
	initialAggregate := &store.PositionAggregate{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		TotalQty:          1,
		AvgEntryPrice:     65000,
		ExecutionEligible: true,
	}
	if _, err := fixedManager.EnsureFixedProtectionForAggregate(initialAggregate, nil); err != nil {
		t.Fatalf("seed fixed protection failed: %v", err)
	}

	scaleOutManager := NewScaleOutManager(st, fake)
	plan, err := scaleOutManager.EnsureScaleOutPlanForAggregate(initialAggregate, nil, &ScaleOutPlanConfig{
		LevelRatios:   []float64{0.5, 0.5},
		TargetPercents: []float64{0.03, 0.06},
		TargetType:    "limit_price",
	})
	if err != nil {
		t.Fatalf("create scale-out plan failed: %v", err)
	}
	if len(fake.limitOrderCalls) != 2 {
		t.Fatalf("expected two limit orders, got %d", len(fake.limitOrderCalls))
	}

	firstOrderID := fake.limitOrderCalls[0].ClientID
	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              "SELL",
		PositionSideMode:  "one_way",
		OrderRole:         "reduce",
		LocalIntentID:     firstOrderID,
		LinkedGroupID:     plan.ScaleOutPlanID,
		LinkedPositionKey: symbol + "|" + side,
		ExchangeOrderID:   firstOrderID,
		ClientOrderID:     firstOrderID,
		OrderType:         "LIMIT",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		OrigQty:           fake.limitOrderCalls[0].Quantity,
		ExecutedQty:       0.2,
		Status:            "PARTIALLY_FILLED",
		Source:            "user_stream",
		EventType:         "ORDER_TRADE_UPDATE",
		EventSource:       "unit_test",
		PayloadJSON:       `{"kind":"partial_fill"}`,
		EventTime:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed partial fill failed: %v", err)
	}

	fake.setPosition(symbol, side, 0.8, 65000, 65250)
	reconciler := NewScaleOutStateReconciler(st, NewProtectionRebalanceManager(st, fixedManager))
	preview, err := reconciler.SyncTraderScaleOutState(traderID, symbol, fake, true)
	if err != nil {
		t.Fatalf("sync scale-out state failed: %v", err)
	}
	if preview == nil {
		t.Fatalf("expected scale-out preview")
	}
	if preview.ScaleOutStatus != "partially_filled" {
		t.Fatalf("expected partially_filled scale-out status, got %s", preview.ScaleOutStatus)
	}
	if !preview.HasScaleOutPlan {
		t.Fatalf("expected scale-out plan to remain active after partial fill")
	}
	if !preview.ProtectionRebalanced {
		t.Fatalf("expected protection quantity to be rebalanced after partial fill")
	}
	if diff := math.Abs(preview.ExecutedScaleOutQty - 0.2); diff > 1e-9 {
		t.Fatalf("expected executed scale-out qty 0.2, got %.12f", preview.ExecutedScaleOutQty)
	}
	if diff := math.Abs(preview.RemainingScaleOutQty - 0.8); diff > 1e-9 {
		t.Fatalf("expected remaining scale-out qty 0.8, got %.12f", preview.RemainingScaleOutQty)
	}

	planSummary, err := st.ScaleOutPlan().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		t.Fatalf("summarize scale-out plan failed: %v", err)
	}
	if planSummary.ScaleOutStatus != "partially_filled" {
		t.Fatalf("expected partially_filled plan summary, got %s", planSummary.ScaleOutStatus)
	}
	if planSummary.ConsistencyStatus != "pending" {
		t.Fatalf("expected pending consistency after partial fill, got %s", planSummary.ConsistencyStatus)
	}

	protectionSummary, err := st.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		t.Fatalf("summarize protection group failed: %v", err)
	}
	if diff := math.Abs(protectionSummary.ProtectedQuantity - 0.8); diff > 1e-9 {
		t.Fatalf("expected protection quantity 0.8, got %.12f", protectionSummary.ProtectedQuantity)
	}

	eventsAfterFirst, err := st.ScaleOutEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-out events failed: %v", err)
	}
	cancelStopCalls := len(fake.cancelStopLossCalls)
	cancelTakeCalls := len(fake.cancelTakeProfitCalls)
	createStopCalls := len(fake.createStopLossCalls)
	createTakeCalls := len(fake.createTakeProfitCalls)

	previewAgain, err := reconciler.SyncTraderScaleOutState(traderID, symbol, fake, true)
	if err != nil {
		t.Fatalf("sync scale-out state replay failed: %v", err)
	}
	if previewAgain.ScaleOutStatus != "partially_filled" {
		t.Fatalf("expected replay to remain partially_filled, got %s", previewAgain.ScaleOutStatus)
	}
	eventsAfterSecond, err := st.ScaleOutEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-out events failed: %v", err)
	}
	if len(eventsAfterSecond) != len(eventsAfterFirst) {
		t.Fatalf("expected idempotent replay to keep event count stable, got %d then %d", len(eventsAfterFirst), len(eventsAfterSecond))
	}
	if len(fake.cancelStopLossCalls) != cancelStopCalls || len(fake.cancelTakeProfitCalls) != cancelTakeCalls {
		t.Fatalf("expected idempotent replay to avoid duplicate protection cancels")
	}
	if len(fake.createStopLossCalls) != createStopCalls || len(fake.createTakeProfitCalls) != createTakeCalls {
		t.Fatalf("expected idempotent replay to avoid duplicate protection recreate")
	}
}

func TestScaleOutStateReconciler_CancelReplayIsIdempotent(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-out-cancel"
	symbol := "BTCUSDT"
	side := "LONG"

	if err := st.StrategyProfile().Upsert(&store.StrategyProfile{
		TraderID:         traderID,
		Exchange:         "binance_usdm",
		Mode:             "one_way",
		ExecutionEnabled: true,
		ProtectionMode:   "fixed",
		DecisionStyle:    "ai_trading",
	}); err != nil {
		t.Fatalf("seed strategy profile failed: %v", err)
	}
	seedFixedProtectionDecision(t, st, traderID, symbol, 64000, 68000)

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

	fake := &phase4ScaleOutTrader{}
	fixedManager := NewFixedProtectionManager(st, fake)
	initialAggregate := &store.PositionAggregate{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		TotalQty:          1,
		AvgEntryPrice:     65000,
		ExecutionEligible: true,
	}
	if _, err := fixedManager.EnsureFixedProtectionForAggregate(initialAggregate, nil); err != nil {
		t.Fatalf("seed fixed protection failed: %v", err)
	}

	scaleOutManager := NewScaleOutManager(st, fake)
	plan, err := scaleOutManager.EnsureScaleOutPlanForAggregate(initialAggregate, nil, &ScaleOutPlanConfig{
		LevelRatios:   []float64{1},
		TargetPercents: []float64{0.03},
		TargetType:    "limit_price",
	})
	if err != nil {
		t.Fatalf("create scale-out plan failed: %v", err)
	}
	if len(fake.limitOrderCalls) != 1 {
		t.Fatalf("expected one limit order, got %d", len(fake.limitOrderCalls))
	}

	orderID := fake.limitOrderCalls[0].ClientID
	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              "SELL",
		PositionSideMode:  "one_way",
		OrderRole:         "reduce",
		LocalIntentID:     orderID,
		LinkedGroupID:     plan.ScaleOutPlanID,
		LinkedPositionKey: symbol + "|" + side,
		ExchangeOrderID:   orderID,
		ClientOrderID:     orderID,
		OrderType:         "LIMIT",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		OrigQty:           fake.limitOrderCalls[0].Quantity,
		ExecutedQty:       0,
		Status:            "CANCELED",
		Source:            "user_stream",
		EventType:         "ORDER_TRADE_UPDATE",
		EventSource:       "unit_test",
		PayloadJSON:       `{"kind":"cancel"}`,
		EventTime:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed cancel event failed: %v", err)
	}

	fake.setPosition(symbol, side, 1, 65000, 65100)
	reconciler := NewScaleOutStateReconciler(st, NewProtectionRebalanceManager(st, fixedManager))
	preview, err := reconciler.SyncTraderScaleOutState(traderID, symbol, fake, true)
	if err != nil {
		t.Fatalf("sync scale-out cancel failed: %v", err)
	}
	if preview.ScaleOutStatus != "invalid" {
		t.Fatalf("expected invalid scale-out status after cancel, got %s", preview.ScaleOutStatus)
	}
	if !preview.HasStateMismatch {
		t.Fatalf("expected cancel replay to surface a state mismatch")
	}

	eventsAfterFirst, err := st.ScaleOutEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-out events failed: %v", err)
	}
	cancelStopCalls := len(fake.cancelStopLossCalls)
	cancelTakeCalls := len(fake.cancelTakeProfitCalls)
	createStopCalls := len(fake.createStopLossCalls)
	createTakeCalls := len(fake.createTakeProfitCalls)

	previewAgain, err := reconciler.SyncTraderScaleOutState(traderID, symbol, fake, true)
	if err != nil {
		t.Fatalf("sync scale-out cancel replay failed: %v", err)
	}
	if previewAgain.ScaleOutStatus != "invalid" {
		t.Fatalf("expected replay to remain invalid, got %s", previewAgain.ScaleOutStatus)
	}
	eventsAfterSecond, err := st.ScaleOutEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-out events failed: %v", err)
	}
	if len(eventsAfterSecond) != len(eventsAfterFirst) {
		t.Fatalf("expected cancel replay to keep event count stable, got %d then %d", len(eventsAfterFirst), len(eventsAfterSecond))
	}
	if len(fake.cancelStopLossCalls) != cancelStopCalls || len(fake.cancelTakeProfitCalls) != cancelTakeCalls {
		t.Fatalf("expected cancel replay to avoid duplicate protection cancels")
	}
	if len(fake.createStopLossCalls) != createStopCalls || len(fake.createTakeProfitCalls) != createTakeCalls {
		t.Fatalf("expected cancel replay to avoid duplicate protection recreate")
	}
}

func TestRuntimeCapabilityResolver_EnablesReducePositionOnlyWhenEligible(t *testing.T) {
	resolver := NewRuntimeCapabilityResolver()
	preview, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:            "trader-eligible",
		ExecutionMode:       "live",
		AllowOrderPlacement: true,
		UserStreamReady:     true,
		StrategyProfile: &store.StrategyProfile{
			TraderID:               "trader-eligible",
			Exchange:               "binance_usdm",
			Mode:                   "one_way",
			AllowPartialTakeProfit: true,
			ExecutionEnabled:       true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-eligible",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvailableQty:      0.25,
			ExecutionEligible: true,
		},
		HasScaleOutPlan:      true,
		ScaleOutStatus:       "armed",
		ScaleOutConsistency:  "consistent",
		PendingScaleOutQty:   0.75,
		RemainingScaleOutQty: 0.75,
		ExecutedScaleOutQty:  0.25,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if !containsString(preview.AllowedActions, "reduce_position") {
		t.Fatalf("expected reduce_position to be allowed when eligible")
	}
	if !containsString(preview.BlockedActions, "arm_partial_take_profit") {
		t.Fatalf("expected arm_partial_take_profit to stay blocked while a scale-out plan is active")
	}

	blocked, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:            "trader-blocked",
		ExecutionMode:       "live",
		AllowOrderPlacement: true,
		UserStreamReady:     true,
		StrategyProfile: &store.StrategyProfile{
			TraderID:         "trader-blocked",
			Exchange:         "binance_usdm",
			Mode:             "one_way",
			AllowPartialTakeProfit: true,
			ExecutionEnabled: true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-blocked",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvailableQty:      1,
			ExecutionEligible: true,
		},
		HasScaleOutPlan:     false,
		ScaleOutStatus:      "completed",
		ScaleOutConsistency: "consistent",
	})
	if err != nil {
		t.Fatalf("resolve blocked case failed: %v", err)
	}
	if containsString(blocked.AllowedActions, "reduce_position") {
		t.Fatalf("did not expect reduce_position to be allowed without an active scale-out plan")
	}
	if !containsBlockedReason(blocked.BlockReasons, "reduce_position", "scale_out", "no active scale-out plan") {
		t.Fatalf("expected no-active-plan block reason, got %#v", blocked.BlockReasons)
	}
}

func TestScaleOutPreview_ReturnsConsistentPlanState(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-out-preview"
	symbol := "BTCUSDT"
	side := "LONG"
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

	if _, err := st.ScaleOutPlanBuilder().ApplyScaleOutEvent(&store.ScaleOutPlanEventInput{
		TraderID:            traderID,
		Symbol:              symbol,
		Side:                side,
		LinkedPositionKey:   symbol + "|" + side,
		ScaleOutPlanID:      "so-preview",
		PlanMode:            "fixed_ratio",
		Status:              "completed",
		TotalPlannedQty:     1,
		RemainingPlannedQty: 0,
		ExecutedQty:         1,
		Source:              "unit_test",
		EventType:           "SCALE_OUT_PLAN_COMPLETED",
		EventSource:         "unit_test",
		PayloadJSON:         "{}",
		EventTime:           now,
		Levels: []*store.ScaleOutPlanLevelInput{
			{
				TraderID:           traderID,
				Symbol:             symbol,
				Side:               side,
				LinkedPositionKey:  symbol + "|" + side,
				ScaleOutPlanID:     "so-preview",
				LevelIndex:         1,
				TargetType:         "limit_price",
				TargetPrice:        66000,
				PlannedQty:         0.5,
				ExecutedQty:        0.5,
				RemainingQty:       0,
				LinkedOrderIntentID:"so-preview-l1",
				Status:             "filled",
			},
			{
				TraderID:           traderID,
				Symbol:             symbol,
				Side:               side,
				LinkedPositionKey:  symbol + "|" + side,
				ScaleOutPlanID:     "so-preview",
				LevelIndex:         2,
				TargetType:         "limit_price",
				TargetPrice:        67000,
				PlannedQty:         0.5,
				ExecutedQty:        0.5,
				RemainingQty:       0,
				LinkedOrderIntentID:"so-preview-l2",
				Status:             "filled",
			},
		},
	}); err != nil {
		t.Fatalf("seed completed scale-out plan failed: %v", err)
	}

	preview, err := BuildRuntimeScaleOutPreview(st, traderID, symbol, true)
	if err != nil {
		t.Fatalf("build scale-out preview failed: %v", err)
	}
	if preview.ScaleOutStatus != "completed" {
		t.Fatalf("expected completed scale-out status, got %s", preview.ScaleOutStatus)
	}
	if preview.ScaleOutConsistency != "consistent" {
		t.Fatalf("expected consistent scale-out state, got %s", preview.ScaleOutConsistency)
	}
	if preview.HasStateMismatch {
		t.Fatalf("did not expect scale-out state mismatch")
	}
	if preview.ScaleOutPlan == nil {
		t.Fatalf("expected preview to include scale-out plan")
	}
}
