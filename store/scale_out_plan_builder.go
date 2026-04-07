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

// ScaleOutPlanEventInput is the normalized input for the scale-out plan builder.
// It can originate from a scale-out manager, a user-stream reconciliation event, or a bootstrap recovery flow.
type ScaleOutPlanEventInput struct {
	TraderID            string                   // System-owned trader key for this event.
	Symbol              string                   // Truth-layer symbol scope attached to the event.
	Side                string                   // Truth-layer one-way side scope attached to the event.
	LinkedPositionKey   string                   // System-local position key used by the truth layer.
	ScaleOutPlanID      string                   // System-generated scale-out plan identifier.
	PlanMode            string                   // Plan policy label; current phase only allows fixed_ratio.
	Status              string                   // Plan truth status after this event.
	TotalPlannedQty     float64                  // System-derived total planned quantity across levels.
	RemainingPlannedQty float64                  // System-derived remaining planned quantity across levels.
	ExecutedQty         float64                  // System-derived executed quantity across levels.
	Source              string                   // System truth source label for the plan row.
	EventType           string                   // Raw event type preserved in the evidence chain.
	EventSource         string                   // Raw event source label preserved in the evidence chain.
	PayloadJSON         string                   // Raw JSON payload preserved for audit/debugging.
	EventTime           time.Time                // Raw event timestamp or the best recovered timestamp.
	Levels              []*ScaleOutPlanLevelInput // Optional level updates written together with the plan.
}

// ScaleOutPlanLevelInput is the normalized input for one scale-out plan level.
// It is a plan-level write model, not a raw exchange order event.
type ScaleOutPlanLevelInput struct {
	TraderID             string  // System-owned trader key for this level.
	Symbol               string  // Truth-layer symbol scope attached to the level.
	Side                 string  // Truth-layer one-way side scope attached to the level.
	LinkedPositionKey    string  // System-local position key used by the truth layer.
	ScaleOutPlanID       string  // System-generated scale-out plan identifier.
	LevelIndex           int     // System-owned level index within the plan.
	TargetType           string  // Level target type; current phase allows limit_price or reduce_market.
	TargetPrice          float64 // System-derived target price for limit-price levels.
	PlannedQty           float64 // System-derived planned quantity for this level.
	ExecutedQty          float64 // System-derived executed quantity for this level.
	RemainingQty         float64 // System-derived remaining quantity for this level.
	LinkedOrderIntentID   string  // System-local order intent id used for correlation.
	LinkedExchangeOrderID string  // Exchange raw order id when available.
	Status               string  // Level truth status after this event.
}

// ScaleOutPlanBuilder centralizes all scale-out truth updates.
// Managers and reconcilers call into this builder instead of recalculating plan status in multiple places.
type ScaleOutPlanBuilder struct {
	store *Store
}

// NewScaleOutPlanBuilder creates a new scale-out plan builder.
func NewScaleOutPlanBuilder(st *Store) *ScaleOutPlanBuilder {
	return &ScaleOutPlanBuilder{store: st}
}

