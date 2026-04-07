package trader

import (
	"fmt"
	"strings"
	"time"

	"nofx/store"
)

// TruthSnapshotMetadata tags a preview/snapshot with validation lineage.
// It is validation metadata and does not change execution behavior.
type TruthSnapshotMetadata struct {
	TruthSnapshotVersion    string `json:"truth_snapshot_version"`              // Validation snapshot version for traceability.
	ReplayedFromFixture     string `json:"replayed_from_fixture,omitempty"`     // Validation fixture id when the response was derived from a replay run.
	RestoredFromSnapshot    bool   `json:"restored_from_snapshot"`              // True when the response was produced by restore validation.
	MigrationFixtureVersion string `json:"migration_fixture_version,omitempty"` // Schema migration fixture version when applicable.
}

// ValidationMismatchReason is a machine-readable validation mismatch explanation.
// It is derived by replay/restore/migration validation and is not an execution command.
type ValidationMismatchReason struct {
	Category string `json:"category"`           // Validation mismatch bucket.
	Reason   string `json:"reason"`             // Human-readable validation mismatch explanation.
	Field    string `json:"field,omitempty"`    // Snapshot field whose value diverged.
	Symbol   string `json:"symbol,omitempty"`   // Symbol scope for the mismatch when available.
	Expected string `json:"expected,omitempty"` // Expected or pre-restore value.
	Actual   string `json:"actual,omitempty"`   // Actual or post-restore value.
}

// TruthSnapshotSummary is the compact validation snapshot used by replay, restore, conflict, and migration runners.
// It is derived from the existing truth layers and preview builders, not from raw exchange payloads.
type TruthSnapshotSummary struct {
	TruthSnapshotMetadata
	TraderID          string                        `json:"trader_id"`                    // System-owned trader key.
	SelectedSymbol    string                        `json:"selected_symbol,omitempty"`    // Selected symbol scope for the snapshot.
	Exchange          string                        `json:"exchange"`                     // Fixed validation scope: binance_usdm.
	Mode              string                        `json:"mode"`                         // Fixed validation scope: one_way.
	PositionAggregate *store.PositionAggregate      `json:"position_aggregate,omitempty"` // System-owned aggregate truth snapshot.
	OrderSummary      *store.OrderRegistrySummary   `json:"order_summary,omitempty"`      // System-derived order truth summary.
	ProtectionSummary *store.ProtectionGroupSummary `json:"protection_summary,omitempty"` // System-derived protection truth summary.
	ScaleOutSummary   *store.ScaleOutPlanSummary    `json:"scale_out_summary,omitempty"`  // System-derived scale-out truth summary.
	ScaleInSummary    *store.ScaleInPlanSummary     `json:"scale_in_summary,omitempty"`   // System-derived scale-in truth summary.
	QuantityAudit     *QuantityInvariantAudit       `json:"quantity_audit,omitempty"`     // Validation-only quantity invariant audit derived from the truth layers.
	GeneratedAt       time.Time                     `json:"generated_at"`                 // Snapshot generation timestamp.
}

// QuantityInvariantAudit is the validation-only quantity consistency check for one truth snapshot.
// It is not an execution command; it exists so replay/restore/capability previews can expose hard mismatches.
type QuantityInvariantAudit struct {
	Status                  string                     `json:"status"`                        // Validation status: consistent, mismatch, pending, or unknown.
	HasMismatch             bool                       `json:"has_mismatch"`                  // True when one or more quantity invariants are violated.
	Reasons                 []ValidationMismatchReason `json:"reasons"`                       // Machine-readable quantity invariant mismatches.
	TotalQty                float64                    `json:"total_qty"`                     // Position-layer total quantity.
	AvailableQty            float64                    `json:"available_qty"`                 // Position-layer available quantity after reserves.
	PendingReduceQty        float64                    `json:"pending_reduce_qty"`            // Position-layer pending reduce reserve.
	PendingScaleOutQty      float64                    `json:"pending_scale_out_qty"`         // Position-layer active scale-out working reserve.
	RemainingScaleOutQty    float64                    `json:"remaining_scale_out_qty"`       // Position-layer scale-out remaining plan quantity.
	ProtectedQuantity       float64                    `json:"protected_quantity"`            // Position-layer protected quantity.
	OrderPendingReduceQty   float64                    `json:"order_pending_reduce_qty"`      // Order-layer reduce reserve from the registry summary.
	OrderProtectionCoverage float64                    `json:"order_protection_coverage_qty"` // Order-layer protection coverage from the registry summary.
}

