package trader

import (
	"strings"
	"testing"
	"time"

	"nofx/store"
)

func TestProtectionAdjustmentManager_ArmsBreakEvenWhenEligible(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-phase6-break-even"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-phase6-break-even"

	seedPhase6StrategyAndPosition(t, st, traderID, symbol, side, true, true, true, true, 1, 65000)
	stopIntent, takeIntent, stopExchange, takeExchange := seedPhase6DynamicProtectionState(t, st, traderID, symbol, side, groupID, "fixed", "armed", 64000, 64000, 68000, 68000, 1, false, false, "", 0, 1)
	seedPhase6ProtectionOrders(t, st, traderID, symbol, side, groupID, stopIntent, takeIntent, stopExchange, takeExchange, 1)
	seedPhase6TrailingRule(t, st, traderID, symbol, side, "rule-phase6-break-even", "break_even", "profit_pct", 1, 1, 100, 3)

	preview, err := BuildRuntimeProtectionAdjustmentPreview(st, traderID, symbol, true, 66000)
	if err != nil {
		t.Fatalf("build preview failed: %v", err)
	}
	if preview.GuardAssessment == nil || !preview.GuardAssessment.CanArmBreakEven {
		t.Fatalf("expected break-even to be eligible, got %#v", preview.GuardAssessment)
	}

	fakeTrader := &fakeProtectionTrader{}
	manager := NewProtectionAdjustmentManager(st, fakeTrader)
	updatedGroup, err := manager.ApplyProtectionAdjustmentPreview(preview)
	if err != nil {
		t.Fatalf("apply protection adjustment failed: %v", err)
	}
	if updatedGroup == nil {
		t.Fatalf("expected updated protection group")
	}
	if !updatedGroup.BreakEvenArmed {
		t.Fatalf("expected break-even to be armed after apply")
	}
	if updatedGroup.StopLossCurrentTriggerPrice != 65000 {
		t.Fatalf("expected current stop-loss 65000, got %.6f", updatedGroup.StopLossCurrentTriggerPrice)
	}
	if updatedGroup.ProtectionRevision != 2 {
		t.Fatalf("expected protection revision 2, got %d", updatedGroup.ProtectionRevision)
	}
	if len(fakeTrader.cancelStopLossCalls) != 1 {
		t.Fatalf("expected 1 cancel-stop-loss call, got %d", len(fakeTrader.cancelStopLossCalls))
	}
	if len(fakeTrader.createStopLossCalls) != 1 {
		t.Fatalf("expected 1 create-stop-loss call, got %d", len(fakeTrader.createStopLossCalls))
	}
	if len(fakeTrader.createStopLossCalls) > 0 && fakeTrader.createStopLossCalls[0].Price != 65000 {
		t.Fatalf("expected stop-loss to move to break-even 65000, got %.6f", fakeTrader.createStopLossCalls[0].Price)
	}

	orders, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list order registry rows failed: %v", err)
	}
	if len(orders) != 3 {
		t.Fatalf("expected 3 registry rows after break-even move, got %d", len(orders))
	}

	events, err := st.ProtectionAdjustmentEventLog().ListRecentByTrader(traderID, 20)
	if err != nil {
		t.Fatalf("list protection adjustment events failed: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 protection adjustment events after break-even move, got %d", len(events))
	}
}

