package trader

import (
	"fmt"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

var supportedRuntimeActions = []string{
	"open_long",
	"open_short",
	"close_position",
	"set_protection",
	"add_position",
	"reduce_position",
	"arm_partial_take_profit",
	"move_stop_loss",
	"set_break_even_stop",
	"set_trailing_protection",
}

var unsupportedPhaseOneActions = []string{}

// CapabilityReason is a machine-readable debug reason for a blocked capability.
// It is runtime clipping output, not a strategy-layer authorization flag.
type CapabilityReason struct {
	Action   string `json:"action"`   // Runtime-clipped action name; not an execution command by itself.
	Category string `json:"category"` // Block classification used for debugging and UI explanation.
	Reason   string `json:"reason"`   // Human-readable reason describing why the action was blocked.
}

// RuntimeCapabilityRequest is the runtime clipping input.
// It combines strategy authorization with current runtime state for one selected symbol.
type RuntimeCapabilityRequest struct {
	TraderID                         string                                // System-owned trader key used to load profile and aggregates.
	ExecutionMode                    string                                // Runtime execution mode; affects clipping only, not strategy authorization.
	SelectedSymbol                   string                                // Runtime-selected symbol used for clipping and debug logging only.
	AllowOrderPlacement              bool                                  // Runtime gate for whether the current cycle may submit orders.
	HasPendingOrders                 bool                                  // Runtime state flag derived from selected-symbol pending orders.
	HasProtectionOrders              bool                                  // Runtime state flag derived from selected-symbol protection orders.
	HasProtection                    bool                                  // Runtime state flag derived from the protection truth layer.
	ProtectionGroupStatus            string                                // Runtime protection group status used for blocking/allowing set_protection.
	StopLossArmed                    bool                                  // Runtime protection leg flag derived from the protection truth layer.
	TakeProfitArmed                  bool                                  // Runtime protection leg flag derived from the protection truth layer.
	ProtectionConsistency            string                                // Runtime protection consistency enum from the truth layer: consistent, mismatch, pending, or unknown.
	HasProtectionMismatch            bool                                  // Runtime protection mismatch flag used for high-risk action blocking.
	ProtectionBlockReasons           []store.ProtectionStateMismatchReason // Protection mismatch explanation list used for debug and blocking.
	ProtectionAdjustmentStatus       string                                // Runtime dynamic-protection status label from the protection-adjustment truth layer.
	ProtectionAdjustmentConsistency  string                                // Runtime dynamic-protection consistency enum from the truth layer.
	ProtectionAdjustmentBlockReasons []CapabilityReason                    // Dynamic-protection block reasons used by debug output.
	ProtectionRevision               int                                   // Runtime protection revision snapshot.
	CurrentStopLossPrice             float64                               // Runtime current stop-loss price.
	InitialStopLossPrice             float64                               // Runtime initial stop-loss price.
	BreakEvenArmed                   bool                                  // Runtime dynamic-protection break-even flag.
	TrailingArmed                    bool                                  // Runtime dynamic-protection trailing flag.
	RemainingMoveBudget              float64                               // Remaining dynamic-protection move budget.
	CanArmBreakEven                  bool                                  // Guard flag showing break-even may be armed now.
	CanArmTrailing                   bool                                  // Guard flag showing trailing may be armed now.
	CanMoveStopLoss                  bool                                  // Guard flag showing the stop-loss may be moved now.
	HasScaleOutPlan                  bool                                  // Runtime scale-out plan flag derived from the scale-out truth layer.
	ScaleOutStatus                   string                                // Runtime scale-out status used for blocking/allowing reduce_position and arm_partial_take_profit.
	ScaleOutConsistency              string                                // Runtime scale-out consistency enum from the truth layer.
	HasScaleOutMismatch              bool                                  // Runtime scale-out mismatch flag used for high-risk action blocking.
	PendingScaleOutQty               float64                               // System-derived working-order reserve for the active scale-out plan; this is a subset of RemainingScaleOutQty.
	RemainingScaleOutQty             float64                               // System-derived total quantity still outstanding across the current plan, including working and not-yet-working levels.
	ExecutedScaleOutQty              float64                               // System-derived executed scale-out quantity.
	ScaleOutBlockReasons             []CapabilityReason                    // Scale-out block explanations used by debug output.
	ProtectionRebalanced             bool                                  // Runtime flag showing whether ProtectionGroup.ProtectedQuantity matches the current remaining position after scale-out rebalance.
	HasScaleInPlan                   bool                                  // Runtime scale-in plan flag derived from the scale-in truth layer.
	ScaleInStatus                    string                                // Runtime scale-in status used for blocking/allowing add_position.
	ScaleInConsistency               string                                // Runtime scale-in consistency enum from the truth layer.
	HasScaleInMismatch               bool                                  // Runtime scale-in mismatch flag used for high-risk action blocking.
	PendingScaleInQty                float64                               // System-derived working-order reserve for the active scale-in plan; this is a subset of RemainingScaleInQty.
	RemainingScaleInQty              float64                               // System-derived total quantity still outstanding across the current add plan, including working and not-yet-working levels.
	ExecutedScaleInQty               float64                               // System-derived executed scale-in quantity.
	ScaleInCount                     int                                   // Strategy-limited add-attempt count used for blocking and preview.
	ScaleInBlockReasons              []CapabilityReason                    // Machine-readable scale-in block reasons used by the UI and resolver preview.
	RiskBudgetRemaining              float64                               // Remaining notional risk budget under the strategy profile.
	HasWorkingOrders                 bool                                  // Runtime state flag derived from the order registry truth layer.
	HasPendingCancelReplace          bool                                  // Runtime state flag for a pending cancel_replace flow.
	HasStateMismatch                 bool                                  // Runtime state flag when the registry and exchange disagree.
	PendingAddQty                    float64                               // System-derived entry-layer reserve from the order registry; it is separate from reduce/protection quantities.
	PendingReduceQty                 float64                               // System-derived order-layer reduce reserve from the order registry; includes scale-out reduce rows and fixed protection rows, not the remaining position size.
	UserStreamReady                  bool                                  // Runtime readiness flag for the Binance user-stream bridge.
	MismatchReasons                  []OrderStateMismatchReason            // Reconcile mismatch reasons used for debug and UI display.
	LegacyOrAuditOnly                bool                                  // Runtime audit flag; when true, all execution actions stay blocked.
	StrategyProfile                  *store.StrategyProfile                // Strategy-layer authorization snapshot; not a live execution command.
	PositionAggregate                *store.PositionAggregate              // System-owned truth snapshot for the selected symbol; not an exchange mirror row.
}

// RuntimeCapabilityPreview is the read-only runtime clipping result returned to API/UI.
// It includes both the strategy-layer profile and the per-symbol execution decision.
type RuntimeCapabilityPreview struct {
	TruthSnapshotMetadata
	TraderID                         string                                `json:"trader_id"`                           // System-owned trader key for this preview.
	Exchange                         string                                `json:"exchange"`                            // Fixed scope value for phase 1: binance_usdm.
	Mode                             string                                `json:"mode"`                                // Fixed scope value for phase 1: one_way.
	ExecutionMode                    string                                `json:"execution_mode"`                      // Runtime execution mode used for clipping decisions.
	SelectedSymbol                   string                                `json:"selected_symbol,omitempty"`           // Selected symbol for the current preview; may be inferred from pending orders.
	StrategyProfile                  *store.StrategyProfile                `json:"strategy_profile"`                    // Strategy-layer authorization snapshot returned for debugging.
	PositionAggregate                *store.PositionAggregate              `json:"position_aggregate,omitempty"`        // System-derived aggregate truth for the selected symbol.
	PositionAggregates               []*store.PositionAggregate            `json:"position_aggregates"`                 // System-derived aggregate snapshot list persisted for UI/debug review.
	QuantityAudit                    *QuantityInvariantAudit               `json:"quantity_audit,omitempty"`            // Validation-only quantity invariant audit for the selected symbol.
	AllowedActions                   []string                              `json:"allowed_actions"`                     // Runtime-clipped actions that the UI may expose for this cycle.
	BlockedActions                   []string                              `json:"blocked_actions"`                     // Runtime-clipped actions that stay hidden or disabled in this cycle.
	BlockReasons                     []CapabilityReason                    `json:"block_reasons"`                       // Machine-readable explanation list for each blocked action.
	HasPendingOrders                 bool                                  `json:"has_pending_orders"`                  // Runtime state flag; false means no pending orders on the selected symbol.
	HasProtectionOrders              bool                                  `json:"has_protection_orders"`               // Runtime state flag; true means protection orders already exist.
	HasProtection                    bool                                  `json:"has_protection"`                      // Runtime protection state flag derived from the protection truth layer.
	ProtectionGroupStatus            string                                `json:"protection_group_status"`             // Current protection group status used by the resolver.
	StopLossArmed                    bool                                  `json:"stop_loss_armed"`                     // Runtime protection leg flag derived from the protection truth layer.
	TakeProfitArmed                  bool                                  `json:"take_profit_armed"`                   // Runtime protection leg flag derived from the protection truth layer.
	ProtectionConsistency            string                                `json:"protection_consistency"`              // Runtime protection consistency enum returned by the truth layer.
	HasProtectionMismatch            bool                                  `json:"has_protection_mismatch"`             // Runtime mismatch flag for the protection truth layer.
	ProtectionBlockReasons           []store.ProtectionStateMismatchReason `json:"protection_block_reasons"`            // Machine-readable protection mismatch reasons for UI/debug.
	ProtectionAdjustmentStatus       string                                `json:"protection_adjustment_status"`        // Runtime dynamic-protection status label returned by the truth layer.
	ProtectionAdjustmentConsistency  string                                `json:"protection_adjustment_consistency"`   // Runtime dynamic-protection consistency enum returned by the truth layer.
	ProtectionAdjustmentBlockReasons []CapabilityReason                    `json:"protection_adjustment_block_reasons"` // Machine-readable dynamic-protection block reasons for UI/debug.
	ProtectionRevision               int                                   `json:"protection_revision"`                 // Runtime protection revision snapshot.
	CurrentStopLossPrice             float64                               `json:"current_stop_loss_price"`             // Runtime current stop-loss price.
	InitialStopLossPrice             float64                               `json:"initial_stop_loss_price"`             // Runtime initial stop-loss price.
	BreakEvenArmed                   bool                                  `json:"break_even_armed"`                    // Runtime dynamic-protection break-even flag.
	TrailingArmed                    bool                                  `json:"trailing_armed"`                      // Runtime dynamic-protection trailing flag.
	RemainingMoveBudget              float64                               `json:"remaining_move_budget"`               // Remaining dynamic-protection move budget.
	CanArmBreakEven                  bool                                  `json:"can_arm_break_even"`                  // Guard flag showing break-even may be armed now.
	CanArmTrailing                   bool                                  `json:"can_arm_trailing"`                    // Guard flag showing trailing may be armed now.
	CanMoveStopLoss                  bool                                  `json:"can_move_stop_loss"`                  // Guard flag showing the stop-loss may be moved now.
	HasScaleOutPlan                  bool                                  `json:"has_scale_out_plan"`                  // Runtime scale-out plan flag derived from the scale-out truth layer.
	ScaleOutStatus                   string                                `json:"scale_out_status"`                    // Runtime scale-out status used for capability clipping.
	ScaleOutConsistency              string                                `json:"scale_out_consistency"`               // Runtime scale-out consistency enum returned by the truth layer.
	HasScaleOutMismatch              bool                                  `json:"has_scale_out_mismatch"`              // Runtime mismatch flag for the scale-out truth layer.
	PendingScaleOutQty               float64                               `json:"pending_scale_out_qty"`               // System-derived working-order reserve for the active scale-out plan; this is a subset of RemainingScaleOutQty.
	RemainingScaleOutQty             float64                               `json:"remaining_scale_out_qty"`             // System-derived total quantity still outstanding across the current plan, including working and not-yet-working levels.
	ExecutedScaleOutQty              float64                               `json:"executed_scale_out_qty"`              // System-derived cumulative executed quantity across the current plan.
	ScaleOutBlockReasons             []CapabilityReason                    `json:"scale_out_block_reasons"`             // Machine-readable scale-out block reasons for UI/debug.
	ProtectionRebalanced             bool                                  `json:"protection_rebalanced"`               // Derived flag showing whether ProtectionGroup.ProtectedQuantity matches the current remaining position size after scale-out rebalance.
	HasScaleInPlan                   bool                                  `json:"has_scale_in_plan"`                   // Runtime scale-in plan flag derived from the scale-in truth layer.
	ScaleInStatus                    string                                `json:"scale_in_status"`                     // Runtime scale-in status used for capability clipping.
	ScaleInConsistency               string                                `json:"scale_in_consistency"`                // Runtime scale-in consistency enum returned by the truth layer.
	HasScaleInMismatch               bool                                  `json:"has_scale_in_mismatch"`               // Runtime mismatch flag for the scale-in truth layer.
	PendingScaleInQty                float64                               `json:"pending_scale_in_qty"`                // System-derived working-order reserve for the active scale-in plan; this is a subset of RemainingScaleInQty.
	RemainingScaleInQty              float64                               `json:"remaining_scale_in_qty"`              // System-derived total quantity still outstanding across the current add plan, including working and not-yet-working levels.
	ExecutedScaleInQty               float64                               `json:"executed_scale_in_qty"`               // System-derived cumulative executed quantity across the current add plan.
	ScaleInCount                     int                                   `json:"scale_in_count"`                      // Strategy-limited add-attempt count used by the resolver.
	ScaleInBlockReasons              []CapabilityReason                    `json:"scale_in_block_reasons"`              // Machine-readable scale-in block reasons for UI/debug.
	RiskBudgetRemaining              float64                               `json:"risk_budget_remaining"`               // Remaining notional risk budget under the strategy profile.
	HasWorkingOrders                 bool                                  `json:"has_working_orders"`                  // Runtime truth-layer flag; true means the order registry still has working rows.
	HasPendingCancelReplace          bool                                  `json:"has_pending_cancel_replace"`          // Runtime truth-layer flag for a pending cancel_replace flow.
	HasStateMismatch                 bool                                  `json:"has_state_mismatch"`                  // Runtime truth-layer flag; true means the registry and exchange disagree.
	PendingAddQty                    float64                               `json:"pending_add_qty"`                     // System-derived entry-layer reserve from working entry orders; it is separate from reduce/protection quantities.
	PendingReduceQty                 float64                               `json:"pending_reduce_qty"`                  // System-derived order-layer reduce reserve from the order registry; includes scale-out reduce rows and fixed protection rows, not the remaining position size.
	MismatchReasons                  []OrderStateMismatchReason            `json:"mismatch_reasons"`                    // Reconcile reasons shown to the UI for blocking/debugging.
	UserStreamReady                  bool                                  `json:"user_stream_ready"`                   // Runtime readiness flag for the Binance user-stream bridge.
	AllowOrderPlacement              bool                                  `json:"allow_order_placement"`               // Runtime execution gate for this cycle; not a strategy authorization flag.
	LegacyOrAuditOnly                bool                                  `json:"legacy_or_audit_only"`                // Runtime audit flag; true means this preview is non-executable.
	GeneratedAt                      time.Time                             `json:"generated_at"`                        // System-generated timestamp for debugging and caching.
}

// RuntimeCapabilityResolver computes the runtime action set for a single symbol.
type RuntimeCapabilityResolver struct{}

// NewRuntimeCapabilityResolver creates a new runtime capability resolver.
func NewRuntimeCapabilityResolver() *RuntimeCapabilityResolver {
	return &RuntimeCapabilityResolver{}
}

// Resolve computes the allowed and blocked capability set for the current cycle.
func (r *RuntimeCapabilityResolver) Resolve(req *RuntimeCapabilityRequest) (*RuntimeCapabilityPreview, error) {
	if req == nil {
		return nil, fmt.Errorf("runtime capability request is required")
	}

	now := time.Now().UTC()
	preview := &RuntimeCapabilityPreview{
		TraderID:                         req.TraderID,
		Exchange:                         "binance_usdm",
		Mode:                             "one_way",
		ExecutionMode:                    normalizeExecutionMode(req.ExecutionMode),
		StrategyProfile:                  req.StrategyProfile,
		PositionAggregate:                req.PositionAggregate,
		AllowedActions:                   []string{},
		BlockedActions:                   []string{},
		BlockReasons:                     []CapabilityReason{},
		HasPendingOrders:                 req.HasPendingOrders,
		HasProtectionOrders:              req.HasProtectionOrders,
		HasProtection:                    req.HasProtection,
		ProtectionGroupStatus:            normalizeRuntimeProtectionGroupStatus(req.ProtectionGroupStatus),
		StopLossArmed:                    req.StopLossArmed,
		TakeProfitArmed:                  req.TakeProfitArmed,
		ProtectionConsistency:            store.NormalizeProtectionConsistencyStatus(req.ProtectionConsistency),
		HasProtectionMismatch:            req.HasProtectionMismatch,
		ProtectionBlockReasons:           req.ProtectionBlockReasons,
		ProtectionAdjustmentStatus:       req.ProtectionAdjustmentStatus,
		ProtectionAdjustmentConsistency:  normalizeProtectionAdjustmentConsistency(req.ProtectionAdjustmentConsistency),
		ProtectionAdjustmentBlockReasons: req.ProtectionAdjustmentBlockReasons,
		ProtectionRevision:               req.ProtectionRevision,
		CurrentStopLossPrice:             req.CurrentStopLossPrice,
		InitialStopLossPrice:             req.InitialStopLossPrice,
		BreakEvenArmed:                   req.BreakEvenArmed,
		TrailingArmed:                    req.TrailingArmed,
		RemainingMoveBudget:              req.RemainingMoveBudget,
		CanArmBreakEven:                  req.CanArmBreakEven,
		CanArmTrailing:                   req.CanArmTrailing,
		CanMoveStopLoss:                  req.CanMoveStopLoss,
		HasScaleOutPlan:                  req.HasScaleOutPlan,
		ScaleOutStatus:                   normalizeRuntimeScaleOutStatus(req.ScaleOutStatus),
		ScaleOutConsistency:              store.NormalizeScaleOutConsistencyStatus(req.ScaleOutConsistency),
		HasScaleOutMismatch:              req.HasScaleOutMismatch,
		PendingScaleOutQty:               req.PendingScaleOutQty,
		RemainingScaleOutQty:             req.RemainingScaleOutQty,
		ExecutedScaleOutQty:              req.ExecutedScaleOutQty,
		ScaleOutBlockReasons:             req.ScaleOutBlockReasons,
		ProtectionRebalanced:             req.ProtectionRebalanced,
		HasScaleInPlan:                   req.HasScaleInPlan,
		ScaleInStatus:                    normalizeRuntimeScaleInStatus(req.ScaleInStatus),
		ScaleInConsistency:               store.NormalizeScaleInConsistencyStatus(req.ScaleInConsistency),
		HasScaleInMismatch:               req.HasScaleInMismatch,
		PendingScaleInQty:                req.PendingScaleInQty,
		RemainingScaleInQty:              req.RemainingScaleInQty,
		ExecutedScaleInQty:               req.ExecutedScaleInQty,
		ScaleInCount:                     req.ScaleInCount,
		ScaleInBlockReasons:              req.ScaleInBlockReasons,
		RiskBudgetRemaining:              req.RiskBudgetRemaining,
		HasWorkingOrders:                 req.HasWorkingOrders,
		HasPendingCancelReplace:          req.HasPendingCancelReplace,
		HasStateMismatch:                 req.HasStateMismatch,
		PendingAddQty:                    req.PendingAddQty,
		PendingReduceQty:                 req.PendingReduceQty,
		MismatchReasons:                  req.MismatchReasons,
		UserStreamReady:                  req.UserStreamReady,
		AllowOrderPlacement:              req.AllowOrderPlacement,
		LegacyOrAuditOnly:                req.LegacyOrAuditOnly,
		GeneratedAt:                      now,
	}
	preview.TruthSnapshotMetadata = populateRuntimeMetadata(preview.GeneratedAt, "", false, "")
	if trimmedSymbol := strings.ToUpper(strings.TrimSpace(req.SelectedSymbol)); trimmedSymbol != "" {
		preview.SelectedSymbol = trimmedSymbol
	} else if req.PositionAggregate != nil {
		preview.SelectedSymbol = req.PositionAggregate.Symbol
	}

	allowedSet := make(map[string]bool)
	blockedSet := make(map[string]bool)

	addAllowed := func(action string) {
		if !allowedSet[action] {
			preview.AllowedActions = append(preview.AllowedActions, action)
			allowedSet[action] = true
		}
	}

	addBlocked := func(action, category, reason string) {
		if !blockedSet[action] {
			preview.BlockedActions = append(preview.BlockedActions, action)
			blockedSet[action] = true
		}
		for _, existing := range preview.BlockReasons {
			if existing.Action == action && existing.Category == category && existing.Reason == reason {
				goto logOnly
			}
		}
		preview.BlockReasons = append(preview.BlockReasons, CapabilityReason{
			Action:   action,
			Category: category,
			Reason:   reason,
		})
	logOnly:
		logger.Infof("blocked capability reason: trader=%s symbol=%s action=%s category=%s reason=%s",
			req.TraderID, preview.SelectedSymbol, action, category, reason)
		if category == "order_state" || category == "stream" || category == "pending_ack" || category == "reconcile" {
			logger.Infof("runtime capability blocked by order state: trader=%s symbol=%s action=%s category=%s reason=%s",
				req.TraderID, preview.SelectedSymbol, action, category, reason)
		}
	}

	blockSupported := func(category, reason string) {
		for _, action := range supportedRuntimeActions {
			addBlocked(action, category, reason)
		}
	}

	// Phase 1 never exposes the advanced tool names, even if the profile says they exist later.
	for _, action := range unsupportedPhaseOneActions {
		addBlocked(action, "unsupported", "phase 1 capability cut: execution logic is not implemented")
	}

	if req.StrategyProfile == nil {
		blockSupported("profile", "strategy profile is missing")
		preview.finishWithLog()
		return preview, nil
	}

	if !strings.EqualFold(req.StrategyProfile.Exchange, "binance_usdm") {
		blockSupported("scope", "strategy profile is outside the binance_usdm scope")
	}
	if !strings.EqualFold(req.StrategyProfile.Mode, "one_way") {
		blockSupported("scope", "strategy profile is outside one_way mode")
	}
	if !req.StrategyProfile.ExecutionEnabled {
		blockSupported("profile", "strategy profile is not execution-enabled")
	}
	if !preview.isLiveMode() {
		blockSupported("mode", "current execution mode is not live")
	}
	if !req.AllowOrderPlacement {
		blockSupported("mode", "current runtime does not allow order placement")
	}
	if req.LegacyOrAuditOnly {
		blockSupported("legacy", "trader is in legacy/audit-only state")
	}
	if !req.UserStreamReady {
		blockSupported("stream", "current user stream is not ready")
	}
	if req.HasPendingCancelReplace {
		blockSupported("order_state", "current cancel_replace is not completed")
	}
	if req.HasStateMismatch {
		blockSupported("order_state", "current order state is not consistent")
	}

	protectionConsistency := store.NormalizeProtectionConsistencyStatus(req.ProtectionConsistency)
	protectionLifecycleActive := isRuntimeNonTerminalProtectionGroupStatus(preview.ProtectionGroupStatus)
	protectionInconsistent := req.HasProtectionMismatch || protectionConsistency == "mismatch"
	protectionArmed := req.HasProtection && (req.StopLossArmed || req.TakeProfitArmed || preview.ProtectionGroupStatus == "armed")
	if protectionInconsistent {
		blockSupported("protection", "current protection truth is mismatched")
	}
	if protectionLifecycleActive && !protectionInconsistent {
		addBlocked("set_protection", "protection", fmt.Sprintf("protection group status %s is still non-terminal", preview.ProtectionGroupStatus))
	}
	if !protectionLifecycleActive && !protectionInconsistent && (req.HasProtectionOrders || protectionArmed) {
		addBlocked("set_protection", "protection", "fixed protection is already active on the selected symbol")
	}
	if !protectionLifecycleActive && !protectionInconsistent && !req.HasProtectionOrders && !protectionArmed && protectionConsistency == "unknown" && req.HasProtection {
		addBlocked("set_protection", "protection", "current protection state is unknown and cannot be reused safely")
	}

	addProtectionAdjustmentAction := func(action, fallbackCategory, fallbackReason string, allowed bool) {
		if allowed {
			addAllowed(action)
			return
		}

		matched := false
		for _, reason := range req.ProtectionAdjustmentBlockReasons {
			if reason.Action != action {
				continue
			}
			addBlocked(action, reason.Category, reason.Reason)
			matched = true
		}
		if !matched {
			addBlocked(action, fallbackCategory, fallbackReason)
		}
	}

	scaleOutConsistency := store.NormalizeScaleOutConsistencyStatus(req.ScaleOutConsistency)
	scaleOutLifecycleActive := isRuntimeActiveScaleOutStatus(preview.ScaleOutStatus)
	scaleOutInconsistent := req.HasScaleOutMismatch || scaleOutConsistency == "mismatch"
	if scaleOutInconsistent {
		blockSupported("scale_out", "current scale-out truth is mismatched")
	}
	scaleInConsistency := store.NormalizeScaleInConsistencyStatus(req.ScaleInConsistency)
	scaleInLifecycleActive := isRuntimeActiveScaleInStatus(preview.ScaleInStatus)
	scaleInInconsistent := req.HasScaleInMismatch || scaleInConsistency == "mismatch"
	if scaleInInconsistent {
		blockSupported("scale_in", "current scale-in truth is mismatched")
	}

	canSubmit := preview.isLiveMode() && req.AllowOrderPlacement && req.StrategyProfile.ExecutionEnabled && !req.LegacyOrAuditOnly && req.UserStreamReady && !req.HasPendingCancelReplace && !req.HasStateMismatch && !protectionInconsistent && !scaleOutInconsistent && !scaleInInconsistent
	hasPosition := req.PositionAggregate != nil && req.PositionAggregate.TotalQty > 0

	if req.PositionAggregate != nil && !req.PositionAggregate.ExecutionEligible {
		blockSupported("aggregate", "position aggregate is not execution eligible")
	}

	switch {
	case hasPosition:
		addBlocked("open_long", "position", "selected symbol already has an open position; add_position is not exposed in phase 1")
		addBlocked("open_short", "position", "selected symbol already has an open position; add_position is not exposed in phase 1")

		if canSubmit {
			if protectionLifecycleActive {
				addBlocked("set_protection", "protection", fmt.Sprintf("protection group status %s is still non-terminal", preview.ProtectionGroupStatus))
			} else if req.HasProtectionOrders || protectionArmed {
				addBlocked("set_protection", "protection", "fixed protection is already active on the selected symbol")
			} else if protectionInconsistent {
				addBlocked("set_protection", "protection", "current protection truth is mismatched")
			} else {
				addAllowed("set_protection")
			}

			if req.HasPendingCancelReplace {
				addBlocked("add_position", "pending", "current cancel_replace is not completed")
			} else if req.HasStateMismatch {
				addBlocked("add_position", "order_state", "current order state is not consistent")
			} else if scaleInLifecycleActive {
				addBlocked("add_position", "scale_in", fmt.Sprintf("active scale-in plan %s is still managing the selected symbol", preview.ScaleInStatus))
			} else if scaleInInconsistent {
				addBlocked("add_position", "scale_in", "current scale-in truth is mismatched")
			} else if len(req.ScaleInBlockReasons) > 0 {
				for _, reason := range req.ScaleInBlockReasons {
					addBlocked("add_position", reason.Category, reason.Reason)
				}
			} else if req.HasScaleOutPlan && scaleOutLifecycleActive && !scaleOutInconsistent {
				addBlocked("add_position", "scale_out", fmt.Sprintf("active scale-out plan %s is still managing the selected symbol", preview.ScaleOutStatus))
			} else if req.HasScaleOutPlan && scaleOutInconsistent {
				addBlocked("add_position", "scale_out", "current scale-out truth is mismatched")
			} else if req.StrategyProfile.AllowAddPosition {
				addAllowed("add_position")
			} else {
				addBlocked("add_position", "profile", "strategy profile does not allow add_position")
			}

			addProtectionAdjustmentAction("set_break_even_stop", "protection_adjustment", "break-even stop is not currently eligible", req.CanArmBreakEven)
			addProtectionAdjustmentAction("set_trailing_protection", "protection_adjustment", "trailing protection is not currently eligible", req.CanArmTrailing)
			addProtectionAdjustmentAction("move_stop_loss", "protection_adjustment", "stop-loss move is not currently eligible", req.CanMoveStopLoss)

			if req.HasPendingCancelReplace {
				addBlocked("close_position", "pending", "current cancel_replace is not completed")
			} else if req.HasStateMismatch {
				addBlocked("close_position", "order_state", "current order state is not consistent")
			} else if req.HasWorkingOrders && !scaleOutLifecycleActive {
				addBlocked("close_position", "order_state", "current working orders are still active")
			} else if scaleOutLifecycleActive && !scaleOutInconsistent {
				addBlocked("close_position", "scale_out", fmt.Sprintf("active scale-out plan %s is still managing the selected symbol", preview.ScaleOutStatus))
			} else {
				addAllowed("close_position")
			}

			if req.StrategyProfile.AllowPartialTakeProfit {
				if !req.HasScaleOutPlan {
					addBlocked("reduce_position", "scale_out", "no active scale-out plan exists on the selected symbol")
				} else if !scaleOutLifecycleActive {
					addBlocked("reduce_position", "scale_out", fmt.Sprintf("scale-out status %s is not active", preview.ScaleOutStatus))
				} else if scaleOutInconsistent {
					addBlocked("reduce_position", "scale_out", "current scale-out truth is mismatched")
				} else if req.HasPendingCancelReplace {
					addBlocked("reduce_position", "pending", "current cancel_replace is not completed")
				} else if req.HasStateMismatch {
					addBlocked("reduce_position", "order_state", "current order state is not consistent")
				} else if protectionInconsistent {
					addBlocked("reduce_position", "protection", "current protection truth is mismatched")
				} else if req.PendingAddQty > 0 {
					addBlocked("reduce_position", "order_state", "selected symbol still has reserved pending entry quantity")
				} else {
					addAllowed("reduce_position")
				}

				if req.HasScaleOutPlan && scaleOutLifecycleActive && !scaleOutInconsistent {
					addBlocked("arm_partial_take_profit", "scale_out", fmt.Sprintf("scale-out plan %s is already active", preview.ScaleOutStatus))
				} else if req.HasPendingOrders || req.HasWorkingOrders || req.PendingAddQty > 0 || req.PendingReduceQty > 0 {
					addBlocked("arm_partial_take_profit", "order_state", "pending orders already exist on the selected symbol")
				} else if req.HasPendingCancelReplace {
					addBlocked("arm_partial_take_profit", "pending", "current cancel_replace is not completed")
				} else if req.HasStateMismatch {
					addBlocked("arm_partial_take_profit", "order_state", "current order state is not consistent")
				} else if protectionInconsistent {
					addBlocked("arm_partial_take_profit", "protection", "current protection truth is mismatched")
				} else {
					addAllowed("arm_partial_take_profit")
				}
			} else {
				addBlocked("reduce_position", "profile", "strategy profile does not allow partial take profit")
				addBlocked("arm_partial_take_profit", "profile", "strategy profile does not allow partial take profit")
			}
		}
	case !hasPosition:
		addBlocked("add_position", "position", "no open position exists on the selected symbol")
		addBlocked("close_position", "position", "no open position exists on the selected symbol")
		addBlocked("reduce_position", "position", "no open position exists on the selected symbol")
		addBlocked("arm_partial_take_profit", "position", "no open position exists on the selected symbol")
		addBlocked("move_stop_loss", "position", "no open position exists on the selected symbol")
		addBlocked("set_break_even_stop", "position", "no open position exists on the selected symbol")
		addBlocked("set_trailing_protection", "position", "no open position exists on the selected symbol")

		if req.HasPendingOrders || req.HasWorkingOrders || req.PendingAddQty > 0 || req.PendingReduceQty > 0 {
			addBlocked("open_long", "pending", "pending orders already exist on the selected symbol")
			addBlocked("open_short", "pending", "pending orders already exist on the selected symbol")
			addBlocked("set_protection", "pending", "pending orders already exist on the selected symbol")
		} else if canSubmit {
			addAllowed("open_long")
			addAllowed("open_short")
		} else {
			addBlocked("open_long", "mode", "current execution mode does not allow order placement")
			addBlocked("open_short", "mode", "current execution mode does not allow order placement")
		}
		addBlocked("set_protection", "position", "no open position exists on the selected symbol")
	}

	if !canSubmit {
		// If execution is not live, the allowed set must stay empty regardless of state.
		preview.AllowedActions = preview.AllowedActions[:0]
	}

	logger.Infof("runtime capability gate state: trader=%s symbol=%s execution_mode=%s allow_order_placement=%v user_stream_ready=%v legacy_or_audit_only=%v has_pending_orders=%v has_working_orders=%v has_pending_cancel_replace=%v has_state_mismatch=%v has_protection=%v has_protection_orders=%v protection_group_status=%s protection_consistency=%s protection_adjustment_status=%s protection_adjustment_consistency=%s protection_revision=%d current_stop_loss_price=%.6f initial_stop_loss_price=%.6f break_even_armed=%v trailing_armed=%v remaining_move_budget=%.6f can_arm_break_even=%v can_arm_trailing=%v can_move_stop_loss=%v protection_lifecycle_active=%v protection_inconsistent=%v has_scale_out_plan=%v scale_out_status=%s scale_out_consistency=%s scale_out_active=%v scale_out_inconsistent=%v pending_scale_out_qty=%.6f remaining_scale_out_qty=%.6f executed_scale_out_qty=%.6f has_scale_in_plan=%v scale_in_status=%s scale_in_consistency=%s scale_in_active=%v scale_in_inconsistent=%v pending_scale_in_qty=%.6f remaining_scale_in_qty=%.6f executed_scale_in_qty=%.6f scale_in_count=%d risk_budget_remaining=%.6f can_submit=%v",
		preview.TraderID, preview.SelectedSymbol, preview.ExecutionMode, req.AllowOrderPlacement, req.UserStreamReady, req.LegacyOrAuditOnly, req.HasPendingOrders, req.HasWorkingOrders, req.HasPendingCancelReplace, req.HasStateMismatch, req.HasProtection, req.HasProtectionOrders, preview.ProtectionGroupStatus, preview.ProtectionConsistency, preview.ProtectionAdjustmentStatus, preview.ProtectionAdjustmentConsistency, preview.ProtectionRevision, preview.CurrentStopLossPrice, preview.InitialStopLossPrice, preview.BreakEvenArmed, preview.TrailingArmed, preview.RemainingMoveBudget, preview.CanArmBreakEven, preview.CanArmTrailing, preview.CanMoveStopLoss, protectionLifecycleActive, protectionInconsistent, preview.HasScaleOutPlan, preview.ScaleOutStatus, preview.ScaleOutConsistency, scaleOutLifecycleActive, scaleOutInconsistent, preview.PendingScaleOutQty, preview.RemainingScaleOutQty, preview.ExecutedScaleOutQty, preview.HasScaleInPlan, preview.ScaleInStatus, preview.ScaleInConsistency, scaleInLifecycleActive, scaleInInconsistent, preview.PendingScaleInQty, preview.RemainingScaleInQty, preview.ExecutedScaleInQty, preview.ScaleInCount, preview.RiskBudgetRemaining, canSubmit)
	preview.finishWithLog()
	return preview, nil
}

func (p *RuntimeCapabilityPreview) isLiveMode() bool {
	return strings.EqualFold(strings.TrimSpace(p.ExecutionMode), "live")
}

func (p *RuntimeCapabilityPreview) finishWithLog() {
	quantityStatus := "unknown"
	quantityReasons := 0
	if p.QuantityAudit != nil {
		quantityStatus = p.QuantityAudit.Status
		quantityReasons = len(p.QuantityAudit.Reasons)
	}
	logger.Infof("runtime capability resolved with protection status: trader=%s symbol=%s execution_mode=%s protection_consistency=%s protection_group_status=%s protection_adjustment_status=%s protection_adjustment_consistency=%s protection_revision=%d current_stop_loss_price=%.6f initial_stop_loss_price=%.6f break_even_armed=%v trailing_armed=%v remaining_move_budget=%.6f scale_out_status=%s scale_out_consistency=%s scale_in_status=%s scale_in_consistency=%s quantity_status=%s quantity_reasons=%d allowed=%v blocked=%v",
		p.TraderID, p.SelectedSymbol, p.ExecutionMode, p.ProtectionConsistency, p.ProtectionGroupStatus, p.ProtectionAdjustmentStatus, p.ProtectionAdjustmentConsistency, p.ProtectionRevision, p.CurrentStopLossPrice, p.InitialStopLossPrice, p.BreakEvenArmed, p.TrailingArmed, p.RemainingMoveBudget, p.ScaleOutStatus, p.ScaleOutConsistency, p.ScaleInStatus, p.ScaleInConsistency, quantityStatus, quantityReasons, p.AllowedActions, p.BlockedActions)
	logger.Infof("runtime capability resolved with protection adjustment status: trader=%s symbol=%s execution_mode=%s protection_adjustment_status=%s protection_adjustment_consistency=%s can_arm_break_even=%v can_arm_trailing=%v can_move_stop_loss=%v remaining_move_budget=%.6f blocked=%v",
		p.TraderID, p.SelectedSymbol, p.ExecutionMode, p.ProtectionAdjustmentStatus, p.ProtectionAdjustmentConsistency, p.CanArmBreakEven, p.CanArmTrailing, p.CanMoveStopLoss, p.RemainingMoveBudget, p.ProtectionAdjustmentBlockReasons)
	logger.Infof("runtime capability resolved with scale in status: trader=%s symbol=%s execution_mode=%s scale_in_status=%s scale_in_consistency=%s pending_scale_in_qty=%.6f remaining_scale_in_qty=%.6f executed_scale_in_qty=%.6f risk_budget_remaining=%.6f blocked=%v",
		p.TraderID, p.SelectedSymbol, p.ExecutionMode, p.ScaleInStatus, p.ScaleInConsistency, p.PendingScaleInQty, p.RemainingScaleInQty, p.ExecutedScaleInQty, p.RiskBudgetRemaining, p.ScaleInBlockReasons)
}

func normalizeExecutionMode(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "live", "sim", "readonly":
		return normalized
	default:
		if normalized == "" {
			return "readonly"
		}
		return normalized
	}
}

func normalizeRuntimeProtectionGroupStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "pending_attach", "armed", "cancel_pending", "partial_invalid", "closed", "unknown":
		if normalized == "" {
			return "unknown"
		}
		return normalized
	default:
		return "unknown"
	}
}

func normalizeRuntimeScaleOutStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "draft", "armed", "partially_filled", "completed", "invalid", "unknown":
		if normalized == "" {
			return "unknown"
		}
		return normalized
	default:
		return "unknown"
	}
}

func isRuntimeActiveScaleOutStatus(status string) bool {
	switch normalizeRuntimeScaleOutStatus(status) {
	case "draft", "armed", "partially_filled":
		return true
	default:
		return false
	}
}

func normalizeRuntimeScaleInStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "draft", "armed", "partially_filled", "completed", "invalid", "unknown":
		if normalized == "" {
			return "unknown"
		}
		return normalized
	default:
		return "unknown"
	}
}

func normalizeProtectionAdjustmentConsistency(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "consistent", "mismatch", "pending", "unknown":
		if normalized == "" {
			return "unknown"
		}
		return normalized
	default:
		return "unknown"
	}
}

func selectProtectionAdjustmentMarketPrice(liveSnapshots []store.PositionSnapshot, aggregate *store.PositionAggregate, selectedSymbol string) float64 {
	if aggregate != nil && aggregate.Symbol != "" {
		for _, snap := range liveSnapshots {
			if strings.EqualFold(strings.TrimSpace(snap.Symbol), strings.TrimSpace(aggregate.Symbol)) && strings.EqualFold(strings.TrimSpace(snap.Side), strings.TrimSpace(aggregate.Side)) {
				if snap.MarkPrice > 0 {
					return snap.MarkPrice
				}
				if snap.EntryPrice > 0 {
					return snap.EntryPrice
				}
			}
		}
	}
	normalizedSymbol := normalizeCapabilitySymbol(selectedSymbol)
	if normalizedSymbol != "" {
		for _, snap := range liveSnapshots {
			if strings.EqualFold(strings.TrimSpace(snap.Symbol), normalizedSymbol) {
				if snap.MarkPrice > 0 {
					return snap.MarkPrice
				}
				if snap.EntryPrice > 0 {
					return snap.EntryPrice
				}
			}
		}
	}
	return 0
}