func buildTruthSnapshotMetadata(at time.Time, replayedFromFixture string, restored bool, migrationFixtureVersion string) TruthSnapshotMetadata {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return TruthSnapshotMetadata{
		TruthSnapshotVersion:    at.Format(time.RFC3339Nano),
		ReplayedFromFixture:     strings.TrimSpace(replayedFromFixture),
		RestoredFromSnapshot:    restored,
		MigrationFixtureVersion: strings.TrimSpace(migrationFixtureVersion),
	}
}

func populateRuntimeMetadata(at time.Time, replayedFromFixture string, restored bool, migrationFixtureVersion string) TruthSnapshotMetadata {
	return buildTruthSnapshotMetadata(at, replayedFromFixture, restored, migrationFixtureVersion)
}

// BuildTruthSnapshotSummary reads the current truth-layer summary for one trader/symbol scope.
// It is validation-only and does not execute trading actions.
func BuildTruthSnapshotSummary(st *store.Store, traderID, selectedSymbol string, userStreamReady bool) (*TruthSnapshotSummary, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}
	_ = userStreamReady

	symbol, side, err := chooseTruthSnapshotScope(st, traderID, selectedSymbol)
	if err != nil {
		return nil, err
	}

	var aggregate *store.PositionAggregate
	if symbol != "" && side != "" {
		aggregate, err = st.PositionAggregate().GetByTraderSymbolSide(traderID, symbol, side)
		if err != nil {
			return nil, err
		}
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
			if len(summaries) > 0 {
				protectionSummary = summaries[0]
			}
		}
		if err != nil {
			return nil, err
		}
	}

	scaleOutSummary := &store.ScaleOutPlanSummary{}
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

	scaleInSummary := &store.ScaleInPlanSummary{}
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

	quantityAudit := buildQuantityInvariantAudit(aggregate, orderSummary, protectionSummary, scaleOutSummary, scaleInSummary)
	now := time.Now().UTC()
	return &TruthSnapshotSummary{
		TruthSnapshotMetadata: buildTruthSnapshotMetadata(now, "", false, ""),
		TraderID:              traderID,
		SelectedSymbol:        symbol,
		Exchange:              "binance_usdm",
		Mode:                  "one_way",
		PositionAggregate:     aggregate,
		OrderSummary:          orderSummary,
		ProtectionSummary:     protectionSummary,
		ScaleOutSummary:       scaleOutSummary,
		ScaleInSummary:        scaleInSummary,
		QuantityAudit:         quantityAudit,
		GeneratedAt:           now,
	}, nil
}

