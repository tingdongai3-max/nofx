package trader

import (
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

// ProtectionAdjustmentPreview is the read-only dynamic protection truth preview returned by the backend.
// It combines the protection group truth layer, the trailing rule truth layer, the evidence chain, and the guard result.
type ProtectionAdjustmentPreview struct {
	TruthSnapshotMetadata
	TraderID                         string                                `json:"trader_id"`                           // System-owned trader key.
	SelectedSymbol                   string                                `json:"selected_symbol,omitempty"`           // Selected symbol scope used for the preview.
	Exchange                         string                                `json:"exchange"`                            // Fixed phase-6 exchange scope: binance_usdm.
	Mode                             string                                `json:"mode"`                                // Fixed phase-6 mode scope: one_way.
	ProtectionGroup                  *store.ProtectionGroup                `json:"protection_group,omitempty"`          // Current protection truth row; system-owned, not an exchange raw order.
	ProtectionSummary                *store.ProtectionGroupSummary         `json:"protection_summary,omitempty"`        // Current protection truth rollup; system-derived, not an exchange raw order.
	TrailingRule                     *store.TrailingRule                   `json:"trailing_rule,omitempty"`             // Current dynamic trailing rule truth row; system-owned, not an exchange raw order.
	ProtectionEventLogs              []*store.ProtectionAdjustmentEventLog `json:"protection_event_logs"`               // Append-only raw dynamic-protection evidence chain.
	OrderRegistrySummary             *store.OrderRegistrySummary           `json:"order_registry_summary,omitempty"`    // System-derived order truth summary for the selected scope.
	ScaleOutSummary                  *store.ScaleOutPlanSummary            `json:"scale_out_summary,omitempty"`         // System-derived scale-out truth summary used for conflict detection.
	ScaleInSummary                   *store.ScaleInPlanSummary             `json:"scale_in_summary,omitempty"`          // System-derived scale-in truth summary used for conflict detection.
	PositionAggregate                *store.PositionAggregate              `json:"position_aggregate,omitempty"`        // System-owned position truth snapshot for the selected scope.
	HasProtection                    bool                                  `json:"has_protection"`                      // System-derived flag that an active protection group exists.
	ProtectionMode                   string                                `json:"protection_mode"`                     // Current protection policy label.
	ProtectionGroupStatus            string                                `json:"protection_group_status"`             // Current protection truth status.
	ProtectionConsistency            string                                `json:"protection_consistency"`              // Protection consistency enum: consistent, mismatch, pending, or unknown.
	ProtectionAdjustmentConsistency  string                                `json:"protection_adjustment_consistency"`   // Dynamic-protection consistency enum: consistent, mismatch, pending, or unknown.
	HasStateMismatch                 bool                                  `json:"has_state_mismatch"`                  // Reconcile result flag; true means truth layers disagree.
	MismatchReasons                  []store.ProtectionStateMismatchReason `json:"mismatch_reasons"`                    // Machine-readable mismatch explanation list.
	HasWorkingOrders                 bool                                  `json:"has_working_orders"`                  // Truth-layer flag derived from linked working protection rows.
	HasPendingCancelReplace          bool                                  `json:"has_pending_cancel_replace"`          // Truth-layer flag showing that the linked stop-loss leg is in cancel/replace.
	StopLossArmed                    bool                                  `json:"stop_loss_armed"`                     // System-derived stop-loss leg working flag.
	TakeProfitArmed                  bool                                  `json:"take_profit_armed"`                   // System-derived take-profit leg working flag.
	BreakEvenArmed                   bool                                  `json:"break_even_armed"`                    // System-derived flag showing the protection has moved to break-even.
	TrailingArmed                    bool                                  `json:"trailing_armed"`                      // System-derived flag showing that segmented trailing is active.
	TrailingRuleID                   string                                `json:"trailing_rule_id"`                    // System-owned trailing rule identifier.
	TrailingAnchorPrice              float64                               `json:"trailing_anchor_price"`               // System-derived anchor price used by the latest trailing move.
	TrailingMoveCount                int                                   `json:"trailing_move_count"`                 // System-derived count of completed trailing moves and break-even moves.
	ProtectionRevision               int                                   `json:"protection_revision"`                 // System-owned protection revision counter.
	LastProtectionAction             string                                `json:"last_protection_action"`              // Last protection mutation action label.
	LastProtectionActionAt           time.Time                             `json:"last_protection_action_at"`           // Last protection mutation timestamp.
	InitialStopLossPrice             float64                               `json:"initial_stop_loss_price"`             // Initial stop-loss trigger price before any movement.
	CurrentStopLossPrice             float64                               `json:"current_stop_loss_price"`             // Current stop-loss trigger price after any movement.
	InitialTakeProfitPrice           float64                               `json:"initial_take_profit_price"`           // Initial take-profit trigger price before any movement.
	CurrentTakeProfitPrice           float64                               `json:"current_take_profit_price"`           // Current take-profit trigger price after any movement.
	CurrentMarketPrice               float64                               `json:"current_market_price"`                // Best-effort current market price used for trigger evaluation; debug only.
	CurrentPnLPct                    float64                               `json:"current_pnl_pct"`                     // Best-effort current unrealized PnL percent used for trigger evaluation.
	RemainingMoveBudget              float64                               `json:"remaining_move_budget"`               // Remaining stop-loss move budget under the current trailing rule.
	ProtectionAdjustmentBlockReasons []CapabilityReason                    `json:"protection_adjustment_block_reasons"` // Machine-readable dynamic-protection block reasons.
	GuardAssessment                  *ProtectionAdjustmentGuardAssessment  `json:"guard_assessment,omitempty"`          // Execution-facing guard result used for debug/UI review.
	CanArmBreakEven                  bool                                  `json:"can_arm_break_even"`                  // Guard-derived flag showing break-even may be armed now.
	CanArmTrailing                   bool                                  `json:"can_arm_trailing"`                    // Guard-derived flag showing trailing may be armed now.
	CanMoveStopLoss                  bool                                  `json:"can_move_stop_loss"`                  // Guard-derived flag showing the current stop-loss may be moved now.
	ProtectionAdjustmentStatus       string                                `json:"protection_adjustment_status"`        // Guard-derived dynamic protection status label.
	HasScaleOutPlan                  bool                                  `json:"has_scale_out_plan"`                  // Truth-layer flag showing whether a scale-out plan exists.
	ScaleOutStatus                   string                                `json:"scale_out_status"`                    // Current scale-out plan truth status.
	ScaleOutConsistency              string                                `json:"scale_out_consistency"`               // Scale-out consistency enum: consistent, mismatch, pending, or unknown.
	HasScaleOutMismatch              bool                                  `json:"has_scale_out_mismatch"`              // Reconcile flag showing that the scale-out truth layer disagrees.
	PendingScaleOutQty               float64                               `json:"pending_scale_out_qty"`               // Truth-layer working-order reserve for active scale-out levels.
	RemainingScaleOutQty             float64                               `json:"remaining_scale_out_qty"`             // Truth-layer total quantity still outstanding across the current scale-out plan.
	ExecutedScaleOutQty              float64                               `json:"executed_scale_out_qty"`              // Truth-layer cumulative executed quantity across the current scale-out plan.
	HasScaleInPlan                   bool                                  `json:"has_scale_in_plan"`                   // Truth-layer flag showing whether a scale-in plan exists.
	ScaleInStatus                    string                                `json:"scale_in_status"`                     // Current scale-in plan truth status.
	ScaleInConsistency               string                                `json:"scale_in_consistency"`                // Scale-in consistency enum: consistent, mismatch, pending, or unknown.
	HasScaleInMismatch               bool                                  `json:"has_scale_in_mismatch"`               // Reconcile flag showing that the scale-in truth layer disagrees.
	PendingScaleInQty                float64                               `json:"pending_scale_in_qty"`                // Truth-layer working-order reserve for active scale-in levels.
	RemainingScaleInQty              float64                               `json:"remaining_scale_in_qty"`              // Truth-layer total quantity still outstanding across the current scale-in plan.
	ExecutedScaleInQty               float64                               `json:"executed_scale_in_qty"`               // Truth-layer cumulative executed quantity across the current scale-in plan.
	UserStreamReady                  bool                                  `json:"user_stream_ready"`                   // Runtime readiness flag for the Binance user-stream bridge.
	GeneratedAt                      time.Time                             `json:"generated_at"`                        // Response generation timestamp.
}

// BuildRuntimeProtectionAdjustmentPreview builds the read-only dynamic protection preview from store state.
// It never changes execution behavior; it only reads runtime state and returns a protection-adjustment snapshot.
func BuildRuntimeProtectionAdjustmentPreview(st *store.Store, traderID, selectedSymbol string, userStreamReady bool, marketPrice float64) (*ProtectionAdjustmentPreview, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	reconciler := NewProtectionStateReconciler(st, nil)
	symbol, side, err := reconciler.chooseScope(traderID, selectedSymbol)
	if err != nil {
		return nil, err
	}

	protectionSummary, err := st.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		return nil, err
	}

	var scaleOutSummary *store.ScaleOutPlanSummary
	if symbol != "" {
		if side != "" {
			scaleOutSummary, err = st.ScaleOutPlan().SummarizeForTraderSymbolSide(traderID, symbol, side)
		} else {
			scaleOutSummary, err = st.ScaleOutPlan().SummarizeForTraderSymbol(traderID, symbol)
		}
		if err != nil {
			return nil, err
		}
	}

	var scaleInSummary *store.ScaleInPlanSummary
	if symbol != "" {
		if side != "" {
			scaleInSummary, err = st.ScaleInPlan().SummarizeForTraderSymbolSide(traderID, symbol, side)
		} else {
			scaleInSummary, err = st.ScaleInPlan().SummarizeForTraderSymbol(traderID, symbol)
		}
		if err != nil {
			return nil, err
		}
	}

	var protectionGroup *store.ProtectionGroup
	if protectionSummary != nil && protectionSummary.ProtectionGroupID != "" {
		protectionGroup, err = st.ProtectionGroup().GetByKeys(traderID, protectionSummary.ProtectionGroupID, protectionSummary.LinkedPositionKey)
		if err != nil {
			return nil, err
		}
	}

	var eventLogs []*store.ProtectionAdjustmentEventLog
	if symbol != "" {
		eventLogs, err = st.ProtectionAdjustmentEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
		if err != nil {
			return nil, err
		}
	} else {
		eventLogs, err = st.ProtectionAdjustmentEventLog().ListRecentByTrader(traderID, 20)
		if err != nil {
			return nil, err
		}
	}

	var orderSummary *store.OrderRegistrySummary
	if symbol != "" {
		if side != "" {
			orderSummary, err = st.OrderRegistry().SummarizeForTraderSymbolSide(traderID, symbol, side)
		} else {
			orderSummary, err = st.OrderRegistry().SummarizeForTraderSymbol(traderID, symbol)
		}
		if err != nil {
			return nil, err
		}
	}
	if orderSummary == nil {
		orderSummary = &store.OrderRegistrySummary{}
	}

	var positionAggregate *store.PositionAggregate
	if symbol != "" && side != "" {
		positionAggregate, err = st.PositionAggregate().GetByTraderSymbolSide(traderID, symbol, side)
		if err != nil {
			return nil, err
		}
	}

	var trailingRule *store.TrailingRule
	if symbol != "" && side != "" {
		rules, ruleErr := st.TrailingRule().ListByTraderSymbolSide(traderID, symbol, side)
		if ruleErr != nil {
			return nil, ruleErr
		}
		trailingRule = chooseTrailingRule(rules, protectionSummary)
	}

	currentPnLPct := 0.0
	if positionAggregate != nil && marketPrice > 0 && positionAggregate.AvgEntryPrice > 0 {
		switch normalizeOneWaySide(positionAggregate.Side) {
		case "LONG":
			currentPnLPct = ((marketPrice - positionAggregate.AvgEntryPrice) / positionAggregate.AvgEntryPrice) * 100
		case "SHORT":
			currentPnLPct = ((positionAggregate.AvgEntryPrice - marketPrice) / positionAggregate.AvgEntryPrice) * 100
		}
	}

	guard := NewProtectionAdjustmentGuard()
	assessment, err := guard.Assess(&ProtectionAdjustmentGuardRequest{
		StrategyProfile:         mustLoadStrategyProfile(st, traderID),
		PositionAggregate:       positionAggregate,
		ProtectionSummary:       protectionSummary,
		OrderSummary:            orderSummary,
		TrailingRule:            trailingRule,
		ScaleOutSummary:         scaleOutSummary,
		ScaleInSummary:          scaleInSummary,
		ExecutionMode:           chooseProtectionAdjustmentExecutionMode(userStreamReady),
		AllowOrderPlacement:     userStreamReady,
		UserStreamReady:         userStreamReady,
		HasStateMismatch:        protectionSummary != nil && protectionSummary.HasStateMismatch,
		HasProtectionMismatch:   protectionSummary != nil && protectionSummary.ConsistencyStatus == "mismatch",
		HasPendingCancelReplace: orderSummary != nil && orderSummary.HasPendingCancelReplace,
		CurrentMarketPrice:      marketPrice,
		CurrentPnLPct:           currentPnLPct,
	})
	if err != nil {
		return nil, err
	}

	preview := &ProtectionAdjustmentPreview{
		TraderID:                         traderID,
		SelectedSymbol:                   symbol,
		Exchange:                         "binance_usdm",
		Mode:                             "one_way",
		ProtectionGroup:                  protectionGroup,
		ProtectionSummary:                protectionSummary,
		TrailingRule:                     trailingRule,
		ProtectionEventLogs:              eventLogs,
		OrderRegistrySummary:             orderSummary,
		ScaleOutSummary:                  scaleOutSummary,
		ScaleInSummary:                   scaleInSummary,
		PositionAggregate:                positionAggregate,
		HasProtection:                    protectionSummary != nil && protectionSummary.HasProtection,
		ProtectionMode:                   protectionSummaryMode(protectionSummary),
		ProtectionGroupStatus:            protectionSummaryGroupStatus(protectionSummary),
		ProtectionConsistency:            protectionSummaryConsistency(protectionSummary),
		ProtectionAdjustmentConsistency:  deriveProtectionAdjustmentConsistency(protectionSummary),
		HasStateMismatch:                 protectionSummaryValueBool(protectionSummary, func(s *store.ProtectionGroupSummary) bool { return s.HasStateMismatch }),
		MismatchReasons:                  protectionSummaryValueReasons(protectionSummary),
		HasWorkingOrders:                 protectionSummaryValueBool(protectionSummary, func(s *store.ProtectionGroupSummary) bool { return s.HasWorkingOrders }),
		HasPendingCancelReplace:          protectionSummaryValueBool(protectionSummary, func(s *store.ProtectionGroupSummary) bool { return s.HasPendingCancelReplace }),
		StopLossArmed:                    protectionSummaryValueBool(protectionSummary, func(s *store.ProtectionGroupSummary) bool { return s.StopLossArmed }),
		TakeProfitArmed:                  protectionSummaryValueBool(protectionSummary, func(s *store.ProtectionGroupSummary) bool { return s.TakeProfitArmed }),
		BreakEvenArmed:                   protectionSummaryValueBool(protectionSummary, func(s *store.ProtectionGroupSummary) bool { return s.BreakEvenArmed }),
		TrailingArmed:                    protectionSummaryValueBool(protectionSummary, func(s *store.ProtectionGroupSummary) bool { return s.TrailingArmed }),
		TrailingRuleID:                   protectionSummaryValue(protectionSummary, func(s *store.ProtectionGroupSummary) string { return s.TrailingRuleID }),
		TrailingAnchorPrice:              protectionSummaryValueFloat(protectionSummary, func(s *store.ProtectionGroupSummary) float64 { return s.TrailingAnchorPrice }),
		TrailingMoveCount:                protectionSummaryValueInt(protectionSummary, func(s *store.ProtectionGroupSummary) int { return s.TrailingMoveCount }),
		ProtectionRevision:               protectionSummaryValueInt(protectionSummary, func(s *store.ProtectionGroupSummary) int { return s.ProtectionRevision }),
		LastProtectionAction:             protectionSummaryValue(protectionSummary, func(s *store.ProtectionGroupSummary) string { return s.LastProtectionAction }),
		LastProtectionActionAt:           protectionSummaryValueTime(protectionSummary, func(s *store.ProtectionGroupSummary) time.Time { return s.LastProtectionActionAt }),
		InitialStopLossPrice:             protectionSummaryValueFloat(protectionSummary, func(s *store.ProtectionGroupSummary) float64 { return s.InitialStopLossPrice }),
		CurrentStopLossPrice:             protectionSummaryValueFloat(protectionSummary, func(s *store.ProtectionGroupSummary) float64 { return s.CurrentStopLossPrice }),
		InitialTakeProfitPrice:           protectionSummaryValueFloat(protectionSummary, func(s *store.ProtectionGroupSummary) float64 { return s.InitialTakeProfitPrice }),
		CurrentTakeProfitPrice:           protectionSummaryValueFloat(protectionSummary, func(s *store.ProtectionGroupSummary) float64 { return s.CurrentTakeProfitPrice }),
		CurrentMarketPrice:               marketPrice,
		CurrentPnLPct:                    currentPnLPct,
		RemainingMoveBudget:              assessment.RemainingMoveBudget,
		ProtectionAdjustmentBlockReasons: assessment.BlockedReasons,
		GuardAssessment:                  assessment,
		CanArmBreakEven:                  assessment.CanArmBreakEven,
		CanArmTrailing:                   assessment.CanArmTrailing,
		CanMoveStopLoss:                  assessment.CanMoveStopLoss,
		ProtectionAdjustmentStatus:       assessment.ProtectionAdjustmentStatus,
		HasScaleOutPlan:                  scaleOutSummary != nil && scaleOutSummary.HasScaleOutPlan,
		ScaleOutStatus:                   scaleOutPreviewStatus(scaleOutSummary),
		ScaleOutConsistency:              scaleOutPreviewConsistency(scaleOutSummary),
		HasScaleOutMismatch:              scaleOutSummary != nil && scaleOutSummary.HasStateMismatch,
		PendingScaleOutQty:               scaleOutPreviewPendingQty(scaleOutSummary),
		RemainingScaleOutQty:             scaleOutPreviewRemainingQty(scaleOutSummary),
		ExecutedScaleOutQty:              scaleOutPreviewExecutedQty(scaleOutSummary),
		HasScaleInPlan:                   scaleInSummary != nil && scaleInSummary.HasScaleInPlan,
		ScaleInStatus:                    scaleInPreviewStatus(scaleInSummary),
		ScaleInConsistency:               scaleInPreviewConsistency(scaleInSummary),
		HasScaleInMismatch:               scaleInSummary != nil && scaleInSummary.HasStateMismatch,
		PendingScaleInQty:                scaleInPreviewPendingQty(scaleInSummary),
		RemainingScaleInQty:              scaleInPreviewRemainingQty(scaleInSummary),
		ExecutedScaleInQty:               scaleInPreviewExecutedQty(scaleInSummary),
		UserStreamReady:                  userStreamReady,
		GeneratedAt:                      time.Now().UTC(),
	}
	preview.TruthSnapshotMetadata = populateRuntimeMetadata(preview.GeneratedAt, "", false, "")

	logger.Infof("protection adjustment preview built: trader=%s symbol=%s protection_revision=%d current_stop_loss_price=%.6f break_even_armed=%v trailing_armed=%v protection_adjustment_consistency=%s remaining_move_budget=%.6f has_pending_cancel_replace=%v",
		preview.TraderID, preview.SelectedSymbol, preview.ProtectionRevision, preview.CurrentStopLossPrice, preview.BreakEvenArmed, preview.TrailingArmed, preview.ProtectionAdjustmentConsistency, preview.RemainingMoveBudget, preview.HasPendingCancelReplace)

	return preview, nil
}

