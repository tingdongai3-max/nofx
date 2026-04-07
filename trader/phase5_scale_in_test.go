package trader

import (
	"math"
	"strings"
	"testing"
	"time"

	"nofx/store"
)

func seedScaleInStrategyProfile(t *testing.T, st *store.Store, traderID string, allowAdd bool, maxScaleInCount int, maxRiskPct float64) {
	t.Helper()

	if err := st.StrategyProfile().Upsert(&store.StrategyProfile{
		TraderID:               traderID,
		Exchange:               "binance_usdm",
		Mode:                   "one_way",
		AllowAddPosition:       allowAdd,
		AllowPartialTakeProfit: false,
		AllowMoveStopLoss:      false,
		AllowTrailingStop:      false,
		ProtectionMode:         "fixed",
		MaxScaleInCount:        maxScaleInCount,
		MaxPositionRiskPct:     maxRiskPct,
		DecisionStyle:          "ai_trading",
		ExecutionEnabled:       true,
	}); err != nil {
		t.Fatalf("seed strategy profile failed: %v", err)
	}
}

func seedScaleInOpenPosition(t *testing.T, st *store.Store, traderID, symbol, side string, quantity, entryPrice float64) {
	t.Helper()

	builder := store.NewPositionBuilder(st.Position())
	action := "open_long"
	if strings.EqualFold(side, "SHORT") {
		action = "open_short"
	}
	if err := builder.ProcessTrade(
		traderID,
		"exchange-1",
		"binance",
		symbol,
		side,
		action,
		quantity,
		entryPrice,
		0,
		0,
		time.Now().UTC().UnixMilli(),
		"seed-open",
	); err != nil {
		t.Fatalf("seed open position failed: %v", err)
	}
}

func seedScaleInAggregate(traderID, symbol, side string, quantity, entryPrice float64) *store.PositionAggregate {
	return &store.PositionAggregate{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		TotalQty:          quantity,
		AvgEntryPrice:     entryPrice,
		ExecutionEligible: true,
	}
}

func placeScaleInMarketPlan(t *testing.T, st *store.Store, fake *phase4ScaleOutTrader, traderID, symbol, side string, totalQty float64) (*store.ScaleInPlan, string) {
	t.Helper()

	manager := NewScaleInManager(st, fake, NewScaleInRiskGuard())
	plan, err := manager.EnsureScaleInPlanForAggregate(seedScaleInAggregate(traderID, symbol, side, totalQty, 65000), nil, &ScaleInPlanConfig{
		FixedQty:     totalQty,
		TargetType:   "market",
		Leverage:     5,
		LevelRatios:  []float64{1},
		TargetPrices: []float64{65000},
	}, 100000)
	if err != nil {
		t.Fatalf("ensure scale-in plan failed: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected scale-in plan")
	}

	levels, err := st.ScaleInPlanLevel().ListByPlanID(plan.ScaleInPlanID)
	if err != nil {
		t.Fatalf("list scale-in levels failed: %v", err)
	}
	if len(levels) != 1 {
		t.Fatalf("expected 1 scale-in level, got %d", len(levels))
	}
	if levels[0].LinkedExchangeOrderID == "" {
		t.Fatalf("expected linked exchange order id on scale-in level")
	}
	return plan, levels[0].LinkedExchangeOrderID
}

func applyScaleInOrderState(t *testing.T, st *store.Store, traderID, symbol, side, planID, intentID, exchangeOrderID, status string, origQty, executedQty float64) {
	t.Helper()

	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              scaleInExchangeSide(side),
		PositionSideMode:  "one_way",
		OrderRole:         "entry",
		LocalIntentID:     intentID,
		LinkedGroupID:     planID,
		LinkedPositionKey: scaleInPositionKey(symbol, side),
		ExchangeOrderID:   exchangeOrderID,
		ClientOrderID:     exchangeOrderID,
		OrderType:         "MARKET",
		TimeInForce:       "GTC",
		ReduceOnly:        false,
		ClosePosition:     false,
		OrigQty:           origQty,
		ExecutedQty:       executedQty,
		Status:            status,
		Source:            "unit_test",
		EventType:         "ORDER_TRADE_UPDATE",
		EventSource:       "unit_test",
		PayloadJSON:       `{"source":"unit_test"}`,
		EventTime:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("apply scale-in order state failed: %v", err)
	}
}

