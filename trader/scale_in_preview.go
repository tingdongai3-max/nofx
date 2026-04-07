package trader

import (
	"fmt"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

// ScaleInReconcilePreview is the read-only scale-in truth preview returned by the backend.
// It combines the scale-in plan truth layer, linked order evidence, risk gate state, and reconcile status.
type ScaleInReconcilePreview struct {
	TruthSnapshotMetadata
	TraderID                         string                             `json:"trader_id"`                           // System-owned trader key.
	SelectedSymbol                   string                             `json:"selected_symbol,omitempty"`           // Selected symbol scope used for the preview.
	Exchange                         string                             `json:"exchange"`                            // Fixed phase-5 exchange scope: binance_usdm.
	Mode                             string                             `json:"mode"`                                // Fixed phase-5 mode scope: one_way.
	ScaleInPlan                      *store.ScaleInPlan                 `json:"scale_in_plan,omitempty"`             // Current scale-in plan truth row.
	ScaleInPlans                     []*store.ScaleInPlan               `json:"scale_in_plans"`                      // Current scale-in plan snapshot rows for debug.
	ScaleInLevels                    []*store.ScaleInPlanLevel          `json:"scale_in_levels"`                     // Current scale-in level rows for debug.
	ScaleInEventLogs                 []*store.ScaleInEventLog           `json:"scale_in_event_logs"`                 // Append-only raw scale-in evidence chain.
	OrderRegistrySummary             *store.OrderRegistrySummary        `json:"order_registry_summary,omitempty"`    // System-derived order truth summary for the selected scope.
	ProtectionSummary                *store.ProtectionGroupSummary      `json:"protection_summary,omitempty"`        // System-derived protection truth summary for the selected scope.
	PositionAggregate                *store.PositionAggregate           `json:"position_aggregate,omitempty"`        // System-owned position truth snapshot for the selected scope.
	RiskAssessment                   *ScaleInRiskAssessment             `json:"risk_assessment,omitempty"`           // Execution-facing add-position guard result; debug output, not a direct permit.
	ProtectionAdjustmentStatus       string                             `json:"protection_adjustment_status"`        // Dynamic protection status label.
	ProtectionAdjustmentConsistency  string                             `json:"protection_adjustment_consistency"`   // Dynamic protection consistency enum: consistent, mismatch, pending, or unknown.
	ProtectionAdjustmentBlockReasons []CapabilityReason                 `json:"protection_adjustment_block_reasons"` // Machine-readable dynamic-protection block reasons.
	ProtectionRevision               int                                `json:"protection_revision"`                 // System-owned protection revision snapshot.
	CurrentStopLossPrice             float64                            `json:"current_stop_loss_price"`             // Current stop-loss trigger price from the protection group.
	InitialStopLossPrice             float64                            `json:"initial_stop_loss_price"`             // Initial stop-loss trigger price before any protection movement.
	BreakEvenArmed                   bool                               `json:"break_even_armed"`                    // System-derived break-even flag.
	TrailingArmed                    bool                               `json:"trailing_armed"`                      // System-derived trailing flag.
	RemainingMoveBudget              float64                            `json:"remaining_move_budget"`               // Remaining dynamic-protection move budget.
	HasScaleInPlan                   bool                               `json:"has_scale_in_plan"`                   // Truth-layer flag showing whether a scale-in plan exists.
	ScaleInStatus                    string                             `json:"scale_in_status"`                     // Current scale-in plan truth status.
	ScaleInConsistency               string                             `json:"scale_in_consistency"`                // Scale-in consistency enum: consistent, mismatch, pending, or unknown.
	HasStateMismatch                 bool                               `json:"has_state_mismatch"`                  // Reconcile result flag; true means truth layers disagree.
	MismatchReasons                  []store.ScaleInStateMismatchReason `json:"mismatch_reasons"`                    // Machine-readable mismatch explanation list.
	HasWorkingOrders                 bool                               `json:"has_working_orders"`                  // Truth-layer flag derived from linked working scale-in rows.
	PendingAddQty                    float64                            `json:"pending_add_qty"`                     // Truth-layer pending add quantity from the order registry summary.
	PendingScaleInQty                float64                            `json:"pending_scale_in_qty"`                // Truth-layer working-order reserve for the active scale-in plan.
	RemainingScaleInQty              float64                            `json:"remaining_scale_in_qty"`              // Truth-layer total quantity still outstanding across the current plan.
	ExecutedScaleInQty               float64                            `json:"executed_scale_in_qty"`               // Truth-layer cumulative executed quantity across the current plan.
	ScaleInCount                     int                                `json:"scale_in_count"`                      // Truth-layer executed add-attempt count from the plan snapshot.
	ScaleInBlockReasons              []CapabilityReason                 `json:"scale_in_block_reasons"`              // Capability-style reasons used by the UI and resolver preview.
	RiskBudgetRemaining              float64                            `json:"risk_budget_remaining"`               // Remaining notional risk budget under the strategy profile.
	UserStreamReady                  bool                               `json:"user_stream_ready"`                   // Runtime readiness flag for the Binance user-stream bridge.
	GeneratedAt                      time.Time                          `json:"generated_at"`                        // Response generation timestamp.
}

// BuildRuntimeScaleInPreview builds the read-only scale-in preview from store state and the current risk guard.
// It never changes execution behavior; it only reads runtime state and returns a scale-in snapshot.
func BuildRuntimeScaleInPreview(st *store.Store, traderID, selectedSymbol string, userStreamReady bool, accountEquity float64) (*ScaleInReconcilePreview, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	plans, err := st.ScaleInPlan().ListByTraderID(traderID)
	if err != nil {
		return nil, err
	}
	protectionAdjustmentPreview, adjustmentErr := BuildRuntimeProtectionAdjustmentPreview(st, traderID, selectedSymbol, userStreamReady, 0)
	if adjustmentErr != nil {
		logger.Infof("protection adjustment preview fallback for scale-in trader %s: %v", traderID, adjustmentErr)
	}

	symbol := strings.ToUpper(strings.TrimSpace(selectedSymbol))
	side := ""
	if symbol == "" {
		symbol, side = chooseScaleInPreviewScope(plans)
	} else {
		symbol, side = chooseScaleInPreviewScope(filterScaleInPlansBySymbol(plans, symbol))
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
			protectionSummary = chooseScaleInProtectionSummary(summaries)
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

	summary, err := st.ScaleInPlan().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		return nil, err
	}

	levels := []*store.ScaleInPlanLevel{}
	if summary.ScaleInPlanID != "" {
		levels, err = st.ScaleInPlanLevel().ListByPlanID(summary.ScaleInPlanID)
		if err != nil {
			return nil, err
		}
	}

	eventLogs := []*store.ScaleInEventLog{}
	if symbol != "" {
		eventLogs, err = st.ScaleInEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
		if err != nil {
			return nil, err
		}
	} else {
		eventLogs, err = st.ScaleInEventLog().ListRecentByTrader(traderID, 20)
		if err != nil {
			return nil, err
		}
	}

	riskGuard := NewScaleInRiskGuard()
	assessment, err := riskGuard.Assess(&ScaleInRiskRequest{
		StrategyProfile:         mustLoadStrategyProfile(st, traderID),
		PositionAggregate:       positionAggregate,
		ScaleInSummary:          summary,
		OrderSummary:            orderSummary,
		ExecutionMode:           chooseScaleInExecutionMode(userStreamReady),
		AllowOrderPlacement:     userStreamReady,
		UserStreamReady:         userStreamReady,
		HasStateMismatch:        summary.HasStateMismatch,
		HasProtectionMismatch:   protectionSummary != nil && protectionSummary.ConsistencyStatus == "mismatch",
		HasPendingCancelReplace: orderSummary != nil && orderSummary.HasPendingCancelReplace,
		AccountEquity:           accountEquity,
	})
	if err != nil {
		return nil, err
	}

	preview := &ScaleInReconcilePreview{
		TraderID:                         traderID,
		SelectedSymbol:                   symbol,
		Exchange:                         "binance_usdm",
		Mode:                             "one_way",
		ScaleInPlan:                      chooseScaleInCurrentPlan(plans, summary.ScaleInPlanID),
		ScaleInPlans:                     plans,
		ScaleInLevels:                    levels,
		ScaleInEventLogs:                 eventLogs,
		OrderRegistrySummary:             orderSummary,
		ProtectionSummary:                protectionSummary,
		PositionAggregate:                positionAggregate,
		RiskAssessment:                   assessment,
		ProtectionAdjustmentStatus:       pickProtectionAdjustmentStatus(protectionAdjustmentPreview),
		ProtectionAdjustmentConsistency:  pickProtectionAdjustmentConsistency(protectionAdjustmentPreview),
		ProtectionAdjustmentBlockReasons: pickProtectionAdjustmentBlockReasons(protectionAdjustmentPreview),
		ProtectionRevision:               pickProtectionAdjustmentRevision(protectionAdjustmentPreview),
		CurrentStopLossPrice:             pickProtectionAdjustmentCurrentStop(protectionAdjustmentPreview),
		InitialStopLossPrice:             pickProtectionAdjustmentInitialStop(protectionAdjustmentPreview),
		BreakEvenArmed:                   pickProtectionAdjustmentBreakEven(protectionAdjustmentPreview),
		TrailingArmed:                    pickProtectionAdjustmentTrailing(protectionAdjustmentPreview),
		RemainingMoveBudget:              pickProtectionAdjustmentRemainingBudget(protectionAdjustmentPreview),
		HasScaleInPlan:                   summary.HasScaleInPlan,
		ScaleInStatus:                    summary.ScaleInStatus,
		ScaleInConsistency:               summary.ConsistencyStatus,
		HasStateMismatch:                 summary.HasStateMismatch,
		MismatchReasons:                  summary.MismatchReasons,
		HasWorkingOrders:                 summary.HasWorkingOrders,
		PendingAddQty:                    orderSummary.PendingAddQty,
		PendingScaleInQty:                summary.PendingScaleInQty,
		RemainingScaleInQty:              summary.RemainingScaleInQty,
		ExecutedScaleInQty:               summary.ExecutedScaleInQty,
		ScaleInCount:                     summary.CurrentScaleInCount,
		ScaleInBlockReasons:              assessment.BlockedReasons,
		RiskBudgetRemaining:              assessment.RiskBudgetRemaining,
		UserStreamReady:                  userStreamReady,
		GeneratedAt:                      time.Now().UTC(),
	}
	preview.TruthSnapshotMetadata = populateRuntimeMetadata(preview.GeneratedAt, "", false, "")

	logger.Infof("scale in preview built: trader=%s symbol=%s scale_in_status=%s scale_in_consistency=%s protection_adjustment_status=%s protection_adjustment_consistency=%s pending_scale_in_qty=%.6f remaining_scale_in_qty=%.6f risk_budget_remaining=%.6f",
		preview.TraderID, preview.SelectedSymbol, preview.ScaleInStatus, preview.ScaleInConsistency, preview.ProtectionAdjustmentStatus, preview.ProtectionAdjustmentConsistency, preview.PendingScaleInQty, preview.RemainingScaleInQty, preview.RiskBudgetRemaining)

	return preview, nil
}

func chooseScaleInPreviewScope(plans []*store.ScaleInPlan) (string, string) {
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(plan.Symbol))
		if symbol == "" {
			continue
		}
		return symbol, normalizeScaleInPreviewSide(plan.Side)
	}
	return "", ""
}

