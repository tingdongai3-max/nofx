package store

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"nofx/logger"

	"gorm.io/gorm"
)

// OrderRegistryEventInput is the normalized input for the order registry builder.
// It can originate from a user-stream event, a local submit confirmation, or a bootstrap recovery row.
type OrderRegistryEventInput struct {
	TraderID          string    // System-owned trader key for this event.
	Symbol            string    // Truth-layer symbol scope attached to the event.
	Side              string    // Raw exchange direction or normalized truth side; the builder converts it into the registry side.
	PositionSideMode  string    // Binance futures position-side mode; fixed to one_way in this scope.
	OrderRole         string    // System-derived role to preserve future capability cuts without guessing.
	LocalIntentID     string    // System-local intent id used for correlation and recovery.
	LinkedGroupID     string    // System-local group id for mutually aware orders.
	LinkedPositionKey string    // System-local position key used by the truth layer.
	ExchangeOrderID   string    // Exchange raw order id when available.
	ClientOrderID     string    // Exchange raw client order id when available.
	OrderType         string    // Exchange raw order type.
	TimeInForce       string    // Exchange raw time-in-force.
	ReduceOnly        bool      // Exchange raw reduce-only flag.
	ClosePosition     bool      // Exchange raw close-position flag.
	OrigQty           float64   // Exchange raw original quantity.
	ExecutedQty       float64   // System-derived executed quantity from user-stream/order snapshots.
	AvgPrice          float64   // Exchange raw average fill price when available.
	TriggerPrice      float64   // Exchange raw trigger price.
	ActivationPrice   float64   // Exchange raw activation price.
	CallbackRate      float64   // Exchange raw callback rate.
	Status            string    // Exchange/order state after this event.
	Source            string    // System truth source label for the registry row.
	EventType         string    // Raw event type preserved in the evidence chain.
	EventSource       string    // Raw event source label preserved in the evidence chain.
	PayloadJSON       string    // Raw JSON payload preserved for audit/debugging.
	EventTime         time.Time // Raw event timestamp or the best recovered timestamp.
}

// OrderRegistryBuilder centralizes all order truth updates.
// Handlers, traders, and reconcilers call into this builder instead of recalculating remaining quantity in multiple places.
type OrderRegistryBuilder struct {
	store *Store
}

// NewOrderRegistryBuilder creates a new order registry builder.
func NewOrderRegistryBuilder(st *Store) *OrderRegistryBuilder {
	return &OrderRegistryBuilder{store: st}
}

// ApplyOrderEvent appends the raw event log row and upserts the registry row in one transaction.
func (b *OrderRegistryBuilder) ApplyOrderEvent(input *OrderRegistryEventInput) (*OrderRegistry, error) {
	if b == nil || b.store == nil {
		return nil, fmt.Errorf("order registry builder store is not configured")
	}
	if input == nil {
		return nil, fmt.Errorf("order registry event input is required")
	}
	if input.TraderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	now := time.Now().UTC()
	eventTime := input.EventTime
	if eventTime.IsZero() {
		eventTime = now
	}

	row := b.buildRegistryRow(input, eventTime)
	eventLog := &OrderEventLog{
		TraderID:        input.TraderID,
		Symbol:          normalizeAggregateSymbol(input.Symbol),
		LocalIntentID:   chooseString(input.LocalIntentID, row.LocalIntentID),
		ExchangeOrderID: row.ExchangeOrderID,
		EventType:       chooseString(input.EventType, "ORDER_EVENT"),
		EventSource:     chooseString(input.EventSource, row.Source),
		PayloadJSON:     input.PayloadJSON,
		EventTime:       eventTime,
	}

	var updated *OrderRegistry
	if err := b.store.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(eventLog).Error; err != nil {
			return fmt.Errorf("failed to append order event log: %w", err)
		}

		registryStore := NewOrderRegistryStore(tx)
		existing, err := registryStore.findExisting(tx, row.TraderID, row.ExchangeOrderID, row.ClientOrderID, row.LocalIntentID)
		if err != nil {
			return err
		}
		if existing != nil {
			row.ID = existing.ID
			row.CreatedAt = existing.CreatedAt
			if row.LocalIntentID == "" {
				row.LocalIntentID = existing.LocalIntentID
			}
			if row.LinkedGroupID == "" {
				row.LinkedGroupID = existing.LinkedGroupID
			}
			if row.LinkedPositionKey == "" {
				row.LinkedPositionKey = existing.LinkedPositionKey
			}
		}
		row.UpdatedAt = eventTime
		row.LastExchangeUpdateAt = eventTime
		if existing != nil {
			return tx.Save(row).Error
		}
		return tx.Create(row).Error
	}); err != nil {
		return nil, err
	}

	updated, err := b.store.OrderRegistry().GetByKeys(row.TraderID, row.ExchangeOrderID, row.ClientOrderID, row.LocalIntentID)
	if err != nil {
		return nil, err
	}

	logger.Infof("order event received: trader=%s symbol=%s event_type=%s source=%s exchange_order_id=%s client_order_id=%s",
		input.TraderID, row.Symbol, eventLog.EventType, eventLog.EventSource, row.ExchangeOrderID, row.ClientOrderID)
	logger.Infof("order registry updated: trader=%s symbol=%s side=%s role=%s status=%s remaining_qty=%.6f working=%v",
		row.TraderID, row.Symbol, row.Side, row.OrderRole, row.Status, row.RemainingQty, row.IsWorking)

	return updated, nil
}