func TestScaleInManager_CreatesPlanAndEntryAddOrders(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-in-create"
	symbol := "BTCUSDT"
	side := "LONG"

	seedScaleInStrategyProfile(t, st, traderID, true, 2, 90)

	fake := &phase4ScaleOutTrader{}
	manager := NewScaleInManager(st, fake, NewScaleInRiskGuard())
	plan, err := manager.EnsureScaleInPlanForAggregate(seedScaleInAggregate(traderID, symbol, side, 1, 65000), nil, &ScaleInPlanConfig{
		FixedQty:     0.2,
		TargetType:   "market",
		Leverage:     5,
		LevelRatios:  []float64{1},
		TargetPrices: []float64{65000},
	}, 100000)
	if err != nil {
		t.Fatalf("ensure scale-in plan failed: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected scale-in plan")
	}
	if plan.Status != "armed" {
		t.Fatalf("expected armed scale-in plan, got %s", plan.Status)
	}
	if len(fake.addLongCalls) != 1 {
		t.Fatalf("expected one add-long call, got %d", len(fake.addLongCalls))
	}
	if diff := math.Abs(fake.addLongCalls[0].Quantity - 0.2); diff > 1e-9 {
		t.Fatalf("expected add quantity 0.2, got %.12f", fake.addLongCalls[0].Quantity)
	}

	rows, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list order registry rows failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 order registry row, got %d", len(rows))
	}
	if rows[0].OrderRole != "entry" {
		t.Fatalf("expected entry order role, got %s", rows[0].OrderRole)
	}
	if !rows[0].IsWorking {
		t.Fatalf("expected working add order row")
	}

	eventLogs, err := st.ScaleInEventLog().ListRecentByTrader(traderID, 10)
	if err != nil {
		t.Fatalf("list scale-in event logs failed: %v", err)
	}
	if len(eventLogs) < 2 {
		t.Fatalf("expected plan creation and attachment evidence, got %d rows", len(eventLogs))
	}
}

func TestScaleInStateReconciler_PartialFillAdvancesPlan(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-in-partial"
	symbol := "BTCUSDT"
	side := "LONG"

	seedScaleInStrategyProfile(t, st, traderID, true, 2, 90)
	fake := &phase4ScaleOutTrader{}
	plan, exchangeOrderID := placeScaleInMarketPlan(t, st, fake, traderID, symbol, side, 0.2)
	level, err := st.ScaleInPlanLevel().GetByPlanIDAndLevelIndex(plan.ScaleInPlanID, 1)
	if err != nil {
		t.Fatalf("load scale-in level failed: %v", err)
	}

	applyScaleInOrderState(t, st, traderID, symbol, side, plan.ScaleInPlanID, level.LinkedOrderIntentID, exchangeOrderID, "PARTIALLY_FILLED", 0.2, 0.1)

	reconciler := NewScaleInStateReconciler(st, nil)
	preview, err := reconciler.SyncTraderScaleInState(traderID, symbol, fake, true)
	if err != nil {
		t.Fatalf("sync scale-in state failed: %v", err)
	}
	if preview == nil {
		t.Fatalf("expected scale-in preview")
	}
	if preview.ScaleInStatus != "partially_filled" {
		t.Fatalf("expected partially_filled status, got %s", preview.ScaleInStatus)
	}
	if !preview.HasScaleInPlan {
		t.Fatalf("expected scale-in plan to remain active after partial fill")
	}
	if diff := math.Abs(preview.ExecutedScaleInQty - 0.1); diff > 1e-9 {
		t.Fatalf("expected executed qty 0.1, got %.12f", preview.ExecutedScaleInQty)
	}
	if diff := math.Abs(preview.RemainingScaleInQty - 0.1); diff > 1e-9 {
		t.Fatalf("expected remaining qty 0.1, got %.12f", preview.RemainingScaleInQty)
	}

	updatedPlan, err := st.ScaleInPlan().GetByKeys(traderID, plan.ScaleInPlanID, scaleInPositionKey(symbol, side))
	if err != nil {
		t.Fatalf("load updated scale-in plan failed: %v", err)
	}
	if updatedPlan == nil || updatedPlan.Status != "partially_filled" {
		t.Fatalf("expected partially_filled plan row, got %#v", updatedPlan)
	}

	eventsAfterFirst, err := st.ScaleInEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-in events failed: %v", err)
	}
	if len(eventsAfterFirst) < 3 {
		t.Fatalf("expected plan advance evidence, got %d rows", len(eventsAfterFirst))
	}
}