func pickProtectionAdjustmentStatus(preview *ProtectionAdjustmentPreview) string {
	if preview == nil || strings.TrimSpace(preview.ProtectionAdjustmentStatus) == "" {
		return "unknown"
	}
	return preview.ProtectionAdjustmentStatus
}

func pickProtectionAdjustmentConsistency(preview *ProtectionAdjustmentPreview) string {
	if preview == nil {
		return "unknown"
	}
	return normalizeProtectionAdjustmentConsistency(preview.ProtectionAdjustmentConsistency)
}

func pickProtectionAdjustmentBlockReasons(preview *ProtectionAdjustmentPreview) []CapabilityReason {
	if preview == nil || len(preview.ProtectionAdjustmentBlockReasons) == 0 {
		return []CapabilityReason{}
	}
	return preview.ProtectionAdjustmentBlockReasons
}

func pickProtectionAdjustmentRevision(preview *ProtectionAdjustmentPreview) int {
	if preview == nil {
		return 0
	}
	return preview.ProtectionRevision
}

func pickProtectionAdjustmentCurrentStop(preview *ProtectionAdjustmentPreview) float64 {
	if preview == nil {
		return 0
	}
	return preview.CurrentStopLossPrice
}

func pickProtectionAdjustmentInitialStop(preview *ProtectionAdjustmentPreview) float64 {
	if preview == nil {
		return 0
	}
	return preview.InitialStopLossPrice
}

