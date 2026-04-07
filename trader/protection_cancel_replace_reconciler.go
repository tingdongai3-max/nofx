package trader

import (
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

// protectionAdjustmentExchangeClient is the execution surface required for protection cancel/replace flows.
// It is an execution-side dependency, not a truth-layer model.
type protectionAdjustmentExchangeClient interface {
	CancelStopLossOrders(symbol string) error
	CreateStopLossOrder(symbol, positionSide string, quantity, stopPrice float64, clientAlgoID string) (string, error)
}

// ProtectionAdjustmentAction labels the dynamic protection action selected by the guard.
// It is an execution-facing action label, not a persisted truth row.
type ProtectionAdjustmentAction string

const (
	ProtectionActionMoveStopLoss       ProtectionAdjustmentAction = "move_stop_loss"
	ProtectionActionSetBreakEvenStop   ProtectionAdjustmentAction = "set_break_even_stop"
	ProtectionActionSetTrailingProtection ProtectionAdjustmentAction = "set_trailing_protection"
)

// ProtectionAdjustmentPlan is the execution-side plan for moving or arming a dynamic protection stop.
// It is a service-layer input, not a persisted truth row.
type ProtectionAdjustmentPlan struct {
	TraderID                string    // System-owned trader key for this adjustment.
	Symbol                  string    // Truth-layer symbol scope.
	Side                    string    // One-way side: LONG or SHORT.
	LinkedPositionKey       string    // System-owned position correlation key.
	ProtectionGroupID       string    // System-generated protection group identifier.
	Action                  string    // Selected dynamic protection action.
	Reason                  string    // Human-readable reason for the adjustment.
	CurrentMarketPrice      float64   // Best-effort market price used for trigger calculation.
	CurrentPnLPct           float64   // Best-effort unrealized PnL percent used for trigger calculation.
	CurrentStopLossPrice    float64   // Current stop-loss trigger price before the change.
	InitialStopLossPrice    float64   // Initial stop-loss trigger price before any protection movement.
	NewStopLossPrice        float64   // Requested stop-loss trigger price after the change.
	CurrentTakeProfitPrice  float64   // Current take-profit trigger price before the change.
	InitialTakeProfitPrice  float64   // Initial take-profit trigger price before the change.
	ProtectionMode          string    // Requested protection mode after the change.
	ProtectedQuantity       float64   // Quantity covered by the protection group.
	TrailingRuleID          string    // System-owned trailing rule identifier.
	TrailingAnchorPrice     float64   // Trailing anchor price for the latest move.
	TrailingMoveCount       int       // Completed trailing move count before the change.
	ProtectionRevision      int       // Current protection revision before the change.
	LastProtectionAction    string    // Last protection mutation label before the change.
	LastProtectionActionAt  time.Time // Last protection mutation timestamp before the change.
	EventTime               time.Time // Event timestamp for the start/completion sequence.
}

// ProtectionCancelReplaceReconciler coordinates the cancel/replace lifecycle for moving protection stops.
// It is the single coordination point for the protection start/completion sequence and registry updates.
type ProtectionCancelReplaceReconciler struct {
	store        *store.Store
	exchange     protectionAdjustmentExchangeClient
	builder      *store.ProtectionAdjustmentBuilder
	orderBuilder *store.OrderRegistryBuilder
}

// NewProtectionCancelReplaceReconciler creates a new protection cancel/replace reconciler.
func NewProtectionCancelReplaceReconciler(st *store.Store, client Trader) *ProtectionCancelReplaceReconciler {
	var exchange protectionAdjustmentExchangeClient
	if typed, ok := client.(protectionAdjustmentExchangeClient); ok {
		exchange = typed
	}
	if st == nil {
		return &ProtectionCancelReplaceReconciler{exchange: exchange}
	}
	return &ProtectionCancelReplaceReconciler{
		store:        st,
		exchange:     exchange,
		builder:      st.ProtectionAdjustmentBuilder(),
		orderBuilder: st.OrderRegistryBuilder(),
	}
}

// Reconcile applies a single dynamic protection move using a cancel/replace lifecycle.
// It is idempotent: repeated requests for the same target stop and revision return the current truth row.
func (r *ProtectionCancelReplaceReconciler) Reconcile(plan *ProtectionAdjustmentPlan) (*store.ProtectionGroup, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("protection cancel/replace reconciler store is not configured")
	}
	if plan == nil {
		return nil, fmt.Errorf("protection adjustment plan is required")
	}
	if strings.TrimSpace(plan.TraderID) == "" {
		return nil, fmt.Errorf("trader ID is required")
	}
	if strings.TrimSpace(plan.Symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	symbol := normalizeAggregateSymbol(plan.Symbol)
	side := normalizeAggregateSide(plan.Side)
	summary, err := r.store.ProtectionGroup().SummarizeForTraderSymbolSide(plan.TraderID, symbol, side)
	if err != nil {
		return nil, err
	}
	if summary == nil || !summary.HasProtection || strings.TrimSpace(summary.ProtectionGroupID) == "" {
		return nil, fmt.Errorf("no active protection group exists on the selected symbol")
	}

	currentGroup, err := r.store.ProtectionGroup().GetByKeys(plan.TraderID, summary.ProtectionGroupID, summary.LinkedPositionKey)
	if err != nil {
		return nil, err
	}
	currentStop := summary.CurrentStopLossPrice
	if currentStop <= 0 {
		currentStop = summary.InitialStopLossPrice
	}

	if summary.ProtectionGroupStatus == "cancel_pending" && almostEqual(currentStop, plan.CurrentStopLossPrice) {
		if currentGroup != nil && almostEqual(summary.CurrentStopLossPrice, plan.CurrentStopLossPrice) {
			return currentGroup, nil
		}
	}
	if summary.ProtectionGroupStatus == "armed" && almostEqual(summary.CurrentStopLossPrice, plan.NewStopLossPrice) && summary.ProtectionRevision >= plan.ProtectionRevision+1 {
		if currentGroup != nil {
			return currentGroup, nil
		}
	}

	startEventType := protectionAdjustmentStartEventType(plan.Action)
	completeEventType := protectionAdjustmentCompleteEventType(plan.Action, summary)
	now := plan.EventTime
	if now.IsZero() {
		now = time.Now().UTC()
	}

	logger.Infof("protection cancel_replace started: trader=%s symbol=%s side=%s group=%s action=%s current_stop_loss_price=%.6f new_stop_loss_price=%.6f revision=%d",
		plan.TraderID, symbol, side, summary.ProtectionGroupID, plan.Action, currentStop, plan.NewStopLossPrice, summary.ProtectionRevision)
	logger.Infof("stop loss move requested: trader=%s symbol=%s side=%s group=%s action=%s current_stop_loss_price=%.6f new_stop_loss_price=%.6f", plan.TraderID, symbol, side, summary.ProtectionGroupID, plan.Action, currentStop, plan.NewStopLossPrice)
	switch plan.Action {
	case string(ProtectionActionSetBreakEvenStop):
		logger.Infof("break even armed: trader=%s symbol=%s side=%s group=%s revision=%d", plan.TraderID, symbol, side, summary.ProtectionGroupID, summary.ProtectionRevision)
	case string(ProtectionActionSetTrailingProtection):
		logger.Infof("trailing rule armed: trader=%s symbol=%s side=%s group=%s trailing_rule_id=%s revision=%d", plan.TraderID, symbol, side, summary.ProtectionGroupID, plan.TrailingRuleID, summary.ProtectionRevision)
	}

	started, err := r.builder.ApplyProtectionAdjustmentEvent(&store.ProtectionAdjustmentEventInput{
		TraderID:                   plan.TraderID,
		Symbol:                     symbol,
		Side:                       side,
		LinkedPositionKey:          plan.LinkedPositionKey,
		ProtectionGroupID:          summary.ProtectionGroupID,
		ProtectionMode:             summary.ProtectionMode,
		StopLossOrderIntentID:      summary.ProtectionGroupID + "-sl",
		TakeProfitOrderIntentID:    summary.ProtectionGroupID + "-tp",
		StopLossExchangeOrderID:    summary.StopLossOrderIntentID,
		TakeProfitExchangeOrderID:  summary.TakeProfitOrderIntentID,
		StopLossInitialTriggerPrice: plan.InitialStopLossPrice,
		StopLossCurrentTriggerPrice: currentStop,
		TakeProfitInitialTriggerPrice: plan.InitialTakeProfitPrice,
		TakeProfitCurrentTriggerPrice: plan.CurrentTakeProfitPrice,
		BreakEvenArmed:             summary.BreakEvenArmed,
		TrailingArmed:              summary.TrailingArmed,
		TrailingRuleID:             plan.TrailingRuleID,
		TrailingAnchorPrice:        plan.TrailingAnchorPrice,
		TrailingLastMoveAt:         summary.LastProtectionActionAt,
		TrailingMoveCount:          summary.TrailingMoveCount,
		ProtectionRevision:         summary.ProtectionRevision,
		AdvanceRevision:            false,
		LastProtectionAction:       plan.Action + "_requested",
		LastProtectionActionAt:     now,
		ProtectedQuantity:          plan.ProtectedQuantity,
		Status:                     "cancel_pending",
		Source:                     "protection_adjustment_manager",
		OldStopLossPrice:           currentStop,
		NewStopLossPrice:           plan.NewStopLossPrice,
		OldTakeProfitPrice:         plan.CurrentTakeProfitPrice,
		NewTakeProfitPrice:         plan.CurrentTakeProfitPrice,
		Reason:                     plan.Reason,
		EventType:                  startEventType,
		EventSource:                "protection_adjustment_manager",
		PayloadJSON:                mustJSON(map[string]interface{}{"lifecycle_event": "PROTECTION_CANCEL_REPLACE_STARTED", "action": plan.Action, "current_stop_loss_price": currentStop, "new_stop_loss_price": plan.NewStopLossPrice}),
		EventTime:                  now,
	})
	if err != nil {
		return nil, err
	}
	_ = started

	oldRow, err := r.findProtectionOrderRow(plan.TraderID, symbol, side, summary.StopLossExchangeOrderID, summary.StopLossOrderIntentID)
	if err != nil {
		return nil, err
	}
	oldLocalIntentID := summary.StopLossOrderIntentID
	if oldRow != nil && strings.TrimSpace(oldRow.LocalIntentID) != "" {
		oldLocalIntentID = oldRow.LocalIntentID
	}
	oldExchangeOrderID := summary.StopLossExchangeOrderID
	if oldRow != nil && strings.TrimSpace(oldRow.ExchangeOrderID) != "" {
		oldExchangeOrderID = oldRow.ExchangeOrderID
	}
	oldClientOrderID := summary.StopLossOrderIntentID
	if oldRow != nil && strings.TrimSpace(oldRow.ClientOrderID) != "" {
		oldClientOrderID = oldRow.ClientOrderID
	}
	oldOrderType := "STOP_MARKET"
	if oldRow != nil && strings.TrimSpace(oldRow.OrderType) != "" {
		oldOrderType = oldRow.OrderType
	}
	oldQty := plan.ProtectedQuantity
	if oldQty <= 0 && oldRow != nil {
		oldQty = oldRow.OrigQty
	}

	if err := r.cancelCurrentStopLoss(symbol); err != nil {
		return r.markProtectionPartialInvalid(plan, summary, now, currentStop, err)
	}

	newExchangeOrderID := ""
	newClientOrderID := protectionAdjustmentIntentID(summary.ProtectionGroupID, summary.ProtectionRevision+1)
	if r.exchange != nil {
		var createErr error
		newExchangeOrderID, createErr = r.exchange.CreateStopLossOrder(symbol, side, plan.ProtectedQuantity, plan.NewStopLossPrice, newClientOrderID)
		if createErr != nil {
			return r.markProtectionPartialInvalid(plan, summary, now, currentStop, createErr)
		}
	}
	newLocalIntentID := chooseString(newClientOrderID, newExchangeOrderID)
	if newLocalIntentID == "" {
		newLocalIntentID = newClientOrderID
	}
	if newExchangeOrderID == "" {
		newExchangeOrderID = chooseString(newClientOrderID, summary.StopLossExchangeOrderID)
	}

	if _, err := r.orderBuilder.ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          plan.TraderID,
		Symbol:            symbol,
		Side:              adjustProtectionOrderSide(side),
		PositionSideMode:  "one_way",
		OrderRole:         "cancel_replace",
		LocalIntentID:     oldLocalIntentID,
		LinkedGroupID:     summary.ProtectionGroupID,
		LinkedPositionKey: plan.LinkedPositionKey,
		ExchangeOrderID:   oldExchangeOrderID,
		ClientOrderID:     oldClientOrderID,
		OrderType:         oldOrderType,
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     true,
		OrigQty:           oldQty,
		ExecutedQty:       oldRowExecutedQty(oldRow),
		AvgPrice:          oldRowAvgPrice(oldRow),
		TriggerPrice:      currentStop,
		Status:            "PENDING_REPLACE",
		Source:            "protection_adjustment_manager",
		EventType:         "PROTECTION_CANCEL_REPLACE_STARTED",
		EventSource:       "protection_adjustment_manager",
		PayloadJSON:       mustJSON(map[string]interface{}{"lifecycle_event": "PROTECTION_CANCEL_REPLACE_STARTED", "action": plan.Action, "current_stop_loss_price": currentStop, "new_stop_loss_price": plan.NewStopLossPrice}),
		EventTime:         now,
	}); err != nil {
		return nil, err
	}

	if _, err := r.orderBuilder.ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          plan.TraderID,
		Symbol:            symbol,
		Side:              adjustProtectionOrderSide(side),
		PositionSideMode:  "one_way",
		OrderRole:         "stop_loss",
		LocalIntentID:     newLocalIntentID,
		LinkedGroupID:     summary.ProtectionGroupID,
		LinkedPositionKey: plan.LinkedPositionKey,
		ExchangeOrderID:   newExchangeOrderID,
		ClientOrderID:     newClientOrderID,
		OrderType:         "STOP_MARKET",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     true,
		OrigQty:           plan.ProtectedQuantity,
		ExecutedQty:       0,
		TriggerPrice:      plan.NewStopLossPrice,
		Status:            "NEW",
		Source:            "protection_adjustment_manager",
		EventType:         completeEventType,
		EventSource:       "protection_adjustment_manager",
		PayloadJSON:       mustJSON(map[string]interface{}{"lifecycle_event": "PROTECTION_CANCEL_REPLACE_COMPLETED", "action": plan.Action, "current_stop_loss_price": currentStop, "new_stop_loss_price": plan.NewStopLossPrice}),
		EventTime:         now,
	}); err != nil {
		return nil, err
	}

	completed, err := r.builder.ApplyProtectionAdjustmentEvent(&store.ProtectionAdjustmentEventInput{
		TraderID:                   plan.TraderID,
		Symbol:                     symbol,
		Side:                       side,
		LinkedPositionKey:          plan.LinkedPositionKey,
		ProtectionGroupID:          summary.ProtectionGroupID,
		ProtectionMode:             chooseProtectionAdjustmentMode(plan.Action, summary.ProtectionMode),
		StopLossOrderIntentID:      newLocalIntentID,
		TakeProfitOrderIntentID:    summary.TakeProfitOrderIntentID,
		StopLossExchangeOrderID:    newExchangeOrderID,
		TakeProfitExchangeOrderID:  summary.TakeProfitExchangeOrderID,
		StopLossInitialTriggerPrice: chooseProtectionInitialStop(plan.InitialStopLossPrice, summary.InitialStopLossPrice, currentStop),
		StopLossCurrentTriggerPrice: plan.NewStopLossPrice,
		TakeProfitInitialTriggerPrice: chooseProtectionInitialTakeProfit(plan.InitialTakeProfitPrice, summary.InitialTakeProfitPrice),
		TakeProfitCurrentTriggerPrice: chooseProtectionInitialTakeProfit(plan.CurrentTakeProfitPrice, summary.CurrentTakeProfitPrice),
		BreakEvenArmed:             chooseProtectionBreakEvenArmed(plan.Action, summary),
		TrailingArmed:              chooseProtectionTrailingArmed(plan.Action, summary),
		TrailingRuleID:             chooseProtectionTrailingRuleID(plan.Action, plan.TrailingRuleID, summary),
		TrailingAnchorPrice:        chooseProtectionTrailingAnchor(plan.Action, plan.TrailingAnchorPrice, plan.CurrentMarketPrice, plan.NewStopLossPrice),
		TrailingLastMoveAt:         now,
		TrailingMoveCount:          chooseProtectionTrailingMoveCount(plan.Action, summary.TrailingMoveCount),
		ProtectionRevision:         summary.ProtectionRevision,
		AdvanceRevision:            true,
		LastProtectionAction:       plan.Action,
		LastProtectionActionAt:     now,
		ProtectedQuantity:          plan.ProtectedQuantity,
		Status:                     "armed",
		Source:                     "protection_adjustment_manager",
		OldStopLossPrice:           currentStop,
		NewStopLossPrice:           plan.NewStopLossPrice,
		OldTakeProfitPrice:         summary.CurrentTakeProfitPrice,
		NewTakeProfitPrice:         summary.CurrentTakeProfitPrice,
		Reason:                     plan.Reason,
		EventType:                  completeEventType,
		EventSource:                "protection_adjustment_manager",
		PayloadJSON:                mustJSON(map[string]interface{}{"lifecycle_event": "PROTECTION_CANCEL_REPLACE_COMPLETED", "action": plan.Action, "current_stop_loss_price": currentStop, "new_stop_loss_price": plan.NewStopLossPrice}),
		EventTime:                  now,
	})
	if err != nil {
		return nil, err
	}

	logger.Infof("protection cancel_replace completed: trader=%s symbol=%s side=%s group=%s action=%s old_stop_loss_price=%.6f new_stop_loss_price=%.6f revision=%d",
		plan.TraderID, symbol, side, summary.ProtectionGroupID, plan.Action, currentStop, plan.NewStopLossPrice, completed.ProtectionRevision)
	logger.Infof("stop loss move applied: trader=%s symbol=%s side=%s group=%s action=%s stop_loss_current=%.6f revision=%d",
		plan.TraderID, symbol, side, summary.ProtectionGroupID, plan.Action, completed.StopLossCurrentTriggerPrice, completed.ProtectionRevision)

	return completed, nil
}

