package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// OrderEventLogStore stores the append-only event evidence chain for order truth updates.
type OrderEventLogStore struct {
	db *gorm.DB
}

// NewOrderEventLogStore creates an order event log store.
func NewOrderEventLogStore(db *gorm.DB) *OrderEventLogStore {
	return &OrderEventLogStore{db: db}
}

// OrderEventLog is the append-only event evidence chain for the order truth layer.
// It is a raw event record, not a reconciled order-state snapshot.
type OrderEventLog struct {
	ID              int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                                 // System persistence row id for the raw evidence record.
	TraderID        string    `gorm:"column:trader_id;not null;index:idx_order_event_logs_trader_symbol,priority:1" json:"trader_id"`                     // System-owned trader key for the raw event.
	Symbol          string    `gorm:"column:symbol;not null;index:idx_order_event_logs_trader_symbol,priority:2" json:"symbol"`                           // Raw or normalized symbol attached to the event.
	LocalIntentID   string    `gorm:"column:local_intent_id;not null;default:'';index:idx_order_event_logs_local_intent_id" json:"local_intent_id"`       // System-local intent identifier used for correlation.
	ExchangeOrderID string    `gorm:"column:exchange_order_id;not null;default:'';index:idx_order_event_logs_exchange_order_id" json:"exchange_order_id"` // Exchange raw order id when available.
	EventType       string    `gorm:"column:event_type;not null;default:''" json:"event_type"`                                                            // Raw event type from user stream or reconciliation source.
	EventSource     string    `gorm:"column:event_source;not null;default:''" json:"event_source"`                                                        // Source label: user_stream, local_submit, exchange_snapshot, bootstrap.
	PayloadJSON     string    `gorm:"column:payload_json;type:text;not null;default:''" json:"payload_json"`                                              // Raw payload preserved for evidence and debugging.
	EventTime       time.Time `gorm:"column:event_time;index:idx_order_event_logs_event_time" json:"event_time"`                                          // Raw exchange/event timestamp.
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                                 // Persistence timestamp.
}

// TableName returns the table name for OrderEventLog.
func (OrderEventLog) TableName() string {
	return "order_event_logs"
}

// initTables initializes the event log table and indexes.
func (s *OrderEventLogStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'order_event_logs'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&OrderEventLog{}); err != nil {
		return fmt.Errorf("failed to migrate order_event_logs table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *OrderEventLogStore) ensureIndexes() error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_order_event_logs_trader_symbol ON order_event_logs(trader_id, symbol)`,
		`CREATE INDEX IF NOT EXISTS idx_order_event_logs_local_intent_id ON order_event_logs(local_intent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_event_logs_exchange_order_id ON order_event_logs(exchange_order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_event_logs_event_time ON order_event_logs(event_time)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create order event log index: %w", err)
		}
	}
	return nil
}

// Append stores a raw event log row.
func (s *OrderEventLogStore) Append(entry *OrderEventLog) error {
	if entry == nil {
		return fmt.Errorf("order event log entry is nil")
	}
	entry.Symbol = normalizeAggregateSymbol(entry.Symbol)
	entry.LocalIntentID = strings.TrimSpace(entry.LocalIntentID)
	entry.ExchangeOrderID = strings.TrimSpace(entry.ExchangeOrderID)
	entry.EventType = strings.TrimSpace(entry.EventType)
	entry.EventSource = strings.TrimSpace(entry.EventSource)
	return s.db.Create(entry).Error
}

// ListRecentByTrader returns the most recent event evidence rows for one trader.
func (s *OrderEventLogStore) ListRecentByTrader(traderID string, limit int) ([]*OrderEventLog, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows []*OrderEventLog
	err := s.db.Where("trader_id = ?", traderID).
		Order("event_time DESC, created_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list recent order event logs: %w", err)
	}
	return rows, nil
}

// ListRecentByTraderSymbol returns the most recent event evidence rows for one trader/symbol.
func (s *OrderEventLogStore) ListRecentByTraderSymbol(traderID, symbol string, limit int) ([]*OrderEventLog, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows []*OrderEventLog
	err := s.db.Where("trader_id = ? AND symbol = ?", traderID, normalizeAggregateSymbol(symbol)).
		Order("event_time DESC, created_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list recent order event logs for symbol: %w", err)
	}
	return rows, nil
}
