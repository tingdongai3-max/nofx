package trader

import (
	"fmt"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

// ProtectionAdjustmentManager coordinates dynamic protection transitions for Binance USDⓈ-M Futures one-way mode.
// It is an execution-side coordinator, not a truth-layer model.
type ProtectionAdjustmentManager struct {
	store      *store.Store
	trader     Trader
	reconciler *ProtectionCancelReplaceReconciler
}

// NewProtectionAdjustmentManager creates a new dynamic protection adjustment manager.
func NewProtectionAdjustmentManager(st *store.Store, client Trader) *ProtectionAdjustmentManager {
	return &ProtectionAdjustmentManager{
		store:      st,
		trader:     client,
		reconciler: NewProtectionCancelReplaceReconciler(st, client),
	}
}

// ApplyProtectionAdjustmentPreview applies the current read-only protection-adjustment preview if the guard allows it.
// It is idempotent because the reconciler and truth builder are idempotent.
func (m *ProtectionAdjustmentManager) ApplyProtectionAdjustmentPreview(preview *ProtectionAdjustmentPreview) (*store.ProtectionGroup, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("protection adjustment manager store is not configured")
	}
	if preview == nil {
		return nil, fmt.Errorf("protection adjustment preview is required")
	}
	if preview.GuardAssessment == nil {
		return preview.ProtectionGroup, nil
	}
	if !preview.GuardAssessment.Allowed {
		return preview.ProtectionGroup, nil
	}

	plan, shouldApply, err := m.buildProtectionAdjustmentPlan(preview)
	if err != nil {
		return nil, err
	}
	if !shouldApply || plan == nil {
		return preview.ProtectionGroup, nil
	}

	logger.Infof("protection adjustment plan built: trader=%s symbol=%s side=%s group=%s action=%s current_stop_loss_price=%.6f new_stop_loss_price=%.6f protection_adjustment_status=%s protection_adjustment_consistency=%s",
		plan.TraderID, plan.Symbol, plan.Side, plan.ProtectionGroupID, plan.Action, plan.CurrentStopLossPrice, plan.NewStopLossPrice, preview.ProtectionAdjustmentStatus, preview.ProtectionAdjustmentConsistency)

	group, err := m.reconciler.Reconcile(plan)
	if err != nil {
		return nil, err
	}

	if err := m.refreshPositionAggregates(preview.TraderID); err != nil {
		return group, err
	}

	return group, nil
}

