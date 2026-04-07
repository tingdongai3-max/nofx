package trader

import (
	"fmt"
	"strings"

	"nofx/logger"
	"nofx/store"
)

// ScaleInRiskRequest is the runtime input for the same-symbol add-position risk gate.
// It is execution-facing guard input, not a persisted truth row.
type ScaleInRiskRequest struct {
	StrategyProfile         *store.StrategyProfile      // Strategy-layer authorization snapshot; required for execution decisions.
	PositionAggregate       *store.PositionAggregate    // System-owned position truth snapshot; required for execution decisions.
	ScaleInSummary          *store.ScaleInPlanSummary   // Scale-in plan truth snapshot; required for conflict detection.
	ScaleOutSummary         *store.ScaleOutPlanSummary  // Scale-out plan truth snapshot; used to block conflicting overlap.
	OrderSummary            *store.OrderRegistrySummary // Order truth snapshot; used to detect pending order conflicts.
	ExecutionMode           string                      // Runtime execution mode used for gating only.
	AllowOrderPlacement     bool                        // Runtime order-placement gate used for gating only.
	UserStreamReady         bool                        // User-stream readiness gate used for gating only.
	HasStateMismatch        bool                        // Order-truth mismatch flag; when true, the guard blocks execution.
	HasProtectionMismatch   bool                        // Protection-truth mismatch flag; when true, the guard blocks execution.
	HasPendingCancelReplace bool                        // Pending cancel_replace flag; when true, the guard blocks execution.
	AccountEquity           float64                     // Account equity used for the max-risk-budget check when available.
	ProposedAddQty          float64                     // Proposed add quantity; used for the proposed-notional check when available.
	ProposedAddPrice        float64                     // Proposed add price; used when the guard needs a notional estimate.
}

// ScaleInRiskAssessment is the execution-facing result produced by the add-position risk gate.
// It is a guard result, not a truth-layer snapshot.
type ScaleInRiskAssessment struct {
	Allowed                 bool               `json:"allowed"`                   // Runtime permit emitted by the guard.
	BlockedReasons          []CapabilityReason `json:"blocked_reasons"`           // Machine-readable explanations for every blocked branch.
	MaxScaleInCount         int                `json:"max_scale_in_count"`        // Strategy-layer add-attempt ceiling.
	CurrentScaleInCount     int                `json:"current_scale_in_count"`    // Current add-attempt count from the scale-in truth layer.
	RiskBudgetRemaining     float64            `json:"risk_budget_remaining"`     // Remaining notional budget under the profile max-risk limit.
	CurrentPositionRiskPct  float64            `json:"current_position_risk_pct"` // Current notional as a percent of account equity when equity is known.
	CurrentPositionNotional float64            `json:"current_position_notional"` // Current notional derived from the position aggregate.
	ProposedAddNotional     float64            `json:"proposed_add_notional"`     // Proposed add notional used by the guard.
}

// ScaleInRiskGuard evaluates whether a same-symbol add-position request is allowed for this runtime cycle.
// It is a standalone guard so the resolver and execution path do not duplicate risk math.
type ScaleInRiskGuard struct{}

// NewScaleInRiskGuard creates a new add-position risk guard.
func NewScaleInRiskGuard() *ScaleInRiskGuard {
	return &ScaleInRiskGuard{}
}