func filterScaleInPlansBySymbol(plans []*store.ScaleInPlan, symbol string) []*store.ScaleInPlan {
	normalizedSymbol := normalizeScaleInPreviewSymbol(symbol)
	if normalizedSymbol == "" {
		return plans
	}
	filtered := make([]*store.ScaleInPlan, 0, len(plans))
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		if strings.EqualFold(normalizeScaleInPreviewSymbol(plan.Symbol), normalizedSymbol) {
			filtered = append(filtered, plan)
		}
	}
	return filtered
}

func chooseScaleInCurrentPlan(plans []*store.ScaleInPlan, planID string) *store.ScaleInPlan {
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
		if strings.TrimSpace(plan.ScaleInPlanID) == normalizedPlanID {
			return plan
		}
	}
	return nil
}

func chooseScaleInProtectionSummary(summaries []*store.ProtectionGroupSummary) *store.ProtectionGroupSummary {
	for _, summary := range summaries {
		if summary != nil {
			return summary
		}
	}
	return &store.ProtectionGroupSummary{ConsistencyStatus: "unknown", MismatchReasons: []store.ProtectionStateMismatchReason{}}
}

func mustLoadStrategyProfile(st *store.Store, traderID string) *store.StrategyProfile {
	if st == nil || traderID == "" {
		return nil
	}
	profile, err := st.StrategyProfile().GetByTraderID(traderID)
	if err != nil {
		return nil
	}
	return profile
}

func chooseScaleInExecutionMode(userStreamReady bool) string {
	if userStreamReady {
		return "live"
	}
	return "readonly"
}

func normalizeScaleInPreviewSide(side string) string {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "BUY", "LONG":
		return "LONG"
	case "SELL", "SHORT":
		return "SHORT"
	default:
		return ""
	}
}

func normalizeScaleInPreviewSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}
