package trader

import (
	"fmt"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
	"nofx/trader/types"
)

// OrderStateMismatchReason is a machine-readable order-truth mismatch explanation.
// It is derived from reconciliation, not from a raw exchange event.
type OrderStateMismatchReason struct {
	Category        string `json:"category"`                    // Mismatch bucket used by preview/debug tooling.
	Reason          string `json:"reason"`                      // Human-readable mismatch explanation.
	Symbol          string `json:"symbol,omitempty"`            // Symbol scope for the mismatch.
	OrderRole       string `json:"order_role,omitempty"`        // Registry role involved in the mismatch.
	LocalIntentID   string `json:"local_intent_id,omitempty"`   // Local intent id when available.
	ExchangeOrderID string `json:"exchange_order_id,omitempty"` // Exchange order id when available.
	ClientOrderID   string `json:"client_order_id,omitempty"`   // Exchange client id when available.
}

// OrderReconcilePreview is the read-only order truth preview returned by the reconciliation layer.
// It is not a raw exchange order list: it combines registry truth, recent events, and reconcile status.
type OrderReconcilePreview struct {
	TruthSnapshotMetadata
	TraderID                         string                                `json:"trader_id"`                           // System-owned trader key.
	SelectedSymbol                   string                                `json:"selected_symbol,omitempty"`           // Selected symbol scope used for summary and mismatch detection.
	Exchange                         string                                `json:"exchange"`                            // Fixed phase-2 scope: binance_usdm.
	Mode                             string                                `json:"mode"`                                // Fixed phase-2 scope: one_way.
	OrderRegistry                    []*store.OrderRegistry                `json:"order_registry"`                      // System-owned registry rows, not the raw exchange order table.
	OrderEventLogs                   []*store.OrderEventLog                `json:"order_event_logs"`                    // Append-only raw event evidence chain.
	HasProtection                    bool                                  `json:"has_protection"`                      // System-derived flag showing whether a protection group is present.
	ProtectionMode                   string                                `json:"protection_mode"`                     // System-derived protection policy label.
	ProtectionGroupStatus            string                                `json:"protection_group_status"`             // Current protection group truth status.
	StopLossArmed                    bool                                  `json:"stop_loss_armed"`                     // System-derived stop-loss leg working flag.
	TakeProfitArmed                  bool                                  `json:"take_profit_armed"`                   // System-derived take-profit leg working flag.
	ProtectionConsistency            string                                `json:"protection_consistency"`              // Protection consistency enum: consistent, mismatch, pending, or unknown.
	ProtectionAdjustmentStatus       string                                `json:"protection_adjustment_status"`        // Dynamic protection status label.
	ProtectionAdjustmentConsistency  string                                `json:"protection_adjustment_consistency"`   // Dynamic protection consistency enum: consistent, mismatch, pending, or unknown.
	ProtectionAdjustmentBlockReasons []CapabilityReason                    `json:"protection_adjustment_block_reasons"` // Machine-readable dynamic-protection block reasons.
	ProtectionRevision               int                                   `json:"protection_revision"`                 // System-owned protection revision snapshot.
	CurrentStopLossPrice             float64                               `json:"current_stop_loss_price"`             // Current stop-loss trigger price from the protection group.
	InitialStopLossPrice             float64                               `json:"initial_stop_loss_price"`             // Initial stop-loss trigger price before any protection movement.
	BreakEvenArmed                   bool                                  `json:"break_even_armed"`                    // System-derived break-even flag.
	TrailingArmed                    bool                                  `json:"trailing_armed"`                      // System-derived trailing flag.
	RemainingMoveBudget              float64                               `json:"remaining_move_budget"`               // Remaining dynamic-protection move budget.
	ProtectionBlockReasons           []store.ProtectionStateMismatchReason `json:"protection_block_reasons"`            // Machine-readable protection mismatch explanation list.
	HasScaleOutPlan                  bool                                  `json:"has_scale_out_plan"`                  // Truth-layer flag showing whether a scale-out plan exists.
	ScaleOutStatus                   string                                `json:"scale_out_status"`                    // Current scale-out plan truth status.
	ScaleOutConsistency              string                                `json:"scale_out_consistency"`               // Scale-out consistency enum: consistent, mismatch, pending, or unknown.
	HasScaleOutMismatch              bool                                  `json:"has_scale_out_mismatch"`              // Reconcile flag showing that the scale-out truth layer disagrees.
	PendingScaleOutQty               float64                               `json:"pending_scale_out_qty"`               // Truth-layer working-order reserve for active scale-out levels; subset of RemainingScaleOutQty.
	RemainingScaleOutQty             float64                               `json:"remaining_scale_out_qty"`             // Truth-layer total quantity still outstanding across the current plan, including working and not-yet-working levels.
	ExecutedScaleOutQty              float64                               `json:"executed_scale_out_qty"`              // Truth-layer cumulative executed quantity across the current plan.
	ScaleOutBlockReasons             []CapabilityReason                    `json:"scale_out_block_reasons"`             // Machine-readable scale-out block reasons for UI/debug.
	ProtectionRebalanced             bool                                  `json:"protection_rebalanced"`               // Derived flag showing whether protection coverage matches the current position.
	ReconcileStatus                  string                                `json:"reconcile_status"`                    // Reconcile status label for debug/UI review.
	HasWorkingOrders                 bool                                  `json:"has_working_orders"`                  // Truth-layer flag derived from registry rows.
	HasPendingCancelReplace          bool                                  `json:"has_pending_cancel_replace"`          // Truth-layer flag derived from registry rows.
	PendingAddQty                    float64                               `json:"pending_add_qty"`                     // Truth-layer pending add quantity.
	PendingReduceQty                 float64                               `json:"pending_reduce_qty"`                  // Truth-layer order-layer reduce reserve, including scale-out and protection rows; not the remaining position size.
	HasStateMismatch                 bool                                  `json:"has_state_mismatch"`                  // Reconcile result flag; true means registry and exchange disagree.
	MismatchReasons                  []OrderStateMismatchReason            `json:"mismatch_reasons"`                    // Machine-readable mismatch explanation list.
	UserStreamReady                  bool                                  `json:"user_stream_ready"`                   // Runtime readiness flag for user-stream ingestion.
	GeneratedAt                      time.Time                             `json:"generated_at"`                        // Response generation timestamp.
	LocalSummary                     *store.OrderRegistrySummary           `json:"local_summary,omitempty"`             // System-derived summary used by preview and capability clipping.
}

