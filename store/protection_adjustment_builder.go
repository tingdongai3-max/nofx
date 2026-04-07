package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nofx/logger"

	"gorm.io/gorm"
)

// ProtectionAdjustmentEventInput is the normalized input for the dynamic protection builder.
// It can originate from a protection adjustment manager, a cancel/replace reconciler, or a bootstrap recovery flow.
type ProtectionAdjustmentEventInput struct {
	TraderID                string    // System-owned trader key for this event.
	Symbol                  string    // Truth-layer symbol scope attached to the event.
	Side                    string    // Truth-layer one-way side scope attached to the event.
	LinkedPositionKey       string    // System-owned position correlation key used by the truth layer.
	ProtectionGroupID       string    // System-generated protection group identifier.
	ProtectionMode          string    // Protection policy label: fixed, break_even, or trailing_segmented.
	StopLossOrderIntentID   string    // System-local stop-loss intent identifier.
	TakeProfitOrderIntentID string    // System-local take-profit intent identifier.
	StopLossExchangeOrderID string    // Exchange raw stop-loss order id when available.
	TakeProfitExchangeOrderID string  // Exchange raw take-profit order id when available.
	StopLossInitialTriggerPrice float64 // Initial stop-loss trigger price before any movement.
	StopLossCurrentTriggerPrice float64 // Current stop-loss trigger price after this event.
	TakeProfitInitialTriggerPrice float64 // Initial take-profit trigger price before any movement.
	TakeProfitCurrentTriggerPrice float64 // Current take-profit trigger price after this event.
	BreakEvenArmed          bool      // System-derived break-even flag after this event.
	TrailingArmed           bool      // System-derived trailing flag after this event.
	TrailingRuleID          string    // System-owned trailing rule identifier.
	TrailingAnchorPrice     float64   // System-derived trailing anchor price after this event.
	TrailingLastMoveAt      time.Time // System-derived trailing move timestamp.
	TrailingMoveCount       int       // System-derived trailing move count after this event.
	ProtectionRevision      int       // System-owned protection revision after this event.
	AdvanceRevision         bool      // When true, the builder advances the stored protection revision by one for this event.
	LastProtectionAction    string    // Last protection mutation action label.
	LastProtectionActionAt  time.Time // Last protection mutation timestamp.
	ProtectedQuantity       float64   // System-derived active protected quantity.
	Status                  string    // Protection truth status after this event.
	Source                  string    // System truth source label for the protection row.
	OldStopLossPrice        float64   // Previous stop-loss trigger price before the change.
	NewStopLossPrice        float64   // New stop-loss trigger price after the change.
	OldTakeProfitPrice      float64   // Previous take-profit trigger price before the change.
	NewTakeProfitPrice      float64   // New take-profit trigger price after the change.
	Reason                  string    // Human-readable reason for the protection adjustment.
	EventType               string    // Raw event type preserved in the evidence chain.
	EventSource             string    // Raw event source label preserved in the evidence chain.
	PayloadJSON             string    // Raw JSON payload preserved for audit/debugging.
	EventTime               time.Time // Raw event timestamp or the best recovered timestamp.
}

// ProtectionAdjustmentBuilder centralizes dynamic protection truth updates.
// Managers and reconcilers call into this builder instead of recalculating revision/price state in multiple places.
type ProtectionAdjustmentBuilder struct {
	store *Store
}

// NewProtectionAdjustmentBuilder creates a new dynamic protection builder.
func NewProtectionAdjustmentBuilder(st *Store) *ProtectionAdjustmentBuilder {
	return &ProtectionAdjustmentBuilder{store: st}
}

