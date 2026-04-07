package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TrailingRuleStore stores the dynamic protection rule truth rows.
// It is a rule-layer truth table, not an exchange order mirror.
type TrailingRuleStore struct {
	db *gorm.DB
}

// NewTrailingRuleStore creates a trailing rule store.
func NewTrailingRuleStore(db *gorm.DB) *TrailingRuleStore {
	return &TrailingRuleStore{db: db}
}

// TrailingRule is the system-owned rule model for break-even or step-trailing protection.
// It is a rule-layer truth row, not a protection order snapshot.
type TrailingRule struct {
	ID                 int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                               // System persistence row id; not an execution identifier.
	TraderID           string    `gorm:"column:trader_id;not null;index:idx_trailing_rules_trader_symbol_side_status,priority:1" json:"trader_id"`           // System-owned trader key.
	Symbol             string    `gorm:"column:symbol;not null;index:idx_trailing_rules_trader_symbol_side_status,priority:2" json:"symbol"`                 // Exchange-normalized symbol in the trailing-rule scope.
	Side               string    `gorm:"column:side;not null;index:idx_trailing_rules_trader_symbol_side_status,priority:3" json:"side"`                     // One-way side in the trailing-rule scope: LONG or SHORT.
	LinkedPositionKey  string    `gorm:"column:linked_position_key;not null;default:'';index:idx_trailing_rules_linked_position_key" json:"linked_position_key"` // System-owned position correlation key.
	TrailingRuleID     string    `gorm:"column:trailing_rule_id;not null;default:'';uniqueIndex:idx_trailing_rules_trailing_rule_id;index:idx_trailing_rules_trailing_rule_id" json:"trailing_rule_id"` // System-generated rule identifier.
	RuleMode           string    `gorm:"column:rule_mode;not null;default:break_even" json:"rule_mode"`                                                       // Rule policy label: break_even or step_trailing.
	ActivationType     string    `gorm:"column:activation_type;not null;default:profit_pct" json:"activation_type"`                                         // Rule activation type: profit_r_multiple, profit_pct, or price_level.
	ActivationValue    float64   `gorm:"column:activation_value;not null;default:0" json:"activation_value"`                                                   // Rule activation threshold value.
	StepTriggerType    string    `gorm:"column:step_trigger_type;not null;default:profit_pct" json:"step_trigger_type"`                                       // Step trigger type: profit_r_multiple, profit_pct, or price_level.
	StepTriggerValue   float64   `gorm:"column:step_trigger_value;not null;default:0" json:"step_trigger_value"`                                               // Step trigger threshold value.
	StepMoveType       string    `gorm:"column:step_move_type;not null;default:price_offset" json:"step_move_type"`                                          // Step move type: price_offset, price_pct, or price_level.
	StepMoveValue      float64   `gorm:"column:step_move_value;not null;default:0" json:"step_move_value"`                                                     // Step move step value.
	MaxMoveCount       int       `gorm:"column:max_move_count;not null;default:0" json:"max_move_count"`                                                       // Maximum number of allowed stop-loss moves.
	Status             string    `gorm:"column:status;not null;default:draft;index:idx_trailing_rules_trader_symbol_side_status,priority:4" json:"status"`   // Rule truth status: draft, armed, paused, invalid, closed.
	Source             string    `gorm:"column:source;not null;default:''" json:"source"`                                                                     // Truth source label: adjustment_manager, bootstrap, user_stream, exchange_snapshot.
	CreatedAt          time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                                   // Persistence timestamp.
	UpdatedAt          time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`                                                                   // Persistence timestamp.
}

// TableName returns the table name for TrailingRule.
func (TrailingRule) TableName() string {
	return "trailing_rules"
}

// initTables initializes the trailing rule table and indexes.
func (s *TrailingRuleStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'trailing_rules'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&TrailingRule{}); err != nil {
		return fmt.Errorf("failed to migrate trailing_rules table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *TrailingRuleStore) ensureIndexes() error {
	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_trailing_rules_trailing_rule_id ON trailing_rules(trailing_rule_id)`,
		`CREATE INDEX IF NOT EXISTS idx_trailing_rules_trader_symbol_side_status ON trailing_rules(trader_id, symbol, side, status)`,
		`CREATE INDEX IF NOT EXISTS idx_trailing_rules_linked_position_key ON trailing_rules(linked_position_key)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create trailing rule index: %w", err)
		}
	}
	return nil
}

// GetByKeys loads a trailing rule row by the strongest available keys.
func (s *TrailingRuleStore) GetByKeys(traderID, trailingRuleID, linkedPositionKey string) (*TrailingRule, error) {
	var row TrailingRule
	query := s.db.Where("trader_id = ?", traderID)
	switch {
	case trailingRuleID != "":
		query = query.Where("trailing_rule_id = ?", trailingRuleID)
	case linkedPositionKey != "":
		query = query.Where("linked_position_key = ?", linkedPositionKey)
	default:
		return nil, nil
	}

	err := query.First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get trailing rule row: %w", err)
	}
	return &row, nil
}

// ListByTraderID returns all trailing rule rows for a trader.
func (s *TrailingRuleStore) ListByTraderID(traderID string) ([]*TrailingRule, error) {
	var rows []*TrailingRule
	err := s.db.Where("trader_id = ?", traderID).
		Order("updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list trailing rules: %w", err)
	}
	return rows, nil
}

// ListByTraderSymbolSide returns all trailing rule rows for one trader/symbol/side scope.
func (s *TrailingRuleStore) ListByTraderSymbolSide(traderID, symbol, side string) ([]*TrailingRule, error) {
	var rows []*TrailingRule
	err := s.db.Where("trader_id = ? AND symbol = ? AND side = ?", traderID, normalizeAggregateSymbol(symbol), normalizeAggregateSide(side)).
		Order("updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list trailing rules for symbol: %w", err)
	}
	return rows, nil
}

// Upsert stores or updates a trailing rule row using the strongest available correlation keys.
func (s *TrailingRuleStore) Upsert(row *TrailingRule) error {
	if row == nil {
		return fmt.Errorf("trailing rule row is nil")
	}

	row.Symbol = normalizeAggregateSymbol(row.Symbol)
	row.Side = normalizeAggregateSide(row.Side)
	row.LinkedPositionKey = strings.TrimSpace(row.LinkedPositionKey)
	row.TrailingRuleID = strings.TrimSpace(row.TrailingRuleID)
	row.RuleMode = normalizeTrailingRuleMode(row.RuleMode)
	row.ActivationType = normalizeTrailingRuleActivationType(row.ActivationType)
	row.StepTriggerType = normalizeTrailingRuleStepType(row.StepTriggerType)
	row.StepMoveType = normalizeTrailingRuleStepMoveType(row.StepMoveType)
	row.Status = normalizeTrailingRuleStatus(row.Status)
	if row.Source == "" {
		row.Source = "unknown"
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		existing, err := s.findExisting(tx, row.TraderID, row.TrailingRuleID, row.LinkedPositionKey)
		if err != nil {
			return err
		}
		if existing != nil {
			row.ID = existing.ID
			row.CreatedAt = existing.CreatedAt
			return tx.Save(row).Error
		}
		return tx.Create(row).Error
	})
}

func (s *TrailingRuleStore) findExisting(tx *gorm.DB, traderID, trailingRuleID, linkedPositionKey string) (*TrailingRule, error) {
	var row TrailingRule
	query := tx.Where("trader_id = ?", traderID)
	switch {
	case trailingRuleID != "":
		query = query.Where("trailing_rule_id = ?", trailingRuleID)
	case linkedPositionKey != "":
		query = query.Where("linked_position_key = ?", linkedPositionKey)
	default:
		return nil, nil
	}

	err := query.First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find existing trailing rule row: %w", err)
	}
	return &row, nil
}

func normalizeTrailingRuleMode(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "", "break_even", "step_trailing":
		if normalized == "" {
			return "break_even"
		}
		return normalized
	default:
		return "break_even"
	}
}

func normalizeTrailingRuleActivationType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", "profit_r_multiple", "profit_pct", "price_level":
		if normalized == "" {
			return "profit_pct"
		}
		return normalized
	default:
		return "profit_pct"
	}
}

func normalizeTrailingRuleStepType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", "profit_r_multiple", "profit_pct", "price_level":
		if normalized == "" {
			return "profit_pct"
		}
		return normalized
	default:
		return "profit_pct"
	}
}

func normalizeTrailingRuleStepMoveType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", "price_offset", "price_pct", "price_level":
		if normalized == "" {
			return "price_offset"
		}
		return normalized
	default:
		return "price_offset"
	}
}

func normalizeTrailingRuleStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "", "draft", "armed", "paused", "invalid", "closed":
		if normalized == "" {
			return "draft"
		}
		return normalized
	default:
		return "draft"
	}
}