type openOrderReader interface {
	GetOpenOrders(symbol string) ([]types.OpenOrder, error)
}

// OrderStateReconciler compares local registry truth with exchange open orders and builds the preview response.
type OrderStateReconciler struct {
	store   *store.Store
	builder *store.OrderRegistryBuilder
}

// NewOrderStateReconciler creates a new reconciler.
func NewOrderStateReconciler(st *store.Store) *OrderStateReconciler {
	if st == nil {
		return &OrderStateReconciler{}
	}
	return &OrderStateReconciler{
		store:   st,
		builder: st.OrderRegistryBuilder(),
	}
}

// ReconcileTraderOrders compares the local order truth layer with the exchange open-order snapshot.
// It keeps the registry as the single truth source and only refreshes from legacy rows when the registry is empty.
func (r *OrderStateReconciler) ReconcileTraderOrders(traderID, selectedSymbol string, reader openOrderReader, userStreamReady bool) (*OrderReconcilePreview, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("order state reconciler store is not configured")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	logger.Infof("order reconcile started: trader=%s symbol=%s user_stream_ready=%v", traderID, selectedSymbol, userStreamReady)

	registryRows, err := r.store.OrderRegistry().ListByTraderID(traderID)
	if err != nil {
		return nil, err
	}
	protectionAdjustmentPreview, adjustmentErr := BuildRuntimeProtectionAdjustmentPreview(r.store, traderID, selectedSymbol, userStreamReady, 0)
	if adjustmentErr != nil {
		logger.Infof("protection adjustment preview fallback for trader %s: %v", traderID, adjustmentErr)
	}
	scaleOutPreview, err := BuildRuntimeScaleOutPreview(r.store, traderID, selectedSymbol, userStreamReady)
	if err != nil {
		return nil, err
	}
	if len(registryRows) == 0 {
		if _, err := r.builder.BuildFromLegacyOrders(traderID); err != nil {
			return nil, err
		}
		registryRows, err = r.store.OrderRegistry().ListByTraderID(traderID)
		if err != nil {
			return nil, err
		}
	}

	selectedSymbol = chooseOrderPreviewSymbol(selectedSymbol, registryRows)
	selectedSide := chooseOrderPreviewSide(selectedSymbol, registryRows)

	protectionSummary, err := r.store.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, selectedSymbol, selectedSide)
	if err != nil {
		return nil, err
	}

	localSummary := &store.OrderRegistrySummary{}
	if selectedSymbol != "" {
		if selectedSide != "" {
			localSummary, err = r.store.OrderRegistry().SummarizeForTraderSymbolSide(traderID, selectedSymbol, selectedSide)
		} else {
			localSummary, err = r.store.OrderRegistry().SummarizeForTraderSymbol(traderID, selectedSymbol)
		}
		if err != nil {
			return nil, err
		}
	}

	eventLogs, err := r.store.OrderEventLog().ListRecentByTrader(traderID, 20)
	if err != nil {
		return nil, err
	}

	reasons := make([]OrderStateMismatchReason, 0)
	reconcileStatus := "empty"
	hasStateMismatch := false
	var exchangeOrders []types.OpenOrder
	if reader != nil && selectedSymbol != "" {
		exchangeOrders, err = reader.GetOpenOrders(selectedSymbol)
		if err != nil {
			return nil, err
		}
	}

	localSelectedRows := filterRegistryRowsBySymbolSide(registryRows, selectedSymbol, selectedSide)
	if len(localSelectedRows) > 0 || len(exchangeOrders) > 0 {
		reconcileStatus = "synced"
	}

	localWorking := make(map[string]*store.OrderRegistry)
	for _, row := range localSelectedRows {
		if row == nil {
			continue
		}
		if row.ExchangeOrderID == "" && row.IsWorking {
			reasons = append(reasons, OrderStateMismatchReason{
				Category:      "pending_ack",
				Reason:        "local order is waiting for exchange acknowledgement",
				Symbol:        selectedSymbol,
				OrderRole:     row.OrderRole,
				LocalIntentID: row.LocalIntentID,
				ClientOrderID: row.ClientOrderID,
			})
			if reconcileStatus == "synced" || reconcileStatus == "empty" {
				reconcileStatus = "pending_exchange_ack"
			}
		}
		if row.IsWorking {
			if row.ExchangeOrderID != "" {
				localWorking[row.ExchangeOrderID] = row
			}
		}
		if store.NormalizeOrderRegistryRole(row.OrderRole) == "cancel_replace" || store.NormalizeOrderRegistryStatus(row.Status) == "PENDING_REPLACE" {
			reasons = append(reasons, OrderStateMismatchReason{
				Category:        "order_state",
				Reason:          "current cancel_replace is not completed",
				Symbol:          selectedSymbol,
				OrderRole:       row.OrderRole,
				LocalIntentID:   row.LocalIntentID,
				ExchangeOrderID: row.ExchangeOrderID,
				ClientOrderID:   row.ClientOrderID,
			})
			hasStateMismatch = true
		}
		if row.RemainingQty > 0 && !row.IsWorking {
			reasons = append(reasons, OrderStateMismatchReason{
				Category:        "order_state",
				Reason:          "registry row still has remaining quantity but is not marked working",
				Symbol:          selectedSymbol,
				OrderRole:       row.OrderRole,
				LocalIntentID:   row.LocalIntentID,
				ExchangeOrderID: row.ExchangeOrderID,
				ClientOrderID:   row.ClientOrderID,
			})
			hasStateMismatch = true
		}
	}

	exchangeWorking := make(map[string]types.OpenOrder)
	for _, order := range exchangeOrders {
		exchangeWorking[order.OrderID] = order
	}

	for exchangeOrderID, row := range localWorking {
		if row == nil {
			continue
		}

		order, exists := exchangeWorking[exchangeOrderID]
		if !exists {
			reasons = append(reasons, OrderStateMismatchReason{
				Category:        "order_state",
				Reason:          "registry has a working order but the exchange snapshot does not",
				Symbol:          selectedSymbol,
				OrderRole:       row.OrderRole,
				LocalIntentID:   row.LocalIntentID,
				ExchangeOrderID: row.ExchangeOrderID,
				ClientOrderID:   row.ClientOrderID,
			})
			hasStateMismatch = true
			continue
		}

		if !stringEqualFold(order.Symbol, row.Symbol) || !stringEqualFold(order.Side, expectedExchangeSide(row.Side)) || !stringEqualFold(order.PositionSide, expectedExchangePositionSide(row.Side)) {
			reasons = append(reasons, OrderStateMismatchReason{
				Category:        "order_state",
				Reason:          "exchange working order metadata differs from registry metadata",
				Symbol:          selectedSymbol,
				OrderRole:       row.OrderRole,
				LocalIntentID:   row.LocalIntentID,
				ExchangeOrderID: row.ExchangeOrderID,
				ClientOrderID:   row.ClientOrderID,
			})
			hasStateMismatch = true
		}

		if row.OrigQty > 0 && !floatEqual(row.OrigQty, order.Quantity, 0.000001) {
			reasons = append(reasons, OrderStateMismatchReason{
				Category:        "order_state",
				Reason:          "exchange order quantity differs from registry quantity",
				Symbol:          selectedSymbol,
				OrderRole:       row.OrderRole,
				LocalIntentID:   row.LocalIntentID,
				ExchangeOrderID: row.ExchangeOrderID,
				ClientOrderID:   row.ClientOrderID,
			})
			hasStateMismatch = true
		}
	}

	for exchangeOrderID, order := range exchangeWorking {
		row, exists := localWorking[exchangeOrderID]
		if exists && row != nil {
			continue
		}
		reasons = append(reasons, OrderStateMismatchReason{
			Category:        "order_state",
			Reason:          "exchange has a working order that is not tracked locally",
			Symbol:          selectedSymbol,
			ExchangeOrderID: order.OrderID,
		})
		hasStateMismatch = true
	}

	if !userStreamReady {
		reconcileStatus = "pending_user_stream"
	}
	if hasStateMismatch {
		reconcileStatus = "mismatch"
	}
	if localSummary != nil && localSummary.HasWorkingOrders && !hasStateMismatch && userStreamReady {
		reconcileStatus = "synced"
	}
	if localSummary != nil && localSummary.HasWorkingOrders && !userStreamReady {
		reconcileStatus = "pending_user_stream"
	}
	if localSummary != nil && localSummary.HasPendingCancelReplace {
		reconcileStatus = "pending_cancel_replace"
	}
	if len(exchangeOrders) == 0 && len(localSelectedRows) == 0 {
		reconcileStatus = "empty"
	}

	if hasStateMismatch {
		logger.Infof("order reconcile mismatch found: trader=%s symbol=%s reasons=%d", traderID, selectedSymbol, len(reasons))
		for _, reason := range reasons {
			logger.Infof("order reconcile mismatch found: trader=%s symbol=%s category=%s reason=%s exchange_order_id=%s local_intent_id=%s",
				traderID, reason.Symbol, reason.Category, reason.Reason, reason.ExchangeOrderID, reason.LocalIntentID)
		}
	}

	if r.store != nil {
		if _, err := store.NewPositionAggregateBuilder(r.store).BuildForTrader(traderID, nil, nil); err != nil {
			return nil, err
		}
	}

	preview := &OrderReconcilePreview{
		TraderID:                         traderID,
		SelectedSymbol:                   selectedSymbol,
		Exchange:                         "binance_usdm",
		Mode:                             "one_way",
		OrderRegistry:                    registryRows,
		OrderEventLogs:                   eventLogs,
		HasProtection:                    protectionSummary.HasProtection,
		ProtectionMode:                   protectionSummary.ProtectionMode,
		ProtectionGroupStatus:            protectionSummary.ProtectionGroupStatus,
		StopLossArmed:                    protectionSummary.StopLossArmed,
		TakeProfitArmed:                  protectionSummary.TakeProfitArmed,
		ProtectionConsistency:            protectionSummary.ConsistencyStatus,
		ProtectionAdjustmentStatus:       pickProtectionAdjustmentStatus(protectionAdjustmentPreview),
		ProtectionAdjustmentConsistency:  pickProtectionAdjustmentConsistency(protectionAdjustmentPreview),
		ProtectionAdjustmentBlockReasons: pickProtectionAdjustmentBlockReasons(protectionAdjustmentPreview),
		ProtectionRevision:               pickProtectionAdjustmentRevision(protectionAdjustmentPreview),
		CurrentStopLossPrice:             pickProtectionAdjustmentCurrentStop(protectionAdjustmentPreview),
		InitialStopLossPrice:             pickProtectionAdjustmentInitialStop(protectionAdjustmentPreview),
		BreakEvenArmed:                   pickProtectionAdjustmentBreakEven(protectionAdjustmentPreview),
		TrailingArmed:                    pickProtectionAdjustmentTrailing(protectionAdjustmentPreview),
		RemainingMoveBudget:              pickProtectionAdjustmentRemainingBudget(protectionAdjustmentPreview),
		ProtectionBlockReasons:           protectionSummary.MismatchReasons,
		HasScaleOutPlan:                  scaleOutPreview.HasScaleOutPlan,
		ScaleOutStatus:                   scaleOutPreview.ScaleOutStatus,
		ScaleOutConsistency:              scaleOutPreview.ScaleOutConsistency,
		HasScaleOutMismatch:              scaleOutPreview.HasStateMismatch,
		PendingScaleOutQty:               scaleOutPreview.PendingScaleOutQty,
		RemainingScaleOutQty:             scaleOutPreview.RemainingScaleOutQty,
		ExecutedScaleOutQty:              scaleOutPreview.ExecutedScaleOutQty,
		ScaleOutBlockReasons:             scaleOutPreview.ScaleOutBlockReasons,
		ProtectionRebalanced:             scaleOutPreview.ProtectionRebalanced,
		ReconcileStatus:                  reconcileStatus,
		HasWorkingOrders:                 localSummary != nil && localSummary.HasWorkingOrders,
		HasPendingCancelReplace:          localSummary != nil && localSummary.HasPendingCancelReplace,
		PendingAddQty:                    localSummary.PendingAddQty,
		PendingReduceQty:                 localSummary.PendingReduceQty,
		HasStateMismatch:                 hasStateMismatch,
		MismatchReasons:                  reasons,
		UserStreamReady:                  userStreamReady,
		GeneratedAt:                      time.Now().UTC(),
		LocalSummary:                     localSummary,
	}
	preview.TruthSnapshotMetadata = populateRuntimeMetadata(preview.GeneratedAt, "", false, "")
	logger.Infof("order reconcile built: trader=%s symbol=%s protection_adjustment_status=%s protection_adjustment_consistency=%s protection_revision=%d current_stop_loss_price=%.6f initial_stop_loss_price=%.6f break_even_armed=%v trailing_armed=%v remaining_move_budget=%.6f",
		preview.TraderID, preview.SelectedSymbol, preview.ProtectionAdjustmentStatus, preview.ProtectionAdjustmentConsistency, preview.ProtectionRevision, preview.CurrentStopLossPrice, preview.InitialStopLossPrice, preview.BreakEvenArmed, preview.TrailingArmed, preview.RemainingMoveBudget)
	return preview, nil
}