func TestScaleInStateReconciler_FillUpdatesAverageEntryPrice(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-in-avg"
	symbol := "BTCUSDT"
	side := "LONG"

	seedScaleInStrategyProfile(t, st, traderID, true, 2, 90)
	seedScaleInOpenPosition(t, st, traderID, symbol, side, 1, 65000)
	fake := &phase4ScaleOutTrader{}
	plan, exchangeOrderID := placeScaleInMarketPlan(t, st, fake, traderID, symbol, side, 0.2)
	level, err := st.ScaleInPlanLevel().GetByPlanIDAndLevelIndex(plan.ScaleInPlanID, 1)
	if err != nil {
		t.Fatalf("load scale-in level failed: %v", err)
	}
	if err := store.NewPositionBuilder(st.Position()).ProcessTrade(traderID, "exchange-1", "binance", symbol, side, "add_position", 0.2, 70000, 0, 0, time.Now().UTC().UnixMilli(), "fill-add"); err != nil {
		t.Fatalf("process add-position trade failed: %v", err)
	}

	applyScaleInOrderState(t, st, traderID, symbol, side, plan.ScaleInPlanID, level.LinkedOrderIntentID, exchangeOrderID, "FILLED", 0.2, 0.2)

	reconciler := NewScaleInStateReconciler(st, nil)
	preview, err := reconciler.SyncTraderScaleInState(traderID, symbol, fake, true)
	if err != nil {
		t.Fatalf("sync scale-in state failed: %v", err)
	}
	if preview == nil {
		t.Fatalf("expected scale-in preview")
	}
	if preview.ScaleInStatus != "completed" {
		t.Fatalf("expected completed scale-in status, got %s", preview.ScaleInStatus)
	}

	aggregate, err := st.PositionAggregate().GetByTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		t.Fatalf("load aggregate failed: %v", err)
	}
	if aggregate == nil {
		t.Fatalf("expected position aggregate")
	}
	if diff := math.Abs(aggregate.AvgEntryPrice - 65833.33333333333); diff > 0.01 {
		t.Fatalf("expected average entry price recalculation, got %.12f", aggregate.AvgEntryPrice)
	}
	if diff := math.Abs(aggregate.TotalQty - 1.2); diff > 1e-9 {
		t.Fatalf("expected total qty 1.2 after add trade, got %.12f", aggregate.TotalQty)
	}
}

func TestScaleInStateReconciler_DuplicateFillReplayIsIdempotent(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-in-dup-fill"
	symbol := "BTCUSDT"
	side := "LONG"

	seedScaleInStrategyProfile(t, st, traderID, true, 2, 90)
	fake := &phase4ScaleOutTrader{}
	plan, exchangeOrderID := placeScaleInMarketPlan(t, st, fake, traderID, symbol, side, 0.2)
	level, err := st.ScaleInPlanLevel().GetByPlanIDAndLevelIndex(plan.ScaleInPlanID, 1)
	if err != nil {
		t.Fatalf("load scale-in level failed: %v", err)
	}
	applyScaleInOrderState(t, st, traderID, symbol, side, plan.ScaleInPlanID, level.LinkedOrderIntentID, exchangeOrderID, "FILLED", 0.2, 0.2)

	reconciler := NewScaleInStateReconciler(st, nil)
	if _, err := reconciler.SyncTraderScaleInState(traderID, symbol, fake, true); err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	eventsAfterFirst, err := st.ScaleInEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-in events failed: %v", err)
	}

	if _, err := reconciler.SyncTraderScaleInState(traderID, symbol, fake, true); err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	eventsAfterSecond, err := st.ScaleInEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-in events failed: %v", err)
	}
	if len(eventsAfterSecond) != len(eventsAfterFirst) {
		t.Fatalf("expected duplicate fill replay to be idempotent, got %d then %d scale-in events", len(eventsAfterFirst), len(eventsAfterSecond))
	}
}