func chooseTrailingRule(rules []*store.TrailingRule, summary *store.ProtectionGroupSummary) *store.TrailingRule {
	if summary != nil {
		for _, rule := range rules {
			if rule == nil {
				continue
			}
			if strings.TrimSpace(rule.TrailingRuleID) == strings.TrimSpace(summary.TrailingRuleID) {
				return rule
			}
		}
	}
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(rule.Status), "armed") {
			return rule
		}
	}
	if len(rules) > 0 {
		return rules[0]
	}
	return nil
}

func chooseProtectionAdjustmentExecutionMode(userStreamReady bool) string {
	if userStreamReady {
		return "live"
	}
	return "readonly"
}

func calcProtectionMarketPnLPct(aggregate *store.PositionAggregate, marketPrice float64) float64 {
	if aggregate == nil || marketPrice <= 0 || aggregate.AvgEntryPrice <= 0 {
		return 0
	}
	switch normalizeOneWaySide(aggregate.Side) {
	case "LONG":
		return ((marketPrice - aggregate.AvgEntryPrice) / aggregate.AvgEntryPrice) * 100
	case "SHORT":
		return ((aggregate.AvgEntryPrice - marketPrice) / aggregate.AvgEntryPrice) * 100
	default:
		return 0
	}
}

func protectionAdjustmentPositionKey(symbol, side string) string {
	normalizedSymbol := strings.ToUpper(strings.TrimSpace(symbol))
	normalizedSide := normalizeOneWaySide(side)
	if normalizedSymbol == "" {
		return normalizedSide
	}
	if normalizedSide == "" {
		return normalizedSymbol
	}
	return normalizedSymbol + "|" + normalizedSide
}

