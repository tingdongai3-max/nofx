package trader

import (
	"fmt"
	"time"

	"nofx/logger"
	"nofx/store"
)

// ProtectionReconcilePreview is the read-only protection truth preview returned by the backend.
// It combines the protection group truth layer, linked order evidence, and reconcile status.
type ProtectionReconcilePreview struct {
	TruthSnapshotMetadata
	TraderID                         string                                `json:"trader_id"`                           // System-owned trader key.
	SelectedSymbol                   string                                `json:"selected_symbol,omitempty"`           // Selected symbol scope used for the preview.
	Exchange                         string                                `json:"exchange"`                            // Fixed phase-3 exchange scope: binance_usdm.
	Mode                             string                                `json:"mode"`                                // Fixed phase-3 mode scope: one_way.
	ProtectionGroup                  *store.ProtectionGroup                `json:"protection_group,omitempty"`          // Current protection group truth row.
	ProtectionGroups                 []*store.ProtectionGroup              `json:"protection_groups"`                   // Current protection snapshot rows for debug.
	ProtectionEventLogs              []*store.ProtectionEventLog           `json:"protection_event_logs"`               // Append-only raw protection evidence chain.
	PositionAggregate                *store.PositionAggregate              `json:"position_aggregate,omitempty"`        // System-owned position truth snapshot for the selected scope.
	OrderRegistrySummary             *store.OrderRegistrySummary           `json:"order_registry_summary,omitempty"`    // System-owned order truth summary for the selected scope.
	HasProtection                    bool                                  `json:"has_protection"`                      // System-derived flag that active protection exists.
	ProtectionMode                   string                                `json:"protection_mode"`                     // Current protection policy label.
	ProtectionGroupStatus            string                                `json:"protection_group_status"`             // Current protection truth status.
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
	HasStateMismatch                 bool                                  `json:"has_state_mismatch"`                  // Reconcile result flag; true means truth layers disagree.
	MismatchReasons                  []store.ProtectionStateMismatchReason `json:"mismatch_reasons"`                    // Machine-readable mismatch explanation list.
	HasWorkingOrders                 bool                                  `json:"has_working_orders"`                  // Truth-layer flag derived from linked working protection rows.
	HasProtectionOrders              bool                                  `json:"has_protection_orders"`               // Truth-layer flag indicating protection rows exist.
	PendingAddQty                    float64                               `json:"pending_add_qty"`                     // Truth-layer pending add quantity from the order registry summary.
	PendingReduceQty                 float64                               `json:"pending_reduce_qty"`                  // Truth-layer order-layer reduce reserve from the order registry summary; includes scale-out and protection rows.
	HasScaleOutPlan                  bool                                  `json:"has_scale_out_plan"`                  // Truth-layer flag showing whether a scale-out plan exists.
	ScaleOutStatus                   string                                `json:"scale_out_status"`                    // Current scale-out plan truth status.
	ScaleOutConsistency              string                                `json:"scale_out_consistency"`               // Scale-out consistency enum: consistent, mismatch, pending, or unknown.
	HasScaleOutMismatch              bool                                  `json:"has_scale_out_mismatch"`              // Reconcile flag showing that the scale-out truth layer disagrees.
	PendingScaleOutQty               float64                               `json:"pending_scale_out_qty"`               // Truth-layer working-order reserve for active scale-out levels; subset of RemainingScaleOutQty.
	RemainingScaleOutQty             float64                               `json:"remaining_scale_out_qty"`             // Truth-layer total quantity still outstanding across the current plan, including working and not-yet-working levels.
	ExecutedScaleOutQty              float64                               `json:"executed_scale_out_qty"`              // Truth-layer cumulative executed quantity across the current plan.
	ScaleOutBlockReasons             []CapabilityReason                    `json:"scale_out_block_reasons"`             // Machine-readable scale-out block reasons for UI/debug.
	ProtectionRebalanced             bool                                  `json:"protection_rebalanced"`               // Derived flag showing whether protection coverage matches the current position.
	UserStreamReady                  bool                                  `json:"user_stream_ready"`                   // Runtime readiness flag for the Binance user-stream bridge.
	GeneratedAt                      time.Time                             `json:"generated_at"`                        // Response generation timestamp.
}