func TestScaleInStateReconciler_DuplicateCancelReplayIsIdempotent(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-in-dup-cancel"
	symbol := "BTCUSDT"
	side := "LONG"

	seedScaleInStrategyProfile(t, st, traderID, true, 2, 90)
	fake := &phase4ScaleOutTrader{}
	plan, exchangeOrderID := placeScaleInMarketPlan(t, st, fake, traderID, symbol, side, 0.2)
	level, err := st.ScaleInPlanLevel().GetByPlanIDAndLevelIndex(plan.ScaleInPlanID, 1)
	if err != nil {
		t.Fatalf("load scale-in level failed: %v", err)
	}
	applyScaleInOrderState(t, st, traderID, symbol, side, plan.ScaleInPlanID, level.LinkedOrderIntentID, exchangeOrderID, "CANCELED", 0.2, 0)

	reconciler := NewScaleInStateReconciler(st, nil)
	if _, err := reconciler.SyncTraderScaleInState(traderID, symbol, fake, true); err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	eventsAfterFirst, err := st.ScaleInEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-in events failed: %v", err)
	}

	if _, err := reconciler.SyncTraderScaleInState(traderID, symbol, fake, true); err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	eventsAfterSecond, err := st.ScaleInEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-in events failed: %v", err)
	}
	if len(eventsAfterSecond) != len(eventsAfterFirst) {
		t.Fatalf("expected duplicate cancel replay to be idempotent, got %d then %d scale-in events", len(eventsAfterFirst), len(eventsAfterSecond))
	}
}

func TestScaleInStateReconciler_RestartRecoveryDoesNotDuplicatePlan(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-in-restart"
	symbol := "BTCUSDT"
	side := "LONG"

	seedScaleInStrategyProfile(t, st, traderID, true, 2, 90)
	fake := &phase4ScaleOutTrader{}
	manager := NewScaleInManager(st, fake, NewScaleInRiskGuard())
	firstPlan, err := manager.EnsureScaleInPlanForAggregate(seedScaleInAggregate(traderID, symbol, side, 1, 65000), nil, &ScaleInPlanConfig{
		FixedQty:     0.2,
		TargetType:   "market",
		Leverage:     5,
		LevelRatios:  []float64{1},
		TargetPrices: []float64{65000},
	}, 100000)
	if err != nil {
		t.Fatalf("initial scale-in plan failed: %v", err)
	}

	restartedManager := NewScaleInManager(st, fake, NewScaleInRiskGuard())
	reusedPlan, err := restartedManager.EnsureScaleInPlanForAggregate(seedScaleInAggregate(traderID, symbol, side, 1, 65000), nil, &ScaleInPlanConfig{
		FixedQty:     0.2,
		TargetType:   "market",
		Leverage:     5,
		LevelRatios:  []float64{1},
		TargetPrices: []float64{65000},
	}, 100000)
	if err != nil {
		t.Fatalf("restart recovery scale-in plan failed: %v", err)
	}
	if reusedPlan == nil || reusedPlan.ScaleInPlanID != firstPlan.ScaleInPlanID {
		t.Fatalf("expected restart recovery to reuse existing plan, got %#v", reusedPlan)
	}

	plans, err := st.ScaleInPlan().ListByTraderID(traderID)
	if err != nil {
		t.Fatalf("list scale-in plans failed: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected restart recovery to keep one scale-in plan row, got %d", len(plans))
	}
}

