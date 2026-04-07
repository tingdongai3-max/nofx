package store

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/logger"
)

// PositionAggregateBuilder rebuilds the current system-owned position truth from positions, fills, orders, and decision records.
type PositionAggregateBuilder struct {
	store *Store
}

// NewPositionAggregateBuilder creates a new PositionAggregateBuilder.
func NewPositionAggregateBuilder(st *Store) *PositionAggregateBuilder {
	return &PositionAggregateBuilder{store: st}
}

// BuildForTrader builds, persists, and returns the current position aggregates for a trader.
// All aggregation math is centralized here so handlers and traders never duplicate reconciliation rules.
func (b *PositionAggregateBuilder) BuildForTrader(traderID string, liveSnapshots []PositionSnapshot, peakPnLCache map[string]float64) ([]*PositionAggregate, error) {
	if b == nil || b.store == nil {
		return nil, fmt.Errorf("position aggregate builder store is not configured")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	openPositions, err := b.store.Position().GetOpenPositions(traderID)
	if err != nil {
		return nil, err
	}

	existingAggregates, err := b.store.PositionAggregate().ListByTraderID(traderID)
	if err != nil {
		return nil, err
	}
	existingMap := make(map[string]*PositionAggregate, len(existingAggregates))
	for _, existing := range existingAggregates {
		existingMap[aggregateKey(existing.TraderID, existing.Symbol, existing.Side)] = existing
	}

	latestRecords, err := b.store.Decision().GetLatestRecords(traderID, 10)
	if err != nil {
		return nil, err
	}

	var latestRecord *DecisionRecord
	if len(latestRecords) > 0 {
		latestRecord = latestRecords[len(latestRecords)-1]
	}

	liveMap := buildLiveSnapshotMap(liveSnapshots)
	decisionMap := buildDecisionSnapshotMap(latestRecord)

	orderKeyOrder := make([]string, 0)
	groupedPositions := make(map[string][]*TraderPosition)
	for _, pos := range openPositions {
		symbol := normalizeAggregateSymbol(pos.Symbol)
		side := normalizeAggregateSide(pos.Side)
		if symbol == "" || side == "" {
			continue
		}

		key := aggregateKey(traderID, symbol, side)
		if _, exists := groupedPositions[key]; !exists {
			orderKeyOrder = append(orderKeyOrder, key)
		}
		groupedPositions[key] = append(groupedPositions[key], pos)
	}

	aggregates := make([]*PositionAggregate, 0, len(groupedPositions))
	now := time.Now().UTC()
	for _, key := range orderKeyOrder {
		positions := groupedPositions[key]
		if len(positions) == 0 {
			continue
		}

		summary, err := b.store.OrderRegistry().SummarizeForTraderSymbolSide(traderID, positions[0].Symbol, positions[0].Side)
		if err != nil {
			return nil, err
		}
		protectionSummary, err := b.store.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, positions[0].Symbol, positions[0].Side)
		if err != nil {
			return nil, err
		}
		scaleInSummary, err := b.store.ScaleInPlan().SummarizeForTraderSymbolSide(traderID, positions[0].Symbol, positions[0].Side)
		if err != nil {
			return nil, err
		}
		scaleOutSummary, err := b.store.ScaleOutPlan().SummarizeForTraderSymbolSide(traderID, positions[0].Symbol, positions[0].Side)
		if err != nil {
			return nil, err
		}

		aggregate := buildAggregateForGroup(traderID, positions, liveMap, decisionMap, summary, protectionSummary, scaleInSummary, scaleOutSummary, peakPnLCache, latestRecord)
		aggregate.LastReconciledAt = now
		aggregate.UpdatedAt = now
		if existing, ok := existingMap[key]; ok && !existing.CreatedAt.IsZero() {
			aggregate.CreatedAt = existing.CreatedAt
			aggregate.ID = existing.ID
		} else {
			aggregate.CreatedAt = now
		}

		logger.Infof("position aggregate built: trader=%s symbol=%s side=%s total_qty=%.6f available_qty=%.6f pending_add=%.6f pending_reduce=%.6f",
			aggregate.TraderID, aggregate.Symbol, aggregate.Side, aggregate.TotalQty, aggregate.AvailableQty, aggregate.PendingAddQty, aggregate.PendingReduceQty)
		logger.Infof("position aggregate refreshed from protection state: trader=%s symbol=%s side=%s has_protection=%v protection_group_id=%s protection_group_status=%s protection_consistency=%s stop_loss_armed=%v take_profit_armed=%v",
			aggregate.TraderID, aggregate.Symbol, aggregate.Side, aggregate.HasProtection, aggregate.ProtectionGroupID, protectionSummary.ProtectionGroupStatus, protectionSummary.ConsistencyStatus, aggregate.StopLossArmed, aggregate.TakeProfitArmed)
		logger.Infof("position aggregate refreshed from scale in state: trader=%s symbol=%s side=%s has_scale_in_plan=%v scale_in_plan_id=%s scale_in_status=%s pending_add_qty=%.6f executed_scale_in_qty=%.6f remaining_scale_in_qty=%.6f scale_in_count=%d total_notional=%.6f",
			aggregate.TraderID, aggregate.Symbol, aggregate.Side, aggregate.HasScaleInPlan, aggregate.ScaleInPlanID, aggregate.ScaleInStatus, aggregate.PendingAddQty, aggregate.ExecutedScaleInQty, aggregate.RemainingScaleInQty, aggregate.ScaleInCount, aggregate.TotalNotional)
		logger.Infof("position aggregate refreshed from scale out state: trader=%s symbol=%s side=%s has_scale_out_plan=%v scale_out_plan_id=%s scale_out_status=%s pending_scale_out_qty=%.6f executed_scale_out_qty=%.6f remaining_scale_out_qty=%.6f",
			aggregate.TraderID, aggregate.Symbol, aggregate.Side, aggregate.HasScaleOutPlan, aggregate.ScaleOutPlanID, aggregate.ScaleOutStatus, aggregate.PendingScaleOutQty, aggregate.ExecutedScaleOutQty, aggregate.RemainingScaleOutQty)

		aggregates = append(aggregates, aggregate)
	}

	if len(aggregates) == 0 {
		logger.Infof("position aggregate built: trader=%s aggregates=0", traderID)
	}

	// Keep the persisted table aligned with the freshly built truth snapshot.
	if err := b.store.PositionAggregate().ReplaceForTrader(traderID, aggregates); err != nil {
		return nil, err
	}
	logger.Infof("position aggregate refreshed from order registry: trader=%s aggregates=%d", traderID, len(aggregates))

	return aggregates, nil
}