// ProtectionStateReconciler compares local protection truth with exchange/user-stream state and builds the preview response.
// It also owns the automatic fixed-protection attach and sibling-cancel flow for Binance-only one-way mode.
type ProtectionStateReconciler struct {
	store        *store.Store
	fixedManager *FixedProtectionManager
}

// NewProtectionStateReconciler creates a new protection state reconciler.
func NewProtectionStateReconciler(st *store.Store, fixedManager *FixedProtectionManager) *ProtectionStateReconciler {
	if st == nil {
		return &ProtectionStateReconciler{}
	}
	return &ProtectionStateReconciler{
		store:        st,
		fixedManager: fixedManager,
	}
}

// PreviewTraderProtection returns the read-only protection preview for a trader.
// It never changes execution behavior; it only reads runtime state and returns a protection snapshot.
func (r *ProtectionStateReconciler) PreviewTraderProtection(traderID, selectedSymbol string, client Trader, userStreamReady bool) (*ProtectionReconcilePreview, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("protection state reconciler store is not configured")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	symbol, side, err := r.chooseScope(traderID, selectedSymbol)
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

	protectionSummary, err := r.store.ProtectionGroup().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		return nil, err
	}

	var protectionGroup *store.ProtectionGroup
	var protectionGroups []*store.ProtectionGroup
	if symbol != "" {
		protectionGroups, err = r.store.ProtectionGroup().ListByTraderSymbolSide(traderID, symbol, side)
		if err != nil {
			return nil, err
		}
		if len(protectionGroups) > 0 {
			protectionGroup = protectionGroups[0]
		}
	}

	orderSummary, err := r.store.OrderRegistry().SummarizeForTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		return nil, err
	}

	var positionAggregate *store.PositionAggregate
	if symbol != "" {
		positionAggregate, err = r.store.PositionAggregate().GetByTraderSymbolSide(traderID, symbol, side)
		if err != nil {
			return nil, err
		}
	}

	eventLogs := []*store.ProtectionEventLog{}
	if symbol != "" {
		eventLogs, err = r.store.ProtectionEventLog().ListRecentByTraderSymbol(traderID, symbol, 20)
		if err != nil {
			return nil, err
		}
	} else {
		eventLogs, err = r.store.ProtectionEventLog().ListRecentByTrader(traderID, 20)
		if err != nil {
			return nil, err
		}
	}

	preview := &ProtectionReconcilePreview{
		TraderID:                         traderID,
		SelectedSymbol:                   symbol,
		Exchange:                         "binance_usdm",
		Mode:                             "one_way",
		ProtectionGroup:                  protectionGroup,
		ProtectionGroups:                 protectionGroups,
		ProtectionEventLogs:              eventLogs,
		PositionAggregate:                positionAggregate,
		OrderRegistrySummary:             orderSummary,
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
		HasStateMismatch:                 protectionSummary.HasStateMismatch,
		MismatchReasons:                  protectionSummary.MismatchReasons,
		HasWorkingOrders:                 protectionSummary.HasWorkingOrders,
		HasProtectionOrders:              protectionSummary.HasProtection || protectionSummary.HasWorkingOrders,
		PendingAddQty:                    orderSummary.PendingAddQty,
		PendingReduceQty:                 orderSummary.PendingReduceQty,
		HasScaleOutPlan:                  scaleOutPreview.HasScaleOutPlan,
		ScaleOutStatus:                   scaleOutPreview.ScaleOutStatus,
		ScaleOutConsistency:              scaleOutPreview.ScaleOutConsistency,
		HasScaleOutMismatch:              scaleOutPreview.HasStateMismatch,
		PendingScaleOutQty:               scaleOutPreview.PendingScaleOutQty,
		RemainingScaleOutQty:             scaleOutPreview.RemainingScaleOutQty,
		ExecutedScaleOutQty:              scaleOutPreview.ExecutedScaleOutQty,
		ScaleOutBlockReasons:             scaleOutPreview.ScaleOutBlockReasons,
		ProtectionRebalanced:             scaleOutPreview.ProtectionRebalanced,
		UserStreamReady:                  userStreamReady,
		GeneratedAt:                      time.Now().UTC(),
	}
	preview.TruthSnapshotMetadata = populateRuntimeMetadata(preview.GeneratedAt, "", false, "")

	logger.Infof("protection preview built: trader=%s symbol=%s protection_group_status=%s protection_consistency=%s protection_adjustment_status=%s protection_adjustment_consistency=%s has_protection=%v has_working_orders=%v has_pending_cancel_replace=%v has_state_mismatch=%v user_stream_ready=%v",
		preview.TraderID, preview.SelectedSymbol, preview.ProtectionGroupStatus, preview.ProtectionConsistency, preview.ProtectionAdjustmentStatus, preview.ProtectionAdjustmentConsistency, preview.HasProtection, preview.HasWorkingOrders, preview.OrderRegistrySummary != nil && preview.OrderRegistrySummary.HasPendingCancelReplace, preview.HasStateMismatch, preview.UserStreamReady)

	return preview, nil
}