// ApplyScaleOutEvent appends the raw event log row and upserts the plan/level snapshot in one transaction.
func (b *ScaleOutPlanBuilder) ApplyScaleOutEvent(input *ScaleOutPlanEventInput) (*ScaleOutPlan, error) {
	if b == nil || b.store == nil {
		return nil, fmt.Errorf("scale-out plan builder store is not configured")
	}
	if input == nil {
		return nil, fmt.Errorf("scale-out plan event input is required")
	}
	if input.TraderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}
	if input.ScaleOutPlanID == "" {
		return nil, fmt.Errorf("scale-out plan id is required")
	}

	now := time.Now().UTC()
	eventTime := input.EventTime
	if eventTime.IsZero() {
		eventTime = now
	}

	planRow := b.buildPlanRow(input, eventTime)
	levelRows := b.buildLevelRows(input, eventTime)
	eventLog := &ScaleOutEventLog{
		TraderID:       input.TraderID,
		Symbol:         planRow.Symbol,
		ScaleOutPlanID: planRow.ScaleOutPlanID,
		LevelIndex:     chooseScaleOutEventLevelIndex(levelRows),
		EventType:      chooseString(input.EventType, "SCALE_OUT_EVENT"),
		EventSource:    chooseString(input.EventSource, planRow.Source),
		PayloadJSON:    input.PayloadJSON,
		EventTime:      eventTime,
	}

	var updated *ScaleOutPlan
	if err := b.store.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(eventLog).Error; err != nil {
			return fmt.Errorf("failed to append scale-out event log: %w", err)
		}

		planStore := NewScaleOutPlanStore(tx)
		existing, err := planStore.findExisting(tx, planRow.TraderID, planRow.ScaleOutPlanID, planRow.LinkedPositionKey)
		if err != nil {
			return err
		}
		if existing != nil {
			planRow.ID = existing.ID
			planRow.CreatedAt = existing.CreatedAt
		}
		planRow.UpdatedAt = eventTime
		if err := upsertScaleOutPlanRow(tx, planRow, existing != nil); err != nil {
			return err
		}

		levelStore := NewScaleOutPlanLevelStore(tx)
		for _, levelRow := range levelRows {
			if err := upsertScaleOutPlanLevelRow(tx, levelStore, levelRow, eventTime); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	updated, err := b.store.ScaleOutPlan().GetByKeys(planRow.TraderID, planRow.ScaleOutPlanID, planRow.LinkedPositionKey)
	if err != nil {
		return nil, err
	}

	logger.Infof("scale out event received: trader=%s symbol=%s event_type=%s source=%s scale_out_plan_id=%s level_index=%d",
		input.TraderID, planRow.Symbol, eventLog.EventType, eventLog.EventSource, planRow.ScaleOutPlanID, eventLog.LevelIndex)
	logger.Infof("scale out plan updated: trader=%s symbol=%s side=%s plan=%s status=%s remaining_qty=%.6f executed_qty=%.6f",
		planRow.TraderID, planRow.Symbol, planRow.Side, planRow.ScaleOutPlanID, planRow.Status, planRow.RemainingPlannedQty, planRow.ExecutedQty)

	return updated, nil
}

// BuildFromExistingPlan rewrites an already-loaded plan through the append-only event log.
// It is useful for recovery flows that need to persist the current plan snapshot again.
func (b *ScaleOutPlanBuilder) BuildFromExistingPlan(plan *ScaleOutPlan, eventType, eventSource string) (*ScaleOutPlan, error) {
	if plan == nil {
		return nil, fmt.Errorf("scale-out plan is nil")
	}
	payload, _ := json.Marshal(plan)
	return b.ApplyScaleOutEvent(&ScaleOutPlanEventInput{
		TraderID:            plan.TraderID,
		Symbol:              plan.Symbol,
		Side:                plan.Side,
		LinkedPositionKey:   plan.LinkedPositionKey,
		ScaleOutPlanID:      plan.ScaleOutPlanID,
		PlanMode:            plan.PlanMode,
		Status:              plan.Status,
		TotalPlannedQty:     plan.TotalPlannedQty,
		RemainingPlannedQty: plan.RemainingPlannedQty,
		ExecutedQty:         plan.ExecutedQty,
		Source:              plan.Source,
		EventType:           eventType,
		EventSource:         eventSource,
		PayloadJSON:         string(payload),
		EventTime:           time.Now().UTC(),
	})
}

func (b *ScaleOutPlanBuilder) buildPlanRow(input *ScaleOutPlanEventInput, eventTime time.Time) *ScaleOutPlan {
	normalizedSymbol := normalizeAggregateSymbol(input.Symbol)
	normalizedSide := normalizeAggregateSide(input.Side)
	normalizedMode := normalizeScaleOutPlanMode(input.PlanMode)
	normalizedStatus := normalizeScaleOutPlanStatus(input.Status)
	totalPlannedQty := math.Abs(input.TotalPlannedQty)
	remainingPlannedQty := math.Abs(input.RemainingPlannedQty)
	executedQty := math.Abs(input.ExecutedQty)
	if remainingPlannedQty <= 0 && len(input.Levels) > 0 {
		_, remainingPlannedQty, executedQty = computeScaleOutTotals(input.Levels)
	}
	if totalPlannedQty <= 0 && len(input.Levels) > 0 {
		totalPlannedQty, _, _ = computeScaleOutTotals(input.Levels)
	}
	if remainingPlannedQty <= 0 && executedQty < totalPlannedQty {
		remainingPlannedQty = totalPlannedQty - executedQty
	}
	if remainingPlannedQty < 0 {
		remainingPlannedQty = 0
	}

	return &ScaleOutPlan{
		TraderID:            input.TraderID,
		Symbol:              normalizedSymbol,
		Side:                normalizedSide,
		LinkedPositionKey:   chooseScaleOutString(input.LinkedPositionKey, orderRegistryPositionKey(normalizedSymbol, normalizedSide)),
		ScaleOutPlanID:      strings.TrimSpace(input.ScaleOutPlanID),
		PlanMode:            normalizedMode,
		Status:              normalizedStatus,
		TotalPlannedQty:     totalPlannedQty,
		RemainingPlannedQty: remainingPlannedQty,
		ExecutedQty:         executedQty,
		Source:              chooseString(input.Source, "scale_out_manager"),
		CreatedAt:           eventTime,
		UpdatedAt:           eventTime,
	}
}