// BuildFromLegacyOrders bootstraps the registry from the legacy trader_orders table.
// This is a recovery path only and is used to seed the new truth layer when it is empty.
func (b *OrderRegistryBuilder) BuildFromLegacyOrders(traderID string) ([]*OrderRegistry, error) {
	if b == nil || b.store == nil {
		return nil, fmt.Errorf("order registry builder store is not configured")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	legacyOrders, err := b.store.Order().GetTraderOrders(traderID, 10000)
	if err != nil {
		return nil, err
	}

	sort.Slice(legacyOrders, func(i, j int) bool {
		return legacyOrders[i].CreatedAt < legacyOrders[j].CreatedAt
	})

	built := make([]*OrderRegistry, 0, len(legacyOrders))
	for _, legacy := range legacyOrders {
		eventTime := time.UnixMilli(legacy.CreatedAt).UTC()
		if legacy.FilledAt > 0 {
			eventTime = time.UnixMilli(legacy.FilledAt).UTC()
		}
		executedQty := legacy.FilledQuantity
		if executedQty <= 0 {
			executedQty = legacy.Quantity
		}

		input := &OrderRegistryEventInput{
			TraderID:         legacy.TraderID,
			Symbol:           legacy.Symbol,
			Side:             legacy.Side,
			PositionSideMode: "one_way",
			OrderRole:        deriveOrderRoleFromLegacy(legacy),
			LocalIntentID:    chooseString(legacy.ClientOrderID, legacy.ExchangeOrderID),
			ExchangeOrderID:  legacy.ExchangeOrderID,
			ClientOrderID:    legacy.ClientOrderID,
			OrderType:        legacy.Type,
			TimeInForce:      legacy.TimeInForce,
			ReduceOnly:       legacy.ReduceOnly,
			ClosePosition:    legacy.ClosePosition,
			OrigQty:          legacy.Quantity,
			ExecutedQty:      executedQty,
			AvgPrice:         legacy.AvgFillPrice,
			TriggerPrice:     legacy.StopPrice,
			Status:           legacy.Status,
			Source:           "bootstrap",
			EventType:        "LEGACY_BACKFILL",
			EventSource:      "bootstrap",
			PayloadJSON:      legacyOrderPayloadJSON(legacy),
			EventTime:        eventTime,
		}

		row, applyErr := b.ApplyOrderEvent(input)
		if applyErr != nil {
			return nil, applyErr
		}
		built = append(built, row)
	}

	return b.store.OrderRegistry().ListByTraderID(traderID)
}

func (b *OrderRegistryBuilder) buildRegistryRow(input *OrderRegistryEventInput, eventTime time.Time) *OrderRegistry {
	normalizedSymbol := normalizeAggregateSymbol(input.Symbol)
	normalizedSide := deriveOrderRegistrySide(input)
	positionSideMode := normalizePositionSideMode(input.PositionSideMode)
	orderRole := deriveOrderRoleFromEvent(input)
	status := normalizeOrderRegistryStatus(input.Status)
	executedQty := input.ExecutedQty
	origQty := input.OrigQty

	if origQty <= 0 && executedQty > 0 {
		origQty = executedQty
	}
	if origQty < 0 {
		origQty = math.Abs(origQty)
	}
	if executedQty < 0 {
		executedQty = math.Abs(executedQty)
	}
	if executedQty > origQty && origQty > 0 {
		executedQty = origQty
	}
	remainingQty := origQty - executedQty
	if remainingQty < 0 {
		remainingQty = 0
	}

	localIntentID := input.LocalIntentID
	if localIntentID == "" {
		localIntentID = chooseString(input.ClientOrderID, input.ExchangeOrderID)
	}

	row := &OrderRegistry{
		TraderID:             input.TraderID,
		Symbol:               normalizedSymbol,
		Side:                 normalizedSide,
		PositionSideMode:     positionSideMode,
		OrderRole:            orderRole,
		LocalIntentID:        localIntentID,
		LinkedGroupID:        chooseString(input.LinkedGroupID, localIntentID),
		LinkedPositionKey:    chooseString(input.LinkedPositionKey, orderRegistryPositionKey(normalizedSymbol, normalizedSide)),
		ExchangeOrderID:      chooseString(input.ExchangeOrderID, ""),
		ClientOrderID:        chooseString(input.ClientOrderID, ""),
		OrderType:            strings.ToUpper(strings.TrimSpace(input.OrderType)),
		TimeInForce:          strings.ToUpper(strings.TrimSpace(input.TimeInForce)),
		ReduceOnly:           input.ReduceOnly,
		ClosePosition:        input.ClosePosition,
		OrigQty:              origQty,
		ExecutedQty:          executedQty,
		RemainingQty:         remainingQty,
		AvgPrice:             input.AvgPrice,
		TriggerPrice:         input.TriggerPrice,
		ActivationPrice:      input.ActivationPrice,
		CallbackRate:         input.CallbackRate,
		Status:               status,
		Source:               chooseString(input.Source, "user_stream"),
		IsWorking:            isOrderRegistryWorkingStatus(status),
		LastExchangeUpdateAt: eventTime,
		CreatedAt:            eventTime,
		UpdatedAt:            eventTime,
	}

	if row.IsWorking && row.RemainingQty <= 0 && row.Status == "FILLED" {
		row.IsWorking = false
	}
	if row.RemainingQty > 0 && !row.IsWorking {
		switch row.Status {
		case "NEW", "PARTIALLY_FILLED", "PENDING_NEW", "WORKING", "OPEN", "PENDING_CANCEL", "PENDING_REPLACE":
			row.IsWorking = true
		}
	}

	return row
}

func chooseString(primary, fallback string) string {
	trimmed := strings.TrimSpace(primary)
	if trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(fallback)
}

func isOrderRegistryWorkingStatus(status string) bool {
	switch normalizeOrderRegistryStatus(status) {
	case "NEW", "PARTIALLY_FILLED", "PENDING_NEW", "WORKING", "OPEN", "PENDING_CANCEL", "PENDING_REPLACE":
		return true
	default:
		return false
	}
}

func deriveOrderRoleFromEvent(input *OrderRegistryEventInput) string {
	if input == nil {
		return "entry"
	}

	role := normalizeOrderRegistryRole(input.OrderRole)
	if role != "entry" || input.OrderRole == "entry" {
		return role
	}

	orderType := strings.ToUpper(strings.TrimSpace(input.OrderType))
	source := strings.ToLower(strings.TrimSpace(input.Source))
	status := normalizeOrderRegistryStatus(input.Status)

	switch {
	case strings.Contains(source, "cancel_replace") || status == "PENDING_REPLACE" || status == "REPLACED":
		return "cancel_replace"
	case strings.Contains(orderType, "TRAILING"):
		return "trailing_protection"
	case strings.Contains(orderType, "TAKE_PROFIT"):
		return "take_profit"
	case strings.Contains(orderType, "STOP"):
		return "stop_loss"
	case input.ReduceOnly || input.ClosePosition:
		return "reduce"
	default:
		return "entry"
	}
}

func deriveOrderRegistrySide(input *OrderRegistryEventInput) string {
	if input == nil {
		return ""
	}

	if linkedSide := parseOrderRegistrySideFromKey(input.LinkedPositionKey); linkedSide != "" {
		return linkedSide
	}

	if normalized := normalizeAggregateSide(input.Side); normalized == "LONG" || normalized == "SHORT" {
		if normalizePositionSideMode(input.PositionSideMode) == "one_way" && isOrderRegistryReduceLike(input) && isOrderRegistryDirectionalSide(input.Side) {
			return invertAggregateSide(normalized)
		}
		return normalized
	}

	return normalizeAggregateSide(input.Side)
}

func isOrderRegistryDirectionalSide(side string) bool {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "BUY", "SELL":
		return true
	default:
		return false
	}
}

