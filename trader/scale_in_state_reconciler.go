package trader

import (
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

// ScaleInStateReconciler advances the same-symbol add-position truth layer from registry and fill state.
// It is the single coordination point for add-plan progression and downstream protection rebalance.
type ScaleInStateReconciler struct {
	store                  *store.Store
	protectionRebalanceMgr *ProtectionRebalanceManager
}

// NewScaleInStateReconciler creates a new scale-in state reconciler.
func NewScaleInStateReconciler(st *store.Store, protectionRebalanceMgr *ProtectionRebalanceManager) *ScaleInStateReconciler {
	return &ScaleInStateReconciler{
		store:                  st,
		protectionRebalanceMgr: protectionRebalanceMgr,
	}
}

// PreviewTraderScaleIn returns the current read-only scale-in preview without mutating state.
func (r *ScaleInStateReconciler) PreviewTraderScaleIn(traderID, selectedSymbol string, client Trader, userStreamReady bool) (*ScaleInReconcilePreview, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("scale in state reconciler store is not configured")
	}
	return BuildRuntimeScaleInPreview(r.store, traderID, selectedSymbol, userStreamReady, 0)
}

// SyncTraderScaleInState reconciles scale-in rows against registry truth and downstream position/protection state.
func (r *ScaleInStateReconciler) SyncTraderScaleInState(traderID, selectedSymbol string, client Trader, userStreamReady bool) (*ScaleInReconcilePreview, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("scale in state reconciler store is not configured")
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

	preview, err := BuildRuntimeScaleInPreview(r.store, traderID, selectedSymbol, userStreamReady, 0)
	if err != nil {
		return nil, err
	}

	changed, eventType, err := r.reconcileScaleInPreview(preview)
	if err != nil {
		return nil, err
	}
	if changed {
		symbol := preview.SelectedSymbol
		side := ""
		if preview.ScaleInPlan != nil {
			side = preview.ScaleInPlan.Side
		}
		logger.Infof("scale in plan advanced: trader=%s symbol=%s side=%s plan=%s status=%s remaining_qty=%.6f executed_qty=%.6f event_type=%s",
			preview.TraderID, symbol, side, preview.ScaleInPlan.ScaleInPlanID, preview.ScaleInStatus, preview.RemainingScaleInQty, preview.ExecutedScaleInQty, eventType)
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
			group, err := r.protectionRebalanceMgr.RebalanceForAggregate(aggregate)
			if err != nil {
				return nil, err
			}
			if group != nil {
				logger.Infof("protection quantity rebalanced after scale in: trader=%s symbol=%s side=%s protection_group_id=%s protected_quantity=%.6f total_qty=%.6f",
					group.TraderID, group.Symbol, group.Side, group.ProtectionGroupID, group.ProtectedQuantity, aggregate.TotalQty)
			}
		}
	}

	return BuildRuntimeScaleInPreview(r.store, traderID, selectedSymbol, userStreamReady, 0)
}

