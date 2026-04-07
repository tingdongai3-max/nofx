package store

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/logger"

	"gorm.io/gorm"
)

// ScaleInPlanEventInput is the normalized input for the scale-in plan builder.
// It can originate from a scale-in manager, a user-stream reconciliation event, or a bootstrap recovery flow.
type ScaleInPlanEventInput struct {
	TraderID            string                   // System-owned trader key for this event.
	Symbol              string                   // Truth-layer symbol scope attached to the event.
	Side                string                   // Truth-layer one-way side scope attached to the event.
	LinkedPositionKey   string                   // System-local position key used by the truth layer.
	ScaleInPlanID       string                   // System-generated scale-in plan identifier.
	PlanMode            string                   // Plan policy label; current phase allows fixed_qty or fixed_ratio.
	Status              string                   // Plan truth status after this event.
	TotalPlannedQty     float64                  // System-derived total planned quantity across levels.
	RemainingPlannedQty float64                  // System-derived remaining planned quantity across levels.
	ExecutedQty         float64                  // System-derived executed quantity across levels.
	MaxScaleInCount     int                      // Strategy-layer cap for how many add attempts are allowed.
	CurrentScaleInCount int                      // System-derived executed add attempts in the current plan snapshot.
	Source              string                   // System truth source label for the plan row.
	EventType           string                   // Raw event type preserved in the evidence chain.
	EventSource         string                   // Raw event source label preserved in the evidence chain.
	PayloadJSON         string                   // Raw JSON payload preserved for audit/debugging.
	EventTime           time.Time                // Raw event timestamp or the best recovered timestamp.
	Levels              []*ScaleInPlanLevelInput // Optional level updates written together with the plan.
}

// ScaleInPlanLevelInput is the normalized input for one scale-in plan level.
// It is a plan-level write model, not a raw exchange order event.
type ScaleInPlanLevelInput struct {
	TraderID              string  // System-owned trader key for this level.
	Symbol                string  // Truth-layer symbol scope attached to the level.
	Side                  string  // Truth-layer one-way side scope attached to the level.
	LinkedPositionKey     string  // System-local position key used by the truth layer.
	ScaleInPlanID         string  // System-generated scale-in plan identifier.
	LevelIndex            int     // System-owned level index within the plan.
	TargetType            string  // Level target type; current phase allows market or limit_price.
	TargetPrice           float64 // System-derived target price for limit-price levels.
	PlannedQty            float64 // System-derived planned quantity for this level.
	ExecutedQty           float64 // System-derived executed quantity for this level.
	RemainingQty          float64 // System-derived remaining quantity for this level.
	LinkedOrderIntentID   string  // System-local order intent id used for correlation.
	LinkedExchangeOrderID string  // Exchange raw order id when available.
	Status                string  // Level truth status after this event.
}

// ScaleInPlanBuilder centralizes all scale-in truth updates.
// Managers and reconcilers call into this builder instead of recalculating plan status in multiple places.
type ScaleInPlanBuilder struct {
	store *Store
}

// NewScaleInPlanBuilder creates a new scale-in plan builder.
func NewScaleInPlanBuilder(st *Store) *ScaleInPlanBuilder {
	return &ScaleInPlanBuilder{store: st}
}

// ApplyScaleInEvent appends the raw event log row and upserts the plan/level snapshot in one transaction.
func (b *ScaleInPlanBuilder) ApplyScaleInEvent(input *ScaleInPlanEventInput) (*ScaleInPlan, error) {
	if b == nil || b.store == nil {
		return nil, fmt.Errorf("scale-in plan builder store is not configured")
	}
	if input == nil {
		return nil, fmt.Errorf("scale-in plan event input is required")
	}
	if input.TraderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}
	if input.ScaleInPlanID == "" {
		return nil, fmt.Errorf("scale-in plan id is required")
	}

	now := time.Now().UTC()
	eventTime := input.EventTime
	if eventTime.IsZero() {
		eventTime = now
	}

	planRow := b.buildPlanRow(input, eventTime)
	levelRows := b.buildLevelRows(input, eventTime)
	eventLog := &ScaleInEventLog{
		TraderID:      input.TraderID,
		Symbol:        planRow.Symbol,
		ScaleInPlanID: planRow.ScaleInPlanID,
		LevelIndex:    chooseScaleInEventLevelIndex(levelRows),
		EventType:     chooseScaleInString(input.EventType, "SCALE_IN_EVENT"),
		EventSource:   chooseScaleInString(input.EventSource, planRow.Source),
		PayloadJSON:   input.PayloadJSON,
		EventTime:     eventTime,
	}

	var updated *ScaleInPlan
	if err := b.store.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(eventLog).Error; err != nil {
			return fmt.Errorf("failed to append scale-in event log: %w", err)
		}

		planStore := NewScaleInPlanStore(tx)
		existing, err := planStore.findExisting(tx, planRow.TraderID, planRow.ScaleInPlanID, planRow.LinkedPositionKey)
		if err != nil {
			return err
		}
		if existing != nil {
			planRow.ID = existing.ID
			planRow.CreatedAt = existing.CreatedAt
		}
		planRow.UpdatedAt = eventTime
		if err := upsertScaleInPlanRow(tx, planRow, existing != nil); err != nil {
			return err
		}

		levelStore := NewScaleInPlanLevelStore(tx)
		for _, levelRow := range levelRows {
			if err := upsertScaleInPlanLevelRow(tx, levelStore, levelRow, eventTime); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	updated, err := b.store.ScaleInPlan().GetByKeys(planRow.TraderID, planRow.ScaleInPlanID, planRow.LinkedPositionKey)
	if err != nil {
		return nil, err
	}

	logger.Infof("scale in event received: trader=%s symbol=%s event_type=%s source=%s scale_in_plan_id=%s level_index=%d",
		input.TraderID, planRow.Symbol, eventLog.EventType, eventLog.EventSource, planRow.ScaleInPlanID, eventLog.LevelIndex)
	logger.Infof("scale in plan updated: trader=%s symbol=%s side=%s plan=%s status=%s remaining_qty=%.6f executed_qty=%.6f",
		planRow.TraderID, planRow.Symbol, planRow.Side, planRow.ScaleInPlanID, planRow.Status, planRow.RemainingPlannedQty, planRow.ExecutedQty)

	return updated, nil
}

