package trader

import (
	"fmt"
	"strings"

	"nofx/logger"
	"nofx/store"
)

// ProtectionAdjustmentGuardRequest is the execution-facing input for dynamic protection gating.
// It is a runtime guard input, not a persisted truth row.
type ProtectionAdjustmentGuardRequest struct {
	StrategyProfile         *store.StrategyProfile        // Strategy-layer authorization snapshot; required for execution decisions.
	PositionAggregate       *store.PositionAggregate      // System-owned position truth snapshot; required for execution decisions.
	ProtectionSummary       *store.ProtectionGroupSummary // Protection truth snapshot; required for move/break-even/trailing gating.
	OrderSummary            *store.OrderRegistrySummary   // Order truth snapshot; used to detect pending-order conflicts.
	TrailingRule            *store.TrailingRule           // Trailing-rule truth snapshot; required for dynamic protection activation.
	ScaleOutSummary         *store.ScaleOutPlanSummary    // Scale-out truth snapshot; used to block conflicting overlap.
	ScaleInSummary          *store.ScaleInPlanSummary     // Scale-in truth snapshot; used to block conflicting overlap.
	ExecutionMode           string                        // Runtime execution mode used for gating only.
	AllowOrderPlacement     bool                          // Runtime order-placement gate used for gating only.
	UserStreamReady         bool                          // User-stream readiness gate used for gating only.
	HasStateMismatch        bool                          // Order-truth mismatch flag; when true, the guard blocks execution.
	HasProtectionMismatch   bool                          // Protection-truth mismatch flag; when true, the guard blocks execution.
	HasPendingCancelReplace bool                          // Pending cancel_replace flag; when true, the guard blocks execution.
	CurrentMarketPrice      float64                       // Current market price used for trigger evaluation and debug output.
	CurrentPnLPct           float64                       // Current unrealized PnL percent used for trigger evaluation and debug output.
}

// ProtectionAdjustmentGuardAssessment is the execution-facing result produced by the dynamic protection guard.
// It is a guard result, not a truth-layer snapshot.
type ProtectionAdjustmentGuardAssessment struct {
	Allowed                         bool               `json:"allowed"`                           // Runtime permit emitted by the guard.
	BlockedReasons                  []CapabilityReason `json:"blocked_reasons"`                   // Machine-readable explanations for every blocked branch.
	CanArmBreakEven                 bool               `json:"can_arm_break_even"`                // True when break-even can be armed right now.
	CanArmTrailing                  bool               `json:"can_arm_trailing"`                  // True when segmented trailing can be armed right now.
	CanMoveStopLoss                 bool               `json:"can_move_stop_loss"`                // True when the current stop-loss may be moved right now.
	RemainingMoveBudget             float64            `json:"remaining_move_budget"`             // Remaining stop-loss move budget under the current rule.
	CurrentStopLossPrice            float64            `json:"current_stop_loss_price"`           // System-derived current stop-loss price.
	InitialStopLossPrice            float64            `json:"initial_stop_loss_price"`           // System-derived initial stop-loss price.
	ProtectionRevision              int                `json:"protection_revision"`               // System-owned protection revision snapshot.
	ProtectionGroupStatus           string             `json:"protection_group_status"`           // Current protection lifecycle status.
	ProtectionConsistency           string             `json:"protection_consistency"`            // Protection consistency enum: consistent, mismatch, pending, or unknown.
	ProtectionAdjustmentConsistency string             `json:"protection_adjustment_consistency"` // Dynamic-protection consistency enum: consistent, mismatch, pending, or unknown.
	ProtectionAdjustmentStatus      string             `json:"protection_adjustment_status"`      // Dynamic protection status label used for debug/UI review.
	HasPendingCancelReplace         bool               `json:"has_pending_cancel_replace"`        // Runtime truth flag for a pending cancel/replace flow.
	TrailingRuleID                  string             `json:"trailing_rule_id"`                  // System-owned trailing-rule identifier.
}

// ProtectionAdjustmentGuard evaluates whether dynamic protection actions are allowed in this runtime cycle.
// It is a standalone guard so the resolver and execution path do not duplicate trigger and safety checks.
type ProtectionAdjustmentGuard struct{}

// NewProtectionAdjustmentGuard creates a new dynamic protection guard.
func NewProtectionAdjustmentGuard() *ProtectionAdjustmentGuard {
	return &ProtectionAdjustmentGuard{}
}