func normalizedDynamicProtectionMode(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "fixed", "break_even", "trailing_segmented":
		return normalized
	default:
		return "fixed"
	}
}

func deriveProtectionAdjustmentConsistency(summary *store.ProtectionGroupSummary) string {
	if summary == nil || !summary.HasProtection {
		return "unknown"
	}

	consistency := store.NormalizeProtectionConsistencyStatus(summary.ConsistencyStatus)
	switch consistency {
	case "consistent", "mismatch", "pending":
		return consistency
	default:
		if summary.HasPendingCancelReplace || summary.ProtectionGroupStatus == "cancel_pending" {
			return "pending"
		}
		if summary.HasStateMismatch {
			return "mismatch"
		}
		return "consistent"
	}
}

func protectionMovePriceFromRule(currentStop float64, rule *store.TrailingRule, aggregate *store.PositionAggregate, marketPrice float64) float64 {
	if aggregate == nil || rule == nil {
		return currentStop
	}
	side := normalizeOneWaySide(aggregate.Side)
	switch strings.ToLower(strings.TrimSpace(rule.StepMoveType)) {
	case "price_level":
		if rule.StepMoveValue > 0 {
			return rule.StepMoveValue
		}
	case "price_pct":
		if currentStop > 0 && rule.StepMoveValue > 0 {
			if side == "LONG" {
				return currentStop * (1 + rule.StepMoveValue/100)
			}
			return currentStop * (1 - rule.StepMoveValue/100)
		}
	case "price_offset":
		if currentStop > 0 && rule.StepMoveValue > 0 {
			if side == "LONG" {
				return currentStop + rule.StepMoveValue
			}
			return currentStop - rule.StepMoveValue
		}
	}
	if marketPrice > 0 {
		if side == "LONG" {
			return math.Max(currentStop, marketPrice)
		}
		return math.Min(currentStop, marketPrice)
	}
	return currentStop
}