func TestProtectionAdjustmentManager_TriggersStepTrailingWhenEligible(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-phase6-trailing"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-phase6-trailing"

	seedPhase6StrategyAndPosition(t, st, traderID, symbol, side, true, true, true, true, 1, 65000)
	stopIntent, takeIntent, stopExchange, takeExchange := seedPhase6DynamicProtectionState(t, st, traderID, symbol, side, groupID, "break_even", "armed", 64000, 65000, 68000, 68000, 1, true, false, "rule-phase6-trailing", 1, 2)
	seedPhase6ProtectionOrders(t, st, traderID, symbol, side, groupID, stopIntent, takeIntent, stopExchange, takeExchange, 1)
	seedPhase6TrailingRule(t, st, traderID, symbol, side, "rule-phase6-trailing", "step_trailing", "profit_pct", 1, 2, 500, 3)

	preview, err := BuildRuntimeProtectionAdjustmentPreview(st, traderID, symbol, true, 67000)
	if err != nil {
		t.Fatalf("build preview failed: %v", err)
	}
	if preview.GuardAssessment == nil || !preview.GuardAssessment.CanArmTrailing {
		t.Fatalf("expected trailing to be eligible, got %#v", preview.GuardAssessment)
	}
	if preview.GuardAssessment.CanArmBreakEven {
		t.Fatalf("did not expect break-even to be selected when trailing is already armed in the snapshot")
	}

	fakeTrader := &fakeProtectionTrader{}
	manager := NewProtectionAdjustmentManager(st, fakeTrader)
	updatedGroup, err := manager.ApplyProtectionAdjustmentPreview(preview)
	if err != nil {
		t.Fatalf("apply protection adjustment failed: %v", err)
	}
	if updatedGroup == nil {
		t.Fatalf("expected updated protection group")
	}
	if !updatedGroup.TrailingArmed {
		t.Fatalf("expected trailing to be armed after apply")
	}
	if updatedGroup.StopLossCurrentTriggerPrice != 65500 {
		t.Fatalf("expected current stop-loss 65500, got %.6f", updatedGroup.StopLossCurrentTriggerPrice)
	}
	if updatedGroup.ProtectionRevision != 3 {
		t.Fatalf("expected protection revision 3, got %d", updatedGroup.ProtectionRevision)
	}
	if updatedGroup.TrailingMoveCount != 2 {
		t.Fatalf("expected trailing move count 2, got %d", updatedGroup.TrailingMoveCount)
	}
	if len(fakeTrader.cancelStopLossCalls) != 1 {
		t.Fatalf("expected 1 cancel-stop-loss call, got %d", len(fakeTrader.cancelStopLossCalls))
	}
	if len(fakeTrader.createStopLossCalls) != 1 {
		t.Fatalf("expected 1 create-stop-loss call, got %d", len(fakeTrader.createStopLossCalls))
	}
	if len(fakeTrader.createStopLossCalls) > 0 && fakeTrader.createStopLossCalls[0].Price != 65500 {
		t.Fatalf("expected stop-loss to trail to 65500, got %.6f", fakeTrader.createStopLossCalls[0].Price)
	}

	orders, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list order registry rows failed: %v", err)
	}
	if len(orders) != 3 {
		t.Fatalf("expected 3 registry rows after trailing move, got %d", len(orders))
	}
}