func (m *ProtectionAdjustmentManager) buildProtectionAdjustmentPlan(preview *ProtectionAdjustmentPreview) (*ProtectionAdjustmentPlan, bool, error) {
	if preview == nil {
		return nil, false, fmt.Errorf("protection adjustment preview is required")
	}
	summary := preview.ProtectionSummary
	if summary == nil || !summary.HasProtection {
		return nil, false, nil
	}
	if preview.PositionAggregate == nil {
		return nil, false, nil
	}

	action := selectProtectionAdjustmentAction(preview)
	if action == "" {
		return nil, false, nil
	}

	currentStop := preview.CurrentStopLossPrice
	if currentStop <= 0 {
		currentStop = summary.CurrentStopLossPrice
	}
	if currentStop <= 0 {
		currentStop = summary.InitialStopLossPrice
	}

	newStop := currentStop
	switch action {
	case string(ProtectionActionSetBreakEvenStop):
		newStop = chooseProtectionBreakEvenStop(preview.PositionAggregate, currentStop, preview.CurrentMarketPrice)
	case string(ProtectionActionSetTrailingProtection), string(ProtectionActionMoveStopLoss):
		newStop = protectionMovePriceFromRule(currentStop, preview.TrailingRule, preview.PositionAggregate, preview.CurrentMarketPrice)
	}
	if newStop <= 0 {
		return nil, false, fmt.Errorf("unable to derive next stop-loss price for protection adjustment")
	}
	if almostEqual(newStop, currentStop) && action == string(ProtectionActionMoveStopLoss) {
		return nil, false, nil
	}

	protectedQty := summary.ProtectedQuantity
	if protectedQty <= 0 && preview.PositionAggregate != nil {
		protectedQty = preview.PositionAggregate.TotalQty
	}

	reason := selectProtectionAdjustmentReason(preview, action)
	eventTime := time.Now().UTC()
	return &ProtectionAdjustmentPlan{
		TraderID:               preview.TraderID,
		Symbol:                 summary.Symbol,
		Side:                   summary.Side,
		LinkedPositionKey:      summary.LinkedPositionKey,
		ProtectionGroupID:      summary.ProtectionGroupID,
		Action:                 action,
		Reason:                 reason,
		CurrentMarketPrice:     preview.CurrentMarketPrice,
		CurrentPnLPct:          preview.CurrentPnLPct,
		CurrentStopLossPrice:   currentStop,
		InitialStopLossPrice:   preview.InitialStopLossPrice,
		NewStopLossPrice:       newStop,
		CurrentTakeProfitPrice: preview.CurrentTakeProfitPrice,
		InitialTakeProfitPrice: preview.InitialTakeProfitPrice,
		ProtectionMode:         normalizedDynamicProtectionMode(summary.ProtectionMode),
		ProtectedQuantity:      protectedQty,
		TrailingRuleID:         preview.TrailingRuleID,
		TrailingAnchorPrice:    preview.TrailingAnchorPrice,
		TrailingMoveCount:      preview.TrailingMoveCount,
		ProtectionRevision:     preview.ProtectionRevision,
		LastProtectionAction:   preview.LastProtectionAction,
		LastProtectionActionAt: preview.LastProtectionActionAt,
		EventTime:              eventTime,
	}, true, nil
}

func (m *ProtectionAdjustmentManager) refreshPositionAggregates(traderID string) error {
	if m == nil || m.store == nil || traderID == "" {
		return nil
	}

	liveSnapshots := []store.PositionSnapshot(nil)
	if m.trader != nil {
		snapshots, err := CollectLivePositionSnapshots(m.trader)
		if err == nil {
			liveSnapshots = snapshots
		}
	}

	_, err := store.NewPositionAggregateBuilder(m.store).BuildForTrader(traderID, liveSnapshots, nil)
	return err
}

func selectProtectionAdjustmentAction(preview *ProtectionAdjustmentPreview) string {
	if preview == nil || preview.GuardAssessment == nil {
		return ""
	}
	if preview.GuardAssessment.CanArmBreakEven && !preview.BreakEvenArmed {
		return string(ProtectionActionSetBreakEvenStop)
	}
	if preview.GuardAssessment.CanArmTrailing && !preview.TrailingArmed {
		return string(ProtectionActionSetTrailingProtection)
	}
	if preview.GuardAssessment.CanMoveStopLoss {
		return string(ProtectionActionMoveStopLoss)
	}
	return ""
}

func selectProtectionAdjustmentReason(preview *ProtectionAdjustmentPreview, action string) string {
	switch strings.TrimSpace(action) {
	case string(ProtectionActionSetBreakEvenStop):
		return "break-even activation met"
	case string(ProtectionActionSetTrailingProtection):
		return "trailing protection activation met"
	case string(ProtectionActionMoveStopLoss):
		return "step trailing trigger met"
	default:
		if preview != nil && preview.GuardAssessment != nil {
			return preview.GuardAssessment.ProtectionAdjustmentStatus
		}
		return "protection adjustment"
	}
}

func chooseProtectionBreakEvenStop(aggregate *store.PositionAggregate, currentStop, marketPrice float64) float64 {
	if aggregate != nil && aggregate.AvgEntryPrice > 0 {
		return aggregate.AvgEntryPrice
	}
	if currentStop > 0 {
		return currentStop
	}
	return marketPrice
}