func protectionSummaryValue(summary *store.ProtectionGroupSummary, selector func(*store.ProtectionGroupSummary) string) string {
	if summary == nil || selector == nil {
		return ""
	}
	return selector(summary)
}

func protectionSummaryValueBool(summary *store.ProtectionGroupSummary, selector func(*store.ProtectionGroupSummary) bool) bool {
	if summary == nil || selector == nil {
		return false
	}
	return selector(summary)
}

func protectionSummaryValueFloat(summary *store.ProtectionGroupSummary, selector func(*store.ProtectionGroupSummary) float64) float64 {
	if summary == nil || selector == nil {
		return 0
	}
	return selector(summary)
}

func protectionSummaryValueInt(summary *store.ProtectionGroupSummary, selector func(*store.ProtectionGroupSummary) int) int {
	if summary == nil || selector == nil {
		return 0
	}
	return selector(summary)
}

func protectionSummaryValueTime(summary *store.ProtectionGroupSummary, selector func(*store.ProtectionGroupSummary) time.Time) time.Time {
	if summary == nil || selector == nil {
		return time.Time{}
	}
	return selector(summary)
}

func protectionSummaryValueReasons(summary *store.ProtectionGroupSummary) []store.ProtectionStateMismatchReason {
	if summary == nil || len(summary.MismatchReasons) == 0 {
		return []store.ProtectionStateMismatchReason{}
	}
	return summary.MismatchReasons
}