func buildQuantityInvariantAudit(
	aggregate *store.PositionAggregate,
	orderSummary *store.OrderRegistrySummary,
	protectionSummary *store.ProtectionGroupSummary,
	scaleOutSummary *store.ScaleOutPlanSummary,
	_ *store.ScaleInPlanSummary,
) *QuantityInvariantAudit {
	if aggregate == nil {
		return &QuantityInvariantAudit{
			Status:      "unknown",
			HasMismatch: false,
			Reasons:     []ValidationMismatchReason{},
		}
	}

	audit := &QuantityInvariantAudit{
		Status:               "consistent",
		Reasons:              []ValidationMismatchReason{},
		TotalQty:             aggregate.TotalQty,
		AvailableQty:         aggregate.AvailableQty,
		PendingReduceQty:     aggregate.PendingReduceQty,
		PendingScaleOutQty:   aggregate.PendingScaleOutQty,
		RemainingScaleOutQty: aggregate.RemainingScaleOutQty,
		ProtectedQuantity:    aggregate.ProtectedQuantity,
	}
	if orderSummary != nil {
		audit.OrderPendingReduceQty = orderSummary.PendingReduceQty
		audit.OrderProtectionCoverage = orderSummary.ProtectionCoverageQty
	}

	appendReason := func(field, reason, expected, actual string) {
		audit.Reasons = append(audit.Reasons, ValidationMismatchReason{
			Category: "quantity",
			Reason:   reason,
			Field:    field,
			Symbol:   aggregate.Symbol,
			Expected: expected,
			Actual:   actual,
		})
	}

	const eps = 1e-9
	if aggregate.TotalQty < 0 {
		appendReason("total_qty", "total quantity must not be negative", ">= 0", fmt.Sprintf("%.6f", aggregate.TotalQty))
	}
	if aggregate.AvailableQty < 0 {
		appendReason("available_qty", "available quantity must not be negative", ">= 0", fmt.Sprintf("%.6f", aggregate.AvailableQty))
	}
	if aggregate.PendingReduceQty < 0 {
		appendReason("pending_reduce_qty", "pending reduce quantity must not be negative", ">= 0", fmt.Sprintf("%.6f", aggregate.PendingReduceQty))
	}
	if aggregate.PendingScaleOutQty < 0 {
		appendReason("pending_scale_out_qty", "pending scale-out quantity must not be negative", ">= 0", fmt.Sprintf("%.6f", aggregate.PendingScaleOutQty))
	}
	if aggregate.RemainingScaleOutQty < 0 {
		appendReason("remaining_scale_out_qty", "remaining scale-out quantity must not be negative", ">= 0", fmt.Sprintf("%.6f", aggregate.RemainingScaleOutQty))
	}
	if aggregate.ProtectedQuantity < 0 {
		appendReason("protected_quantity", "protected quantity must not be negative", ">= 0", fmt.Sprintf("%.6f", aggregate.ProtectedQuantity))
	}

	if aggregate.AvailableQty-aggregate.TotalQty > eps {
		appendReason("available_qty", "available quantity cannot exceed total quantity", fmt.Sprintf("<= %.6f", aggregate.TotalQty), fmt.Sprintf("%.6f", aggregate.AvailableQty))
	}
	if mathAbs(aggregate.TotalQty-(aggregate.AvailableQty+aggregate.PendingReduceQty)) > eps {
		appendReason("total_qty", "total quantity must equal available quantity plus pending reduce reserve", fmt.Sprintf("available_qty + pending_reduce_qty = %.6f", aggregate.TotalQty), fmt.Sprintf("%.6f", aggregate.AvailableQty+aggregate.PendingReduceQty))
	}
	if aggregate.PendingScaleOutQty-aggregate.RemainingScaleOutQty > eps {
		appendReason("pending_scale_out_qty", "pending scale-out reserve cannot exceed remaining scale-out quantity", fmt.Sprintf("<= %.6f", aggregate.RemainingScaleOutQty), fmt.Sprintf("%.6f", aggregate.PendingScaleOutQty))
	}
	if aggregate.PendingReduceQty+eps < aggregate.PendingScaleOutQty {
		appendReason("pending_reduce_qty", "pending reduce reserve must cover the active scale-out reserve", fmt.Sprintf(">= %.6f", aggregate.PendingScaleOutQty), fmt.Sprintf("%.6f", aggregate.PendingReduceQty))
	}
	if aggregate.ProtectedQuantity-aggregate.TotalQty > eps {
		appendReason("protected_quantity", "protected quantity cannot exceed total quantity", fmt.Sprintf("<= %.6f", aggregate.TotalQty), fmt.Sprintf("%.6f", aggregate.ProtectedQuantity))
	}
	if orderSummary != nil {
		if orderSummary.PendingReduceQty+eps < aggregate.PendingReduceQty {
			appendReason("order_pending_reduce_qty", "order registry reduce reserve must cover the aggregate reduce reserve", fmt.Sprintf(">= %.6f", aggregate.PendingReduceQty), fmt.Sprintf("%.6f", orderSummary.PendingReduceQty))
		}
		if orderSummary.PendingReduceQty+eps < orderSummary.ProtectionCoverageQty {
			appendReason("order_pending_reduce_qty", "order registry reduce reserve must cover protection coverage", fmt.Sprintf(">= %.6f", orderSummary.ProtectionCoverageQty), fmt.Sprintf("%.6f", orderSummary.PendingReduceQty))
		}
		if orderSummary.ProtectionCoverageQty+eps < aggregate.ProtectedQuantity {
			appendReason("order_protection_coverage_qty", "order registry protection coverage must cover protected quantity", fmt.Sprintf(">= %.6f", aggregate.ProtectedQuantity), fmt.Sprintf("%.6f", orderSummary.ProtectionCoverageQty))
		}
	}
	if protectionSummary != nil && protectionSummary.ProtectedQuantity+eps < aggregate.ProtectedQuantity {
		appendReason("protected_quantity", "protection summary protected quantity must cover aggregate protected quantity", fmt.Sprintf(">= %.6f", aggregate.ProtectedQuantity), fmt.Sprintf("%.6f", protectionSummary.ProtectedQuantity))
	}
	if scaleOutSummary != nil && scaleOutSummary.PendingScaleOutQty+eps < aggregate.PendingScaleOutQty {
		appendReason("pending_scale_out_qty", "scale-out summary pending quantity must cover aggregate pending scale-out quantity", fmt.Sprintf(">= %.6f", aggregate.PendingScaleOutQty), fmt.Sprintf("%.6f", scaleOutSummary.PendingScaleOutQty))
	}

	if len(audit.Reasons) > 0 {
		audit.Status = "mismatch"
		audit.HasMismatch = true
	}

	if scaleOutSummary == nil && orderSummary == nil && protectionSummary == nil {
		audit.Status = "unknown"
	}
	if aggregate.TotalQty > 0 && !audit.HasMismatch && aggregate.AvailableQty+aggregate.PendingReduceQty > 0 {
		audit.Status = "consistent"
	}
	return audit
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func chooseTruthSnapshotScope(st *store.Store, traderID, selectedSymbol string) (string, string, error) {
	symbol := strings.ToUpper(strings.TrimSpace(selectedSymbol))
	if symbol != "" {
		aggregates, err := st.PositionAggregate().ListByTraderID(traderID)
		if err != nil {
			return "", "", err
		}
		for _, aggregate := range aggregates {
			if aggregate == nil || !strings.EqualFold(aggregate.Symbol, symbol) {
				continue
			}
			return aggregate.Symbol, aggregate.Side, nil
		}
		rows, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
		if err != nil {
			return "", "", err
		}
		for _, row := range rows {
			if row == nil {
				continue
			}
			return row.Symbol, row.Side, nil
		}
		return symbol, "", nil
	}

	aggregates, err := st.PositionAggregate().ListByTraderID(traderID)
	if err != nil {
		return "", "", err
	}
	for _, aggregate := range aggregates {
		if aggregate == nil {
			continue
		}
		if strings.TrimSpace(aggregate.Symbol) != "" {
			return aggregate.Symbol, aggregate.Side, nil
		}
	}

	rows, err := st.OrderRegistry().ListByTraderID(traderID)
	if err != nil {
		return "", "", err
	}
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.Symbol) == "" {
			continue
		}
		return row.Symbol, row.Side, nil
	}

	groups, err := st.ProtectionGroup().ListByTraderID(traderID)
	if err != nil {
		return "", "", err
	}
	for _, group := range groups {
		if group == nil || strings.TrimSpace(group.Symbol) == "" {
			continue
		}
		return group.Symbol, group.Side, nil
	}

	plans, err := st.ScaleOutPlan().ListByTraderID(traderID)
	if err != nil {
		return "", "", err
	}
	for _, plan := range plans {
		if plan == nil || strings.TrimSpace(plan.Symbol) == "" {
			continue
		}
		return plan.Symbol, plan.Side, nil
	}

	inPlans, err := st.ScaleInPlan().ListByTraderID(traderID)
	if err != nil {
		return "", "", err
	}
	for _, plan := range inPlans {
		if plan == nil || strings.TrimSpace(plan.Symbol) == "" {
			continue
		}
		return plan.Symbol, plan.Side, nil
	}

	return "", "", nil
}

