package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ProtectionAdjustmentEventLogStore stores the append-only evidence chain for dynamic protection changes.
// It is a raw event log, not a reconciled protection snapshot.
type ProtectionAdjustmentEventLogStore struct {
	db *gorm.DB
}

// NewProtectionAdjustmentEventLogStore creates a protection adjustment event log store.
func NewProtectionAdjustmentEventLogStore(db *gorm.DB) *ProtectionAdjustmentEventLogStore {
	return &ProtectionAdjustmentEventLogStore{db: db}
}

// ProtectionAdjustmentEventLog is the append-only raw evidence chain for dynamic protection changes.
// It records the old and new trigger prices as event evidence, not as an execution command.
type ProtectionAdjustmentEventLog struct {
	ID                  int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                                                 // System persistence row id for the raw evidence record.
	TraderID            string    `gorm:"column:trader_id;not null;index:idx_protection_adjustment_event_logs_trader_symbol,priority:1" json:"trader_id"`                          // System-owned trader key.
	Symbol              string    `gorm:"column:symbol;not null;index:idx_protection_adjustment_event_logs_trader_symbol,priority:2" json:"symbol"`                                // Truth-layer symbol scope attached to the event.
	ProtectionGroupID   string    `gorm:"column:protection_group_id;not null;default:'';index:idx_protection_adjustment_event_logs_protection_group_id" json:"protection_group_id"` // System-generated protection group identifier.
	ProtectionRevision  int       `gorm:"column:protection_revision;not null;default:0;index:idx_protection_adjustment_event_logs_protection_revision" json:"protection_revision"`   // System-owned protection revision snapshot for the event.
	EventType           string    `gorm:"column:event_type;not null;default:''" json:"event_type"`                                                                          // Raw event type from the adjustment manager or reconciler.
	OldStopLossPrice    float64   `gorm:"column:old_stop_loss_price;not null;default:0" json:"old_stop_loss_price"`                                                          // Previous stop-loss trigger price before the change.
	NewStopLossPrice    float64   `gorm:"column:new_stop_loss_price;not null;default:0" json:"new_stop_loss_price"`                                                          // New stop-loss trigger price after the change.
	OldTakeProfitPrice  float64   `gorm:"column:old_take_profit_price;not null;default:0" json:"old_take_profit_price"`                                                      // Previous take-profit trigger price before the change.
	NewTakeProfitPrice  float64   `gorm:"column:new_take_profit_price;not null;default:0" json:"new_take_profit_price"`                                                      // New take-profit trigger price after the change.
	Reason              string    `gorm:"column:reason;not null;default:''" json:"reason"`                                                                                    // Human-readable reason for the protection adjustment.
	PayloadJSON         string    `gorm:"column:payload_json;type:text;not null;default:''" json:"payload_json"`                                                                // Raw JSON payload preserved for evidence and debugging.
	EventSource         string    `gorm:"column:event_source;not null;default:''" json:"event_source"`                                                                        // Source label: adjustment_manager, cancel_replace_reconciler, bootstrap, user_stream.
	EventTime           time.Time `gorm:"column:event_time;index:idx_protection_adjustment_event_logs_event_time" json:"event_time"`                                          // Raw event timestamp or the best recovered timestamp.
	CreatedAt           time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                                                 // Persistence timestamp.
}

// TableName returns the table name for ProtectionAdjustmentEventLog.
func (ProtectionAdjustmentEventLog) TableName() string {
	return "protection_adjustment_event_logs"
}

// initTables initializes the event log table and indexes.
func (s *ProtectionAdjustmentEventLogStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'protection_adjustment_event_logs'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&ProtectionAdjustmentEventLog{}); err != nil {
		return fmt.Errorf("failed to migrate protection_adjustment_event_logs table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *ProtectionAdjustmentEventLogStore) ensureIndexes() error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_protection_adjustment_event_logs_trader_symbol ON protection_adjustment_event_logs(trader_id, symbol)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_adjustment_event_logs_protection_group_id ON protection_adjustment_event_logs(protection_group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_adjustment_event_logs_protection_revision ON protection_adjustment_event_logs(protection_revision)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_adjustment_event_logs_event_time ON protection_adjustment_event_logs(event_time)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create protection adjustment event log index: %w", err)
		}
	}
	return nil
}

// Append stores a raw dynamic protection event log row.
func (s *ProtectionAdjustmentEventLogStore) Append(entry *ProtectionAdjustmentEventLog) error {
	if entry == nil {
		return fmt.Errorf("protection adjustment event log entry is nil")
	}
	entry.Symbol = normalizeAggregateSymbol(entry.Symbol)
	entry.ProtectionGroupID = strings.TrimSpace(entry.ProtectionGroupID)
	entry.EventType = strings.TrimSpace(entry.EventType)
	entry.EventSource = strings.TrimSpace(entry.EventSource)
	entry.Reason = strings.TrimSpace(entry.Reason)
	return s.db.Create(entry).Error
}

// ListRecentByTrader returns the most recent dynamic protection evidence rows for one trader.
func (s *ProtectionAdjustmentEventLogStore) ListRecentByTrader(traderID string, limit int) ([]*ProtectionAdjustmentEventLog, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows []*ProtectionAdjustmentEventLog
	err := s.db.Where("trader_id = ?", traderID).
		Order("event_time DESC, created_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list recent protection adjustment event logs: %w", err)
	}
	return rows, nil
}

// ListRecentByTraderSymbol returns the most recent dynamic protection evidence rows for one trader/symbol.
func (s *ProtectionAdjustmentEventLogStore) ListRecentByTraderSymbol(traderID, symbol string, limit int) ([]*ProtectionAdjustmentEventLog, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows []*ProtectionAdjustmentEventLog
	err := s.db.Where("trader_id = ? AND symbol = ?", traderID, normalizeAggregateSymbol(symbol)).
		Order("event_time DESC, created_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list recent protection adjustment event logs for symbol: %w", err)
	}
	return rows, nil
}

