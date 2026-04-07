package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ScaleOutPlanLevelStore stores the system-owned scale-out plan levels.
type ScaleOutPlanLevelStore struct {
	db *gorm.DB
}

// NewScaleOutPlanLevelStore creates a scale-out plan level store.
func NewScaleOutPlanLevelStore(db *gorm.DB) *ScaleOutPlanLevelStore {
	return &ScaleOutPlanLevelStore{db: db}
}

// ScaleOutPlanLevel is the system-owned truth model for one scale-out plan level.
// It is a plan-level snapshot, not a raw exchange order mirror.
type ScaleOutPlanLevel struct {
	ID                  int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                                         // System persistence row id; not an execution identifier.
	TraderID            string    `gorm:"column:trader_id;not null;index:idx_scale_out_plan_levels_trader_symbol_side_status,priority:1" json:"trader_id"`              // System-owned trader key.
	Symbol              string    `gorm:"column:symbol;not null;index:idx_scale_out_plan_levels_trader_symbol_side_status,priority:2" json:"symbol"`                    // Exchange-normalized symbol in the scale-out scope.
	Side                string    `gorm:"column:side;not null;index:idx_scale_out_plan_levels_trader_symbol_side_status,priority:3" json:"side"`                       // One-way side in the scale-out scope: LONG or SHORT.
	LinkedPositionKey   string    `gorm:"column:linked_position_key;not null;default:'';index:idx_scale_out_plan_levels_linked_position_key" json:"linked_position_key"` // System-owned position correlation key.
	ScaleOutPlanID      string    `gorm:"column:scale_out_plan_id;not null;default:'';index:idx_scale_out_plan_levels_scale_out_plan_id;index:idx_scale_out_plan_levels_trader_symbol_side_status,priority:4" json:"scale_out_plan_id"` // System-generated plan identifier.
	LevelIndex          int       `gorm:"column:level_index;not null;default:0;index:idx_scale_out_plan_levels_plan_level,priority:2" json:"level_index"`                 // System-owned level index within the plan.
	TargetType          string    `gorm:"column:target_type;not null;default:limit_price" json:"target_type"`                                                          // Level target type: limit_price or reduce_market.
	TargetPrice         float64   `gorm:"column:target_price;not null;default:0" json:"target_price"`                                                                // System-derived target price for limit-price levels.
	PlannedQty          float64   `gorm:"column:planned_qty;not null;default:0" json:"planned_qty"`                                                                  // System-derived quantity planned for the level.
	ExecutedQty         float64   `gorm:"column:executed_qty;not null;default:0" json:"executed_qty"`                                                                // System-derived cumulative executed quantity for the level.
	RemainingQty        float64   `gorm:"column:remaining_qty;not null;default:0" json:"remaining_qty"`                                                              // System-derived remaining quantity for the level.
	LinkedOrderIntentID  string    `gorm:"column:linked_order_intent_id;not null;default:'';index:idx_scale_out_plan_levels_linked_order_intent_id" json:"linked_order_intent_id"`       // System-local order intent id used for correlation.
	LinkedExchangeOrderID string  `gorm:"column:linked_exchange_order_id;not null;default:'';index:idx_scale_out_plan_levels_linked_exchange_order_id" json:"linked_exchange_order_id"` // Exchange raw order id when available.
	Status              string    `gorm:"column:status;not null;default:draft;index:idx_scale_out_plan_levels_trader_symbol_side_status,priority:5" json:"status"`       // Level truth status: draft, armed, partially_filled, filled, cancelled, invalid.
	CreatedAt           time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                                       // Persistence timestamp.
	UpdatedAt           time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`                                                                       // Persistence timestamp.
}

// TableName returns the table name for ScaleOutPlanLevel.
func (ScaleOutPlanLevel) TableName() string {
	return "scale_out_plan_levels"
}

// initTables initializes the scale-out plan level table and indexes.
func (s *ScaleOutPlanLevelStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'scale_out_plan_levels'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&ScaleOutPlanLevel{}); err != nil {
		return fmt.Errorf("failed to migrate scale_out_plan_levels table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *ScaleOutPlanLevelStore) ensureIndexes() error {
	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_scale_out_plan_levels_plan_level ON scale_out_plan_levels(scale_out_plan_id, level_index)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_out_plan_levels_trader_symbol_side_status ON scale_out_plan_levels(trader_id, symbol, side, status)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_out_plan_levels_scale_out_plan_id ON scale_out_plan_levels(scale_out_plan_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_out_plan_levels_linked_position_key ON scale_out_plan_levels(linked_position_key)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_out_plan_levels_linked_order_intent_id ON scale_out_plan_levels(linked_order_intent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_out_plan_levels_linked_exchange_order_id ON scale_out_plan_levels(linked_exchange_order_id)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create scale-out plan level index: %w", err)
		}
	}
	return nil
}

// GetByPlanIDAndLevelIndex returns a single level row for a plan and level index.
func (s *ScaleOutPlanLevelStore) GetByPlanIDAndLevelIndex(planID string, levelIndex int) (*ScaleOutPlanLevel, error) {
	var row ScaleOutPlanLevel
	err := s.db.Where("scale_out_plan_id = ? AND level_index = ?", planID, levelIndex).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get scale-out plan level row: %w", err)
	}
	return &row, nil
}

// ListByPlanID returns all scale-out levels for one plan ordered by the level index.
func (s *ScaleOutPlanLevelStore) ListByPlanID(planID string) ([]*ScaleOutPlanLevel, error) {
	var rows []*ScaleOutPlanLevel
	err := s.db.Where("scale_out_plan_id = ?", planID).
		Order("level_index ASC, updated_at ASC, created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list scale-out plan levels: %w", err)
	}
	return rows, nil
}

// ListByTraderSymbolSide returns scale-out levels for one trader/symbol/side scope.
func (s *ScaleOutPlanLevelStore) ListByTraderSymbolSide(traderID, symbol, side string) ([]*ScaleOutPlanLevel, error) {
	var rows []*ScaleOutPlanLevel
	err := s.db.Where("trader_id = ? AND symbol = ? AND side = ?", traderID, normalizeAggregateSymbol(symbol), normalizeAggregateSide(side)).
		Order("updated_at DESC, created_at DESC, level_index ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list scale-out levels for symbol: %w", err)
	}
	return rows, nil
}

// Upsert stores or updates a level row using the strongest available correlation keys.
func (s *ScaleOutPlanLevelStore) Upsert(row *ScaleOutPlanLevel) error {
	if row == nil {
		return fmt.Errorf("scale-out plan level row is nil")
	}

	row.Symbol = normalizeAggregateSymbol(row.Symbol)
	row.Side = normalizeAggregateSide(row.Side)
	row.LinkedPositionKey = strings.TrimSpace(row.LinkedPositionKey)
	row.ScaleOutPlanID = strings.TrimSpace(row.ScaleOutPlanID)
	row.TargetType = normalizeScaleOutTargetType(row.TargetType)
	row.Status = normalizeScaleOutLevelStatus(row.Status)
	if row.LinkedOrderIntentID == "" {
		row.LinkedOrderIntentID = chooseScaleOutString(row.LinkedOrderIntentID, row.ScaleOutPlanID+"-"+fmt.Sprint(row.LevelIndex))
	}
	if row.RemainingQty < 0 {
		row.RemainingQty = 0
	}
	if row.ExecutedQty < 0 {
		row.ExecutedQty = 0
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		existing, err := s.findExisting(tx, row.TraderID, row.ScaleOutPlanID, row.LevelIndex, row.LinkedOrderIntentID, row.LinkedExchangeOrderID)
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

func (s *ScaleOutPlanLevelStore) findExisting(tx *gorm.DB, traderID, planID string, levelIndex int, linkedOrderIntentID, linkedExchangeOrderID string) (*ScaleOutPlanLevel, error) {
	var row ScaleOutPlanLevel
	queries := []*gorm.DB{
		tx.Where("trader_id = ? AND scale_out_plan_id = ? AND level_index = ?", traderID, planID, levelIndex),
	}
	if linkedOrderIntentID != "" {
		queries = append(queries, tx.Where("trader_id = ? AND scale_out_plan_id = ? AND linked_order_intent_id = ?", traderID, planID, linkedOrderIntentID))
	}
	if linkedExchangeOrderID != "" {
		queries = append(queries, tx.Where("trader_id = ? AND scale_out_plan_id = ? AND linked_exchange_order_id = ?", traderID, planID, linkedExchangeOrderID))
	}

	for _, query := range queries {
		err := query.First(&row).Error
		if err == nil {
			return &row, nil
		}
		if err != gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("failed to find existing scale-out plan level row: %w", err)
		}
	}
	return nil, nil
}

func normalizeScaleOutTargetType(targetType string) string {
	normalized := strings.ToLower(strings.TrimSpace(targetType))
	switch normalized {
	case "", "limit_price", "reduce_market":
		if normalized == "" {
			return "limit_price"
		}
		return normalized
	default:
		return "limit_price"
	}
}

func normalizeScaleOutLevelStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "", "draft":
		return "draft"
	case "armed", "new", "working", "open", "pending_new", "pending_replace":
		return "armed"
	case "partially_filled", "filled", "cancelled", "invalid":
		return normalized
	default:
		return "invalid"
	}
}