func pickProtectionAdjustmentBreakEven(preview *ProtectionAdjustmentPreview) bool {
	if preview == nil {
		return false
	}
	return preview.BreakEvenArmed
}

func pickProtectionAdjustmentTrailing(preview *ProtectionAdjustmentPreview) bool {
	if preview == nil {
		return false
	}
	return preview.TrailingArmed
}

func pickProtectionAdjustmentRemainingBudget(preview *ProtectionAdjustmentPreview) float64 {
	if preview == nil {
		return 0
	}
	return preview.RemainingMoveBudget
}

func pickProtectionAdjustmentCanArmBreakEven(preview *ProtectionAdjustmentPreview) bool {
	if preview == nil {
		return false
	}
	return preview.CanArmBreakEven
}

func pickProtectionAdjustmentCanArmTrailing(preview *ProtectionAdjustmentPreview) bool {
	if preview == nil {
		return false
	}
	return preview.CanArmTrailing
}

func pickProtectionAdjustmentCanMoveStopLoss(preview *ProtectionAdjustmentPreview) bool {
	if preview == nil {
		return false
	}
	return preview.CanMoveStopLoss
}

func isRuntimeActiveScaleInStatus(status string) bool {
	switch normalizeRuntimeScaleInStatus(status) {
	case "draft", "armed", "partially_filled":
		return true
	default:
		return false
	}
}