func protectionSummaryMode(summary *store.ProtectionGroupSummary) string {
	if summary == nil || strings.TrimSpace(summary.ProtectionMode) == "" {
		return "fixed"
	}
	return normalizedDynamicProtectionMode(summary.ProtectionMode)
}

func protectionSummaryGroupStatus(summary *store.ProtectionGroupSummary) string {
	if summary == nil {
		return "unknown"
	}
	status := strings.ToLower(strings.TrimSpace(summary.ProtectionGroupStatus))
	if status == "" {
		return "unknown"
	}
	return status
}

func protectionSummaryConsistency(summary *store.ProtectionGroupSummary) string {
	if summary == nil {
		return "unknown"
	}
	return store.NormalizeProtectionConsistencyStatus(summary.ConsistencyStatus)
}

func scaleOutPreviewStatus(summary *store.ScaleOutPlanSummary) string {
	if summary == nil {
		return "unknown"
	}
	return normalizeRuntimeScaleOutStatus(summary.ScaleOutStatus)
}

func scaleOutPreviewConsistency(summary *store.ScaleOutPlanSummary) string {
	if summary == nil {
		return "unknown"
	}
	return store.NormalizeScaleOutConsistencyStatus(summary.ConsistencyStatus)
}

func scaleOutPreviewPendingQty(summary *store.ScaleOutPlanSummary) float64 {
	if summary == nil {
		return 0
	}
	return summary.PendingScaleOutQty
}

