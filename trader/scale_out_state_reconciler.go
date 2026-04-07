package trader

import (
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

// ScaleOutStateReconciler advances the fixed-ratio scale-out truth layer from registry and fill state.
// It is the single coordination point for partial reduce plan progression and downstream protection rebalance.
type ScaleOutStateReconciler struct {
	store                  *store.Store
	protectionRebalanceMgr *ProtectionRebalanceManager
}

// NewScaleOutStateReconciler creates a new scale-out state reconciler.
func NewScaleOutStateReconciler(st *store.Store, protectionRebalanceMgr *ProtectionRebalanceManager) *ScaleOutStateReconciler {
	return &ScaleOutStateReconciler{
		store:                  st,
		protectionRebalanceMgr: protectionRebalanceMgr,
	}
}

// PreviewTraderScaleOut returns the current read-only scale-out preview without mutating state.
func (r *ScaleOutStateReconciler) PreviewTraderScaleOut(traderID, selectedSymbol string, client Trader, userStreamReady bool) (*ScaleOutReconcilePreview, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("scale out state reconciler store is not configured")
	}
	return BuildRuntimeScaleOutPreview(r.store, traderID, selectedSymbol, userStreamReady)
}

// SyncTraderScaleOutState reconciles scale-out rows against registry truth and downstream position/protection state.
func (r *ScaleOutStateReconciler) SyncTraderScaleOutState(traderID, selectedSymbol string, client Trader, userStreamReady bool) (*ScaleOutReconcilePreview, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("scale out state reconciler store is not configured")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	if client != nil {
		liveSnapshots, err := CollectLivePositionSnapshots(client)
		if err == nil {
			if _, err := store.NewPositionAggregateBuilder(r.store).BuildForTrader(traderID, liveSnapshots, nil); err != nil {
				return nil, err
			}
		}
	}

	preview, err := BuildRuntimeScaleOutPreview(r.store, traderID, selectedSymbol, userStreamReady)
	if err != nil {
		return nil, err
	}

	changed, _, err := r.reconcileScaleOutPreview(preview)
	if err != nil {
		return nil, err
	}
	if changed {
		logger.Infof("scale out plan advanced: trader=%s symbol=%s side=%s plan=%s status=%s remaining_qty=%.6f executed_qty=%.6f",
			preview.TraderID, preview.SelectedSymbol, preview.ScaleOutPlan.Side, preview.ScaleOutPlan.ScaleOutPlanID, preview.ScaleOutStatus, preview.RemainingScaleOutQty, preview.ExecutedScaleOutQty)
	}

	if client != nil {
		liveSnapshots, err := CollectLivePositionSnapshots(client)
		if err == nil {
			if _, err := store.NewPositionAggregateBuilder(r.store).BuildForTrader(traderID, liveSnapshots, nil); err != nil {
				return nil, err
			}
		}
	}

	if r.protectionRebalanceMgr != nil {
		refreshed, err := store.NewPositionAggregateBuilder(r.store).BuildForTrader(traderID, nil, nil)
		if err != nil {
			return nil, err
		}
		for _, aggregate := range refreshed {
			if aggregate == nil || aggregate.TotalQty <= 0 {
				continue
			}
			if selectedSymbol != "" && !strings.EqualFold(aggregate.Symbol, strings.ToUpper(strings.TrimSpace(selectedSymbol))) {
				continue
			}
			if _, err := r.protectionRebalanceMgr.RebalanceForAggregate(aggregate); err != nil {
				return nil, err
			}
		}
	}

	return BuildRuntimeScaleOutPreview(r.store, traderID, selectedSymbol, userStreamReady)
}