// BuildRuntimeOrderPreview is a convenience wrapper for read-only callers.
func BuildRuntimeOrderPreview(st *store.Store, traderID, selectedSymbol string, userStreamReady bool) (*OrderReconcilePreview, error) {
	reconciler := NewOrderStateReconciler(st)
	return reconciler.ReconcileTraderOrders(traderID, selectedSymbol, nil, userStreamReady)
}

func chooseOrderPreviewSymbol(selectedSymbol string, rows []*store.OrderRegistry) string {
	symbol := normalizeCapabilitySymbol(selectedSymbol)
	if symbol != "" {
		return symbol
	}

	for _, row := range rows {
		if row == nil {
			continue
		}
		if sym := normalizeCapabilitySymbol(row.Symbol); sym != "" {
			return sym
		}
	}

	return ""
}

func chooseOrderPreviewSide(symbol string, rows []*store.OrderRegistry) string {
	normalizedSymbol := normalizeCapabilitySymbol(symbol)
	for _, row := range rows {
		if row == nil {
			continue
		}
		if normalizedSymbol != "" && !strings.EqualFold(normalizeCapabilitySymbol(row.Symbol), normalizedSymbol) {
			continue
		}
		if side := normalizeOneWaySide(row.Side); side != "" {
			return side
		}
	}
	return ""
}