func (b *ScaleOutPlanBuilder) buildLevelRows(input *ScaleOutPlanEventInput, eventTime time.Time) []*ScaleOutPlanLevel {
	if len(input.Levels) == 0 {
		return []*ScaleOutPlanLevel{}
	}

	rows := make([]*ScaleOutPlanLevel, 0, len(input.Levels))
	for _, level := range input.Levels {
		if level == nil {
			continue
		}
		normalizedSymbol := normalizeAggregateSymbol(chooseString(level.Symbol, input.Symbol))
		normalizedSide := normalizeAggregateSide(chooseString(level.Side, input.Side))
		plannedQty := math.Abs(level.PlannedQty)
		executedQty := math.Abs(level.ExecutedQty)
		remainingQty := math.Abs(level.RemainingQty)
		if remainingQty <= 0 && plannedQty > executedQty {
			remainingQty = plannedQty - executedQty
		}
		if remainingQty < 0 {
			remainingQty = 0
		}
		rows = append(rows, &ScaleOutPlanLevel{
		TraderID:             chooseScaleOutString(level.TraderID, input.TraderID),
		Symbol:               normalizedSymbol,
		Side:                 normalizedSide,
		LinkedPositionKey:    chooseScaleOutString(level.LinkedPositionKey, input.LinkedPositionKey, orderRegistryPositionKey(normalizedSymbol, normalizedSide)),
		ScaleOutPlanID:       chooseScaleOutString(level.ScaleOutPlanID, input.ScaleOutPlanID),
			LevelIndex:           level.LevelIndex,
			TargetType:           normalizeScaleOutTargetType(level.TargetType),
			TargetPrice:          level.TargetPrice,
			PlannedQty:           plannedQty,
			ExecutedQty:          executedQty,
			RemainingQty:         remainingQty,
		LinkedOrderIntentID:  chooseScaleOutString(level.LinkedOrderIntentID, buildScaleOutLevelIntentID(input.ScaleOutPlanID, level.LevelIndex)),
			LinkedExchangeOrderID: strings.TrimSpace(level.LinkedExchangeOrderID),
			Status:               normalizeScaleOutLevelStatus(level.Status),
			CreatedAt:            eventTime,
			UpdatedAt:            eventTime,
		})
	}
	return rows
}

func computeScaleOutTotals(levels []*ScaleOutPlanLevelInput) (totalPlanned, remainingPlanned, executed float64) {
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

func chooseScaleOutEventLevelIndex(levels []*ScaleOutPlanLevel) int {
	for _, level := range levels {
		if level != nil {
			return level.LevelIndex
		}
	}
	return 0
}

func upsertScaleOutPlanRow(tx *gorm.DB, planRow *ScaleOutPlan, existing bool) error {
	if planRow == nil {
		return fmt.Errorf("scale-out plan row is nil")
	}
	if existing {
		return tx.Save(planRow).Error
	}
	return tx.Create(planRow).Error
}

func upsertScaleOutPlanLevelRow(tx *gorm.DB, levelStore *ScaleOutPlanLevelStore, levelRow *ScaleOutPlanLevel, eventTime time.Time) error {
	if levelStore == nil {
		return fmt.Errorf("scale-out plan level store is nil")
	}
	if levelRow == nil {
		return fmt.Errorf("scale-out plan level row is nil")
	}
	existing, err := levelStore.findExisting(tx, levelRow.TraderID, levelRow.ScaleOutPlanID, levelRow.LevelIndex, levelRow.LinkedOrderIntentID, levelRow.LinkedExchangeOrderID)
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

func buildScaleOutLevelIntentID(planID string, levelIndex int) string {
	trimmedPlanID := strings.TrimSpace(planID)
	if trimmedPlanID == "" {
		trimmedPlanID = "so"
	}
	return fmt.Sprintf("%s-l%d", trimmedPlanID, levelIndex)
}

func chooseScaleOutString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
