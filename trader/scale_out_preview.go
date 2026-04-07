package trader

import (
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

// ScaleOutReconcilePreview is the read-only scale-out truth preview returned by the backend.
// It combines the scale-out plan truth layer, linked order evidence, protection coverage, and reconcile status.
type ScaleOutReconcilePreview struct {
	TruthSnapshotMetadata
	TraderID                         string                              `json:"trader_id"`                           // System-owned trader key.
	SelectedSymbol                   string                              `json:"selected_symbol,omitempty"`           // Selected symbol scope used for the preview.
	Exchange                         string                              `json:"exchange"`                            // Fixed phase-4 exchange scope: binance_usdm.
	Mode                             string                              `json:"mode"`                                // Fixed phase-4 mode scope: one_way.
	ScaleOutPlan                     *store.ScaleOutPlan                 `json:"scale_out_plan,omitempty"`            // Current scale-out plan truth row.
	ScaleOutPlans                    []*store.ScaleOutPlan               `json:"scale_out_plans"`                     // Current scale-out plan snapshot rows for debug.
	ScaleOutLevels                   []*store.ScaleOutPlanLevel          `json:"scale_out_levels"`                    // Current scale-out level rows for debug.
	ScaleOutEventLogs                []*store.ScaleOutEventLog           `json:"scale_out_event_logs"`                // Append-only raw scale-out evidence chain.
	OrderRegistrySummary             *store.OrderRegistrySummary         `json:"order_registry_summary,omitempty"`    // System-derived order truth summary for the selected scope.
	ProtectionSummary                *store.ProtectionGroupSummary       `json:"protection_summary,omitempty"`        // System-derived protection truth summary for the selected scope.
	PositionAggregate                *store.PositionAggregate            `json:"position_aggregate,omitempty"`        // System-owned position truth snapshot for the selected scope.
	ProtectionAdjustmentStatus       string                              `json:"protection_adjustment_status"`        // Dynamic protection status label.
	ProtectionAdjustmentConsistency  string                              `json:"protection_adjustment_consistency"`   // Dynamic protection consistency enum: consistent, mismatch, pending, or unknown.
	ProtectionAdjustmentBlockReasons []CapabilityReason                  `json:"protection_adjustment_block_reasons"` // Machine-readable dynamic-protection block reasons.
	ProtectionRevision               int                                 `json:"protection_revision"`                 // System-owned protection revision snapshot.
	CurrentStopLossPrice             float64                             `json:"current_stop_loss_price"`             // Current stop-loss trigger price from the protection group.
	InitialStopLossPrice             float64                             `json:"initial_stop_loss_price"`             // Initial stop-loss trigger price before any protection movement.
	BreakEvenArmed                   bool                                `json:"break_even_armed"`                    // System-derived break-even flag.
	TrailingArmed                    bool                                `json:"trailing_armed"`                      // System-derived trailing flag.
	RemainingMoveBudget              float64                             `json:"remaining_move_budget"`               // Remaining dynamic-protection move budget.
	HasScaleOutPlan                  bool                                `json:"has_scale_out_plan"`                  // System-derived flag that an active plan exists.
	ScaleOutStatus                   string                              `json:"scale_out_status"`                    // Current scale-out plan truth status.
	ScaleOutConsistency              string                              `json:"scale_out_consistency"`               // Scale-out consistency enum: consistent, mismatch, pending, or unknown.
	HasStateMismatch                 bool                                `json:"has_state_mismatch"`                  // Reconcile result flag; true means truth layers disagree.
	MismatchReasons                  []store.ScaleOutStateMismatchReason `json:"mismatch_reasons"`                    // Machine-readable mismatch explanation list.
	HasWorkingOrders                 bool                                `json:"has_working_orders"`                  // Truth-layer flag derived from linked working scale-out rows.
	PendingAddQty                    float64                             `json:"pending_add_qty"`                     // Truth-layer pending add quantity from the order truth layer; this is a separate conflict signal, not a scale-out reserve.
	PendingScaleOutQty               float64                             `json:"pending_scale_out_qty"`               // Truth-layer working-order reserve for active scale-out levels; this is a subset of RemainingScaleOutQty.
	RemainingScaleOutQty             float64                             `json:"remaining_scale_out_qty"`             // Truth-layer total quantity still outstanding across the current plan, including working and not-yet-working levels.
	ExecutedScaleOutQty              float64                             `json:"executed_scale_out_qty"`              // Truth-layer cumulative executed quantity across the plan.
	ScaleOutBlockReasons             []CapabilityReason                  `json:"scale_out_block_reasons"`             // Capability-style reasons used by the UI and resolver preview.
	ProtectionRebalanced             bool                                `json:"protection_rebalanced"`               // Derived flag showing whether ProtectionGroup.ProtectedQuantity matches the current remaining position size.
	UserStreamReady                  bool                                `json:"user_stream_ready"`                   // Runtime readiness flag for the Binance user-stream bridge.
	GeneratedAt                      time.Time                           `json:"generated_at"`                        // Response generation timestamp.
}

// BuildRuntimeScaleOutPreview builds the read-only scale-out preview from store state.
// It never changes execution behavior; it only reads runtime state and returns a scale-out snapshot.
func BuildRuntimeScaleOutPreview(st *store.Store, traderID, selectedSymbol string, userStreamReady bool) (*ScaleOutReconcilePreview, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	plans, err := st.ScaleOutPlan().ListByTraderID(traderID)
	if err != nil {
		return nil, err
	}
	protectionAdjustmentPreview, adjustmentErr := BuildRuntimeProtectionAdjustmentPreview(st, traderID, selectedSymbol, userStreamReady, 0)
	if adjustmentErr != nil {
		logger.Infof("protection adjustment preview fallback for scale-out trader %s: %v", traderID, adjustmentErr)
	}

	symbol := strings.ToUpper(strings.TrimSpace(selectedSymbol))
	side := ""
	if symbol == "" {
		symbol, side = chooseScaleOutPreviewScope(plans)
	} else {
		symbol, side = chooseScaleOutPreviewScope(filterScaleOutPlansBySymbol(plans, symbol))
	}

	orderSummary := &store.OrderRegistrySummary{}
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

	protectionSummary := &store.ProtectionGroupSummary{}
	if symbol != "" {
		if side != "" {
			protectionSummary, err = st.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, side)
		} else {
			summaries, summaryErr := st.ProtectionGroup().SummarizeForTraderSymbol(traderID, symbol)
			if summaryErr != nil {
				return nil, summaryErr
			}
			protectionSummary = chooseScaleOutProtectionSummary(summaries)
			err = nil
		}
		if err != nil {
			return nil, err
		}
	}

	var positionAggregate *store.PositionAggregate
	if symbol != "" && side != "" {
		positionAggregate, err = st.PositionAggregate().GetByTraderSymbolSide(traderID, symbol, side)
		if err != nil {
			return nil, err
		}
	}

	summary, err := st.ScaleOutPlan().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		return nil, err
	}

	levels := []*store.ScaleOutPlanLevel{}
	if summary.ScaleOutPlanID != "" {
		levels, err = st.ScaleOutPlanLevel().ListByPlanID(summary.ScaleOutPlanID)
		if err != nil {
			return nil, err
		}
	}

	eventLogs := []*store.ScaleOutEventLog{}
	if symbol != "" {
		eventLogs, err = st.ScaleOutEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
		if err != nil {
			return nil, err
		}
	} else {
		eventLogs, err = st.ScaleOutEventLog().ListRecentByTrader(traderID, 20)
		if err != nil {
			return nil, err
		}
	}

	preview := &ScaleOutReconcilePreview{
		TraderID:                         traderID,
		SelectedSymbol:                   symbol,
		Exchange:                         "binance_usdm",
		Mode:                             "one_way",
		ScaleOutPlan:                     chooseScaleOutCurrentPlan(plans, summary.ScaleOutPlanID),
		ScaleOutPlans:                    plans,
		ScaleOutLevels:                   levels,
		ScaleOutEventLogs:                eventLogs,
		OrderRegistrySummary:             orderSummary,
		ProtectionSummary:                protectionSummary,
		PositionAggregate:                positionAggregate,
		ProtectionAdjustmentStatus:       pickProtectionAdjustmentStatus(protectionAdjustmentPreview),
		ProtectionAdjustmentConsistency:  pickProtectionAdjustmentConsistency(protectionAdjustmentPreview),
		ProtectionAdjustmentBlockReasons: pickProtectionAdjustmentBlockReasons(protectionAdjustmentPreview),
		ProtectionRevision:               pickProtectionAdjustmentRevision(protectionAdjustmentPreview),
		CurrentStopLossPrice:             pickProtectionAdjustmentCurrentStop(protectionAdjustmentPreview),
		InitialStopLossPrice:             pickProtectionAdjustmentInitialStop(protectionAdjustmentPreview),
		BreakEvenArmed:                   pickProtectionAdjustmentBreakEven(protectionAdjustmentPreview),
		TrailingArmed:                    pickProtectionAdjustmentTrailing(protectionAdjustmentPreview),
		RemainingMoveBudget:              pickProtectionAdjustmentRemainingBudget(protectionAdjustmentPreview),
		HasScaleOutPlan:                  summary.HasScaleOutPlan,
		ScaleOutStatus:                   summary.ScaleOutStatus,
		ScaleOutConsistency:              summary.ConsistencyStatus,
		HasStateMismatch:                 summary.HasStateMismatch,
		MismatchReasons:                  summary.MismatchReasons,
		HasWorkingOrders:                 summary.HasWorkingOrders,
		PendingAddQty:                    orderSummary.PendingAddQty,
		PendingScaleOutQty:               summary.PendingScaleOutQty,
		RemainingScaleOutQty:             summary.RemainingScaleOutQty,
		ExecutedScaleOutQty:              summary.ExecutedScaleOutQty,
		ScaleOutBlockReasons:             buildScaleOutBlockReasonsFromSummary(summary, positionAggregate != nil && positionAggregate.TotalQty > 0),
		ProtectionRebalanced:             isProtectionRebalanced(positionAggregate, protectionSummary),
		UserStreamReady:                  userStreamReady,
		GeneratedAt:                      time.Now().UTC(),
	}
	preview.TruthSnapshotMetadata = populateRuntimeMetadata(preview.GeneratedAt, "", false, "")

	logger.Infof("scale out preview built: trader=%s symbol=%s scale_out_status=%s scale_out_consistency=%s protection_adjustment_status=%s protection_adjustment_consistency=%s pending_scale_out_qty=%.6f remaining_scale_out_qty=%.6f protection_rebalanced=%v",
		preview.TraderID, preview.SelectedSymbol, preview.ScaleOutStatus, preview.ScaleOutConsistency, preview.ProtectionAdjustmentStatus, preview.ProtectionAdjustmentConsistency, preview.PendingScaleOutQty, preview.RemainingScaleOutQty, preview.ProtectionRebalanced)

	return preview, nil
}