func filterRegistryRowsBySymbolSide(rows []*store.OrderRegistry, symbol, side string) []*store.OrderRegistry {
	if len(rows) == 0 {
		return []*store.OrderRegistry{}
	}
	normalizedSymbol := normalizeCapabilitySymbol(symbol)
	normalizedSide := normalizeOneWaySide(side)
	filtered := make([]*store.OrderRegistry, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		if normalizedSymbol != "" && !strings.EqualFold(normalizeCapabilitySymbol(row.Symbol), normalizedSymbol) {
			continue
		}
		if normalizedSide != "" && !strings.EqualFold(normalizeOneWaySide(row.Side), normalizedSide) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func expectedExchangeSide(side string) string {
	switch normalizeOneWaySide(side) {
	case "LONG":
		return "BUY"
	case "SHORT":
		return "SELL"
	default:
		return strings.ToUpper(strings.TrimSpace(side))
	}
}

func expectedExchangePositionSide(side string) string {
	switch normalizeOneWaySide(side) {
	case "LONG":
		return "LONG"
	case "SHORT":
		return "SHORT"
	default:
		return ""
	}
}

func normalizeOneWaySide(side string) string {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "BUY", "LONG":
		return "LONG"
	case "SELL", "SHORT":
		return "SHORT"
	default:
		return strings.ToUpper(strings.TrimSpace(side))
	}
}

func floatEqual(a, b, tolerance float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance
}

func stringEqualFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