func TestProtectionCancelReplaceReconciler_ReplacesStopLossIdempotently(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-phase6-replace"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-phase6-replace"

	seedPhase6StrategyAndPosition(t, st, traderID, symbol, side, true, true, true, true, 1, 65000)
	stopIntent, takeIntent, stopExchange, takeExchange := seedPhase6DynamicProtectionState(t, st, traderID, symbol, side, groupID, "fixed", "armed", 64000, 64000, 68000, 68000, 1, false, false, "", 0, 4)
	seedPhase6ProtectionOrders(t, st, traderID, symbol, side, groupID, stopIntent, takeIntent, stopExchange, takeExchange, 1)

	reconciler := NewProtectionCancelReplaceReconciler(st, &fakeProtectionTrader{})
	plan := &ProtectionAdjustmentPlan{
		TraderID:               traderID,
		Symbol:                 symbol,
		Side:                   side,
		LinkedPositionKey:      symbol + "|" + side,
		ProtectionGroupID:      groupID,
		Action:                 string(ProtectionActionSetBreakEvenStop),
		Reason:                 "unit-test",
		CurrentMarketPrice:     66000,
		CurrentPnLPct:          1.5,
		CurrentStopLossPrice:   64000,
		InitialStopLossPrice:   64000,
		NewStopLossPrice:       65000,
		CurrentTakeProfitPrice: 68000,
		InitialTakeProfitPrice: 68000,
		ProtectionMode:         "fixed",
		ProtectedQuantity:      1,
		TrailingRuleID:         "rule-phase6-replace",
		TrailingAnchorPrice:    66000,
		TrailingMoveCount:      0,
		ProtectionRevision:     4,
		LastProtectionAction:   "seed",
		LastProtectionActionAt: time.Now().UTC(),
		EventTime:              time.Now().UTC(),
	}

	beforeEvents, err := st.ProtectionAdjustmentEventLog().ListRecentByTrader(traderID, 20)
	if err != nil {
		t.Fatalf("list protection adjustment events failed: %v", err)
	}

	firstGroup, err := reconciler.Reconcile(plan)
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if firstGroup == nil {
		t.Fatalf("expected reconciled protection group")
	}
	if firstGroup.StopLossCurrentTriggerPrice != 65000 {
		t.Fatalf("expected stop-loss to move to 65000, got %.6f", firstGroup.StopLossCurrentTriggerPrice)
	}
	if firstGroup.ProtectionRevision != 5 {
		t.Fatalf("expected protection revision 5 after first reconcile, got %d", firstGroup.ProtectionRevision)
	}

	firstOrders, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list order registry rows failed: %v", err)
	}
	if len(firstOrders) != 3 {
		t.Fatalf("expected 3 registry rows after first reconcile, got %d", len(firstOrders))
	}
	if len(plan.Action) == 0 {
		t.Fatalf("plan action should not be empty")
	}

	firstEvents, err := st.ProtectionAdjustmentEventLog().ListRecentByTrader(traderID, 20)
	if err != nil {
		t.Fatalf("list protection adjustment events failed: %v", err)
	}
	if len(firstEvents) != len(beforeEvents)+2 {
		t.Fatalf("expected two new protection adjustment events, got before=%d after=%d", len(beforeEvents), len(firstEvents))
	}

	reconciledAgain, err := reconciler.Reconcile(plan)
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}
	if reconciledAgain == nil {
		t.Fatalf("expected reconciled protection group on second call")
	}
	if reconciledAgain.ID != firstGroup.ID {
		t.Fatalf("expected idempotent reconcile to keep the same protection row")
	}

	secondOrders, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list order registry rows failed: %v", err)
	}
	if len(secondOrders) != len(firstOrders) {
		t.Fatalf("expected no extra order rows on idempotent reconcile, got before=%d after=%d", len(firstOrders), len(secondOrders))
	}

	secondEvents, err := st.ProtectionAdjustmentEventLog().ListRecentByTrader(traderID, 20)
	if err != nil {
		t.Fatalf("list protection adjustment events failed: %v", err)
	}
	if len(secondEvents) != len(firstEvents) {
		t.Fatalf("expected no extra protection adjustment events on idempotent reconcile, got before=%d after=%d", len(firstEvents), len(secondEvents))
	}
}