func aggregateKey(traderID, symbol, side string) string {
	return traderID + "|" + normalizeAggregateSymbol(symbol) + "|" + normalizeAggregateSide(side)
}

func normalizeAggregateSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func buildLiveSnapshotMap(snapshots []PositionSnapshot) map[string]PositionSnapshot {
	result := make(map[string]PositionSnapshot, len(snapshots))
	for _, snap := range snapshots {
		symbol := normalizeAggregateSymbol(snap.Symbol)
		side := normalizeAggregateSide(snap.Side)
		if symbol == "" || side == "" {
			continue
		}
		result[symbol+"|"+side] = snap
	}
	return result
}

func buildDecisionSnapshotMap(record *DecisionRecord) map[string]PositionSnapshot {
	result := make(map[string]PositionSnapshot)
	if record == nil {
		return result
	}
	for _, snap := range record.Positions {
		symbol := normalizeAggregateSymbol(snap.Symbol)
		side := normalizeAggregateSide(snap.Side)
		if symbol == "" || side == "" {
			continue
		}
		result[symbol+"|"+side] = snap
	}
	return result
}

func buildAggregateForGroup(
	traderID string,
	positions []*TraderPosition,
	liveMap map[string]PositionSnapshot,
	decisionMap map[string]PositionSnapshot,
	orderSummary *OrderRegistrySummary,
	protectionSummary *ProtectionGroupSummary,
	scaleInSummary *ScaleInPlanSummary,
	scaleOutSummary *ScaleOutPlanSummary,
	peakPnLCache map[string]float64,
	latestRecord *DecisionRecord,
) *PositionAggregate {
	first := positions[0]
	symbol := normalizeAggregateSymbol(first.Symbol)
	side := normalizeAggregateSide(first.Side)
	key := symbol + "|" + side

	var totalQty float64
	var weightedEntry float64
	var realizedPnL float64
	for _, pos := range positions {
		qty := math.Abs(pos.Quantity)
		totalQty += qty
		weightedEntry += pos.EntryPrice * qty
		realizedPnL += pos.RealizedPnL
	}

	var avgEntryPrice float64
	if totalQty > 0 {
		avgEntryPrice = weightedEntry / totalQty
	}

	var unrealizedPnL float64
	var currentPnLPct float64

	if snap, ok := liveMap[key]; ok {
		unrealizedPnL = snap.UnrealizedProfit
		avgEntryPrice = chooseNonZero(avgEntryPrice, snap.EntryPrice)
		currentPnLPct = calcPnLPctFromSnapshot(snap)
	} else if snap, ok := decisionMap[key]; ok {
		unrealizedPnL = snap.UnrealizedProfit
		avgEntryPrice = chooseNonZero(avgEntryPrice, snap.EntryPrice)
		currentPnLPct = calcPnLPctFromSnapshot(snap)
	}

	peakPnLPct := currentPnLPct
	if cache, ok := peakPnLCache[key]; ok {
		peakPnLPct = cache
	}

	pendingAddQty := 0.0
	pendingReduceQty := 0.0
	if orderSummary != nil {
		pendingAddQty = orderSummary.PendingAddQty
		pendingReduceQty = orderSummary.PendingReduceQty
	}
	hasProtection := protectionSummary != nil && protectionSummary.HasProtection
	protectionMode := ""
	protectedQuantity := 0.0
	protectionGroupID := ""
	stopLossArmed := false
	takeProfitArmed := false
	currentStopLossPrice := 0.0
	initialStopLossPrice := 0.0
	breakEvenArmed := false
	trailingArmed := false
	trailingRuleID := ""
	protectionRevision := 0
	lastProtectionMoveAt := time.Time{}
	protectionMoveCount := 0
	if protectionSummary != nil {
		protectionMode = protectionSummary.ProtectionMode
		protectedQuantity = protectionSummary.ProtectedQuantity
		protectionGroupID = protectionSummary.ProtectionGroupID
		stopLossArmed = protectionSummary.StopLossArmed
		takeProfitArmed = protectionSummary.TakeProfitArmed
		currentStopLossPrice = protectionSummary.CurrentStopLossPrice
		initialStopLossPrice = protectionSummary.InitialStopLossPrice
		breakEvenArmed = protectionSummary.BreakEvenArmed
		trailingArmed = protectionSummary.TrailingArmed
		trailingRuleID = protectionSummary.TrailingRuleID
		protectionRevision = protectionSummary.ProtectionRevision
		lastProtectionMoveAt = protectionSummary.LastProtectionActionAt
		protectionMoveCount = protectionSummary.TrailingMoveCount
		if breakEvenArmed {
			protectionMoveCount++
		}
		if protectedQuantity <= 0 && orderSummary != nil && orderSummary.ProtectionCoverageQty > 0 {
			protectedQuantity = orderSummary.ProtectionCoverageQty
		}
	}

	hasScaleInPlan := scaleInSummary != nil && scaleInSummary.HasScaleInPlan
	scaleInPlanID := ""
	scaleInStatus := ""
	scaleInConsistency := "unknown"
	scaleInHasStateMismatch := false
	pendingScaleInQty := 0.0
	executedScaleInQty := 0.0
	remainingScaleInQty := 0.0
	scaleInCount := 0
	if scaleInSummary != nil {
		scaleInPlanID = scaleInSummary.ScaleInPlanID
		scaleInStatus = scaleInSummary.ScaleInStatus
		scaleInConsistency = normalizeScaleInConsistencyStatus(scaleInSummary.ConsistencyStatus)
		scaleInHasStateMismatch = scaleInSummary.HasStateMismatch
		pendingScaleInQty = scaleInSummary.PendingScaleInQty
		executedScaleInQty = scaleInSummary.ExecutedScaleInQty
		remainingScaleInQty = scaleInSummary.RemainingScaleInQty
		scaleInCount = scaleInSummary.CurrentScaleInCount
		if pendingScaleInQty > pendingAddQty {
			pendingAddQty = pendingScaleInQty
		}
		if scaleInStatus == "" && hasScaleInPlan {
			scaleInStatus = "draft"
		}
	}

	hasScaleOutPlan := scaleOutSummary != nil && scaleOutSummary.HasScaleOutPlan
	scaleOutPlanID := ""
	scaleOutStatus := ""
	scaleOutConsistency := "unknown"
	scaleOutHasStateMismatch := false
	pendingScaleOutQty := 0.0
	executedScaleOutQty := 0.0
	remainingScaleOutQty := 0.0
	if scaleOutSummary != nil {
		scaleOutPlanID = scaleOutSummary.ScaleOutPlanID
		scaleOutStatus = scaleOutSummary.ScaleOutStatus
		scaleOutConsistency = normalizeScaleOutConsistencyStatus(scaleOutSummary.ConsistencyStatus)
		scaleOutHasStateMismatch = scaleOutSummary.HasStateMismatch
		pendingScaleOutQty = scaleOutSummary.PendingScaleOutQty
		executedScaleOutQty = scaleOutSummary.ExecutedScaleOutQty
		remainingScaleOutQty = scaleOutSummary.RemainingScaleOutQty
		if !hasScaleOutPlan && scaleOutStatus == "" {
			scaleOutStatus = "draft"
		}
	}

	if pendingReduceQty > totalQty {
		pendingReduceQty = totalQty
	}

	reduceReservationQty := maxFloat(pendingReduceQty, remainingScaleOutQty)
	availableQty := totalQty - reduceReservationQty
	if availableQty < 0 {
		availableQty = 0
	}

	protectionState := map[string]interface{}{
		"source":                   "protection_group",
		"has_protection":           hasProtection,
		"protection_mode":          protectionMode,
		"protected_quantity":       protectedQuantity,
		"protection_group_id":      protectionGroupID,
		"stop_loss_armed":          stopLossArmed,
		"take_profit_armed":        takeProfitArmed,
		"break_even_armed":         breakEvenArmed,
		"trailing_armed":           trailingArmed,
		"trailing_rule_id":         trailingRuleID,
		"current_stop_loss_price":   currentStopLossPrice,
		"initial_stop_loss_price":   initialStopLossPrice,
		"protection_revision":      protectionRevision,
		"last_protection_move_at":   lastProtectionMoveAt.UTC().Format(time.RFC3339),
		"protection_move_count":     protectionMoveCount,
		"protection_orders":         []interface{}{},
	}
	if orderSummary != nil {
		protectionState["working_order_ids"] = orderSummary.WorkingOrderIDs
		protectionState["pending_order_ids"] = orderSummary.PendingOrderIDs
		protectionState["has_pending_cancel_replace"] = orderSummary.HasPendingCancelReplace
	}
	if protectionSummary != nil {
		protectionState["consistency_status"] = protectionSummary.ConsistencyStatus
		protectionState["mismatch_reasons"] = protectionSummary.MismatchReasons
		protectionState["linked_position_key"] = protectionSummary.LinkedPositionKey
		protectionState["stop_loss_trigger_price"] = protectionSummary.CurrentStopLossPrice
		protectionState["take_profit_trigger_price"] = protectionSummary.CurrentTakeProfitPrice
	}
	scalePlanState := map[string]interface{}{
		"source":                  "truth_layers",
		"latest_cycle":            0,
		"latest_timestamp":        "",
		"decision_actions":        []DecisionAction{},
		"pending_add_qty":         pendingAddQty,
		"pending_reduce_qty":      pendingReduceQty,
		"has_scale_in_plan":       hasScaleInPlan,
		"scale_in_plan_id":        scaleInPlanID,
		"scale_in_status":         scaleInStatus,
		"scale_in_consistency":    scaleInConsistency,
		"scale_in_has_mismatch":   scaleInHasStateMismatch,
		"pending_scale_in_qty":    pendingScaleInQty,
		"executed_scale_in_qty":   executedScaleInQty,
		"remaining_scale_in_qty":  remainingScaleInQty,
		"scale_in_count":          scaleInCount,
		"has_scale_out_plan":      hasScaleOutPlan,
		"scale_out_plan_id":       scaleOutPlanID,
		"scale_out_status":        scaleOutStatus,
		"scale_out_consistency":   scaleOutConsistency,
		"scale_out_has_mismatch":  scaleOutHasStateMismatch,
		"pending_scale_out_qty":   pendingScaleOutQty,
		"executed_scale_out_qty":  executedScaleOutQty,
		"remaining_scale_out_qty": remainingScaleOutQty,
	}

	if latestRecord != nil {
		scalePlanState["latest_cycle"] = latestRecord.CycleNumber
		scalePlanState["latest_timestamp"] = latestRecord.Timestamp.UTC().Format(time.RFC3339)
		scalePlanState["decision_actions"] = filterDecisionActions(latestRecord.Decisions, symbol)
	}

	protectionJSON, _ := json.Marshal(protectionState)
	scalePlanJSON, _ := json.Marshal(scalePlanState)

	return &PositionAggregate{
		TraderID:             traderID,
		Symbol:               symbol,
		Side:                 side,
		TotalQty:             totalQty,
		AvailableQty:         availableQty,
		PendingAddQty:        pendingAddQty,
		PendingReduceQty:     pendingReduceQty,
		AvgEntryPrice:        avgEntryPrice,
		RealizedPnL:          realizedPnL,
		UnrealizedPnL:        unrealizedPnL,
		PeakPnLPct:           peakPnLPct,
		HasProtection:        hasProtection,
		ProtectionMode:       protectionMode,
		ProtectedQuantity:    protectedQuantity,
		ProtectionGroupID:    protectionGroupID,
		StopLossArmed:        stopLossArmed,
		TakeProfitArmed:      takeProfitArmed,
		CurrentStopLossPrice: currentStopLossPrice,
		InitialStopLossPrice: initialStopLossPrice,
		BreakEvenArmed:       breakEvenArmed,
		TrailingArmed:        trailingArmed,
		TrailingRuleID:       trailingRuleID,
		ProtectionRevision:   protectionRevision,
		LastProtectionMoveAt: lastProtectionMoveAt,
		ProtectionMoveCount:  protectionMoveCount,
		ScaleOutPlanID:       scaleOutPlanID,
		HasScaleOutPlan:      hasScaleOutPlan,
		ScaleOutStatus:       scaleOutStatus,
		PendingScaleOutQty:   pendingScaleOutQty,
		ExecutedScaleOutQty:  executedScaleOutQty,
		RemainingScaleOutQty: remainingScaleOutQty,
		ScaleInPlanID:        scaleInPlanID,
		HasScaleInPlan:       hasScaleInPlan,
		ScaleInStatus:        scaleInStatus,
		ExecutedScaleInQty:   executedScaleInQty,
		RemainingScaleInQty:  remainingScaleInQty,
		ScaleInCount:         scaleInCount,
		TotalNotional:        totalQty * avgEntryPrice,
		ProtectionStateJSON:  string(protectionJSON),
		ScalePlanStateJSON:   string(scalePlanJSON),
		ExecutionEligible:    totalQty > 0 && (scaleOutSummary == nil || scaleOutConsistency != "mismatch") && (scaleInSummary == nil || scaleInConsistency != "mismatch") && (protectionSummary == nil || protectionSummary.ConsistencyStatus != "mismatch"),
	}
}