// BuildFromExistingPlan rewrites an already-loaded plan through the append-only event log.
// It is useful for recovery flows that need to persist the current plan snapshot again.
func (b *ScaleInPlanBuilder) BuildFromExistingPlan(plan *ScaleInPlan, eventType, eventSource string) (*ScaleInPlan, error) {
	if plan == nil {
		return nil, fmt.Errorf("scale-in plan is nil")
	}
	payload, _ := json.Marshal(plan)
	return b.ApplyScaleInEvent(&ScaleInPlanEventInput{
		TraderID:            plan.TraderID,
		Symbol:              plan.Symbol,
		Side:                plan.Side,
		LinkedPositionKey:   plan.LinkedPositionKey,
		ScaleInPlanID:       plan.ScaleInPlanID,
		PlanMode:            plan.PlanMode,
		Status:              plan.Status,
		TotalPlannedQty:     plan.TotalPlannedQty,
		RemainingPlannedQty: plan.RemainingPlannedQty,
		ExecutedQty:         plan.ExecutedQty,
		MaxScaleInCount:     plan.MaxScaleInCount,
		CurrentScaleInCount: plan.CurrentScaleInCount,
		Source:              plan.Source,
		EventType:           eventType,
		EventSource:         eventSource,
		PayloadJSON:         string(payload),
		EventTime:           time.Now().UTC(),
	})
}

func (b *ScaleInPlanBuilder) buildPlanRow(input *ScaleInPlanEventInput, eventTime time.Time) *ScaleInPlan {
	normalizedSymbol := normalizeAggregateSymbol(input.Symbol)
	normalizedSide := normalizeAggregateSide(input.Side)
	normalizedMode := normalizeScaleInPlanMode(input.PlanMode)
	normalizedStatus := normalizeScaleInPlanStatus(input.Status)
	totalPlannedQty := math.Abs(input.TotalPlannedQty)
	remainingPlannedQty := math.Abs(input.RemainingPlannedQty)
	executedQty := math.Abs(input.ExecutedQty)
	if remainingPlannedQty <= 0 && len(input.Levels) > 0 {
		_, remainingPlannedQty, executedQty = computeScaleInTotals(input.Levels)
	}
	if totalPlannedQty <= 0 && len(input.Levels) > 0 {
		totalPlannedQty, _, _ = computeScaleInTotals(input.Levels)
	}
	if remainingPlannedQty <= 0 && executedQty < totalPlannedQty {
		remainingPlannedQty = totalPlannedQty - executedQty
	}
	if remainingPlannedQty < 0 {
		remainingPlannedQty = 0
	}

	return &ScaleInPlan{
		TraderID:            input.TraderID,
		Symbol:              normalizedSymbol,
		Side:                normalizedSide,
		LinkedPositionKey:   chooseScaleInString(input.LinkedPositionKey, orderRegistryPositionKey(normalizedSymbol, normalizedSide)),
		ScaleInPlanID:       strings.TrimSpace(input.ScaleInPlanID),
		PlanMode:            normalizedMode,
		Status:              normalizedStatus,
		TotalPlannedQty:     totalPlannedQty,
		RemainingPlannedQty: remainingPlannedQty,
		ExecutedQty:         executedQty,
		MaxScaleInCount:     input.MaxScaleInCount,
		CurrentScaleInCount: input.CurrentScaleInCount,
		Source:              chooseScaleInString(input.Source, "scale_in_manager"),
		CreatedAt:           eventTime,
		UpdatedAt:           eventTime,
	}
}

