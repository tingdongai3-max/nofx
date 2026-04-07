package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ScaleInEventLogStore stores the append-only event evidence chain for scale-in truth updates.
type ScaleInEventLogStore struct {
	db *gorm.DB
}

// NewScaleInEventLogStore creates a scale-in event log store.
func NewScaleInEventLogStore(db *gorm.DB) *ScaleInEventLogStore {
	return &ScaleInEventLogStore{db: db}
}

// ScaleInEventLog is the append-only event evidence chain for the scale-in truth layer.
// It is a raw event record, not a reconciled plan snapshot.
type ScaleInEventLog struct {
	ID            int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                                 // System persistence row id for the raw evidence record.
	TraderID      string    `gorm:"column:trader_id;not null;index:idx_scale_in_event_logs_trader_symbol,priority:1" json:"trader_id"`                  // System-owned trader key for the raw event.
	Symbol        string    `gorm:"column:symbol;not null;index:idx_scale_in_event_logs_trader_symbol,priority:2" json:"symbol"`                        // Raw or normalized symbol attached to the event.
	ScaleInPlanID string    `gorm:"column:scale_in_plan_id;not null;default:'';index:idx_scale_in_event_logs_scale_in_plan_id" json:"scale_in_plan_id"` // System-generated plan identifier.
	LevelIndex    int       `gorm:"column:level_index;not null;default:0" json:"level_index"`                                                           // System-owned level index when the event targets a specific level.
	EventType     string    `gorm:"column:event_type;not null;default:''" json:"event_type"`                                                            // Raw event type from manager/reconciler/bootstrap.
	PayloadJSON   string    `gorm:"column:payload_json;type:text;not null;default:''" json:"payload_json"`                                              // Raw payload preserved for evidence and debugging.
	EventSource   string    `gorm:"column:event_source;not null;default:''" json:"event_source"`                                                        // Source label: scale_in_manager, user_stream, exchange_snapshot, bootstrap.
	EventTime     time.Time `gorm:"column:event_time;index:idx_scale_in_event_logs_event_time" json:"event_time"`                                       // Raw exchange/event timestamp.
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                                 // Persistence timestamp.
}

// TableName returns the table name for ScaleInEventLog.
func (ScaleInEventLog) TableName() string {
	return "scale_in_event_logs"
}

// initTables initializes the scale-in event log table and indexes.
func (s *ScaleInEventLogStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'scale_in_event_logs'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&ScaleInEventLog{}); err != nil {
		return fmt.Errorf("failed to migrate scale_in_event_logs table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *ScaleInEventLogStore) ensureIndexes() error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_scale_in_event_logs_trader_symbol ON scale_in_event_logs(trader_id, symbol)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_in_event_logs_scale_in_plan_id ON scale_in_event_logs(scale_in_plan_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_in_event_logs_event_time ON scale_in_event_logs(event_time)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create scale-in event log index: %w", err)
		}
	}
	return nil
}

// Append stores a raw scale-in event log row.
func (s *ScaleInEventLogStore) Append(entry *ScaleInEventLog) error {
	if entry == nil {
		return fmt.Errorf("scale-in event log entry is nil")
	}
	entry.Symbol = normalizeAggregateSymbol(entry.Symbol)
	entry.ScaleInPlanID = strings.TrimSpace(entry.ScaleInPlanID)
	entry.EventType = strings.TrimSpace(entry.EventType)
	entry.EventSource = strings.TrimSpace(entry.EventSource)
	return s.db.Create(entry).Error
}

// ListRecentByTrader returns the most recent scale-in evidence rows for one trader.
func (s *ScaleInEventLogStore) ListRecentByTrader(traderID string, limit int) ([]*ScaleInEventLog, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows []*ScaleInEventLog
	err := s.db.Where("trader_id = ?", traderID).
		Order("event_time DESC, created_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list recent scale-in event logs: %w", err)
	}
	return rows, nil
}

// ListRecentByTraderSymbol returns the most recent scale-in evidence rows for one trader/symbol.
func (s *ScaleInEventLogStore) ListRecentByTraderSymbol(traderID, symbol string, limit int) ([]*ScaleInEventLog, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows []*ScaleInEventLog
	err := s.db.Where("trader_id = ? AND symbol = ?", traderID, normalizeAggregateSymbol(symbol)).
		Order("event_time DESC, created_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list recent scale-in event logs for symbol: %w", err)
	}
	return rows, nil
}