func isRuntimeNonTerminalProtectionGroupStatus(status string) bool {
	switch normalizeRuntimeProtectionGroupStatus(status) {
	case "pending_attach", "armed", "cancel_pending", "partial_invalid":
		return true
	default:
		return false
	}
}

// BuildRuntimeCapabilityPreview builds the read-only preview response from store state and optional live snapshots.
// It persists the latest strategy profile and position aggregate snapshot before returning the clipped actions.
func BuildRuntimeCapabilityPreview(
	st *store.Store,
	userID, traderID, selectedSymbol, executionMode string,
	liveSnapshots []store.PositionSnapshot,
	peakPnLCache map[string]float64,
	orderPreview *OrderReconcilePreview,
	scaleOutPreview *ScaleOutReconcilePreview,
	scaleInPreview *ScaleInReconcilePreview,
	allowOrderPlacement bool,
	resolver *RuntimeCapabilityResolver,
) (*RuntimeCapabilityPreview, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	fullCfg, err := st.Trader().GetFullConfig(userID, traderID)
	if err != nil {
		return nil, err
	}

	profile, err := st.StrategyProfile().GetByTraderID(traderID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		profile, err = st.StrategyProfile().BuildAndStoreFromFullConfig(fullCfg)
		if err != nil {
			return nil, err
		}
		logger.Infof("strategy profile loaded from full config: trader=%s exchange=%s mode=%s execution_enabled=%v allow_add=%v allow_partial_tp=%v allow_move_sl=%v allow_trailing=%v protection_mode=%s decision_style=%s",
			profile.TraderID, profile.Exchange, profile.Mode, profile.ExecutionEnabled,
			profile.AllowAddPosition, profile.AllowPartialTakeProfit, profile.AllowMoveStopLoss, profile.AllowTrailingStop,
			profile.ProtectionMode, profile.DecisionStyle)
	} else {
		logger.Infof("strategy profile loaded from store: trader=%s exchange=%s mode=%s execution_enabled=%v allow_add=%v allow_partial_tp=%v allow_move_sl=%v allow_trailing=%v protection_mode=%s decision_style=%s",
			profile.TraderID, profile.Exchange, profile.Mode, profile.ExecutionEnabled,
			profile.AllowAddPosition, profile.AllowPartialTakeProfit, profile.AllowMoveStopLoss, profile.AllowTrailingStop,
			profile.ProtectionMode, profile.DecisionStyle)
	}

	aggregates, err := store.NewPositionAggregateBuilder(st).BuildForTrader(traderID, liveSnapshots, peakPnLCache)
	if err != nil {
		return nil, err
	}

	exchangeType := ""
	if fullCfg.Exchange != nil {
		exchangeType = fullCfg.Exchange.ExchangeType
	}
	legacyOrAuditOnly := !profile.ExecutionEnabled || !strings.EqualFold(profile.Exchange, "binance_usdm") || !strings.EqualFold(exchangeType, "binance")

	if orderPreview == nil {
		orderPreview, err = BuildRuntimeOrderPreview(st, traderID, selectedSymbol, false)
		if err != nil {
			return nil, err
		}
	}
	if orderPreview == nil {
		orderPreview = &OrderReconcilePreview{}
	}
	if scaleOutPreview == nil {
		scaleOutPreview, err = BuildRuntimeScaleOutPreview(st, traderID, selectedSymbol, orderPreview.UserStreamReady)
		if err != nil {
			return nil, err
		}
	}
	if scaleOutPreview == nil {
		scaleOutPreview = &ScaleOutReconcilePreview{}
	}
	if scaleInPreview == nil {
		scaleInPreview, err = BuildRuntimeScaleInPreview(st, traderID, selectedSymbol, orderPreview.UserStreamReady, 0)
		if err != nil {
			return nil, err
		}
	}
	if scaleInPreview == nil {
		scaleInPreview = &ScaleInReconcilePreview{}
	}

	selectedSymbol = chooseCapabilityPreviewSymbol(selectedSymbol, aggregates, orderPreview)
	selectedAggregate := selectPreviewAggregate(aggregates, selectedSymbol)
	marketPrice := selectProtectionAdjustmentMarketPrice(liveSnapshots, selectedAggregate, selectedSymbol)
	if scaleInPreview != nil && !strings.EqualFold(strings.TrimSpace(scaleInPreview.SelectedSymbol), strings.TrimSpace(selectedSymbol)) {
		if rebuilt, rebuildErr := BuildRuntimeScaleInPreview(st, traderID, selectedSymbol, orderPreview.UserStreamReady, 0); rebuildErr == nil && rebuilt != nil {
			scaleInPreview = rebuilt
		}
	}
	protectionAdjustmentPreview, adjustmentErr := BuildRuntimeProtectionAdjustmentPreview(st, traderID, selectedSymbol, orderPreview.UserStreamReady, marketPrice)
	if adjustmentErr != nil {
		logger.Infof("protection adjustment preview fallback for trader %s: %v", traderID, adjustmentErr)
		protectionAdjustmentPreview = nil
	}

	hasPendingOrders := orderPreview.HasWorkingOrders || orderPreview.PendingAddQty > 0 || orderPreview.PendingReduceQty > 0
	hasProtectionOrders := orderPreview.HasProtection || orderPreview.StopLossArmed || orderPreview.TakeProfitArmed || orderPreview.PendingReduceQty > 0 || orderPreview.HasPendingCancelReplace
	hasProtectionMismatch := orderPreview.HasStateMismatch || store.NormalizeProtectionConsistencyStatus(orderPreview.ProtectionConsistency) == "mismatch"

	if resolver == nil {
		resolver = NewRuntimeCapabilityResolver()
	}
	preview, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:                         traderID,
		ExecutionMode:                    executionMode,
		SelectedSymbol:                   selectedSymbol,
		AllowOrderPlacement:              allowOrderPlacement,
		HasPendingOrders:                 hasPendingOrders,
		HasProtectionOrders:              hasProtectionOrders,
		HasProtection:                    orderPreview.HasProtection,
		ProtectionGroupStatus:            orderPreview.ProtectionGroupStatus,
		StopLossArmed:                    orderPreview.StopLossArmed,
		TakeProfitArmed:                  orderPreview.TakeProfitArmed,
		ProtectionConsistency:            orderPreview.ProtectionConsistency,
		HasProtectionMismatch:            hasProtectionMismatch,
		ProtectionBlockReasons:           orderPreview.ProtectionBlockReasons,
		ProtectionAdjustmentStatus:       pickProtectionAdjustmentStatus(protectionAdjustmentPreview),
		ProtectionAdjustmentConsistency:  pickProtectionAdjustmentConsistency(protectionAdjustmentPreview),
		ProtectionAdjustmentBlockReasons: pickProtectionAdjustmentBlockReasons(protectionAdjustmentPreview),
		ProtectionRevision:               pickProtectionAdjustmentRevision(protectionAdjustmentPreview),
		CurrentStopLossPrice:             pickProtectionAdjustmentCurrentStop(protectionAdjustmentPreview),
		InitialStopLossPrice:             pickProtectionAdjustmentInitialStop(protectionAdjustmentPreview),
		BreakEvenArmed:                   pickProtectionAdjustmentBreakEven(protectionAdjustmentPreview),
		TrailingArmed:                    pickProtectionAdjustmentTrailing(protectionAdjustmentPreview),
		RemainingMoveBudget:              pickProtectionAdjustmentRemainingBudget(protectionAdjustmentPreview),
		CanArmBreakEven:                  pickProtectionAdjustmentCanArmBreakEven(protectionAdjustmentPreview),
		CanArmTrailing:                   pickProtectionAdjustmentCanArmTrailing(protectionAdjustmentPreview),
		CanMoveStopLoss:                  pickProtectionAdjustmentCanMoveStopLoss(protectionAdjustmentPreview),
		HasScaleOutPlan:                  scaleOutPreview.HasScaleOutPlan,
		ScaleOutStatus:                   scaleOutPreview.ScaleOutStatus,
		ScaleOutConsistency:              scaleOutPreview.ScaleOutConsistency,
		HasScaleOutMismatch:              scaleOutPreview.HasStateMismatch,
		PendingScaleOutQty:               scaleOutPreview.PendingScaleOutQty,
		RemainingScaleOutQty:             scaleOutPreview.RemainingScaleOutQty,
		ExecutedScaleOutQty:              scaleOutPreview.ExecutedScaleOutQty,
		ScaleOutBlockReasons:             scaleOutPreview.ScaleOutBlockReasons,
		ProtectionRebalanced:             scaleOutPreview.ProtectionRebalanced,
		HasScaleInPlan:                   scaleInPreview.HasScaleInPlan,
		ScaleInStatus:                    scaleInPreview.ScaleInStatus,
		ScaleInConsistency:               scaleInPreview.ScaleInConsistency,
		HasScaleInMismatch:               scaleInPreview.HasStateMismatch,
		PendingScaleInQty:                scaleInPreview.PendingScaleInQty,
		RemainingScaleInQty:              scaleInPreview.RemainingScaleInQty,
		ExecutedScaleInQty:               scaleInPreview.ExecutedScaleInQty,
		ScaleInCount:                     scaleInPreview.ScaleInCount,
		ScaleInBlockReasons:              scaleInPreview.ScaleInBlockReasons,
		RiskBudgetRemaining:              scaleInPreview.RiskBudgetRemaining,
		HasWorkingOrders:                 orderPreview.HasWorkingOrders,
		HasPendingCancelReplace:          orderPreview.HasPendingCancelReplace,
		HasStateMismatch:                 orderPreview.HasStateMismatch,
		PendingAddQty:                    orderPreview.PendingAddQty,
		PendingReduceQty:                 orderPreview.PendingReduceQty,
		UserStreamReady:                  orderPreview.UserStreamReady,
		MismatchReasons:                  orderPreview.MismatchReasons,
		LegacyOrAuditOnly:                legacyOrAuditOnly,
		StrategyProfile:                  profile,
		PositionAggregate:                selectedAggregate,
	})
	if err != nil {
		return nil, err
	}
	preview.QuantityAudit = buildQuantityInvariantAudit(selectedAggregate, orderPreview.LocalSummary, scaleOutPreview.ProtectionSummary, nil, nil)
	preview.PositionAggregates = aggregates
	if preview.PositionAggregates == nil {
		preview.PositionAggregates = []*store.PositionAggregate{}
	}
	preview.StrategyProfile = profile
	preview.PositionAggregate = selectedAggregate
	preview.HasPendingOrders = hasPendingOrders
	preview.HasProtectionOrders = hasProtectionOrders
	preview.HasWorkingOrders = orderPreview.HasWorkingOrders
	preview.HasPendingCancelReplace = orderPreview.HasPendingCancelReplace
	preview.HasStateMismatch = orderPreview.HasStateMismatch
	preview.PendingAddQty = orderPreview.PendingAddQty
	preview.PendingReduceQty = orderPreview.PendingReduceQty
	preview.MismatchReasons = orderPreview.MismatchReasons
	preview.UserStreamReady = orderPreview.UserStreamReady
	preview.HasScaleOutPlan = scaleOutPreview.HasScaleOutPlan
	preview.ScaleOutStatus = scaleOutPreview.ScaleOutStatus
	preview.ScaleOutConsistency = scaleOutPreview.ScaleOutConsistency
	preview.HasScaleOutMismatch = scaleOutPreview.HasStateMismatch
	preview.PendingScaleOutQty = scaleOutPreview.PendingScaleOutQty
	preview.RemainingScaleOutQty = scaleOutPreview.RemainingScaleOutQty
	preview.ExecutedScaleOutQty = scaleOutPreview.ExecutedScaleOutQty
	preview.ScaleOutBlockReasons = scaleOutPreview.ScaleOutBlockReasons
	preview.ProtectionRebalanced = scaleOutPreview.ProtectionRebalanced
	preview.HasScaleInPlan = scaleInPreview.HasScaleInPlan
	preview.ScaleInStatus = scaleInPreview.ScaleInStatus
	preview.ScaleInConsistency = scaleInPreview.ScaleInConsistency
	preview.HasScaleInMismatch = scaleInPreview.HasStateMismatch
	preview.PendingScaleInQty = scaleInPreview.PendingScaleInQty
	preview.RemainingScaleInQty = scaleInPreview.RemainingScaleInQty
	preview.ExecutedScaleInQty = scaleInPreview.ExecutedScaleInQty
	preview.ScaleInCount = scaleInPreview.ScaleInCount
	preview.ScaleInBlockReasons = scaleInPreview.ScaleInBlockReasons
	preview.RiskBudgetRemaining = scaleInPreview.RiskBudgetRemaining
	preview.AllowOrderPlacement = allowOrderPlacement
	preview.LegacyOrAuditOnly = legacyOrAuditOnly
	preview.SelectedSymbol = selectedSymbolFromAggregate(selectedAggregate, selectedSymbol)
	preview.TraderID = traderID
	return preview, nil
}