func TestProtectionAdjustmentGuard_BlocksWhenCancelReplaceInProgress(t *testing.T) {
	guard := NewProtectionAdjustmentGuard()
	assessment, err := guard.Assess(&ProtectionAdjustmentGuardRequest{
		StrategyProfile: &store.StrategyProfile{
			TraderID:         "trader-phase6-guard",
			Exchange:         "binance_usdm",
			Mode:             "one_way",
			ExecutionEnabled: true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-phase6-guard",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvailableQty:      1,
			AvgEntryPrice:     65000,
			ExecutionEligible: true,
		},
		ProtectionSummary: &store.ProtectionGroupSummary{
			TraderID:                "trader-phase6-guard",
			Symbol:                  "BTCUSDT",
			Side:                    "LONG",
			HasProtection:           true,
			ProtectionGroupStatus:   "armed",
			ConsistencyStatus:       "consistent",
			CurrentStopLossPrice:    64000,
			InitialStopLossPrice:    64000,
			BreakEvenArmed:          false,
			TrailingArmed:           false,
			ProtectionRevision:      1,
			HasPendingCancelReplace: true,
		},
		TrailingRule: &store.TrailingRule{
			TraderID:         "trader-phase6-guard",
			Symbol:           "BTCUSDT",
			Side:             "LONG",
			TrailingRuleID:   "rule-phase6-guard",
			RuleMode:         "break_even",
			ActivationType:   "profit_pct",
			ActivationValue:  1,
			StepTriggerType:  "profit_pct",
			StepTriggerValue: 1,
			StepMoveType:     "price_offset",
			StepMoveValue:    100,
			MaxMoveCount:     3,
			Status:           "armed",
		},
		ExecutionMode:           "live",
		AllowOrderPlacement:     true,
		UserStreamReady:         true,
		HasPendingCancelReplace: true,
		CurrentMarketPrice:      66000,
		CurrentPnLPct:           1.5,
	})
	if err != nil {
		t.Fatalf("guard assess failed: %v", err)
	}
	if assessment.Allowed {
		t.Fatalf("expected guard to block when cancel_replace is in progress")
	}
	if assessment.CanMoveStopLoss {
		t.Fatalf("expected move_stop_loss to be blocked while cancel_replace is in progress")
	}
	if !hasCapabilityReason(assessment.BlockedReasons, "move_stop_loss", "pending", "cancel_replace") {
		t.Fatalf("expected a pending cancel_replace reason, got %#v", assessment.BlockedReasons)
	}
}

func TestRuntimeCapabilityResolver_EnablesMoveStopLossOnlyWhenEligible(t *testing.T) {
	resolver := NewRuntimeCapabilityResolver()

	eligiblePreview, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:                        "trader-phase6-resolver",
		ExecutionMode:                   "live",
		AllowOrderPlacement:             true,
		UserStreamReady:                 true,
		HasPendingOrders:                false,
		HasWorkingOrders:                true,
		HasProtection:                   true,
		HasProtectionOrders:             true,
		ProtectionGroupStatus:           "armed",
		StopLossArmed:                   true,
		TakeProfitArmed:                 true,
		ProtectionConsistency:           "consistent",
		HasProtectionMismatch:           false,
		ProtectionAdjustmentStatus:      "trailing_segmented",
		ProtectionAdjustmentConsistency: "consistent",
		HasPendingCancelReplace:         false,
		ProtectionRevision:              3,
		CurrentStopLossPrice:            65500,
		InitialStopLossPrice:            64000,
		BreakEvenArmed:                  true,
		TrailingArmed:                   true,
		RemainingMoveBudget:             2,
		CanArmBreakEven:                 false,
		CanArmTrailing:                  false,
		CanMoveStopLoss:                 true,
		StrategyProfile: &store.StrategyProfile{
			TraderID:         "trader-phase6-resolver",
			Exchange:         "binance_usdm",
			Mode:             "one_way",
			ExecutionEnabled: true,
			AllowAddPosition: true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-phase6-resolver",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvailableQty:      1,
			ExecutionEligible: true,
		},
	})
	if err != nil {
		t.Fatalf("resolver resolve failed: %v", err)
	}
	if !hasString(eligiblePreview.AllowedActions, "move_stop_loss") {
		t.Fatalf("expected move_stop_loss to be allowed when eligible, got %#v", eligiblePreview.AllowedActions)
	}

	blockedPreview, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:                        "trader-phase6-resolver",
		ExecutionMode:                   "live",
		AllowOrderPlacement:             true,
		UserStreamReady:                 true,
		HasPendingOrders:                false,
		HasWorkingOrders:                true,
		HasProtection:                   true,
		HasProtectionOrders:             true,
		ProtectionGroupStatus:           "armed",
		StopLossArmed:                   true,
		TakeProfitArmed:                 true,
		ProtectionConsistency:           "consistent",
		HasProtectionMismatch:           false,
		ProtectionAdjustmentStatus:      "trailing_segmented",
		ProtectionAdjustmentConsistency: "consistent",
		HasPendingCancelReplace:         true,
		ProtectionRevision:              3,
		CurrentStopLossPrice:            65500,
		InitialStopLossPrice:            64000,
		BreakEvenArmed:                  true,
		TrailingArmed:                   true,
		RemainingMoveBudget:             2,
		CanArmBreakEven:                 false,
		CanArmTrailing:                  false,
		CanMoveStopLoss:                 false,
		StrategyProfile: &store.StrategyProfile{
			TraderID:         "trader-phase6-resolver",
			Exchange:         "binance_usdm",
			Mode:             "one_way",
			ExecutionEnabled: true,
			AllowAddPosition: true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-phase6-resolver",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvailableQty:      1,
			ExecutionEligible: true,
		},
	})
	if err != nil {
		t.Fatalf("resolver resolve failed: %v", err)
	}
	if hasString(blockedPreview.AllowedActions, "move_stop_loss") {
		t.Fatalf("did not expect move_stop_loss to be allowed when cancel_replace is in progress")
	}
	if !hasCapabilityReason(blockedPreview.BlockReasons, "move_stop_loss", "order_state", "current cancel_replace") {
		t.Fatalf("expected move_stop_loss to be blocked by pending cancel_replace, got %#v", blockedPreview.BlockReasons)
	}
}