func isOrderRegistryReduceLike(input *OrderRegistryEventInput) bool {
	if input == nil {
		return false
	}

	role := normalizeOrderRegistryRole(input.OrderRole)
	switch role {
	case "reduce", "stop_loss", "take_profit", "trailing_protection":
		return true
	}

	if input.ReduceOnly || input.ClosePosition {
		return true
	}

	orderType := strings.ToUpper(strings.TrimSpace(input.OrderType))
	switch {
	case strings.Contains(orderType, "STOP"):
		return true
	case strings.Contains(orderType, "TAKE_PROFIT"):
		return true
	case strings.Contains(orderType, "TRAILING"):
		return true
	default:
		return false
	}
}

func invertAggregateSide(side string) string {
	switch normalizeAggregateSide(side) {
	case "LONG":
		return "SHORT"
	case "SHORT":
		return "LONG"
	default:
		return normalizeAggregateSide(side)
	}
}

func parseOrderRegistrySideFromKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}

	parts := strings.Split(key, "|")
	if len(parts) == 0 {
		return ""
	}

	candidate := normalizeAggregateSide(parts[len(parts)-1])
	if candidate == "LONG" || candidate == "SHORT" {
		return candidate
	}
	return ""
}

func orderRegistryPositionKey(symbol, side string) string {
	normalizedSymbol := normalizeAggregateSymbol(symbol)
	normalizedSide := normalizeAggregateSide(side)
	if normalizedSymbol == "" {
		return normalizedSide
	}
	if normalizedSide == "" {
		return normalizedSymbol
	}
	return normalizedSymbol + "|" + normalizedSide
}