func chooseNonZero(current, fallback float64) float64 {
	if current > 0 {
		return current
	}
	return fallback
}

func calcPnLPctFromSnapshot(snap PositionSnapshot) float64 {
	qty := math.Abs(snap.PositionAmt)
	if qty <= 0 || snap.EntryPrice <= 0 || snap.MarkPrice <= 0 || snap.Leverage <= 0 {
		return 0
	}

	var pnl float64
	side := normalizeAggregateSide(snap.Side)
	if side == "LONG" {
		pnl = (snap.MarkPrice - snap.EntryPrice) * qty
	} else {
		pnl = (snap.EntryPrice - snap.MarkPrice) * qty
	}
	marginUsed := (qty * snap.MarkPrice) / snap.Leverage
	if marginUsed <= 0 {
		return 0
	}
	return (pnl / marginUsed) * 100
}

func filterDecisionActions(decisions []DecisionAction, symbol string) []DecisionAction {
	if len(decisions) == 0 {
		return []DecisionAction{}
	}

	normalizedSymbol := normalizeAggregateSymbol(symbol)
	filtered := make([]DecisionAction, 0, len(decisions))
	for _, decision := range decisions {
		if normalizedSymbol == "" || strings.EqualFold(normalizeAggregateSymbol(decision.Symbol), normalizedSymbol) {
			filtered = append(filtered, decision)
		}
	}
	return filtered
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