func chooseScaleOutPreviewScope(plans []*store.ScaleOutPlan) (string, string) {
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(plan.Symbol))
		if symbol == "" {
			continue
		}
		return symbol, normalizeScaleOutPreviewSide(plan.Side)
	}
	return "", ""
}

func filterScaleOutPlansBySymbol(plans []*store.ScaleOutPlan, symbol string) []*store.ScaleOutPlan {
	normalizedSymbol := normalizeScaleOutPreviewSymbol(symbol)
	if normalizedSymbol == "" {
		return plans
	}
	filtered := make([]*store.ScaleOutPlan, 0, len(plans))
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		if strings.EqualFold(normalizeScaleOutPreviewSymbol(plan.Symbol), normalizedSymbol) {
			filtered = append(filtered, plan)
		}
	}
	return filtered
}

func chooseScaleOutCurrentPlan(plans []*store.ScaleOutPlan, planID string) *store.ScaleOutPlan {
	normalizedPlanID := strings.TrimSpace(planID)
	if normalizedPlanID == "" {
		for _, plan := range plans {
			if plan != nil {
				return plan
			}
		}
		return nil
	}

	for _, plan := range plans {
		if plan == nil {
			continue
		}
		if strings.TrimSpace(plan.ScaleOutPlanID) == normalizedPlanID {
			return plan
		}
	}
	return nil
}