func TestProtectionAdjustmentPreview_ReturnsConsistentState(t *testing.T) {
	st := newPhase3TraderStore(t)
	traderID := "trader-phase6-preview"
	symbol := "BTCUSDT"
	side := "LONG"
	groupID := "pg-phase6-preview"

	seedPhase6StrategyAndPosition(t, st, traderID, symbol, side, true, true, true, true, 1, 65000)
	stopIntent, takeIntent, stopExchange, takeExchange := seedPhase6DynamicProtectionState(t, st, traderID, symbol, side, groupID, "fixed", "armed", 64000, 64000, 68000, 68000, 1, false, false, "", 0, 1)
	seedPhase6ProtectionOrders(t, st, traderID, symbol, side, groupID, stopIntent, takeIntent, stopExchange, takeExchange, 1)
	seedPhase6TrailingRule(t, st, traderID, symbol, side, "rule-phase6-preview", "break_even", "profit_pct", 1, 1, 100, 3)

	preview, err := BuildRuntimeProtectionAdjustmentPreview(st, traderID, symbol, true, 66000)
	if err != nil {
		t.Fatalf("build preview failed: %v", err)
	}
	if preview.GuardAssessment == nil {
		t.Fatalf("expected guard assessment in preview")
	}
	if preview.ProtectionSummary == nil {
		t.Fatalf("expected protection summary in preview")
	}
	if preview.HasStateMismatch {
		t.Fatalf("did not expect protection mismatch in preview")
	}
	if preview.HasPendingCancelReplace {
		t.Fatalf("did not expect pending cancel_replace in preview")
	}
	if preview.ProtectionAdjustmentConsistency != preview.GuardAssessment.ProtectionAdjustmentConsistency {
		t.Fatalf("expected preview and guard consistency to match, got preview=%s guard=%s", preview.ProtectionAdjustmentConsistency, preview.GuardAssessment.ProtectionAdjustmentConsistency)
	}
	if preview.CurrentStopLossPrice != preview.GuardAssessment.CurrentStopLossPrice {
		t.Fatalf("expected current stop-loss to match guard snapshot, got preview=%.6f guard=%.6f", preview.CurrentStopLossPrice, preview.GuardAssessment.CurrentStopLossPrice)
	}
	if preview.ProtectionRevision != preview.GuardAssessment.ProtectionRevision {
		t.Fatalf("expected protection revision to match guard snapshot, got preview=%d guard=%d", preview.ProtectionRevision, preview.GuardAssessment.ProtectionRevision)
	}
	if preview.CanArmBreakEven != preview.GuardAssessment.CanArmBreakEven {
		t.Fatalf("expected break-even capability to match guard snapshot")
	}
	if preview.CanMoveStopLoss != preview.GuardAssessment.CanMoveStopLoss {
		t.Fatalf("expected move-stop capability to match guard snapshot")
	}
}