func (r *ScaleOutStateReconciler) reconcileScaleOutPreview(preview *ScaleOutReconcilePreview) (bool, string, error) {
	if preview == nil || preview.ScaleOutPlan == nil || len(preview.ScaleOutLevels) == 0 {
		return false, "", nil
	}

	changed := false
	filledSeen := false
	cancelledSeen := false
	levelInputs := make([]*store.ScaleOutPlanLevelInput, 0, len(preview.ScaleOutLevels))
	totalRemaining := 0.0
	totalExecuted := 0.0

	for _, level := range preview.ScaleOutLevels {
		if level == nil {
			continue
		}

		next := *level
		levelChanged := false

		row, err := r.findRegistryRowForLevel(preview.TraderID, level)
		if err != nil {
			return false, "", err
		}
		if row != nil {
			levelChanged, filledSeen, cancelledSeen = reconcileScaleOutLevelFromOrder(level, row, &next, filledSeen, cancelledSeen)
			if levelChanged {
				changed = true
				switch next.Status {
				case "filled":
					logger.Infof("scale out level filled: trader=%s symbol=%s side=%s plan=%s level=%d",
						next.TraderID, next.Symbol, next.Side, next.ScaleOutPlanID, next.LevelIndex)
				case "cancelled":
					logger.Infof("scale out level cancelled: trader=%s symbol=%s side=%s plan=%s level=%d",
						next.TraderID, next.Symbol, next.Side, next.ScaleOutPlanID, next.LevelIndex)
				}
			}
		}

		totalRemaining += next.RemainingQty
		totalExecuted += next.ExecutedQty
		levelInputs = append(levelInputs, &store.ScaleOutPlanLevelInput{
			TraderID:              next.TraderID,
			Symbol:                next.Symbol,
			Side:                  next.Side,
			LinkedPositionKey:     next.LinkedPositionKey,
			ScaleOutPlanID:        next.ScaleOutPlanID,
			LevelIndex:            next.LevelIndex,
			TargetType:            next.TargetType,
			TargetPrice:           next.TargetPrice,
			PlannedQty:            next.PlannedQty,
			ExecutedQty:           next.ExecutedQty,
			RemainingQty:          next.RemainingQty,
			LinkedOrderIntentID:   next.LinkedOrderIntentID,
			LinkedExchangeOrderID: next.LinkedExchangeOrderID,
			Status:                next.Status,
		})
	}

	if !changed {
		return false, "", nil
	}

	status := deriveScaleOutPlanStatus(levelInputs, totalExecuted, totalRemaining)
	eventType := scaleOutEventTypeForStatus(status, filledSeen, cancelledSeen)

	updated, err := r.store.ScaleOutPlanBuilder().ApplyScaleOutEvent(&store.ScaleOutPlanEventInput{
		TraderID:            preview.TraderID,
		Symbol:              preview.SelectedSymbol,
		Side:                preview.ScaleOutPlan.Side,
		LinkedPositionKey:   preview.ScaleOutPlan.LinkedPositionKey,
		ScaleOutPlanID:      preview.ScaleOutPlan.ScaleOutPlanID,
		PlanMode:            preview.ScaleOutPlan.PlanMode,
		Status:              status,
		TotalPlannedQty:     preview.ScaleOutPlan.TotalPlannedQty,
		RemainingPlannedQty: totalRemaining,
		ExecutedQty:         totalExecuted,
		Source:              "scale_out_state_reconciler",
		EventType:           eventType,
		EventSource:         "scale_out_state_reconciler",
		PayloadJSON:         mustJSON(map[string]interface{}{"plan_id": preview.ScaleOutPlan.ScaleOutPlanID, "status": status, "event_type": eventType, "levels": levelInputs}),
		EventTime:           time.Now().UTC(),
		Levels:              levelInputs,
	})
	if err != nil {
		return false, "", err
	}

	preview.ScaleOutPlan = updated
	preview.ScaleOutStatus = updated.Status
	preview.ScaleOutConsistency = store.NormalizeScaleOutConsistencyStatus(preview.ScaleOutConsistency)
	if preview.ScaleOutStatus == "completed" {
		preview.HasScaleOutPlan = false
	}

	return true, eventType, nil
}

func (r *ScaleOutStateReconciler) findRegistryRowForLevel(traderID string, level *store.ScaleOutPlanLevel) (*store.OrderRegistry, error) {
	if level == nil || r == nil || r.store == nil {
		return nil, nil
	}
	row, err := r.store.OrderRegistry().GetByKeys(traderID, strings.TrimSpace(level.LinkedExchangeOrderID), "", strings.TrimSpace(level.LinkedOrderIntentID))
	if err != nil {
		return nil, err
	}
	return row, nil
}