func deriveOrderRoleFromLegacy(order *TraderOrder) string {
	if order == nil {
		return "entry"
	}

	action := strings.ToLower(strings.TrimSpace(order.OrderAction))
	orderType := strings.ToUpper(strings.TrimSpace(order.Type))
	switch {
	case strings.HasPrefix(action, "close_"):
		return "reduce"
	case strings.HasPrefix(action, "open_"):
		return "entry"
	case strings.Contains(orderType, "TRAILING"):
		return "trailing_protection"
	case strings.Contains(orderType, "TAKE_PROFIT"):
		return "take_profit"
	case strings.Contains(orderType, "STOP"):
		return "stop_loss"
	case order.ReduceOnly || order.ClosePosition:
		return "reduce"
	default:
		return "entry"
	}
}

func legacyOrderPayloadJSON(order *TraderOrder) string {
	if order == nil {
		return "{}"
	}
	payload := map[string]interface{}{
		"order_id":          order.ID,
		"trader_id":         order.TraderID,
		"exchange_id":       order.ExchangeID,
		"exchange_type":     order.ExchangeType,
		"exchange_order_id": order.ExchangeOrderID,
		"client_order_id":   order.ClientOrderID,
		"symbol":            order.Symbol,
		"side":              order.Side,
		"position_side":     order.PositionSide,
		"type":              order.Type,
		"time_in_force":     order.TimeInForce,
		"quantity":          order.Quantity,
		"price":             order.Price,
		"stop_price":        order.StopPrice,
		"status":            order.Status,
		"filled_quantity":   order.FilledQuantity,
		"avg_fill_price":    order.AvgFillPrice,
		"reduce_only":       order.ReduceOnly,
		"close_position":    order.ClosePosition,
		"order_action":      order.OrderAction,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(data)
}