func chooseScaleOutProtectionSummary(summaries []*store.ProtectionGroupSummary) *store.ProtectionGroupSummary {
	for _, summary := range summaries {
		if summary != nil {
			return summary
		}
	}
	return &store.ProtectionGroupSummary{ConsistencyStatus: "unknown", MismatchReasons: []store.ProtectionStateMismatchReason{}}
}

func buildScaleOutBlockReasonsFromSummary(summary *store.ScaleOutPlanSummary, hasPosition bool) []CapabilityReason {
	reasons := make([]CapabilityReason, 0)
	if !hasPosition {
		reasons = append(reasons, CapabilityReason{
			Action:   "reduce_position",
			Category: "position",
			Reason:   "no open position exists on the selected symbol",
		})
		reasons = append(reasons, CapabilityReason{
			Action:   "arm_partial_take_profit",
			Category: "position",
			Reason:   "no open position exists on the selected symbol",
		})
		return reasons
	}

	if summary == nil {
		reasons = append(reasons, CapabilityReason{
			Action:   "reduce_position",
			Category: "scale_out",
			Reason:   "scale-out state is unavailable",
		})
		reasons = append(reasons, CapabilityReason{
			Action:   "arm_partial_take_profit",
			Category: "scale_out",
			Reason:   "scale-out state is unavailable",
		})
		return reasons
	}

	consistency := store.NormalizeScaleOutConsistencyStatus(summary.ConsistencyStatus)
	switch consistency {
	case "mismatch":
		reasons = append(reasons, CapabilityReason{
			Action:   "reduce_position",
			Category: "scale_out",
			Reason:   "current scale-out truth is mismatched",
		})
		reasons = append(reasons, CapabilityReason{
			Action:   "arm_partial_take_profit",
			Category: "scale_out",
			Reason:   "current scale-out truth is mismatched",
		})
	case "pending":
		if summary.HasScaleOutPlan {
			reasons = append(reasons, CapabilityReason{
				Action:   "arm_partial_take_profit",
				Category: "scale_out",
				Reason:   fmt.Sprintf("scale-out plan %s is already active", summary.ScaleOutStatus),
			})
		} else if summary.ScaleOutStatus == "completed" {
			reasons = append(reasons, CapabilityReason{
				Action:   "reduce_position",
				Category: "scale_out",
				Reason:   "scale-out plan is already completed",
			})
		}
	}

	if summary.ScaleOutStatus == "invalid" {
		reasons = append(reasons, CapabilityReason{
			Action:   "reduce_position",
			Category: "scale_out",
			Reason:   "scale-out plan is invalid",
		})
		reasons = append(reasons, CapabilityReason{
			Action:   "arm_partial_take_profit",
			Category: "scale_out",
			Reason:   "scale-out plan is invalid",
		})
	}

	return dedupeCapabilityReasons(reasons)
}

func isProtectionRebalanced(positionAggregate *store.PositionAggregate, protectionSummary *store.ProtectionGroupSummary) bool {
	if positionAggregate == nil || protectionSummary == nil {
		return false
	}
	if !protectionSummary.HasProtection {
		return false
	}
	return math.Abs(protectionSummary.ProtectedQuantity-positionAggregate.TotalQty) < 0.000001
}

func dedupeCapabilityReasons(reasons []CapabilityReason) []CapabilityReason {
	if len(reasons) == 0 {
		return reasons
	}

	seen := make(map[string]struct{}, len(reasons))
	deduped := make([]CapabilityReason, 0, len(reasons))
	for _, reason := range reasons {
		key := reason.Action + "|" + reason.Category + "|" + reason.Reason
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, reason)
	}
	return deduped
}

func normalizeScaleOutPreviewSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func normalizeScaleOutPreviewSide(side string) string {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "BUY", "LONG":
		return "LONG"
	case "SELL", "SHORT":
		return "SHORT"
	default:
		return strings.ToUpper(strings.TrimSpace(side))
	}
}