// Assess returns the dynamic protection guard result for the current cycle.
func (g *ProtectionAdjustmentGuard) Assess(req *ProtectionAdjustmentGuardRequest) (*ProtectionAdjustmentGuardAssessment, error) {
	if req == nil {
		return nil, fmt.Errorf("protection adjustment request is required")
	}

	assessment := &ProtectionAdjustmentGuardAssessment{
		BlockedReasons: []CapabilityReason{},
	}

	addReason := func(action, category, reason string) {
		if reason == "" {
			return
		}
		for _, existing := range assessment.BlockedReasons {
			if existing.Action == action && existing.Category == category && existing.Reason == reason {
				return
			}
		}
		assessment.BlockedReasons = append(assessment.BlockedReasons, CapabilityReason{
			Action:   action,
			Category: category,
			Reason:   reason,
		})
		logger.Infof("protection adjustment blocked by guard: symbol=%s action=%s category=%s reason=%s",
			selectedProtectionAdjustmentSymbol(req.PositionAggregate), action, category, reason)
	}

	if req.StrategyProfile == nil {
		addReason("move_stop_loss", "profile", "strategy profile is missing")
		addReason("set_break_even_stop", "profile", "strategy profile is missing")
		addReason("set_trailing_protection", "profile", "strategy profile is missing")
		assessment.Allowed = false
		return assessment, nil
	}

	if !strings.EqualFold(req.StrategyProfile.Exchange, "binance_usdm") || !strings.EqualFold(req.StrategyProfile.Mode, "one_way") {
		addReason("move_stop_loss", "scope", "strategy profile is outside the binance_usdm one_way scope")
		addReason("set_break_even_stop", "scope", "strategy profile is outside the binance_usdm one_way scope")
		addReason("set_trailing_protection", "scope", "strategy profile is outside the binance_usdm one_way scope")
	}
	if !req.StrategyProfile.ExecutionEnabled {
		addReason("move_stop_loss", "profile", "strategy profile is not execution-enabled")
		addReason("set_break_even_stop", "profile", "strategy profile is not execution-enabled")
		addReason("set_trailing_protection", "profile", "strategy profile is not execution-enabled")
	}
	if strings.TrimSpace(strings.ToLower(req.ExecutionMode)) != "live" {
		addReason("move_stop_loss", "mode", "current execution mode is not live")
		addReason("set_break_even_stop", "mode", "current execution mode is not live")
		addReason("set_trailing_protection", "mode", "current execution mode is not live")
	}
	if !req.AllowOrderPlacement {
		addReason("move_stop_loss", "mode", "current runtime does not allow order placement")
		addReason("set_break_even_stop", "mode", "current runtime does not allow order placement")
		addReason("set_trailing_protection", "mode", "current runtime does not allow order placement")
	}
	if !req.UserStreamReady {
		addReason("move_stop_loss", "stream", "current user stream is not ready")
		addReason("set_break_even_stop", "stream", "current user stream is not ready")
		addReason("set_trailing_protection", "stream", "current user stream is not ready")
	}
	if req.HasPendingCancelReplace {
		addReason("move_stop_loss", "pending", "current cancel_replace is not completed")
		addReason("set_break_even_stop", "pending", "current cancel_replace is not completed")
		addReason("set_trailing_protection", "pending", "current cancel_replace is not completed")
	}
	if req.HasStateMismatch {
		addReason("move_stop_loss", "order_state", "current order state is not consistent")
		addReason("set_break_even_stop", "order_state", "current order state is not consistent")
		addReason("set_trailing_protection", "order_state", "current order state is not consistent")
	}

	protectionSummary := req.ProtectionSummary
	assessment.ProtectionAdjustmentStatus = "unknown"
	if protectionSummary != nil {
		assessment.ProtectionGroupStatus = store.NormalizeProtectionGroupStatus(protectionSummary.ProtectionGroupStatus)
		assessment.ProtectionConsistency = store.NormalizeProtectionConsistencyStatus(protectionSummary.ConsistencyStatus)
		assessment.ProtectionAdjustmentConsistency = deriveProtectionAdjustmentConsistency(protectionSummary)
		assessment.HasPendingCancelReplace = protectionSummary.HasPendingCancelReplace
		assessment.CurrentStopLossPrice = protectionSummary.CurrentStopLossPrice
		assessment.InitialStopLossPrice = protectionSummary.InitialStopLossPrice
		assessment.ProtectionRevision = protectionSummary.ProtectionRevision
		assessment.TrailingRuleID = strings.TrimSpace(protectionSummary.TrailingRuleID)
		if assessment.TrailingRuleID == "" && req.TrailingRule != nil {
			assessment.TrailingRuleID = strings.TrimSpace(req.TrailingRule.TrailingRuleID)
		}
		assessment.ProtectionAdjustmentStatus = dynamicProtectionStatusLabel(protectionSummary)
	}

	if protectionSummary == nil || !protectionSummary.HasProtection || assessment.ProtectionGroupStatus == "closed" {
		addReason("move_stop_loss", "position", "no active protection group exists on the selected symbol")
		addReason("set_break_even_stop", "position", "no active protection group exists on the selected symbol")
		addReason("set_trailing_protection", "position", "no active protection group exists on the selected symbol")
	}

	if assessment.ProtectionConsistency == "mismatch" || req.HasProtectionMismatch {
		addReason("move_stop_loss", "protection", "current protection truth is mismatched")
		addReason("set_break_even_stop", "protection", "current protection truth is mismatched")
		addReason("set_trailing_protection", "protection", "current protection truth is mismatched")
	}

	if req.ScaleInSummary != nil && (req.ScaleInSummary.HasScaleInPlan || isRuntimeActiveScaleInStatus(req.ScaleInSummary.ScaleInStatus) || req.ScaleInSummary.PendingScaleInQty > 0 || req.ScaleInSummary.RemainingScaleInQty > 0) {
		addReason("move_stop_loss", "scale_in", "active scale-in plan conflicts with dynamic protection changes")
		addReason("set_break_even_stop", "scale_in", "active scale-in plan conflicts with dynamic protection changes")
		addReason("set_trailing_protection", "scale_in", "active scale-in plan conflicts with dynamic protection changes")
	}
	if req.ScaleOutSummary != nil && (req.ScaleOutSummary.HasScaleOutPlan || isRuntimeActiveScaleOutStatus(req.ScaleOutSummary.ScaleOutStatus) || req.ScaleOutSummary.PendingScaleOutQty > 0 || req.ScaleOutSummary.RemainingScaleOutQty > 0) {
		addReason("move_stop_loss", "scale_out", "active scale-out plan conflicts with dynamic protection changes")
		addReason("set_break_even_stop", "scale_out", "active scale-out plan conflicts with dynamic protection changes")
		addReason("set_trailing_protection", "scale_out", "active scale-out plan conflicts with dynamic protection changes")
	}

	rule := req.TrailingRule
	remainingBudget := 0.0
	if protectionSummary != nil {
		usedMoves := protectionSummary.TrailingMoveCount
		if protectionSummary.BreakEvenArmed {
			usedMoves++
		}
		if rule != nil {
			if rule.MaxMoveCount <= 0 {
				remainingBudget = 1
			} else {
				remainingBudget = float64(rule.MaxMoveCount - usedMoves)
				if remainingBudget < 0 {
					remainingBudget = 0
				}
			}
		}
	}
	assessment.RemainingMoveBudget = remainingBudget

	activationMet := dynamicProtectionActivationMet(rule, req.PositionAggregate, req.CurrentMarketPrice, req.CurrentPnLPct)
	stepMoveMet := dynamicProtectionStepMoveMet(rule, req.PositionAggregate, req.CurrentMarketPrice, req.CurrentPnLPct, assessment.CurrentStopLossPrice)
	hasRule := rule != nil && strings.TrimSpace(rule.TrailingRuleID) != ""
	if !hasRule {
		addReason("set_break_even_stop", "rule", "no trailing rule is armed for the selected symbol")
		addReason("set_trailing_protection", "rule", "no trailing rule is armed for the selected symbol")
		addReason("move_stop_loss", "rule", "no trailing rule is armed for the selected symbol")
	}
	if hasRule && !activationMet {
		addReason("set_break_even_stop", "rule", "activation condition has not been met")
		addReason("set_trailing_protection", "rule", "activation condition has not been met")
		addReason("move_stop_loss", "rule", "activation condition has not been met")
	}
	if hasRule && protectionSummary != nil && protectionSummary.BreakEvenArmed {
		addReason("set_break_even_stop", "protection", "break-even protection is already armed on the selected symbol")
	}
	if hasRule && protectionSummary != nil && protectionSummary.TrailingArmed {
		addReason("set_trailing_protection", "protection", "trailing protection is already armed on the selected symbol")
	}
	if hasRule && remainingBudget <= 0 {
		addReason("move_stop_loss", "budget", "maximum protection move budget has been exhausted")
		addReason("set_break_even_stop", "budget", "maximum protection move budget has been exhausted")
		addReason("set_trailing_protection", "budget", "maximum protection move budget has been exhausted")
	}
	if hasRule && protectionSummary != nil && protectionSummary.TrailingArmed && !stepMoveMet {
		addReason("move_stop_loss", "rule", "step trailing trigger has not been met")
	}

	assessment.CanArmBreakEven = protectionSummary != nil && protectionSummary.HasProtection && hasRule && activationMet && !protectionSummary.BreakEvenArmed && !req.HasPendingCancelReplace && !req.HasStateMismatch && assessment.ProtectionConsistency != "mismatch" && remainingBudget > 0
	assessment.CanArmTrailing = protectionSummary != nil && protectionSummary.HasProtection && hasRule && activationMet && protectionSummary.BreakEvenArmed && !protectionSummary.TrailingArmed && !req.HasPendingCancelReplace && !req.HasStateMismatch && assessment.ProtectionConsistency != "mismatch" && remainingBudget > 0
	assessment.CanMoveStopLoss = protectionSummary != nil && protectionSummary.HasProtection && hasRule && !req.HasPendingCancelReplace && !req.HasStateMismatch && assessment.ProtectionConsistency != "mismatch" && remainingBudget > 0 && (assessment.CanArmBreakEven || assessment.CanArmTrailing || (protectionSummary.TrailingArmed && stepMoveMet))

	assessment.Allowed = assessment.CanArmBreakEven || assessment.CanArmTrailing || assessment.CanMoveStopLoss

	if assessment.Allowed {
		logger.Infof("protection adjustment guard passed: symbol=%s protection_group_status=%s protection_adjustment_status=%s protection_adjustment_consistency=%s has_pending_cancel_replace=%v current_stop_loss_price=%.6f current_pnl_pct=%.6f remaining_move_budget=%.6f can_arm_break_even=%v can_arm_trailing=%v can_move_stop_loss=%v",
			selectedProtectionAdjustmentSymbol(req.PositionAggregate), assessment.ProtectionGroupStatus, assessment.ProtectionAdjustmentStatus, assessment.ProtectionAdjustmentConsistency, assessment.HasPendingCancelReplace, assessment.CurrentStopLossPrice, req.CurrentPnLPct, assessment.RemainingMoveBudget, assessment.CanArmBreakEven, assessment.CanArmTrailing, assessment.CanMoveStopLoss)
	}

	return assessment, nil
}