func (r *ProtectionCancelReplaceReconciler) cancelCurrentStopLoss(symbol string) error {
	if r == nil || r.exchange == nil {
		return nil
	}
	return r.exchange.CancelStopLossOrders(symbol)
}

func (r *ProtectionCancelReplaceReconciler) markProtectionPartialInvalid(plan *ProtectionAdjustmentPlan, summary *store.ProtectionGroupSummary, eventTime time.Time, currentStop float64, cause error) (*store.ProtectionGroup, error) {
	if plan == nil || summary == nil {
		return nil, cause
	}
	_, _ = r.builder.ApplyProtectionAdjustmentEvent(&store.ProtectionAdjustmentEventInput{
		TraderID:                   plan.TraderID,
		Symbol:                     plan.Symbol,
		Side:                       plan.Side,
		LinkedPositionKey:          plan.LinkedPositionKey,
		ProtectionGroupID:          summary.ProtectionGroupID,
		ProtectionMode:             summary.ProtectionMode,
		StopLossOrderIntentID:      summary.StopLossOrderIntentID,
		TakeProfitOrderIntentID:    summary.TakeProfitOrderIntentID,
		StopLossExchangeOrderID:    summary.StopLossExchangeOrderID,
		TakeProfitExchangeOrderID:  summary.TakeProfitExchangeOrderID,
		StopLossInitialTriggerPrice: summary.InitialStopLossPrice,
		StopLossCurrentTriggerPrice: currentStop,
		TakeProfitInitialTriggerPrice: summary.InitialTakeProfitPrice,
		TakeProfitCurrentTriggerPrice: summary.CurrentTakeProfitPrice,
		BreakEvenArmed:             summary.BreakEvenArmed,
		TrailingArmed:              summary.TrailingArmed,
		TrailingRuleID:             summary.TrailingRuleID,
		TrailingAnchorPrice:        summary.TrailingAnchorPrice,
		TrailingLastMoveAt:         summary.LastProtectionActionAt,
		TrailingMoveCount:          summary.TrailingMoveCount,
		ProtectionRevision:         summary.ProtectionRevision,
		AdvanceRevision:            false,
		LastProtectionAction:       "protection_adjustment_failed",
		LastProtectionActionAt:     eventTime,
		ProtectedQuantity:          summary.ProtectedQuantity,
		Status:                     "partial_invalid",
		Source:                     "protection_adjustment_manager",
		OldStopLossPrice:           currentStop,
		NewStopLossPrice:           currentStop,
		OldTakeProfitPrice:         summary.CurrentTakeProfitPrice,
		NewTakeProfitPrice:         summary.CurrentTakeProfitPrice,
		Reason:                     cause.Error(),
		EventType:                  "PROTECTION_CANCEL_REPLACE_FAILED",
		EventSource:                "protection_adjustment_manager",
		PayloadJSON:                mustJSON(map[string]interface{}{"error": cause.Error(), "action": plan.Action}),
		EventTime:                  eventTime,
	})
	logger.Infof("protection adjustment blocked by guard: symbol=%s action=%s category=execution reason=%v", symbolOnly(plan), plan.Action, cause)
	return nil, cause
}