func seedPhase6StrategyAndPosition(t *testing.T, st *store.Store, traderID, symbol, side string, allowAdd, allowPartialTP, allowMoveStopLoss, allowTrailing bool, quantity, entryPrice float64) {
	t.Helper()

	if err := st.StrategyProfile().Upsert(&store.StrategyProfile{
		TraderID:               traderID,
		Exchange:               "binance_usdm",
		Mode:                   "one_way",
		AllowAddPosition:       allowAdd,
		AllowPartialTakeProfit: allowPartialTP,
		AllowMoveStopLoss:      allowMoveStopLoss,
		AllowTrailingStop:      allowTrailing,
		ProtectionMode:         "fixed",
		MaxScaleInCount:        2,
		MaxPositionRiskPct:     90,
		DecisionStyle:          "ai_trading",
		ExecutionEnabled:       true,
	}); err != nil {
		t.Fatalf("seed strategy profile failed: %v", err)
	}

	now := time.Now().UTC()
	if err := st.Position().Create(&store.TraderPosition{
		TraderID:           traderID,
		ExchangeID:         "exchange-1",
		ExchangeType:       "binance",
		ExchangePositionID: traderID + "-position",
		Symbol:             symbol,
		Side:               side,
		Quantity:           quantity,
		EntryPrice:         entryPrice,
		EntryTime:          now.UnixMilli(),
		CreatedAt:          now.UnixMilli(),
		UpdatedAt:          now.UnixMilli(),
	}); err != nil {
		t.Fatalf("seed trader position failed: %v", err)
	}

	if _, err := store.NewPositionAggregateBuilder(st).BuildForTrader(traderID, nil, nil); err != nil {
		t.Fatalf("seed position aggregate failed: %v", err)
	}
}

func seedPhase6DynamicProtectionState(t *testing.T, st *store.Store, traderID, symbol, side, groupID, protectionMode, status string, stopInitial, stopCurrent, takeInitial, takeCurrent, protectedQty float64, breakEvenArmed, trailingArmed bool, trailingRuleID string, trailingMoveCount, protectionRevision int) (string, string, string, string) {
	t.Helper()

	now := time.Now().UTC()
	stopIntent := groupID + "-sl"
	takeIntent := groupID + "-tp"
	stopExchange := groupID + "-sl-ex"
	takeExchange := groupID + "-tp-ex"

	_, err := st.ProtectionAdjustmentBuilder().ApplyProtectionAdjustmentEvent(&store.ProtectionAdjustmentEventInput{
		TraderID:                      traderID,
		Symbol:                        symbol,
		Side:                          side,
		LinkedPositionKey:             symbol + "|" + side,
		ProtectionGroupID:             groupID,
		ProtectionMode:                protectionMode,
		StopLossOrderIntentID:         stopIntent,
		TakeProfitOrderIntentID:       takeIntent,
		StopLossExchangeOrderID:       stopExchange,
		TakeProfitExchangeOrderID:     takeExchange,
		StopLossInitialTriggerPrice:   stopInitial,
		StopLossCurrentTriggerPrice:   stopCurrent,
		TakeProfitInitialTriggerPrice: takeInitial,
		TakeProfitCurrentTriggerPrice: takeCurrent,
		BreakEvenArmed:                breakEvenArmed,
		TrailingArmed:                 trailingArmed,
		TrailingRuleID:                trailingRuleID,
		TrailingAnchorPrice:           stopCurrent,
		TrailingLastMoveAt:            now,
		TrailingMoveCount:             trailingMoveCount,
		ProtectionRevision:            protectionRevision,
		LastProtectionAction:          "seed",
		LastProtectionActionAt:        now,
		ProtectedQuantity:             protectedQty,
		Status:                        status,
		Source:                        "unit_test",
		EventType:                     "PROTECTION_GROUP_CREATED",
		EventSource:                   "unit_test",
		PayloadJSON:                   `{"phase":"phase6"}`,
		EventTime:                     now,
	})
	if err != nil {
		t.Fatalf("seed dynamic protection state failed: %v", err)
	}

	return stopIntent, takeIntent, stopExchange, takeExchange
}