func scaleOutPreviewRemainingQty(summary *store.ScaleOutPlanSummary) float64 {
	if summary == nil {
		return 0
	}
	return summary.RemainingScaleOutQty
}

func scaleOutPreviewExecutedQty(summary *store.ScaleOutPlanSummary) float64 {
	if summary == nil {
		return 0
	}
	return summary.ExecutedScaleOutQty
}

func scaleInPreviewStatus(summary *store.ScaleInPlanSummary) string {
	if summary == nil {
		return "unknown"
	}
	return normalizeRuntimeScaleInStatus(summary.ScaleInStatus)
}

func scaleInPreviewConsistency(summary *store.ScaleInPlanSummary) string {
	if summary == nil {
		return "unknown"
	}
	return store.NormalizeScaleInConsistencyStatus(summary.ConsistencyStatus)
}

func scaleInPreviewPendingQty(summary *store.ScaleInPlanSummary) float64 {
	if summary == nil {
		return 0
	}
	return summary.PendingScaleInQty
}

func scaleInPreviewRemainingQty(summary *store.ScaleInPlanSummary) float64 {
	if summary == nil {
		return 0
	}
	return summary.RemainingScaleInQty
}

func scaleInPreviewExecutedQty(summary *store.ScaleInPlanSummary) float64 {
	if summary == nil {
		return 0
	}
	return summary.ExecutedScaleInQty
}
