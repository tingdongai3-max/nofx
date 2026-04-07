package trader

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/store"
)

// BinanceOrderReconcileManager owns the Binance USDⓈ-M Futures one-way order truth ingestion path.
// It turns user-stream order events into registry rows and exposes the reconcile preview used by API/UI.
type BinanceOrderReconcileManager struct {
	store      *store.Store
	reconciler *OrderStateReconciler
	onOrderRegistryUpdated func(*store.OrderRegistry) error

	mu              sync.RWMutex
	userStreamReady bool
}

// NewBinanceOrderReconcileManager creates a new Binance order reconcile manager.
func NewBinanceOrderReconcileManager(st *store.Store) *BinanceOrderReconcileManager {
	return &BinanceOrderReconcileManager{
		store:      st,
		reconciler: NewOrderStateReconciler(st),
	}
}

// SetOrderRegistryUpdatedCallback installs a callback that runs after each registry update.
// It is used by the Binance protection coordinator to react to confirmed fills.
func (m *BinanceOrderReconcileManager) SetOrderRegistryUpdatedCallback(fn func(*store.OrderRegistry) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onOrderRegistryUpdated = fn
}

// SetUserStreamReady marks whether the Binance user-stream bridge is currently ready.
func (m *BinanceOrderReconcileManager) SetUserStreamReady(ready bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userStreamReady = ready
}

// UserStreamReady returns the current user-stream readiness flag.
func (m *BinanceOrderReconcileManager) UserStreamReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.userStreamReady
}

// HandleOrderTradeUpdate ingests a Binance ORDER_TRADE_UPDATE payload into the order registry.
func (m *BinanceOrderReconcileManager) HandleOrderTradeUpdate(traderID string, payload []byte) (*store.OrderRegistry, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("binance order reconcile manager store is not configured")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	var envelope binanceUserStreamEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("failed to parse binance user stream payload: %w", err)
	}

	order := envelope.Order
	exchangeOrderID := order.ExchangeOrderID()
	localIntentID := firstNonEmpty(order.ClientOrderID, exchangeOrderID)
	input := &store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            order.Symbol,
		Side:              order.Side,
		PositionSideMode:  normalizeBinancePositionMode(order.PositionSide),
		OrderRole:         "",
		LocalIntentID:     localIntentID,
		LinkedGroupID:     localIntentID,
		LinkedPositionKey: "",
		ExchangeOrderID:   exchangeOrderID,
		ClientOrderID:     order.ClientOrderID,
		OrderType:         order.OrderType,
		TimeInForce:       order.TimeInForce,
		ReduceOnly:        order.ReduceOnly,
		ClosePosition:     order.ClosePosition,
		OrigQty:           parseBinanceFloat(order.OrigQty),
		ExecutedQty:       parseBinanceFloat(order.ExecutedQty),
		AvgPrice:          parseBinanceFloat(order.AvgPrice),
		TriggerPrice:      parseBinanceFloat(order.StopPrice),
		ActivationPrice:   parseBinanceFloat(order.ActivationPrice),
		CallbackRate:      parseBinanceFloat(order.CallbackRate),
		Status:            order.Status,
		Source:            "user_stream",
		EventType:         envelope.EventType,
		EventSource:       "binance_user_stream",
		PayloadJSON:       string(payload),
		EventTime:         envelope.eventTime(),
	}

	updated, err := m.store.OrderRegistryBuilder().ApplyOrderEvent(input)
	if err != nil {
		return nil, err
	}
	m.SetUserStreamReady(true)
	m.mu.RLock()
	callback := m.onOrderRegistryUpdated
	m.mu.RUnlock()
	if callback != nil {
		if err := callback(updated); err != nil {
			return updated, err
		}
	}
	return updated, nil
}

// PreviewOrderState returns the current order truth preview for a trader.
func (m *BinanceOrderReconcileManager) PreviewOrderState(traderID, selectedSymbol string, reader openOrderReader) (*OrderReconcilePreview, error) {
	if m == nil || m.reconciler == nil {
		return nil, fmt.Errorf("binance order reconcile manager is not configured")
	}
	return m.reconciler.ReconcileTraderOrders(traderID, selectedSymbol, reader, m.UserStreamReady())
}

type binanceUserStreamEnvelope struct {
	EventType string                    `json:"e"`
	EventTime int64                     `json:"E"`
	TradeTime int64                     `json:"T"`
	Order     binanceUserStreamOrderRaw `json:"o"`
}

func (e binanceUserStreamEnvelope) eventTime() time.Time {
	ts := e.TradeTime
	if ts <= 0 {
		ts = e.EventTime
	}
	if ts <= 0 {
		return time.Now().UTC()
	}
	return time.UnixMilli(ts).UTC()
}

type binanceUserStreamOrderRaw struct {
	Symbol          string `json:"s"`  // Exchange raw symbol.
	ClientOrderID   string `json:"c"`  // Exchange raw client order id.
	Side            string `json:"S"`  // BUY or SELL.
	OrderType       string `json:"o"`  // Exchange raw order type.
	TimeInForce     string `json:"f"`  // Exchange raw time-in-force.
	OrigQty         string `json:"q"`  // Exchange raw original quantity.
	Price           string `json:"p"`  // Exchange raw limit price.
	AvgPrice        string `json:"ap"` // Exchange raw average fill price.
	StopPrice       string `json:"sp"` // Exchange raw trigger price.
	ExecutedQty     string `json:"z"`  // Exchange raw executed quantity.
	Status          string `json:"X"`  // Exchange raw order status.
	OrderID         int64  `json:"i"`  // Exchange raw order id.
	ReduceOnly      bool   `json:"R"`  // Exchange raw reduce-only flag.
	ClosePosition   bool   `json:"cp"` // Exchange raw close-position flag.
	PositionSide    string `json:"ps"` // Exchange raw position side.
	ActivationPrice string `json:"AP"` // Exchange raw activation price.
	CallbackRate    string `json:"cr"` // Exchange raw callback rate.
}

func (o binanceUserStreamOrderRaw) ExchangeOrderID() string {
	if o.OrderID == 0 {
		return ""
	}
	return strconv.FormatInt(o.OrderID, 10)
}

func normalizeBinancePositionMode(positionSide string) string {
	switch strings.ToUpper(strings.TrimSpace(positionSide)) {
	case "BOTH", "":
		return "one_way"
	default:
		return "one_way"
	}
}

func normalizeBinancePositionSide(positionSide string) string {
	switch strings.ToUpper(strings.TrimSpace(positionSide)) {
	case "LONG":
		return "LONG"
	case "SHORT":
		return "SHORT"
	default:
		return ""
	}
}

func normalizeBinanceTruthSide(side string) string {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "BUY":
		return "LONG"
	case "SELL":
		return "SHORT"
	default:
		return ""
	}
}

func parseBinanceFloat(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		logger.Infof("failed to parse binance numeric field %q: %v", value, err)
		return 0
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