func seedPhase6ProtectionOrders(t *testing.T, st *store.Store, traderID, symbol, side, groupID, stopIntent, takeIntent, stopExchange, takeExchange string, quantity float64) {
	t.Helper()

	now := time.Now().UTC()
	for _, order := range []struct {
		role        string
		localIntent string
		exchangeID  string
		clientID    string
		orderType   string
		payload     string
	}{
		{role: "stop_loss", localIntent: stopIntent, exchangeID: stopExchange, clientID: stopIntent + "-client", orderType: "STOP_MARKET", payload: `{"leg":"stop_loss"}`},
		{role: "take_profit", localIntent: takeIntent, exchangeID: takeExchange, clientID: takeIntent + "-client", orderType: "TAKE_PROFIT_MARKET", payload: `{"leg":"take_profit"}`},
	} {
		if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
			TraderID:          traderID,
			Symbol:            symbol,
			Side:              protectionExitSide(side),
			PositionSideMode:  "one_way",
			OrderRole:         order.role,
			LocalIntentID:     order.localIntent,
			LinkedGroupID:     groupID,
			LinkedPositionKey: symbol + "|" + side,
			ExchangeOrderID:   order.exchangeID,
			ClientOrderID:     order.clientID,
			OrderType:         order.orderType,
			TimeInForce:       "GTC",
			ReduceOnly:        true,
			ClosePosition:     true,
			OrigQty:           quantity,
			ExecutedQty:       0,
			Status:            "NEW",
			Source:            "unit_test",
			EventType:         "ORDER_NEW",
			EventSource:       "unit_test",
			PayloadJSON:       order.payload,
			EventTime:         now,
		}); err != nil {
			t.Fatalf("seed protection order failed: %v", err)
		}
	}
}

func seedPhase6TrailingRule(t *testing.T, st *store.Store, traderID, symbol, side, ruleID, ruleMode, activationType string, activationValue, stepTriggerValue, stepMoveValue float64, maxMoveCount int) {
	t.Helper()

	if err := st.TrailingRule().Upsert(&store.TrailingRule{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		LinkedPositionKey: symbol + "|" + side,
		TrailingRuleID:    ruleID,
		RuleMode:          ruleMode,
		ActivationType:    activationType,
		ActivationValue:   activationValue,
		StepTriggerType:   activationType,
		StepTriggerValue:  stepTriggerValue,
		StepMoveType:      "price_offset",
		StepMoveValue:     stepMoveValue,
		MaxMoveCount:      maxMoveCount,
		Status:            "armed",
		Source:            "unit_test",
	}); err != nil {
		t.Fatalf("seed trailing rule failed: %v", err)
	}
}

func protectionExitSide(side string) string {
	if strings.EqualFold(strings.TrimSpace(side), "SHORT") {
		return "BUY"
	}
	return "SELL"
}

func hasString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasCapabilityReason(reasons []CapabilityReason, action, category, reasonFragment string) bool {
	for _, reason := range reasons {
		if reason.Action == action && reason.Category == category && strings.Contains(reason.Reason, reasonFragment) {
			return true
		}
	}
	return false
}