// SyncTraderProtectionState reconciles the protection truth layer and applies automatic fixed-protection actions when needed.
// It is the single side-effect entrypoint used by user-stream and order-sync callbacks.
func (r *ProtectionStateReconciler) SyncTraderProtectionState(traderID, selectedSymbol string, client Trader, userStreamReady bool) (*ProtectionReconcilePreview, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("protection state reconciler store is not configured")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	symbol, side, err := r.chooseScope(traderID, selectedSymbol)
	if err != nil {
		return nil, err
	}

	liveSnapshots, err := CollectLivePositionSnapshots(client)
	if err != nil {
		return nil, err
	}
	if client != nil {
		if _, err := store.NewPositionAggregateBuilder(r.store).BuildForTrader(traderID, liveSnapshots, nil); err != nil {
			return nil, err
		}
	}

	preview, err := r.PreviewTraderProtection(traderID, symbol, client, userStreamReady)
	if err != nil {
		return nil, err
	}

	if r.fixedManager != nil && preview.PositionAggregate != nil && preview.PositionAggregate.TotalQty > 0 {
		if !preview.HasProtection || preview.ProtectionGroupStatus == "closed" || (preview.ProtectionGroupStatus == "pending_attach" && !preview.HasWorkingOrders) {
			group, attachErr := r.fixedManager.EnsureFixedProtectionForAggregate(preview.PositionAggregate, nil)
			if attachErr != nil {
				return nil, attachErr
			}
			if group != nil {
				logger.Infof("fixed protection attach reconciled: trader=%s symbol=%s side=%s group=%s", traderID, group.Symbol, group.Side, group.ProtectionGroupID)
			}
			preview, err = r.PreviewTraderProtection(traderID, symbol, client, userStreamReady)
			if err != nil {
				return nil, err
			}
		}
	}

	if client != nil {
		if err := r.reconcileProtectionLegState(traderID, symbol, side, client, preview); err != nil {
			return nil, err
		}
	}

	if client != nil {
		liveSnapshots, _ = CollectLivePositionSnapshots(client)
		if _, err := store.NewPositionAggregateBuilder(r.store).BuildForTrader(traderID, liveSnapshots, nil); err != nil {
			return nil, err
		}
	}

	preview, err = r.PreviewTraderProtection(traderID, symbol, client, userStreamReady)
	if err != nil {
		return nil, err
	}

	return preview, nil
}