// ApplyProtectionAdjustmentEvent appends the raw event log row and upserts the protection snapshot in one transaction.
func (b *ProtectionAdjustmentBuilder) ApplyProtectionAdjustmentEvent(input *ProtectionAdjustmentEventInput) (*ProtectionGroup, error) {
	if b == nil || b.store == nil {
		return nil, fmt.Errorf("protection adjustment builder store is not configured")
	}
	if input == nil {
		return nil, fmt.Errorf("protection adjustment event input is required")
	}
	if input.TraderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}
	if strings.TrimSpace(input.ProtectionGroupID) == "" {
		return nil, fmt.Errorf("protection group id is required")
	}

	now := time.Now().UTC()
	eventTime := input.EventTime
	if eventTime.IsZero() {
		eventTime = now
	}

	row := b.buildProtectionRow(input, eventTime)
	eventLog := &ProtectionAdjustmentEventLog{
		TraderID:           input.TraderID,
		Symbol:             row.Symbol,
		ProtectionGroupID:  row.ProtectionGroupID,
		ProtectionRevision: row.ProtectionRevision,
		EventType:          chooseString(input.EventType, "PROTECTION_ADJUSTMENT_EVENT"),
		OldStopLossPrice:   mathMaxFloat(input.OldStopLossPrice, 0),
		NewStopLossPrice:   mathMaxFloat(input.NewStopLossPrice, row.StopLossCurrentTriggerPrice),
		OldTakeProfitPrice: mathMaxFloat(input.OldTakeProfitPrice, 0),
		NewTakeProfitPrice: mathMaxFloat(input.NewTakeProfitPrice, row.TakeProfitCurrentTriggerPrice),
		Reason:             input.Reason,
		PayloadJSON:        input.PayloadJSON,
		EventSource:        chooseString(input.EventSource, row.Source),
		EventTime:          eventTime,
	}

	var updated *ProtectionGroup
	if err := b.store.Transaction(func(tx *gorm.DB) error {
		existing, err := NewProtectionGroupStore(tx).findExisting(tx, row.TraderID, row.ProtectionGroupID, row.LinkedPositionKey)
		if err != nil {
			return err
		}
		if existing != nil && b.noopProtectionChange(existing, row) {
			updated = existing
			return nil
		}
		if existing != nil {
			row.ID = existing.ID
			row.CreatedAt = existing.CreatedAt
			if input.AdvanceRevision {
				if row.ProtectionRevision <= existing.ProtectionRevision {
					row.ProtectionRevision = existing.ProtectionRevision + 1
				}
			} else if row.ProtectionRevision < existing.ProtectionRevision {
				row.ProtectionRevision = existing.ProtectionRevision
			}
			if row.LastProtectionActionAt.IsZero() {
				row.LastProtectionActionAt = eventTime
			}
			if row.TrailingMoveCount < existing.TrailingMoveCount {
				row.TrailingMoveCount = existing.TrailingMoveCount
			}
		}

		eventLog.ProtectionRevision = row.ProtectionRevision
		if err := tx.Create(eventLog).Error; err != nil {
			return fmt.Errorf("failed to append protection adjustment event log: %w", err)
		}
		row.UpdatedAt = eventTime
		row.LastSyncedAt = eventTime
		if row.Status == "closed" {
			row.ProtectedQuantity = 0
		}
		if existing != nil {
			if err := tx.Save(row).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if updated == nil {
		var err error
		updated, err = b.store.ProtectionGroup().GetByKeys(row.TraderID, row.ProtectionGroupID, row.LinkedPositionKey)
		if err != nil {
			return nil, err
		}
	}

	logger.Infof("protection adjustment event received: trader=%s symbol=%s event_type=%s source=%s protection_group_id=%s protection_revision=%d",
		input.TraderID, row.Symbol, eventLog.EventType, eventLog.EventSource, row.ProtectionGroupID, row.ProtectionRevision)
	logger.Infof("protection adjustment updated: trader=%s symbol=%s side=%s group=%s revision=%d mode=%s status=%s stop_loss_current=%.6f take_profit_current=%.6f",
		row.TraderID, row.Symbol, row.Side, row.ProtectionGroupID, row.ProtectionRevision, row.ProtectionMode, row.Status, row.StopLossCurrentTriggerPrice, row.TakeProfitCurrentTriggerPrice)

	return updated, nil
}

// BuildFromExistingGroup rewrites an already-loaded protection row through the append-only event log.
// It is useful for recovery flows that need to persist the current dynamic protection snapshot again.
func (b *ProtectionAdjustmentBuilder) BuildFromExistingGroup(group *ProtectionGroup, eventType, eventSource string) (*ProtectionGroup, error) {
	if group == nil {
		return nil, fmt.Errorf("protection group is nil")
	}
	payload, _ := json.Marshal(group)
	return b.ApplyProtectionAdjustmentEvent(&ProtectionAdjustmentEventInput{
		TraderID:                   group.TraderID,
		Symbol:                     group.Symbol,
		Side:                       group.Side,
		LinkedPositionKey:          group.LinkedPositionKey,
		ProtectionGroupID:          group.ProtectionGroupID,
		ProtectionMode:             group.ProtectionMode,
		StopLossOrderIntentID:      group.StopLossOrderIntentID,
		TakeProfitOrderIntentID:    group.TakeProfitOrderIntentID,
		StopLossExchangeOrderID:    group.StopLossExchangeOrderID,
		TakeProfitExchangeOrderID:  group.TakeProfitExchangeOrderID,
		StopLossInitialTriggerPrice: group.StopLossInitialTriggerPrice,
		StopLossCurrentTriggerPrice: group.StopLossCurrentTriggerPrice,
		TakeProfitInitialTriggerPrice: group.TakeProfitInitialTriggerPrice,
		TakeProfitCurrentTriggerPrice: group.TakeProfitCurrentTriggerPrice,
		BreakEvenArmed:             group.BreakEvenArmed,
		TrailingArmed:              group.TrailingArmed,
		TrailingRuleID:             group.TrailingRuleID,
		TrailingAnchorPrice:        group.TrailingAnchorPrice,
		TrailingLastMoveAt:         group.TrailingLastMoveAt,
		TrailingMoveCount:          group.TrailingMoveCount,
		ProtectionRevision:         group.ProtectionRevision,
		LastProtectionAction:       group.LastProtectionAction,
		LastProtectionActionAt:     group.LastProtectionActionAt,
		ProtectedQuantity:          group.ProtectedQuantity,
		Status:                     group.Status,
		Source:                     group.Source,
		OldStopLossPrice:           group.StopLossInitialTriggerPrice,
		NewStopLossPrice:           group.StopLossCurrentTriggerPrice,
		OldTakeProfitPrice:         group.TakeProfitInitialTriggerPrice,
		NewTakeProfitPrice:         group.TakeProfitCurrentTriggerPrice,
		Reason:                     "recovery",
		EventType:                  eventType,
		EventSource:                eventSource,
		PayloadJSON:                string(payload),
		EventTime:                  time.Now().UTC(),
	})
}

func (b *ProtectionAdjustmentBuilder) buildProtectionRow(input *ProtectionAdjustmentEventInput, eventTime time.Time) *ProtectionGroup {
	normalizedSymbol := normalizeAggregateSymbol(input.Symbol)
	normalizedSide := normalizeAggregateSide(input.Side)
	normalizedMode := normalizeProtectionMode(input.ProtectionMode)
	normalizedStatus := normalizeProtectionGroupStatus(input.Status)
	protectedQty := input.ProtectedQuantity
	if protectedQty < 0 {
		protectedQty = -protectedQty
	}
	if normalizedStatus == "closed" {
		protectedQty = 0
	}

	initialStopLoss := input.StopLossInitialTriggerPrice
	if initialStopLoss <= 0 {
		initialStopLoss = input.OldStopLossPrice
	}
	currentStopLoss := input.StopLossCurrentTriggerPrice
	if currentStopLoss <= 0 {
		currentStopLoss = input.NewStopLossPrice
	}
	if currentStopLoss <= 0 {
		currentStopLoss = initialStopLoss
	}
	initialTakeProfit := input.TakeProfitInitialTriggerPrice
	if initialTakeProfit <= 0 {
		initialTakeProfit = input.OldTakeProfitPrice
	}
	currentTakeProfit := input.TakeProfitCurrentTriggerPrice
	if currentTakeProfit <= 0 {
		currentTakeProfit = input.NewTakeProfitPrice
	}
	if currentTakeProfit <= 0 {
		currentTakeProfit = initialTakeProfit
	}

	revision := input.ProtectionRevision
	if revision < 0 {
		revision = 0
	}
	lastActionAt := input.LastProtectionActionAt
	if lastActionAt.IsZero() {
		lastActionAt = eventTime
	}
	trailingLastMoveAt := input.TrailingLastMoveAt
	if trailingLastMoveAt.IsZero() && (input.TrailingArmed || input.TrailingMoveCount > 0) {
		trailingLastMoveAt = eventTime
	}

	return &ProtectionGroup{
		TraderID:                   input.TraderID,
		Symbol:                     normalizedSymbol,
		Side:                       normalizedSide,
		LinkedPositionKey:          chooseString(input.LinkedPositionKey, orderRegistryPositionKey(normalizedSymbol, normalizedSide)),
		ProtectionGroupID:          strings.TrimSpace(input.ProtectionGroupID),
		ProtectionMode:             normalizedMode,
		StopLossOrderIntentID:      strings.TrimSpace(input.StopLossOrderIntentID),
		TakeProfitOrderIntentID:    strings.TrimSpace(input.TakeProfitOrderIntentID),
		StopLossExchangeOrderID:    strings.TrimSpace(input.StopLossExchangeOrderID),
		TakeProfitExchangeOrderID:  strings.TrimSpace(input.TakeProfitExchangeOrderID),
		StopLossTriggerPrice:       currentStopLoss,
		TakeProfitTriggerPrice:     currentTakeProfit,
		StopLossInitialTriggerPrice: initialStopLoss,
		StopLossCurrentTriggerPrice: currentStopLoss,
		TakeProfitInitialTriggerPrice: initialTakeProfit,
		TakeProfitCurrentTriggerPrice: currentTakeProfit,
		BreakEvenArmed:             input.BreakEvenArmed,
		TrailingArmed:              input.TrailingArmed,
		TrailingRuleID:             strings.TrimSpace(input.TrailingRuleID),
		TrailingAnchorPrice:        input.TrailingAnchorPrice,
		TrailingLastMoveAt:         trailingLastMoveAt,
		TrailingMoveCount:          input.TrailingMoveCount,
		ProtectionRevision:         revision,
		LastProtectionAction:       strings.TrimSpace(input.LastProtectionAction),
		LastProtectionActionAt:     lastActionAt,
		ProtectedQuantity:          protectedQty,
		Status:                     normalizedStatus,
		Source:                     chooseString(input.Source, "protection_adjustment_manager"),
		LastSyncedAt:               eventTime,
		CreatedAt:                  eventTime,
		UpdatedAt:                  eventTime,
	}
}

func (b *ProtectionAdjustmentBuilder) noopProtectionChange(existing, next *ProtectionGroup) bool {
	if existing == nil || next == nil {
		return false
	}
	return existing.Status == next.Status &&
		existing.ProtectionMode == next.ProtectionMode &&
		existing.StopLossCurrentTriggerPrice == next.StopLossCurrentTriggerPrice &&
		existing.TakeProfitCurrentTriggerPrice == next.TakeProfitCurrentTriggerPrice &&
		existing.BreakEvenArmed == next.BreakEvenArmed &&
		existing.TrailingArmed == next.TrailingArmed &&
		existing.TrailingRuleID == next.TrailingRuleID &&
		existing.TrailingMoveCount == next.TrailingMoveCount &&
		existing.ProtectionRevision == next.ProtectionRevision &&
		existing.LastProtectionAction == next.LastProtectionAction &&
		mathAbs(existing.ProtectedQuantity-next.ProtectedQuantity) < 0.000001
}

func mathMaxFloat(value, fallback float64) float64 {
	if value > 0 {
		return value
	}
	return fallback
}

func mathAbs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