func selectPreviewAggregate(aggregates []*store.PositionAggregate, selectedSymbol string) *store.PositionAggregate {
	if len(aggregates) == 0 {
		return nil
	}
	if symbol := strings.ToUpper(strings.TrimSpace(selectedSymbol)); symbol != "" {
		for _, aggregate := range aggregates {
			if strings.EqualFold(aggregate.Symbol, symbol) {
				return aggregate
			}
		}
		return nil
	}
	return aggregates[0]
}

func selectedSymbolFromAggregate(selected *store.PositionAggregate, fallback string) string {
	if selected != nil && selected.Symbol != "" {
		return selected.Symbol
	}
	return strings.ToUpper(strings.TrimSpace(fallback))
}

func chooseCapabilityPreviewSymbol(selectedSymbol string, aggregates []*store.PositionAggregate, orderPreview *OrderReconcilePreview) string {
	symbol := normalizeCapabilitySymbol(selectedSymbol)
	if symbol != "" {
		return symbol
	}

	if orderPreview != nil {
		if previewSymbol := normalizeCapabilitySymbol(orderPreview.SelectedSymbol); previewSymbol != "" {
			return previewSymbol
		}
		if orderPreview.LocalSummary != nil && normalizeCapabilitySymbol(orderPreview.LocalSummary.Symbol) != "" {
			return normalizeCapabilitySymbol(orderPreview.LocalSummary.Symbol)
		}
	}

	if selected := selectPreviewAggregate(aggregates, ""); selected != nil && selected.Symbol != "" {
		return normalizeCapabilitySymbol(selected.Symbol)
	}

	return ""
}

func normalizeCapabilitySymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}