func TestScaleInStateReconciler_UserStreamDelayReplayDoesNotJitter(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-in-delay"
	symbol := "BTCUSDT"
	side := "LONG"

	seedScaleInStrategyProfile(t, st, traderID, true, 2, 90)
	fake := &phase4ScaleOutTrader{}
	plan, exchangeOrderID := placeScaleInMarketPlan(t, st, fake, traderID, symbol, side, 0.2)
	level, err := st.ScaleInPlanLevel().GetByPlanIDAndLevelIndex(plan.ScaleInPlanID, 1)
	if err != nil {
		t.Fatalf("load scale-in level failed: %v", err)
	}
	applyScaleInOrderState(t, st, traderID, symbol, side, plan.ScaleInPlanID, level.LinkedOrderIntentID, exchangeOrderID, "PARTIALLY_FILLED", 0.2, 0.1)

	reconciler := NewScaleInStateReconciler(st, nil)
	previewBefore, err := reconciler.SyncTraderScaleInState(traderID, symbol, fake, false)
	if err != nil {
		t.Fatalf("first delayed sync failed: %v", err)
	}
	eventsAfterFirst, err := st.ScaleInEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-in events failed: %v", err)
	}

	previewAfter, err := reconciler.SyncTraderScaleInState(traderID, symbol, fake, true)
	if err != nil {
		t.Fatalf("second replay sync failed: %v", err)
	}
	eventsAfterSecond, err := st.ScaleInEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
	if err != nil {
		t.Fatalf("list scale-in events failed: %v", err)
	}
	if len(eventsAfterSecond) != len(eventsAfterFirst) {
		t.Fatalf("expected delayed replay to remain stable, got %d then %d scale-in events", len(eventsAfterFirst), len(eventsAfterSecond))
	}
	if previewBefore.ScaleInConsistency != previewAfter.ScaleInConsistency {
		t.Fatalf("expected replay consistency to remain stable, got %s then %s", previewBefore.ScaleInConsistency, previewAfter.ScaleInConsistency)
	}
}

func TestProtectionRebalanceManager_IncreasesProtectedQuantityAfterScaleIn(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-in-protection"
	symbol := "BTCUSDT"
	side := "LONG"

	seedScaleInStrategyProfile(t, st, traderID, true, 2, 90)
	seedFixedProtectionDecision(t, st, traderID, symbol, 64000, 68000)
	seedScaleInOpenPosition(t, st, traderID, symbol, side, 1, 65000)

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

	if err := store.NewPositionBuilder(st.Position()).ProcessTrade(traderID, "exchange-1", "binance", symbol, side, "add_position", 0.2, 70000, 0, 0, time.Now().UTC().UnixMilli(), "scale-in-fill"); err != nil {
		t.Fatalf("process add-position trade failed: %v", err)
	}
	aggregate, err := store.NewPositionAggregateBuilder(st).BuildForTrader(traderID, nil, nil)
	if err != nil {
		t.Fatalf("build aggregate failed: %v", err)
	}
	if len(aggregate) != 1 {
		t.Fatalf("expected single aggregate after scale-in, got %d", len(aggregate))
	}
	if diff := math.Abs(aggregate[0].AvgEntryPrice - 65833.33333333333); diff > 0.01 {
		t.Fatalf("expected average entry price recalculation, got %.12f", aggregate[0].AvgEntryPrice)
	}

	rebalanceManager := NewProtectionRebalanceManager(st, fixedManager)
	rebalancedGroup, err := rebalanceManager.RebalanceForAggregate(aggregate[0])
	if err != nil {
		t.Fatalf("rebalance protection failed: %v", err)
	}
	if rebalancedGroup == nil {
		t.Fatalf("expected rebalanced protection group")
	}
	if diff := math.Abs(rebalancedGroup.ProtectedQuantity - 1.2); diff > 1e-9 {
		t.Fatalf("expected protected quantity 1.2, got %.12f", rebalancedGroup.ProtectedQuantity)
	}
	if len(fake.cancelStopLossCalls) == 0 || len(fake.cancelTakeProfitCalls) == 0 {
		t.Fatalf("expected protection rebalance to request sibling cancel and recreate")
	}

	summary, err := st.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		t.Fatalf("summarize protection group failed: %v", err)
	}
	if diff := math.Abs(summary.ProtectedQuantity - 1.2); diff > 1e-9 {
		t.Fatalf("expected summary protected quantity 1.2, got %.12f", summary.ProtectedQuantity)
	}
	if summary.ProtectionGroupStatus != "armed" {
		t.Fatalf("expected armed protection group after rebalance, got %s", summary.ProtectionGroupStatus)
	}
}