func (b *ScaleInPlanBuilder) buildLevelRows(input *ScaleInPlanEventInput, eventTime time.Time) []*ScaleInPlanLevel {
	if len(input.Levels) == 0 {
		return []*ScaleInPlanLevel{}
	}

	rows := make([]*ScaleInPlanLevel, 0, len(input.Levels))
	for _, level := range input.Levels {
		if level == nil {
			continue
		}
		normalizedSymbol := normalizeAggregateSymbol(chooseScaleInString(level.Symbol, input.Symbol))
		normalizedSide := normalizeAggregateSide(chooseScaleInString(level.Side, input.Side))
		plannedQty := math.Abs(level.PlannedQty)
		executedQty := math.Abs(level.ExecutedQty)
		remainingQty := math.Abs(level.RemainingQty)
		if remainingQty <= 0 && plannedQty > executedQty {
			remainingQty = plannedQty - executedQty
		}
		if remainingQty < 0 {
			remainingQty = 0
		}
		rows = append(rows, &ScaleInPlanLevel{
			TraderID:              chooseScaleInString(level.TraderID, input.TraderID),
			Symbol:                normalizedSymbol,
			Side:                  normalizedSide,
			LinkedPositionKey:     chooseScaleInString(level.LinkedPositionKey, input.LinkedPositionKey, orderRegistryPositionKey(normalizedSymbol, normalizedSide)),
			ScaleInPlanID:         chooseScaleInString(level.ScaleInPlanID, input.ScaleInPlanID),
			LevelIndex:            level.LevelIndex,
			TargetType:            normalizeScaleInTargetType(level.TargetType),
			TargetPrice:           level.TargetPrice,
			PlannedQty:            plannedQty,
			ExecutedQty:           executedQty,
			RemainingQty:          remainingQty,
			LinkedOrderIntentID:   chooseScaleInString(level.LinkedOrderIntentID, buildScaleInLevelIntentID(input.ScaleInPlanID, level.LevelIndex)),
			LinkedExchangeOrderID: strings.TrimSpace(level.LinkedExchangeOrderID),
			Status:                normalizeScaleInLevelStatus(level.Status),
			CreatedAt:             eventTime,
			UpdatedAt:             eventTime,
		})
	}
	return rows
}

func computeScaleInTotals(levels []*ScaleInPlanLevelInput) (totalPlanned, remainingPlanned, executed float64) {
	for _, level := range levels {
		if level == nil {
			continue
		}
		totalPlanned += math.Abs(level.PlannedQty)
		executed += math.Abs(level.ExecutedQty)
		remaining := math.Abs(level.RemainingQty)
		if remaining <= 0 && math.Abs(level.PlannedQty) > math.Abs(level.ExecutedQty) {
			remaining = math.Abs(level.PlannedQty) - math.Abs(level.ExecutedQty)
		}
		if remaining < 0 {
			remaining = 0
		}
		remainingPlanned += remaining
	}
	return
}

func chooseScaleInEventLevelIndex(levels []*ScaleInPlanLevel) int {
	for _, level := range levels {
		if level != nil {
			return level.LevelIndex
		}
	}
	return 0
}

func upsertScaleInPlanRow(tx *gorm.DB, planRow *ScaleInPlan, existing bool) error {
	if planRow == nil {
		return fmt.Errorf("scale-in plan row is nil")
	}
	if existing {
		return tx.Save(planRow).Error
	}
	return tx.Create(planRow).Error
}

func upsertScaleInPlanLevelRow(tx *gorm.DB, levelStore *ScaleInPlanLevelStore, levelRow *ScaleInPlanLevel, eventTime time.Time) error {
	if levelStore == nil {
		return fmt.Errorf("scale-in plan level store is nil")
	}
	if levelRow == nil {
		return fmt.Errorf("scale-in plan level row is nil")
	}
	existing, err := levelStore.findExisting(tx, levelRow.TraderID, levelRow.ScaleInPlanID, levelRow.LevelIndex, levelRow.LinkedOrderIntentID, levelRow.LinkedExchangeOrderID)
	if err != nil {
		return err
	}
	if existing != nil {
		levelRow.ID = existing.ID
		levelRow.CreatedAt = existing.CreatedAt
	}
	levelRow.UpdatedAt = eventTime
	if existing != nil {
		return tx.Save(levelRow).Error
	}
	return tx.Create(levelRow).Error
}

func buildScaleInLevelIntentID(planID string, levelIndex int) string {
	trimmedPlanID := strings.TrimSpace(planID)
	if trimmedPlanID == "" {
		trimmedPlanID = "si"
	}
	return fmt.Sprintf("%s-l%d", trimmedPlanID, levelIndex)
}

func chooseScaleInString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