func (r *ProtectionStateReconciler) reconcileProtectionLegState(traderID, symbol, side string, client Trader, preview *ProtectionReconcilePreview) error {
	if preview == nil {
		return nil
	}

	orderRows, err := r.store.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		return err
	}
	var protectionRows []*store.OrderRegistry
	for _, row := range orderRows {
		if row == nil {
			continue
		}
		if normalizeOneWaySide(row.Side) != normalizeOneWaySide(side) {
			continue
		}
		role := store.NormalizeOrderRegistryRole(row.OrderRole)
		if role != "stop_loss" && role != "take_profit" {
			continue
		}
		protectionRows = append(protectionRows, row)
	}

	filledRow, siblingRow := detectFilledProtectionLeg(protectionRows)
	if filledRow == nil {
		if preview.PositionAggregate != nil && preview.PositionAggregate.TotalQty <= 0 && preview.HasProtection {
			return r.closeProtectionGroup(traderID, symbol, side, preview, client, nil)
		}
		return nil
	}

	logger.Infof("protection leg filled: trader=%s symbol=%s side=%s role=%s group=%s status=%s",
		traderID, filledRow.Symbol, filledRow.Side, filledRow.OrderRole, filledRow.LinkedGroupID, filledRow.Status)

	status := "cancel_pending"
	if filledRow.Status == "PARTIALLY_FILLED" || (filledRow.OrigQty > 0 && filledRow.RemainingQty > 0) {
		status = "partial_invalid"
	}

	currentGroupStatus := ""
	if preview.ProtectionGroup != nil {
		currentGroupStatus = store.NormalizeProtectionGroupStatus(preview.ProtectionGroup.Status)
	}
	if currentGroupStatus == status {
		logger.Infof("protection leg replay ignored: trader=%s symbol=%s side=%s role=%s group=%s status=%s",
			traderID, filledRow.Symbol, filledRow.Side, filledRow.OrderRole, filledRow.LinkedGroupID, status)
		return nil
	}
	if status == "cancel_pending" && currentGroupStatus == "closed" {
		logger.Infof("protection leg replay ignored after close: trader=%s symbol=%s side=%s role=%s group=%s",
			traderID, filledRow.Symbol, filledRow.Side, filledRow.OrderRole, filledRow.LinkedGroupID)
		return nil
	}

	if siblingRow != nil {
		if err := r.requestSiblingProtectionCancel(traderID, siblingRow, client); err != nil {
			return err
		}
	}

	if siblingRow != nil && siblingRow.IsWorking {
		if status == "cancel_pending" {
			logger.Infof("sibling protection cancel requested: trader=%s symbol=%s side=%s role=%s group=%s",
				traderID, siblingRow.Symbol, siblingRow.Side, siblingRow.OrderRole, siblingRow.LinkedGroupID)
		}
		return r.recordProtectionGroupStatus(preview.ProtectionGroup, status, "PROTECTION_LEG_FILLED", "protection_state_reconciler", filledRow)
	}

	if siblingRow != nil {
		logger.Infof("sibling protection cancel observed: trader=%s symbol=%s side=%s role=%s group=%s",
			traderID, siblingRow.Symbol, siblingRow.Side, siblingRow.OrderRole, siblingRow.LinkedGroupID)
	} else {
		groupID := ""
		if preview.ProtectionGroup != nil {
			groupID = preview.ProtectionGroup.ProtectionGroupID
		}
		logger.Infof("sibling protection leg missing: trader=%s symbol=%s side=%s group=%s",
			traderID, symbol, side, groupID)
	}
	return r.closeProtectionGroup(traderID, symbol, side, preview, client, []store.ProtectionStateMismatchReason{{
		Category: "protection_state",
		Reason:   "protection leg filled and sibling leg is no longer working",
		Symbol:   symbol,
		ProtectionGroupID: func() string {
			if preview.ProtectionGroup != nil {
				return preview.ProtectionGroup.ProtectionGroupID
			}
			return ""
		}(),
		OrderRole:       filledRow.OrderRole,
		LocalIntentID:   filledRow.LocalIntentID,
		ExchangeOrderID: filledRow.ExchangeOrderID,
		ClientOrderID:   filledRow.ClientOrderID,
	}})
}

func (r *ProtectionStateReconciler) requestSiblingProtectionCancel(traderID string, siblingRow *store.OrderRegistry, client Trader) error {
	if siblingRow == nil {
		return nil
	}
	if store.NormalizeOrderRegistryStatus(siblingRow.Status) == "PENDING_CANCEL" {
		return nil
	}
	if client != nil {
		switch store.NormalizeOrderRegistryRole(siblingRow.OrderRole) {
		case "stop_loss":
			if err := client.CancelStopLossOrders(siblingRow.Symbol); err != nil {
				return err
			}
		case "take_profit":
			if err := client.CancelTakeProfitOrders(siblingRow.Symbol); err != nil {
				return err
			}
		}
	}

	return r.recordProtectionOrderStatus(traderID, siblingRow, "PENDING_CANCEL", "SIBLING_PROTECTION_CANCEL_REQUESTED", "protection_state_reconciler")
}