func (r *ProtectionCancelReplaceReconciler) findProtectionOrderRow(traderID, symbol, side, exchangeOrderID, localIntentID string) (*store.OrderRegistry, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	rows, err := r.store.OrderRegistry().ListByTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		return nil, err
	}
	symbol = normalizeAggregateSymbol(symbol)
	side = normalizeAggregateSide(side)
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(exchangeOrderID) != "" && strings.TrimSpace(row.ExchangeOrderID) == strings.TrimSpace(exchangeOrderID) {
			return row, nil
		}
		if strings.TrimSpace(localIntentID) != "" && strings.TrimSpace(row.LocalIntentID) == strings.TrimSpace(localIntentID) {
			return row, nil
		}
	}
	return nil, nil
}

func protectionAdjustmentStartEventType(action string) string {
	switch strings.TrimSpace(action) {
	case string(ProtectionActionSetBreakEvenStop):
		return "BREAK_EVEN_ARMED"
	case string(ProtectionActionSetTrailingProtection):
		return "TRAILING_ARMED"
	default:
		return "STOP_LOSS_MOVE_REQUESTED"
	}
}

func protectionAdjustmentCompleteEventType(action string, summary *store.ProtectionGroupSummary) string {
	if summary != nil && summary.TrailingArmed {
		return "TRAILING_STEP_TRIGGERED"
	}
	switch strings.TrimSpace(action) {
	case string(ProtectionActionSetBreakEvenStop):
		return "STOP_LOSS_MOVE_APPLIED"
	case string(ProtectionActionSetTrailingProtection):
		return "STOP_LOSS_MOVE_APPLIED"
	default:
		return "STOP_LOSS_MOVE_APPLIED"
	}
}