func reconcileScaleOutLevelFromOrder(level *store.ScaleOutPlanLevel, row *store.OrderRegistry, next *store.ScaleOutPlanLevel, filledSeen, cancelledSeen bool) (bool, bool, bool) {
	if level == nil || row == nil || next == nil {
		return false, filledSeen, cancelledSeen
	}

	desiredStatus := normalizeScaleOutLevelStatus(next.Status)
	desiredExecuted := next.ExecutedQty
	desiredRemaining := next.RemainingQty

	switch store.NormalizeOrderRegistryStatus(row.Status) {
	case "FILLED":
		desiredStatus = "filled"
		desiredExecuted = math.Max(row.ExecutedQty, row.OrigQty)
		if desiredExecuted <= 0 {
			desiredExecuted = next.PlannedQty
		}
		if desiredExecuted > next.PlannedQty {
			desiredExecuted = next.PlannedQty
		}
		desiredRemaining = 0
		filledSeen = true
	case "PARTIALLY_FILLED":
		desiredStatus = "partially_filled"
		desiredExecuted = row.ExecutedQty
		if desiredExecuted <= 0 {
			desiredExecuted = next.ExecutedQty
		}
		if desiredExecuted > next.PlannedQty {
			desiredExecuted = next.PlannedQty
		}
		desiredRemaining = next.PlannedQty - desiredExecuted
		if desiredRemaining < 0 {
			desiredRemaining = 0
		}
	case "CANCELED", "EXPIRED", "REJECTED":
		desiredStatus = "cancelled"
		desiredExecuted = row.ExecutedQty
		if desiredExecuted < 0 {
			desiredExecuted = 0
		}
		if desiredExecuted > next.PlannedQty {
			desiredExecuted = next.PlannedQty
		}
		desiredRemaining = next.PlannedQty - desiredExecuted
		if desiredRemaining < 0 {
			desiredRemaining = 0
		}
		cancelledSeen = true
	default:
		if row.IsWorking {
			desiredStatus = "armed"
		}
	}

	changed := desiredStatus != next.Status || math.Abs(desiredExecuted-next.ExecutedQty) > 0.000001 || math.Abs(desiredRemaining-next.RemainingQty) > 0.000001 || strings.TrimSpace(next.LinkedExchangeOrderID) != strings.TrimSpace(row.ExchangeOrderID)
	if changed {
		next.Status = desiredStatus
		next.ExecutedQty = desiredExecuted
		next.RemainingQty = desiredRemaining
		next.LinkedExchangeOrderID = row.ExchangeOrderID
		if next.LinkedOrderIntentID == "" {
			next.LinkedOrderIntentID = row.LocalIntentID
		}
	}

	return changed, filledSeen, cancelledSeen
}

func deriveScaleOutPlanStatus(levels []*store.ScaleOutPlanLevelInput, totalExecuted, totalRemaining float64) string {
	hasWorking := false
	hasPartial := false
	hasFilled := false
	hasCancelled := false
	hasInvalid := false
	for _, level := range levels {
		if level == nil {
			continue
		}
		switch normalizeScaleOutLevelStatus(level.Status) {
		case "armed":
			hasWorking = true
		case "partially_filled":
			hasPartial = true
		case "filled":
			hasFilled = true
		case "cancelled":
			hasCancelled = true
		case "invalid":
			hasInvalid = true
		}
	}

	switch {
	case hasInvalid:
		return "invalid"
	case totalRemaining <= 0 && totalExecuted > 0 && !hasWorking && !hasPartial:
		return "completed"
	case hasPartial || (totalExecuted > 0 && totalRemaining > 0):
		return "partially_filled"
	case hasWorking:
		return "armed"
	case hasFilled:
		return "completed"
	case hasCancelled:
		return "invalid"
	default:
		return "draft"
	}
}

func scaleOutEventTypeForStatus(status string, filledSeen, cancelledSeen bool) string {
	switch normalizeScaleOutPlanStatus(status) {
	case "completed":
		return "SCALE_OUT_PLAN_COMPLETED"
	case "invalid":
		return "SCALE_OUT_PLAN_INVALIDATED"
	}
	if filledSeen {
		return "SCALE_OUT_LEVEL_FILLED"
	}
	if cancelledSeen {
		return "SCALE_OUT_LEVEL_CANCELLED"
	}
	return "SCALE_OUT_PLAN_ADVANCED"
}