func (r *ProtectionStateReconciler) closeProtectionGroup(traderID, symbol, side string, preview *ProtectionReconcilePreview, client Trader, reason []store.ProtectionStateMismatchReason) error {
	if preview == nil || preview.ProtectionGroup == nil {
		return nil
	}
	group := preview.ProtectionGroup
	if store.NormalizeProtectionGroupStatus(group.Status) == "closed" {
		return nil
	}
	if client != nil && preview.HasWorkingOrders {
		_ = client.CancelStopLossOrders(group.Symbol)
		_ = client.CancelTakeProfitOrders(group.Symbol)
	}

	_, err := r.store.ProtectionGroupBuilder().ApplyProtectionEvent(&store.ProtectionGroupEventInput{
		TraderID:                  group.TraderID,
		Symbol:                    group.Symbol,
		Side:                      group.Side,
		LinkedPositionKey:         group.LinkedPositionKey,
		ProtectionGroupID:         group.ProtectionGroupID,
		ProtectionMode:            group.ProtectionMode,
		StopLossOrderIntentID:     group.StopLossOrderIntentID,
		TakeProfitOrderIntentID:   group.TakeProfitOrderIntentID,
		StopLossExchangeOrderID:   group.StopLossExchangeOrderID,
		TakeProfitExchangeOrderID: group.TakeProfitExchangeOrderID,
		StopLossTriggerPrice:      group.StopLossTriggerPrice,
		TakeProfitTriggerPrice:    group.TakeProfitTriggerPrice,
		ProtectedQuantity:         0,
		Status:                    "closed",
		Source:                    "protection_state_reconciler",
		EventType:                 "PROTECTION_GROUP_CLOSED",
		EventSource:               "protection_state_reconciler",
		PayloadJSON:               mustJSON(map[string]interface{}{"reason": reason}),
		EventTime:                 time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	logger.Infof("protection group closed: trader=%s symbol=%s side=%s group=%s", traderID, symbol, side, group.ProtectionGroupID)
	return nil
}

func (r *ProtectionStateReconciler) recordProtectionOrderStatus(traderID string, row *store.OrderRegistry, status, eventType, eventSource string) error {
	if row == nil {
		return nil
	}
	_, err := r.store.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            row.Symbol,
		Side:              row.Side,
		PositionSideMode:  row.PositionSideMode,
		OrderRole:         row.OrderRole,
		LocalIntentID:     row.LocalIntentID,
		LinkedGroupID:     row.LinkedGroupID,
		LinkedPositionKey: row.LinkedPositionKey,
		ExchangeOrderID:   row.ExchangeOrderID,
		ClientOrderID:     row.ClientOrderID,
		OrderType:         row.OrderType,
		TimeInForce:       row.TimeInForce,
		ReduceOnly:        row.ReduceOnly,
		ClosePosition:     row.ClosePosition,
		OrigQty:           row.OrigQty,
		ExecutedQty:       row.ExecutedQty,
		AvgPrice:          row.AvgPrice,
		TriggerPrice:      row.TriggerPrice,
		ActivationPrice:   row.ActivationPrice,
		CallbackRate:      row.CallbackRate,
		Status:            status,
		Source:            eventSource,
		EventType:         eventType,
		EventSource:       eventSource,
		PayloadJSON:       mustJSON(map[string]interface{}{"status": status, "event_type": eventType}),
		EventTime:         time.Now().UTC(),
	})
	return err
}

