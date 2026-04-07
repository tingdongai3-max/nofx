package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ProtectionEventLogStore stores the append-only event evidence chain for protection truth updates.
type ProtectionEventLogStore struct {
	db *gorm.DB
}

// NewProtectionEventLogStore creates a protection event log store.
func NewProtectionEventLogStore(db *gorm.DB) *ProtectionEventLogStore {
	return &ProtectionEventLogStore{db: db}
}

// ProtectionEventLog is the append-only event evidence chain for the protection truth layer.
// It is a raw event record, not a reconciled protection-state snapshot.
type ProtectionEventLog struct {
	ID                int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                                       // System persistence row id for the raw evidence record.
	TraderID          string    `gorm:"column:trader_id;not null;index:idx_protection_event_logs_trader_symbol,priority:1" json:"trader_id"`                     // System-owned trader key for the raw event.
	Symbol            string    `gorm:"column:symbol;not null;index:idx_protection_event_logs_trader_symbol,priority:2" json:"symbol"`                           // Raw or normalized symbol attached to the event.
	ProtectionGroupID string    `gorm:"column:protection_group_id;not null;default:'';index:idx_protection_event_logs_protection_group_id" json:"protection_group_id"` // System-generated protection group identifier.
	EventType         string    `gorm:"column:event_type;not null;default:''" json:"event_type"`                                                                  // Raw event type from manager/reconciler/bootstrap.
	PayloadJSON       string    `gorm:"column:payload_json;type:text;not null;default:''" json:"payload_json"`                                                    // Raw payload preserved for evidence and debugging.
	EventSource       string    `gorm:"column:event_source;not null;default:''" json:"event_source"`                                                              // Source label: fixed_protection_manager, user_stream, exchange_snapshot, bootstrap.
	EventTime         time.Time `gorm:"column:event_time;index:idx_protection_event_logs_event_time" json:"event_time"`                                            // Raw exchange/event timestamp.
	CreatedAt         time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                                       // Persistence timestamp.
}

// TableName returns the table name for ProtectionEventLog.
func (ProtectionEventLog) TableName() string {
	return "protection_event_logs"
}

// initTables initializes the event log table and indexes.
func (s *ProtectionEventLogStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'protection_event_logs'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&ProtectionEventLog{}); err != nil {
		return fmt.Errorf("failed to migrate protection_event_logs table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *ProtectionEventLogStore) ensureIndexes() error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_protection_event_logs_trader_symbol ON protection_event_logs(trader_id, symbol)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_event_logs_protection_group_id ON protection_event_logs(protection_group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_event_logs_event_time ON protection_event_logs(event_time)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create protection event log index: %w", err)
		}
	}
	return nil
}

// Append stores a raw protection event log row.
func (s *ProtectionEventLogStore) Append(entry *ProtectionEventLog) error {
	if entry == nil {
		return fmt.Errorf("protection event log entry is nil")
	}
	entry.Symbol = normalizeAggregateSymbol(entry.Symbol)
	entry.ProtectionGroupID = strings.TrimSpace(entry.ProtectionGroupID)
	entry.EventType = strings.TrimSpace(entry.EventType)
	entry.EventSource = strings.TrimSpace(entry.EventSource)
	return s.db.Create(entry).Error
}

// ListRecentByTrader returns the most recent protection event evidence rows for one trader.
func (s *ProtectionEventLogStore) ListRecentByTrader(traderID string, limit int) ([]*ProtectionEventLog, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows []*ProtectionEventLog
	err := s.db.Where("trader_id = ?", traderID).
		Order("event_time DESC, created_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list recent protection event logs: %w", err)
	}
	return rows, nil
}

// ListRecentByTraderSymbol returns the most recent protection event evidence rows for one trader/symbol.
func (s *ProtectionEventLogStore) ListRecentByTraderSymbol(traderID, symbol string, limit int) ([]*ProtectionEventLog, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows []*ProtectionEventLog
	err := s.db.Where("trader_id = ? AND symbol = ?", traderID, normalizeAggregateSymbol(symbol)).
		Order("event_time DESC, created_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list recent protection event logs for symbol: %w", err)
	}
	return rows, nil
}

