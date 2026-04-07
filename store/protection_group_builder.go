package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nofx/logger"

	"gorm.io/gorm"
)

// ProtectionGroupEventInput is the normalized input for the protection group builder.
// It can originate from a fixed-protection manager, a user-stream reconciliation event, or a bootstrap recovery row.
type ProtectionGroupEventInput struct {
	TraderID                string    // System-owned trader key for this event.
	Symbol                  string    // Truth-layer symbol scope attached to the event.
	Side                    string    // Truth-layer one-way side scope attached to the event.
	LinkedPositionKey       string    // System-local position key used by the truth layer.
	ProtectionGroupID       string    // System-generated protection group identifier.
	ProtectionMode         string    // Protection policy label; current phase only allows fixed.
	StopLossOrderIntentID   string    // System-local stop-loss intent identifier.
	TakeProfitOrderIntentID string    // System-local take-profit intent identifier.
	StopLossExchangeOrderID string    // Exchange raw stop-loss order id when available.
	TakeProfitExchangeOrderID string  // Exchange raw take-profit order id when available.
	StopLossTriggerPrice    float64   // System-derived stop-loss trigger price.
	TakeProfitTriggerPrice  float64   // System-derived take-profit trigger price.
	ProtectedQuantity       float64   // System-derived active protected quantity.
	Status                  string    // Protection truth status after this event.
	Source                  string    // System truth source label for the protection row.
	EventType               string    // Raw event type preserved in the evidence chain.
	EventSource             string    // Raw event source label preserved in the evidence chain.
	PayloadJSON             string    // Raw JSON payload preserved for audit/debugging.
	EventTime               time.Time // Raw event timestamp or the best recovered timestamp.
}

// ProtectionGroupBuilder centralizes protection truth updates.
// Managers and reconcilers call into this builder instead of recalculating protection state in multiple places.
type ProtectionGroupBuilder struct {
	store *Store
}

// NewProtectionGroupBuilder creates a new protection group builder.
func NewProtectionGroupBuilder(st *Store) *ProtectionGroupBuilder {
	return &ProtectionGroupBuilder{store: st}
}

// ApplyProtectionEvent appends the raw event log row and upserts the protection snapshot row in one transaction.
func (b *ProtectionGroupBuilder) ApplyProtectionEvent(input *ProtectionGroupEventInput) (*ProtectionGroup, error) {
	if b == nil || b.store == nil {
		return nil, fmt.Errorf("protection group builder store is not configured")
	}
	if input == nil {
		return nil, fmt.Errorf("protection group event input is required")
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
	eventLog := &ProtectionEventLog{
		TraderID:          input.TraderID,
		Symbol:            row.Symbol,
		ProtectionGroupID: row.ProtectionGroupID,
		EventType:         chooseString(input.EventType, "PROTECTION_EVENT"),
		EventSource:       chooseString(input.EventSource, row.Source),
		PayloadJSON:       input.PayloadJSON,
		EventTime:         eventTime,
	}

	var updated *ProtectionGroup
	if err := b.store.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(eventLog).Error; err != nil {
			return fmt.Errorf("failed to append protection event log: %w", err)
		}

		groupStore := NewProtectionGroupStore(tx)
		existing, err := groupStore.findExisting(tx, row.TraderID, row.ProtectionGroupID, row.LinkedPositionKey)
		if err != nil {
			return err
		}
		if existing != nil {
			row.ID = existing.ID
			row.CreatedAt = existing.CreatedAt
		}
		row.UpdatedAt = eventTime
		row.LastSyncedAt = eventTime
		if row.Status == "closed" {
			row.ProtectedQuantity = 0
		}
		if existing != nil {
			return tx.Save(row).Error
		}
		return tx.Create(row).Error
	}); err != nil {
		return nil, err
	}

	updated, err := b.store.ProtectionGroup().GetByKeys(row.TraderID, row.ProtectionGroupID, row.LinkedPositionKey)
	if err != nil {
		return nil, err
	}

	logger.Infof("protection event received: trader=%s symbol=%s event_type=%s source=%s protection_group_id=%s",
		input.TraderID, row.Symbol, eventLog.EventType, eventLog.EventSource, row.ProtectionGroupID)
	logger.Infof("protection group updated: trader=%s symbol=%s side=%s group=%s status=%s protected_qty=%.6f",
		row.TraderID, row.Symbol, row.Side, row.ProtectionGroupID, row.Status, row.ProtectedQuantity)

	return updated, nil
}

func (b *ProtectionGroupBuilder) buildProtectionRow(input *ProtectionGroupEventInput, eventTime time.Time) *ProtectionGroup {
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

	return &ProtectionGroup{
		TraderID:                input.TraderID,
		Symbol:                  normalizedSymbol,
		Side:                    normalizedSide,
		LinkedPositionKey:       chooseString(input.LinkedPositionKey, orderRegistryPositionKey(normalizedSymbol, normalizedSide)),
		ProtectionGroupID:       strings.TrimSpace(input.ProtectionGroupID),
		ProtectionMode:         normalizedMode,
		StopLossOrderIntentID:   strings.TrimSpace(input.StopLossOrderIntentID),
		TakeProfitOrderIntentID: strings.TrimSpace(input.TakeProfitOrderIntentID),
		StopLossExchangeOrderID: strings.TrimSpace(input.StopLossExchangeOrderID),
		TakeProfitExchangeOrderID: strings.TrimSpace(input.TakeProfitExchangeOrderID),
		StopLossTriggerPrice:    input.StopLossTriggerPrice,
		TakeProfitTriggerPrice:  input.TakeProfitTriggerPrice,
		ProtectedQuantity:       protectedQty,
		Status:                  normalizedStatus,
		Source:                  chooseString(input.Source, "fixed_protection_manager"),
		LastSyncedAt:            eventTime,
		CreatedAt:               eventTime,
		UpdatedAt:               eventTime,
	}
}

// BuildFromExistingGroup creates a protection group event from an already-loaded row.
// It is useful for bootstrap and recovery flows that want to rewrite the current snapshot through the builder.
func (b *ProtectionGroupBuilder) BuildFromExistingGroup(group *ProtectionGroup, eventType, eventSource string) (*ProtectionGroup, error) {
	if group == nil {
		return nil, fmt.Errorf("protection group is nil")
	}
	payload, _ := json.Marshal(group)
	return b.ApplyProtectionEvent(&ProtectionGroupEventInput{
		TraderID:                group.TraderID,
		Symbol:                  group.Symbol,
		Side:                    group.Side,
		LinkedPositionKey:       group.LinkedPositionKey,
		ProtectionGroupID:       group.ProtectionGroupID,
		ProtectionMode:         group.ProtectionMode,
		StopLossOrderIntentID:   group.StopLossOrderIntentID,
		TakeProfitOrderIntentID: group.TakeProfitOrderIntentID,
		StopLossExchangeOrderID: group.StopLossExchangeOrderID,
		TakeProfitExchangeOrderID: group.TakeProfitExchangeOrderID,
		StopLossTriggerPrice:    group.StopLossTriggerPrice,
		TakeProfitTriggerPrice:  group.TakeProfitTriggerPrice,
		ProtectedQuantity:       group.ProtectedQuantity,
		Status:                  group.Status,
		Source:                  group.Source,
		EventType:               eventType,
		EventSource:             eventSource,
		PayloadJSON:             string(payload),
		EventTime:               time.Now().UTC(),
	})
}