func TestScaleInRiskGuard_BlocksWhenRiskBudgetExceeded(t *testing.T) {
	guard := NewScaleInRiskGuard()
	assessment, err := guard.Assess(&ScaleInRiskRequest{
		StrategyProfile: &store.StrategyProfile{
			TraderID:           "trader-1",
			Exchange:           "binance_usdm",
			Mode:               "one_way",
			AllowAddPosition:   true,
			MaxScaleInCount:    2,
			MaxPositionRiskPct: 10,
			ExecutionEnabled:   true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-1",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvgEntryPrice:     900,
			ExecutionEligible: true,
		},
		ExecutionMode:       "live",
		AllowOrderPlacement: true,
		UserStreamReady:     true,
		AccountEquity:       1000,
		ProposedAddQty:      0.5,
		ProposedAddPrice:    500,
	})
	if err != nil {
		t.Fatalf("assess risk failed: %v", err)
	}
	if assessment.Allowed {
		t.Fatalf("expected risk guard to block add-position when risk budget is exceeded")
	}
	if !hasCapabilityReasonCategory(assessment.BlockedReasons, "risk") {
		t.Fatalf("expected risk block reason, got %#v", assessment.BlockedReasons)
	}
}

func TestRuntimeCapabilityResolver_EnablesAddPositionOnlyWhenEligible(t *testing.T) {
	resolver := NewRuntimeCapabilityResolver()

	t.Run("eligible", func(t *testing.T) {
		preview, err := resolver.Resolve(&RuntimeCapabilityRequest{
			TraderID:            "trader-eligible",
			ExecutionMode:       "live",
			AllowOrderPlacement: true,
			UserStreamReady:     true,
			StrategyProfile: &store.StrategyProfile{
				TraderID:               "trader-eligible",
				Exchange:               "binance_usdm",
				Mode:                   "one_way",
				AllowAddPosition:       true,
				AllowPartialTakeProfit: false,
				ExecutionEnabled:       true,
				MaxScaleInCount:        2,
				MaxPositionRiskPct:     90,
			},
			PositionAggregate: &store.PositionAggregate{
				TraderID:          "trader-eligible",
				Symbol:            "BTCUSDT",
				Side:              "LONG",
				TotalQty:          1,
				AvailableQty:      1,
				ExecutionEligible: true,
			},
			ProtectionGroupStatus:   "closed",
			ProtectionConsistency:   "unknown",
			ScaleOutConsistency:     "consistent",
			ScaleInConsistency:      "consistent",
			ScaleInCount:            0,
			RiskBudgetRemaining:     100,
			HasWorkingOrders:        false,
			HasPendingCancelReplace: false,
			HasStateMismatch:        false,
			HasProtection:           false,
			HasProtectionOrders:     false,
			HasScaleOutPlan:         false,
			HasScaleInPlan:          false,
			PendingAddQty:           0,
			PendingReduceQty:        0,
			PendingScaleInQty:       0,
			RemainingScaleInQty:     0,
			ExecutedScaleInQty:      0,
			ScaleInBlockReasons:     nil,
		})
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}
		if !hasAction(preview.AllowedActions, "add_position") {
			t.Fatalf("expected add_position to be allowed when eligible")
		}
		if hasAction(preview.BlockedActions, "add_position") {
			t.Fatalf("did not expect add_position to be blocked when eligible")
		}
	})

	t.Run("blocked by scale-in guard", func(t *testing.T) {
		preview, err := resolver.Resolve(&RuntimeCapabilityRequest{
			TraderID:            "trader-blocked",
			ExecutionMode:       "live",
			AllowOrderPlacement: true,
			UserStreamReady:     true,
			StrategyProfile: &store.StrategyProfile{
				TraderID:               "trader-blocked",
				Exchange:               "binance_usdm",
				Mode:                   "one_way",
				AllowAddPosition:       true,
				AllowPartialTakeProfit: false,
				ExecutionEnabled:       true,
				MaxScaleInCount:        2,
				MaxPositionRiskPct:     90,
			},
			PositionAggregate: &store.PositionAggregate{
				TraderID:          "trader-blocked",
				Symbol:            "BTCUSDT",
				Side:              "LONG",
				TotalQty:          1,
				AvailableQty:      1,
				ExecutionEligible: true,
			},
			ProtectionGroupStatus: "closed",
			ProtectionConsistency: "unknown",
			ScaleOutConsistency:   "consistent",
			ScaleInConsistency:    "consistent",
			ScaleInBlockReasons: []CapabilityReason{
				{
					Action:   "add_position",
					Category: "risk",
					Reason:   "proposed add position exceeds the configured risk budget",
				},
			},
		})
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}
		if hasAction(preview.AllowedActions, "add_position") {
			t.Fatalf("did not expect add_position to be allowed when blocked by scale-in guard")
		}
		if !hasAction(preview.BlockedActions, "add_position") {
			t.Fatalf("expected add_position to be blocked when scale-in guard reasons exist")
		}
		if !hasBlockedReason(preview.BlockReasons, "add_position", "risk") {
			t.Fatalf("expected add_position risk block reason, got %#v", preview.BlockReasons)
		}
	})
}