func (r *ScaleInStateReconciler) reconcileScaleInPreview(preview *ScaleInReconcilePreview) (bool, string, error) {
	if preview == nil || preview.ScaleInPlan == nil || len(preview.ScaleInLevels) == 0 {
		return false, "", nil
	}

	changed := false
	filledSeen := false
	cancelledSeen := false
	levelInputs := make([]*store.ScaleInPlanLevelInput, 0, len(preview.ScaleInLevels))
	totalRemaining := 0.0
	totalExecuted := 0.0

	for _, level := range preview.ScaleInLevels {
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
			levelChanged, filledSeen, cancelledSeen = reconcileScaleInLevelFromOrder(level, row, &next, filledSeen, cancelledSeen)
			if levelChanged {
				changed = true
				switch next.Status {
				case "filled":
					logger.Infof("scale in level filled: trader=%s symbol=%s side=%s plan=%s level=%d",
						next.TraderID, next.Symbol, next.Side, next.ScaleInPlanID, next.LevelIndex)
				case "cancelled":
					logger.Infof("scale in level cancelled: trader=%s symbol=%s side=%s plan=%s level=%d",
						next.TraderID, next.Symbol, next.Side, next.ScaleInPlanID, next.LevelIndex)
				}
			}
		}

		totalRemaining += next.RemainingQty
		totalExecuted += next.ExecutedQty
		levelInputs = append(levelInputs, &store.ScaleInPlanLevelInput{
			TraderID:              next.TraderID,
			Symbol:                next.Symbol,
			Side:                  next.Side,
			LinkedPositionKey:     next.LinkedPositionKey,
			ScaleInPlanID:         next.ScaleInPlanID,
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

	status := deriveScaleInPlanStatus(levelInputs, totalExecuted, totalRemaining)
	eventType := scaleInEventTypeForStatus(status, filledSeen, cancelledSeen)

	updated, err := r.store.ScaleInPlanBuilder().ApplyScaleInEvent(&store.ScaleInPlanEventInput{
		TraderID:            preview.TraderID,
		Symbol:              preview.SelectedSymbol,
		Side:                preview.ScaleInPlan.Side,
		LinkedPositionKey:   preview.ScaleInPlan.LinkedPositionKey,
		ScaleInPlanID:       preview.ScaleInPlan.ScaleInPlanID,
		PlanMode:            preview.ScaleInPlan.PlanMode,
		Status:              status,
		TotalPlannedQty:     preview.ScaleInPlan.TotalPlannedQty,
		RemainingPlannedQty: totalRemaining,
		ExecutedQty:         totalExecuted,
		MaxScaleInCount:     preview.ScaleInPlan.MaxScaleInCount,
		CurrentScaleInCount: preview.ScaleInPlan.CurrentScaleInCount,
		Source:              "scale_in_state_reconciler",
		EventType:           eventType,
		EventSource:         "scale_in_state_reconciler",
		PayloadJSON:         mustJSON(map[string]interface{}{"plan_id": preview.ScaleInPlan.ScaleInPlanID, "status": status, "event_type": eventType, "levels": levelInputs}),
		EventTime:           time.Now().UTC(),
		Levels:              levelInputs,
	})
	if err != nil {
		return false, "", err
	}

	preview.ScaleInPlan = updated
	preview.ScaleInStatus = updated.Status
	preview.ScaleInConsistency = store.NormalizeScaleInConsistencyStatus(preview.ScaleInConsistency)
	if preview.ScaleInStatus == "completed" {
		preview.HasScaleInPlan = false
	}

	return true, eventType, nil
}

func (r *ScaleInStateReconciler) findRegistryRowForLevel(traderID string, level *store.ScaleInPlanLevel) (*store.OrderRegistry, error) {
	if level == nil || r == nil || r.store == nil {
		return nil, nil
	}
	row, err := r.store.OrderRegistry().GetByKeys(traderID, strings.TrimSpace(level.LinkedExchangeOrderID), "", strings.TrimSpace(level.LinkedOrderIntentID))
	if err != nil {
		return nil, err
	}
	return row, nil
}

func reconcileScaleInLevelFromOrder(level *store.ScaleInPlanLevel, row *store.OrderRegistry, next *store.ScaleInPlanLevel, filledSeen, cancelledSeen bool) (bool, bool, bool) {
	if level == nil || row == nil || next == nil {
		return false, filledSeen, cancelledSeen
	}

	desiredStatus := next.Status
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

	levelChanged := desiredStatus != next.Status || !floatEqual(desiredExecuted, next.ExecutedQty, 0.000001) || !floatEqual(desiredRemaining, next.RemainingQty, 0.000001) || row.ExchangeOrderID != "" && row.ExchangeOrderID != next.LinkedExchangeOrderID
	next.Status = normalizeScaleInReconcileLevelStatus(desiredStatus)
	next.ExecutedQty = desiredExecuted
	next.RemainingQty = desiredRemaining
	if row.ExchangeOrderID != "" {
		next.LinkedExchangeOrderID = row.ExchangeOrderID
	}
	if next.LinkedOrderIntentID == "" {
		next.LinkedOrderIntentID = row.LocalIntentID
	}
	if next.LevelIndex <= 0 {
		next.LevelIndex = level.LevelIndex
	}
	return levelChanged, filledSeen, cancelledSeen
}

func deriveScaleInPlanStatus(levels []*store.ScaleInPlanLevelInput, totalExecuted, totalRemaining float64) string {
	hasActive := false
	hasFilled := false
	hasCancelled := false
	hasInvalid := false
	for _, level := range levels {
		if level == nil {
			continue
		}
		switch normalizeScaleInReconcileLevelStatus(level.Status) {
		case "filled":
			hasFilled = true
		case "cancelled":
			hasCancelled = true
		case "invalid":
			hasInvalid = true
		case "armed", "partially_filled", "draft":
			hasActive = true
		}
	}
	switch {
	case hasInvalid:
		return "invalid"
	case totalRemaining <= 0 && totalExecuted > 0:
		return "completed"
	case hasFilled || totalExecuted > 0:
		return "partially_filled"
	case hasActive:
		return "armed"
	case hasCancelled:
		return "invalid"
	default:
		return "draft"
	}
}

func scaleInEventTypeForStatus(status string, filledSeen, cancelledSeen bool) string {
	switch store.NormalizeScaleInPlanStatus(status) {
	case "completed":
		return "SCALE_IN_PLAN_COMPLETED"
	case "invalid":
		return "SCALE_IN_PLAN_INVALIDATED"
	case "partially_filled":
		if filledSeen {
			return "SCALE_IN_LEVEL_FILLED"
		}
		if cancelledSeen {
			return "SCALE_IN_LEVEL_CANCELLED"
		}
		return "SCALE_IN_PLAN_ARMED"
	case "armed":
		if filledSeen {
			return "SCALE_IN_LEVEL_FILLED"
		}
		if cancelledSeen {
			return "SCALE_IN_LEVEL_CANCELLED"
		}
		return "SCALE_IN_LEVEL_ARMED"
	default:
		return "SCALE_IN_PLAN_CREATED"
	}
}

func normalizeScaleInReconcileLevelStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "", "draft":
		return "draft"
	case "armed", "new", "working", "open", "pending_new", "pending_replace":
		return "armed"
	case "partially_filled", "filled", "cancelled", "invalid":
		return normalized
	default:
		return "invalid"
	}
}