func chooseProtectionAdjustmentMode(action, currentMode string) string {
	switch strings.TrimSpace(action) {
	case string(ProtectionActionSetBreakEvenStop):
		return "break_even"
	case string(ProtectionActionSetTrailingProtection), string(ProtectionActionMoveStopLoss):
		return "trailing_segmented"
	default:
		return store.NormalizeProtectionMode(currentMode)
	}
}

func chooseProtectionBreakEvenArmed(action string, summary *store.ProtectionGroupSummary) bool {
	if summary != nil && summary.BreakEvenArmed {
		return true
	}
	return strings.TrimSpace(action) == string(ProtectionActionSetBreakEvenStop) || strings.TrimSpace(action) == string(ProtectionActionSetTrailingProtection) || strings.TrimSpace(action) == string(ProtectionActionMoveStopLoss)
}

func chooseProtectionTrailingArmed(action string, summary *store.ProtectionGroupSummary) bool {
	if summary != nil && summary.TrailingArmed {
		return true
	}
	return strings.TrimSpace(action) == string(ProtectionActionSetTrailingProtection) || strings.TrimSpace(action) == string(ProtectionActionMoveStopLoss)
}

func chooseProtectionTrailingRuleID(action, ruleID string, summary *store.ProtectionGroupSummary) string {
	if strings.TrimSpace(ruleID) != "" {
		return strings.TrimSpace(ruleID)
	}
	if summary != nil {
		return strings.TrimSpace(summary.TrailingRuleID)
	}
	return ""
}

