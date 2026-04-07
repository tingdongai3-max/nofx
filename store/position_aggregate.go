package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// PositionAggregateStore stores the current system-owned position truth rows.
type PositionAggregateStore struct {
	db *gorm.DB
}

// NewPositionAggregateStore creates a position aggregate store.
func NewPositionAggregateStore(db *gorm.DB) *PositionAggregateStore {
	return &PositionAggregateStore{db: db}
}

// PositionAggregate is the system-owned truth model for a trader's current net position.
// It is not a raw exchange mirror table: the fields below are system-derived reconciliation results.
type PositionAggregate struct {
	ID                   int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	TraderID             string    `gorm:"column:trader_id;not null;index:idx_position_aggregates_trader_symbol_side,priority:1" json:"trader_id"` // System-owned trader key.
	Symbol               string    `gorm:"column:symbol;not null;index:idx_position_aggregates_trader_symbol_side,priority:2" json:"symbol"`       // Exchange-normalized symbol from the open-position truth set.
	Side                 string    `gorm:"column:side;not null;index:idx_position_aggregates_trader_symbol_side,priority:3" json:"side"`           // Normalized one-way side: LONG or SHORT.
	TotalQty             float64   `gorm:"column:total_qty;not null;default:0" json:"total_qty"`                                                   // System-derived current net open quantity.
	AvailableQty         float64   `gorm:"column:available_qty;not null;default:0" json:"available_qty"`                                           // System-derived quantity still available after reserved reduce exposure and scale-out plan exposure are accounted for centrally.
	PendingAddQty        float64   `gorm:"column:pending_add_qty;not null;default:0" json:"pending_add_qty"`                                       // System-derived quantity reserved by same-symbol add orders from the scale-in truth layer and working entry rows; this is not the net position size.
	PendingReduceQty     float64   `gorm:"column:pending_reduce_qty;not null;default:0" json:"pending_reduce_qty"`                                 // System-derived order-layer reduce reserve, including working scale-out reduce legs and fixed-protection legs; not the remaining position size.
	AvgEntryPrice        float64   `gorm:"column:avg_entry_price;not null;default:0" json:"avg_entry_price"`                                       // System-derived weighted average entry price.
	TotalNotional        float64   `gorm:"column:total_notional;not null;default:0" json:"total_notional"`                                         // System-derived current notional value (total_qty × avg_entry_price); used for preview/risk display only.
	RealizedPnL          float64   `gorm:"column:realized_pnl;not null;default:0" json:"realized_pnl"`                                             // System-derived cumulative realized PnL.
	UnrealizedPnL        float64   `gorm:"column:unrealized_pnl;not null;default:0" json:"unrealized_pnl"`                                         // System-derived current unrealized PnL from live snapshots or best-effort reconciliation.
	PeakPnLPct           float64   `gorm:"column:peak_pnl_pct;not null;default:0" json:"peak_pnl_pct"`                                             // System-derived peak PnL percent from the live cache or a best-effort fallback.
	HasProtection        bool      `gorm:"column:has_protection;not null;default:false" json:"has_protection"`                                     // System-derived flag from the protection-group truth layer; not a direct execution permit.
	ProtectionMode       string    `gorm:"column:protection_mode;not null;default:''" json:"protection_mode"`                                      // System-derived protection policy label from the protection-group truth layer.
	ProtectedQuantity    float64   `gorm:"column:protected_quantity;not null;default:0" json:"protected_quantity"`                                 // System-derived protection-layer coverage target for the live position; this tracks covered size, not order reservation size.
	ProtectionGroupID    string    `gorm:"column:protection_group_id;not null;default:''" json:"protection_group_id"`                              // System-derived current protection group identifier.
	StopLossArmed        bool      `gorm:"column:stop_loss_armed;not null;default:false" json:"stop_loss_armed"`                                   // System-derived flag from the protection-group truth layer.
	TakeProfitArmed      bool      `gorm:"column:take_profit_armed;not null;default:false" json:"take_profit_armed"`                               // System-derived flag from the protection-group truth layer.
	CurrentStopLossPrice float64   `gorm:"column:current_stop_loss_price;not null;default:0" json:"current_stop_loss_price"`                       // System-derived current stop-loss trigger price from the protection truth layer.
	InitialStopLossPrice float64   `gorm:"column:initial_stop_loss_price;not null;default:0" json:"initial_stop_loss_price"`                       // System-derived initial stop-loss trigger price before any movement.
	BreakEvenArmed       bool      `gorm:"column:break_even_armed;not null;default:false" json:"break_even_armed"`                                 // System-derived flag showing that the protection has armed break-even.
	TrailingArmed        bool      `gorm:"column:trailing_armed;not null;default:false" json:"trailing_armed"`                                     // System-derived flag showing that the protection has entered segmented trailing.
	TrailingRuleID       string    `gorm:"column:trailing_rule_id;not null;default:''" json:"trailing_rule_id"`                                    // System-derived trailing rule identifier.
	ProtectionRevision   int       `gorm:"column:protection_revision;not null;default:0" json:"protection_revision"`                               // System-derived revision counter for the protection truth layer.
	LastProtectionMoveAt time.Time `gorm:"column:last_protection_move_at" json:"last_protection_move_at"`                                          // System-derived timestamp of the latest protection move.
	ProtectionMoveCount  int       `gorm:"column:protection_move_count;not null;default:0" json:"protection_move_count"`                           // System-derived count of stop-loss movement events, including break-even and segmented trailing steps.
	ScaleOutPlanID       string    `gorm:"column:scale_out_plan_id;not null;default:''" json:"scale_out_plan_id"`                                  // System-derived active scale-out plan identifier.
	HasScaleOutPlan      bool      `gorm:"column:has_scale_out_plan;not null;default:false" json:"has_scale_out_plan"`                             // System-derived flag from the scale-out plan truth layer.
	ScaleOutStatus       string    `gorm:"column:scale_out_status;not null;default:''" json:"scale_out_status"`                                    // System-derived scale-out plan status.
	PendingScaleOutQty   float64   `gorm:"column:pending_scale_out_qty;not null;default:0" json:"pending_scale_out_qty"`                           // System-derived working-order reserve for the active scale-out plan; this is a subset of RemainingScaleOutQty.
	ExecutedScaleOutQty  float64   `gorm:"column:executed_scale_out_qty;not null;default:0" json:"executed_scale_out_qty"`                         // System-derived executed quantity from the scale-out plan.
	RemainingScaleOutQty float64   `gorm:"column:remaining_scale_out_qty;not null;default:0" json:"remaining_scale_out_qty"`                       // System-derived plan-layer quantity still outstanding across the whole scale-out plan, including working and not-yet-working levels.
	ScaleInPlanID        string    `gorm:"column:scale_in_plan_id;not null;default:''" json:"scale_in_plan_id"`                                    // System-derived active scale-in plan identifier.
	HasScaleInPlan       bool      `gorm:"column:has_scale_in_plan;not null;default:false" json:"has_scale_in_plan"`                               // System-derived flag from the scale-in plan truth layer.
	ScaleInStatus        string    `gorm:"column:scale_in_status;not null;default:''" json:"scale_in_status"`                                      // System-derived scale-in plan status.
	ExecutedScaleInQty   float64   `gorm:"column:executed_scale_in_qty;not null;default:0" json:"executed_scale_in_qty"`                           // System-derived executed quantity from the scale-in plan.
	RemainingScaleInQty  float64   `gorm:"column:remaining_scale_in_qty;not null;default:0" json:"remaining_scale_in_qty"`                         // System-derived plan-layer quantity still outstanding across the whole scale-in plan, including working and not-yet-working levels.
	ScaleInCount         int       `gorm:"column:scale_in_count;not null;default:0" json:"scale_in_count"`                                         // System-derived executed add-attempt count from the scale-in plan truth layer.
	ProtectionStateJSON  string    `gorm:"column:protection_state_json;type:text;default:''" json:"protection_state_json"`                         // System-derived JSON blob describing current protection state.
	ScalePlanStateJSON   string    `gorm:"column:scale_plan_state_json;type:text;default:''" json:"scale_plan_state_json"`                         // System-derived JSON blob describing combined scale-in / scale-out plan state.
	ExecutionEligible    bool      `gorm:"column:execution_eligible;not null;default:false" json:"execution_eligible"`                             // System-derived runtime eligibility flag; not a direct order permit.
	LastReconciledAt     time.Time `gorm:"column:last_reconciled_at" json:"last_reconciled_at"`                                                    // System-derived timestamp when the aggregate was last rebuilt.
	CreatedAt            time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt            time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

// TableName returns the table name for PositionAggregate.
func (PositionAggregate) TableName() string {
	return "position_aggregates"
}

// initTables initializes the position aggregate table and indexes.
func (s *PositionAggregateStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'position_aggregates'`).Scan(&tableExists)
		if tableExists > 0 {
			s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_position_aggregates_trader_symbol_side ON position_aggregates(trader_id, symbol, side)`)
			return nil
		}
	}

	if err := s.db.AutoMigrate(&PositionAggregate{}); err != nil {
		return fmt.Errorf("failed to migrate position_aggregates table: %w", err)
	}

	if err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_position_aggregates_trader_symbol_side ON position_aggregates(trader_id, symbol, side)`).Error; err != nil {
		return fmt.Errorf("failed to create position aggregate index: %w", err)
	}

	return nil
}

// ListByTraderID returns all persisted position aggregates for a trader.
func (s *PositionAggregateStore) ListByTraderID(traderID string) ([]*PositionAggregate, error) {
	var aggregates []*PositionAggregate
	err := s.db.Where("trader_id = ?", traderID).
		Order("symbol ASC, side ASC").
		Find(&aggregates).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list position aggregates: %w", err)
	}
	return aggregates, nil
}

// GetByTraderSymbolSide returns a single persisted position aggregate.
func (s *PositionAggregateStore) GetByTraderSymbolSide(traderID, symbol, side string) (*PositionAggregate, error) {
	var aggregate PositionAggregate
	err := s.db.Where("trader_id = ? AND symbol = ? AND side = ?", traderID, normalizeAggregateSymbol(symbol), normalizeAggregateSide(side)).
		First(&aggregate).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get position aggregate: %w", err)
	}
	return &aggregate, nil
}

// ReplaceForTrader replaces all persisted aggregates for a trader with the provided set.
// This keeps the table as the current truth snapshot instead of a history log.
func (s *PositionAggregateStore) ReplaceForTrader(traderID string, aggregates []*PositionAggregate) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("trader_id = ?", traderID).Delete(&PositionAggregate{}).Error; err != nil {
			return fmt.Errorf("failed to clear existing position aggregates: %w", err)
		}
		if len(aggregates) == 0 {
			return nil
		}
		if err := tx.CreateInBatches(aggregates, len(aggregates)).Error; err != nil {
			return fmt.Errorf("failed to store position aggregates: %w", err)
		}
		return nil
	})
}

func normalizeAggregateSide(side string) string {
	normalized := strings.ToUpper(strings.TrimSpace(side))
	switch normalized {
	case "BUY", "LONG":
		return "LONG"
	case "SELL", "SHORT":
		return "SHORT"
	default:
		return normalized
	}
}