func diffTruthSnapshots(before, after *TruthSnapshotSummary) []ValidationMismatchReason {
	reasons := make([]ValidationMismatchReason, 0)
	if before == nil || after == nil {
		return reasons
	}

	if before.SelectedSymbol != after.SelectedSymbol {
		reasons = append(reasons, ValidationMismatchReason{
			Category: "restore",
			Reason:   "selected symbol changed across validation snapshots",
			Field:    "selected_symbol",
			Expected: before.SelectedSymbol,
			Actual:   after.SelectedSymbol,
		})
	}

	appendFloatDiff := func(field string, symbol string, expected, actual float64) {
		if expected == actual {
			return
		}
		reasons = append(reasons, ValidationMismatchReason{
			Category: "restore",
			Reason:   field + " changed across validation snapshots",
			Field:    field,
			Symbol:   symbol,
			Expected: fmt.Sprintf("%.6f", expected),
			Actual:   fmt.Sprintf("%.6f", actual),
		})
	}

	appendStringDiff := func(field string, symbol string, expected, actual string) {
		if expected == actual {
			return
		}
		reasons = append(reasons, ValidationMismatchReason{
			Category: "restore",
			Reason:   field + " changed across validation snapshots",
			Field:    field,
			Symbol:   symbol,
			Expected: expected,
			Actual:   actual,
		})
	}

	if before.PositionAggregate != nil && after.PositionAggregate != nil {
		appendFloatDiff("total_qty", after.PositionAggregate.Symbol, before.PositionAggregate.TotalQty, after.PositionAggregate.TotalQty)
		appendFloatDiff("available_qty", after.PositionAggregate.Symbol, before.PositionAggregate.AvailableQty, after.PositionAggregate.AvailableQty)
		appendFloatDiff("avg_entry_price", after.PositionAggregate.Symbol, before.PositionAggregate.AvgEntryPrice, after.PositionAggregate.AvgEntryPrice)
		appendFloatDiff("protected_quantity", after.PositionAggregate.Symbol, before.PositionAggregate.ProtectedQuantity, after.PositionAggregate.ProtectedQuantity)
		appendFloatDiff("pending_scale_out_qty", after.PositionAggregate.Symbol, before.PositionAggregate.PendingScaleOutQty, after.PositionAggregate.PendingScaleOutQty)
		appendFloatDiff("remaining_scale_out_qty", after.PositionAggregate.Symbol, before.PositionAggregate.RemainingScaleOutQty, after.PositionAggregate.RemainingScaleOutQty)
		appendFloatDiff("pending_add_qty", after.PositionAggregate.Symbol, before.PositionAggregate.PendingAddQty, after.PositionAggregate.PendingAddQty)
		appendFloatDiff("remaining_scale_in_qty", after.PositionAggregate.Symbol, before.PositionAggregate.RemainingScaleInQty, after.PositionAggregate.RemainingScaleInQty)
		appendFloatDiff("current_stop_loss_price", after.PositionAggregate.Symbol, before.PositionAggregate.CurrentStopLossPrice, after.PositionAggregate.CurrentStopLossPrice)
		appendStringDiff("protection_mode", after.PositionAggregate.Symbol, before.PositionAggregate.ProtectionMode, after.PositionAggregate.ProtectionMode)
		appendStringDiff("scale_out_status", after.PositionAggregate.Symbol, before.PositionAggregate.ScaleOutStatus, after.PositionAggregate.ScaleOutStatus)
		appendStringDiff("scale_in_status", after.PositionAggregate.Symbol, before.PositionAggregate.ScaleInStatus, after.PositionAggregate.ScaleInStatus)
	}

	if before.OrderSummary != nil && after.OrderSummary != nil {
		appendFloatDiff("pending_reduce_qty", after.OrderSummary.Symbol, before.OrderSummary.PendingReduceQty, after.OrderSummary.PendingReduceQty)
		appendFloatDiff("pending_add_qty", after.OrderSummary.Symbol, before.OrderSummary.PendingAddQty, after.OrderSummary.PendingAddQty)
		appendFloatDiff("protection_coverage_qty", after.OrderSummary.Symbol, before.OrderSummary.ProtectionCoverageQty, after.OrderSummary.ProtectionCoverageQty)
	}

	if before.ProtectionSummary != nil && after.ProtectionSummary != nil {
		appendStringDiff("protection_group_status", after.ProtectionSummary.Symbol, before.ProtectionSummary.ProtectionGroupStatus, after.ProtectionSummary.ProtectionGroupStatus)
		appendStringDiff("protection_consistency", after.ProtectionSummary.Symbol, before.ProtectionSummary.ConsistencyStatus, after.ProtectionSummary.ConsistencyStatus)
		appendFloatDiff("protection_summary.protected_quantity", after.ProtectionSummary.Symbol, before.ProtectionSummary.ProtectedQuantity, after.ProtectionSummary.ProtectedQuantity)
	}

	if before.ScaleOutSummary != nil && after.ScaleOutSummary != nil {
		appendStringDiff("scale_out_consistency", after.ScaleOutSummary.Symbol, before.ScaleOutSummary.ConsistencyStatus, after.ScaleOutSummary.ConsistencyStatus)
		appendFloatDiff("scale_out.executed_qty", after.ScaleOutSummary.Symbol, before.ScaleOutSummary.ExecutedScaleOutQty, after.ScaleOutSummary.ExecutedScaleOutQty)
		appendFloatDiff("scale_out.remaining_qty", after.ScaleOutSummary.Symbol, before.ScaleOutSummary.RemainingScaleOutQty, after.ScaleOutSummary.RemainingScaleOutQty)
	}

	if before.ScaleInSummary != nil && after.ScaleInSummary != nil {
		appendStringDiff("scale_in_consistency", after.ScaleInSummary.Symbol, before.ScaleInSummary.ConsistencyStatus, after.ScaleInSummary.ConsistencyStatus)
		appendFloatDiff("scale_in.executed_qty", after.ScaleInSummary.Symbol, before.ScaleInSummary.ExecutedScaleInQty, after.ScaleInSummary.ExecutedScaleInQty)
		appendFloatDiff("scale_in.remaining_qty", after.ScaleInSummary.Symbol, before.ScaleInSummary.RemainingScaleInQty, after.ScaleInSummary.RemainingScaleInQty)
	}

	return reasons
}

func chooseNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