func chooseProtectionTrailingAnchor(action string, requestedAnchor, marketPrice, newStop float64) float64 {
	if requestedAnchor > 0 {
		return requestedAnchor
	}
	if marketPrice > 0 {
		return marketPrice
	}
	return newStop
}

func chooseProtectionTrailingMoveCount(action string, current int) int {
	if current < 0 {
		current = 0
	}
	if strings.TrimSpace(action) == string(ProtectionActionSetTrailingProtection) || strings.TrimSpace(action) == string(ProtectionActionMoveStopLoss) || strings.TrimSpace(action) == string(ProtectionActionSetBreakEvenStop) {
		return current + 1
	}
	return current
}

func chooseProtectionInitialStop(requested, fallback, current float64) float64 {
	if requested > 0 {
		return requested
	}
	if fallback > 0 {
		return fallback
	}
	return current
}

func chooseProtectionInitialTakeProfit(requested, fallback float64) float64 {
	if requested > 0 {
		return requested
	}
	return fallback
}

func oldRowExecutedQty(row *store.OrderRegistry) float64 {
	if row == nil {
		return 0
	}
	if row.ExecutedQty > 0 {
		return row.ExecutedQty
	}
	if row.OrigQty > 0 && row.Status == "FILLED" {
		return row.OrigQty
	}
	return 0
}

func oldRowAvgPrice(row *store.OrderRegistry) float64 {
	if row == nil {
		return 0
	}
	return row.AvgPrice
}

func adjustProtectionOrderSide(side string) string {
	normalized := normalizeOneWaySide(side)
	if normalized == "" {
		return normalized
	}
	return normalized
}

func protectionAdjustmentIntentID(groupID string, revision int) string {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		groupID = "protection"
	}
	if revision <= 0 {
		revision = 1
	}
	return fmt.Sprintf("%s-sl-r%d", groupID, revision)
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.000001
}

func symbolOnly(plan *ProtectionAdjustmentPlan) string {
	if plan == nil {
		return ""
	}
	return normalizeAggregateSymbol(plan.Symbol)
}