func dynamicProtectionStatusLabel(summary *store.ProtectionGroupSummary) string {
	if summary == nil {
		return "unknown"
	}
	if summary.TrailingArmed {
		return "trailing_segmented"
	}
	if summary.BreakEvenArmed {
		return "break_even"
	}
	if summary.ProtectionMode != "" {
		return summary.ProtectionMode
	}
	return "fixed"
}

func dynamicProtectionActivationMet(rule *store.TrailingRule, aggregate *store.PositionAggregate, marketPrice, currentPnLPct float64) bool {
	if rule == nil {
		return false
	}

	normalizedMode := strings.ToLower(strings.TrimSpace(rule.RuleMode))
	activationType := strings.ToLower(strings.TrimSpace(rule.ActivationType))
	switch activationType {
	case "price_level":
		if marketPrice <= 0 {
			return false
		}
		if aggregate == nil {
			return false
		}
		switch normalizeOneWaySide(aggregate.Side) {
		case "LONG":
			return marketPrice >= rule.ActivationValue
		case "SHORT":
			return marketPrice <= rule.ActivationValue
		default:
			return false
		}
	case "profit_r_multiple", "profit_pct":
		if currentPnLPct <= 0 {
			return false
		}
		return currentPnLPct >= rule.ActivationValue
	default:
		if normalizedMode == "break_even" || normalizedMode == "step_trailing" {
			if currentPnLPct > 0 {
				return currentPnLPct >= rule.ActivationValue
			}
		}
		return false
	}
}

func dynamicProtectionStepMoveMet(rule *store.TrailingRule, aggregate *store.PositionAggregate, marketPrice, currentPnLPct, currentStopLossPrice float64) bool {
	if rule == nil || aggregate == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(rule.StepTriggerType)) {
	case "price_level":
		if marketPrice <= 0 {
			return false
		}
		switch normalizeOneWaySide(aggregate.Side) {
		case "LONG":
			return marketPrice >= rule.StepTriggerValue
		case "SHORT":
			return marketPrice <= rule.StepTriggerValue
		default:
			return false
		}
	case "profit_r_multiple", "profit_pct":
		if currentPnLPct <= 0 {
			return false
		}
		return currentPnLPct >= rule.StepTriggerValue
	default:
		if rule.StepTriggerValue > 0 {
			return currentPnLPct >= rule.StepTriggerValue
		}
		if marketPrice > 0 && currentStopLossPrice > 0 {
			if normalizeOneWaySide(aggregate.Side) == "LONG" {
				return marketPrice > currentStopLossPrice
			}
			return marketPrice < currentStopLossPrice
		}
		return false
	}
}

func selectedProtectionAdjustmentSymbol(aggregate *store.PositionAggregate) string {
	if aggregate == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(aggregate.Symbol))
}