func (r *ProtectionStateReconciler) recordProtectionGroupStatus(group *store.ProtectionGroup, status, eventType, eventSource string, row *store.OrderRegistry) error {
	if group == nil {
		return nil
	}
	if store.NormalizeProtectionGroupStatus(group.Status) == store.NormalizeProtectionGroupStatus(status) {
		return nil
	}
	_, err := r.store.ProtectionGroupBuilder().ApplyProtectionEvent(&store.ProtectionGroupEventInput{
		TraderID:                  group.TraderID,
		Symbol:                    group.Symbol,
		Side:                      group.Side,
		LinkedPositionKey:         group.LinkedPositionKey,
		ProtectionGroupID:         group.ProtectionGroupID,
		ProtectionMode:            group.ProtectionMode,
		StopLossOrderIntentID:     group.StopLossOrderIntentID,
		TakeProfitOrderIntentID:   group.TakeProfitOrderIntentID,
		StopLossExchangeOrderID:   group.StopLossExchangeOrderID,
		TakeProfitExchangeOrderID: group.TakeProfitExchangeOrderID,
		StopLossTriggerPrice:      group.StopLossTriggerPrice,
		TakeProfitTriggerPrice:    group.TakeProfitTriggerPrice,
		ProtectedQuantity:         group.ProtectedQuantity,
		Status:                    status,
		Source:                    eventSource,
		EventType:                 eventType,
		EventSource:               eventSource,
		PayloadJSON:               mustJSON(map[string]interface{}{"status": status, "row": row}),
		EventTime:                 time.Now().UTC(),
	})
	return err
}

func (r *ProtectionStateReconciler) chooseScope(traderID, selectedSymbol string) (string, string, error) {
	symbol := normalizeCapabilitySymbol(selectedSymbol)
	side := ""

	if symbol != "" {
		summary, err := r.store.ProtectionGroup().SummarizeForTraderSymbol(traderID, symbol)
		if err != nil {
			return "", "", err
		}
		for _, item := range summary {
			if item == nil {
				continue
			}
			if side == "" {
				side = item.Side
			}
			if item.HasProtection || item.HasWorkingOrders {
				side = item.Side
				break
			}
		}
		if side == "" {
			orderSummary, err := r.store.OrderRegistry().SummarizeForTraderSymbol(traderID, symbol)
			if err != nil {
				return "", "", err
			}
			if orderSummary != nil && orderSummary.Side != "" {
				side = orderSummary.Side
			}
		}
	}

	if symbol == "" {
		aggs, err := r.store.PositionAggregate().ListByTraderID(traderID)
		if err != nil {
			return "", "", err
		}
		for _, agg := range aggs {
			if agg == nil {
				continue
			}
			if agg.TotalQty > 0 {
				symbol = normalizeCapabilitySymbol(agg.Symbol)
				side = normalizeOneWaySide(agg.Side)
				break
			}
		}
	}

	if symbol == "" {
		groups, err := r.store.ProtectionGroup().ListByTraderID(traderID)
		if err != nil {
			return "", "", err
		}
		for _, group := range groups {
			if group == nil {
				continue
			}
			symbol = normalizeCapabilitySymbol(group.Symbol)
			side = normalizeOneWaySide(group.Side)
			if symbol != "" {
				break
			}
		}
	}

	if symbol == "" {
		return "", "", nil
	}
	if side == "" {
		side = "LONG"
	}
	return symbol, side, nil
}

func detectFilledProtectionLeg(rows []*store.OrderRegistry) (filledRow *store.OrderRegistry, siblingRow *store.OrderRegistry) {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if row.Status == "FILLED" || row.Status == "PARTIALLY_FILLED" || row.ExecutedQty > 0 {
			filledRow = row
			break
		}
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		if filledRow != nil && row.ID == filledRow.ID {
			continue
		}
		siblingRow = row
		break
	}
	return filledRow, siblingRow
}

// BuildRuntimeProtectionPreview is a convenience wrapper for read-only callers.
func BuildRuntimeProtectionPreview(st *store.Store, traderID, selectedSymbol string, userStreamReady bool) (*ProtectionReconcilePreview, error) {
	reconciler := NewProtectionStateReconciler(st, nil)
	return reconciler.PreviewTraderProtection(traderID, selectedSymbol, nil, userStreamReady)
}