// Assess returns the add-position guard result for the current cycle.
func (g *ScaleInRiskGuard) Assess(req *ScaleInRiskRequest) (*ScaleInRiskAssessment, error) {
	if req == nil {
		return nil, fmt.Errorf("scale-in risk request is required")
	}

	assessment := &ScaleInRiskAssessment{
		BlockedReasons:      []CapabilityReason{},
		MaxScaleInCount:     0,
		CurrentScaleInCount: 0,
		RiskBudgetRemaining: 0,
	}

	allow := true
	addReason := func(category, reason string) {
		assessment.BlockedReasons = append(assessment.BlockedReasons, CapabilityReason{
			Action:   "add_position",
			Category: category,
			Reason:   reason,
		})
		logger.Infof("scale in blocked by risk guard: symbol=%s category=%s reason=%s",
			selectedScaleInSymbol(req.PositionAggregate), category, reason)
		allow = false
	}

	if req.StrategyProfile == nil {
		addReason("profile", "strategy profile is missing")
		assessment.Allowed = false
		return assessment, nil
	}

	assessment.MaxScaleInCount = req.StrategyProfile.MaxScaleInCount
	if assessment.MaxScaleInCount <= 0 && req.StrategyProfile.AllowAddPosition {
		assessment.MaxScaleInCount = 1
	}
	if assessment.MaxScaleInCount <= 0 {
		addReason("profile", "strategy profile does not allow add_position")
	}
	if !req.StrategyProfile.AllowAddPosition {
		addReason("profile", "strategy profile does not allow add_position")
	}
	if !strings.EqualFold(req.StrategyProfile.Exchange, "binance_usdm") || !strings.EqualFold(req.StrategyProfile.Mode, "one_way") {
		addReason("scope", "strategy profile is outside the binance_usdm one_way scope")
	}
	if !req.StrategyProfile.ExecutionEnabled {
		addReason("profile", "strategy profile is not execution-enabled")
	}
	if !strings.EqualFold(strings.TrimSpace(req.ExecutionMode), "live") {
		addReason("mode", "current execution mode is not live")
	}
	if !req.AllowOrderPlacement {
		addReason("mode", "current runtime does not allow order placement")
	}
	if !req.UserStreamReady {
		addReason("stream", "current user stream is not ready")
	}
	if req.HasPendingCancelReplace {
		addReason("order_state", "current cancel_replace is not completed")
	}
	if req.HasStateMismatch {
		addReason("order_state", "current order state is not consistent")
	}
	if req.HasProtectionMismatch {
		addReason("protection", "current protection truth is mismatched")
	}

	activeScaleIn := isRuntimeActiveScaleInStatus(scaleInStatusFromSummary(req.ScaleInSummary))
	if req.ScaleInSummary != nil {
		assessment.CurrentScaleInCount = req.ScaleInSummary.CurrentScaleInCount
	}
	if req.ScaleInSummary != nil && (req.ScaleInSummary.HasScaleInPlan || activeScaleIn || req.ScaleInSummary.PendingScaleInQty > 0) {
		addReason("scale_in", "active scale-in plan already exists on the selected symbol")
	}
	if req.ScaleOutSummary != nil && (req.ScaleOutSummary.HasScaleOutPlan || isRuntimeActiveScaleOutStatus(req.ScaleOutSummary.ScaleOutStatus) || req.ScaleOutSummary.PendingScaleOutQty > 0) {
		addReason("scale_out", "active scale-out plan already exists on the selected symbol")
	}
	if req.OrderSummary != nil {
		if req.OrderSummary.PendingAddQty > 0 {
			addReason("order_state", "selected symbol already has reserved pending entry quantity")
		}
	}

	assessment.CurrentPositionNotional = 0
	assessment.ProposedAddNotional = 0
	if req.PositionAggregate != nil {
		assessment.CurrentPositionNotional = req.PositionAggregate.TotalNotional
		if assessment.CurrentPositionNotional <= 0 && req.PositionAggregate.TotalQty > 0 && req.PositionAggregate.AvgEntryPrice > 0 {
			assessment.CurrentPositionNotional = req.PositionAggregate.TotalQty * req.PositionAggregate.AvgEntryPrice
		}
		if req.AccountEquity > 0 && req.PositionAggregate.TotalQty > 0 {
			assessment.CurrentPositionRiskPct = (assessment.CurrentPositionNotional / req.AccountEquity) * 100
		}
	}
	if req.AccountEquity > 0 {
		maxRiskNotional := req.AccountEquity * (req.StrategyProfile.MaxPositionRiskPct / 100)
		assessment.RiskBudgetRemaining = maxRiskNotional - assessment.CurrentPositionNotional
		if assessment.RiskBudgetRemaining < 0 {
			assessment.RiskBudgetRemaining = 0
		}
	}
	if req.ProposedAddQty > 0 {
		price := req.ProposedAddPrice
		if price <= 0 && req.PositionAggregate != nil && req.PositionAggregate.AvgEntryPrice > 0 {
			price = req.PositionAggregate.AvgEntryPrice
		}
		if price > 0 {
			assessment.ProposedAddNotional = req.ProposedAddQty * price
		}
	}
	if req.AccountEquity > 0 && assessment.ProposedAddNotional > 0 {
		maxRiskNotional := req.AccountEquity * (req.StrategyProfile.MaxPositionRiskPct / 100)
		if assessment.CurrentPositionNotional+assessment.ProposedAddNotional > maxRiskNotional+0.000001 {
			addReason("risk", "proposed add position exceeds the configured risk budget")
		}
	}

	if assessment.MaxScaleInCount > 0 && assessment.CurrentScaleInCount >= assessment.MaxScaleInCount {
		addReason("profile", "strategy profile has reached the maximum add-attempt count")
	}

	assessment.Allowed = allow && len(assessment.BlockedReasons) == 0
	if assessment.Allowed {
		logger.Infof("scale in risk guard passed: symbol=%s current_count=%d max_count=%d risk_budget_remaining=%.6f",
			selectedScaleInSymbol(req.PositionAggregate), assessment.CurrentScaleInCount, assessment.MaxScaleInCount, assessment.RiskBudgetRemaining)
	}
	return assessment, nil
}

func scaleInStatusFromSummary(summary *store.ScaleInPlanSummary) string {
	if summary == nil {
		return ""
	}
	return summary.ScaleInStatus
}

func selectedScaleInSymbol(aggregate *store.PositionAggregate) string {
	if aggregate == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(aggregate.Symbol))
}