func TestScaleInPreview_ReturnsConsistentPlanState(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-scale-in-preview"
	symbol := "BTCUSDT"
	side := "LONG"

	seedScaleInStrategyProfile(t, st, traderID, true, 2, 90)
	seedScaleInOpenPosition(t, st, traderID, symbol, side, 1, 65000)
	fake := &phase4ScaleOutTrader{}
	plan, exchangeOrderID := placeScaleInMarketPlan(t, st, fake, traderID, symbol, side, 0.2)
	level, err := st.ScaleInPlanLevel().GetByPlanIDAndLevelIndex(plan.ScaleInPlanID, 1)
	if err != nil {
		t.Fatalf("load scale-in level failed: %v", err)
	}
	applyScaleInOrderState(t, st, traderID, symbol, side, plan.ScaleInPlanID, level.LinkedOrderIntentID, exchangeOrderID, "FILLED", 0.2, 0.2)

	reconciler := NewScaleInStateReconciler(st, nil)
	preview, err := reconciler.SyncTraderScaleInState(traderID, symbol, fake, true)
	if err != nil {
		t.Fatalf("sync scale-in state failed: %v", err)
	}
	if preview == nil {
		t.Fatalf("expected scale-in preview")
	}
	if preview.ScaleInConsistency != "consistent" {
		t.Fatalf("expected consistent scale-in preview, got %s", preview.ScaleInConsistency)
	}
	if preview.HasStateMismatch {
		t.Fatalf("did not expect state mismatch in consistent preview")
	}
	if preview.ScaleInPlan == nil {
		t.Fatalf("expected scale-in plan snapshot")
	}
	if preview.RiskAssessment == nil {
		t.Fatalf("expected risk assessment in preview")
	}
}

func hasAction(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasCapabilityReasonCategory(reasons []CapabilityReason, target string) bool {
	for _, reason := range reasons {
		if reason.Category == target {
			return true
		}
	}
	return false
}

func hasBlockedReason(reasons []CapabilityReason, action, category string) bool {
	for _, reason := range reasons {
		if reason.Action == action && reason.Category == category {
			return true
		}
	}
	return false
}
