package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ScaleInPlanStore stores the system-owned same-symbol add-position plan truth rows.
type ScaleInPlanStore struct {
	db *gorm.DB
}

// NewScaleInPlanStore creates a scale-in plan store.
func NewScaleInPlanStore(db *gorm.DB) *ScaleInPlanStore {
	return &ScaleInPlanStore{db: db}
}

// ScaleInPlan is the system-owned truth model for a same-symbol add-position plan.
// It is a plan-layer snapshot, not a raw exchange order mirror.
type ScaleInPlan struct {
	ID                  int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                                     // System persistence row id; not an execution identifier.
	TraderID            string    `gorm:"column:trader_id;not null;index:idx_scale_in_plans_trader_symbol_side_status,priority:1" json:"trader_id"`               // System-owned trader key.
	Symbol              string    `gorm:"column:symbol;not null;index:idx_scale_in_plans_trader_symbol_side_status,priority:2" json:"symbol"`                     // Exchange-normalized symbol in the scale-in scope.
	Side                string    `gorm:"column:side;not null;index:idx_scale_in_plans_trader_symbol_side_status,priority:3" json:"side"`                         // One-way side in the scale-in scope: LONG or SHORT.
	LinkedPositionKey   string    `gorm:"column:linked_position_key;not null;default:'';index:idx_scale_in_plans_linked_position_key" json:"linked_position_key"` // System-owned position correlation key.
	ScaleInPlanID       string    `gorm:"column:scale_in_plan_id;not null;default:'';uniqueIndex:idx_scale_in_plans_scale_in_plan_id" json:"scale_in_plan_id"`    // System-generated plan identifier.
	PlanMode            string    `gorm:"column:plan_mode;not null;default:fixed_qty" json:"plan_mode"`                                                           // Plan policy label; current scope is fixed_qty or fixed_ratio.
	Status              string    `gorm:"column:status;not null;default:draft;index:idx_scale_in_plans_trader_symbol_side_status,priority:4" json:"status"`       // Plan truth status: draft, armed, partially_filled, completed, invalid.
	TotalPlannedQty     float64   `gorm:"column:total_planned_qty;not null;default:0" json:"total_planned_qty"`                                                   // System-derived total quantity planned for the add-position plan.
	RemainingPlannedQty float64   `gorm:"column:remaining_planned_qty;not null;default:0" json:"remaining_planned_qty"`                                           // System-derived quantity still reserved by the current plan.
	ExecutedQty         float64   `gorm:"column:executed_qty;not null;default:0" json:"executed_qty"`                                                             // System-derived cumulative executed quantity across the plan.
	MaxScaleInCount     int       `gorm:"column:max_scale_in_count;not null;default:0" json:"max_scale_in_count"`                                                 // Strategy-layer cap for how many add attempts are allowed.
	CurrentScaleInCount int       `gorm:"column:current_scale_in_count;not null;default:0" json:"current_scale_in_count"`                                         // System-derived count of executed add attempts in the current plan snapshot.
	Source              string    `gorm:"column:source;not null;default:''" json:"source"`                                                                        // Truth source label: scale_in_manager, user_stream, exchange_snapshot, bootstrap.
	CreatedAt           time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                                     // Persistence timestamp.
	UpdatedAt           time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`                                                                     // Persistence timestamp.
}

// TableName returns the table name for ScaleInPlan.
func (ScaleInPlan) TableName() string {
	return "scale_in_plans"
}

// ScaleInStateMismatchReason is a machine-readable scale-in reconcile explanation.
// It is derived from the system truth layers, not from a raw exchange event.
type ScaleInStateMismatchReason struct {
	Category            string `json:"category"`                         // Mismatch bucket used by preview/debug tooling.
	Reason              string `json:"reason"`                           // Human-readable mismatch explanation.
	Symbol              string `json:"symbol,omitempty"`                 // Symbol scope for the mismatch.
	ScaleInPlanID       string `json:"scale_in_plan_id,omitempty"`       // Scale-in plan involved in the mismatch.
	LevelIndex          int    `json:"level_index,omitempty"`            // Scale-in level involved in the mismatch.
	LinkedPositionKey   string `json:"linked_position_key,omitempty"`    // System-owned position correlation key.
	LinkedOrderIntentID string `json:"linked_order_intent_id,omitempty"` // Local intent id involved in the mismatch.
	ExchangeOrderID     string `json:"exchange_order_id,omitempty"`      // Exchange order id when available.
	ClientOrderID       string `json:"client_order_id,omitempty"`        // Exchange client id when available.
}

// ScaleInPlanSummary is the system-derived rollup for one trader/symbol/side scale-in scope.
// It drives preview output, capability clipping, and position-aggregate add-position fields.
type ScaleInPlanSummary struct {
	TraderID            string                       `json:"trader_id"`              // System-owned trader key.
	Symbol              string                       `json:"symbol"`                 // Truth-layer symbol scope.
	Side                string                       `json:"side"`                   // Truth-layer one-way side scope.
	LinkedPositionKey   string                       `json:"linked_position_key"`    // System-owned position correlation key.
	ScaleInPlanID       string                       `json:"scale_in_plan_id"`       // System-generated scale-in plan identifier.
	PlanMode            string                       `json:"plan_mode"`              // Plan policy label.
	ScaleInStatus       string                       `json:"scale_in_status"`        // Current plan truth status.
	HasScaleInPlan      bool                         `json:"has_scale_in_plan"`      // System-derived flag that an active plan exists.
	TotalPlannedQty     float64                      `json:"total_planned_qty"`      // System-derived total quantity planned by the active plan.
	PendingScaleInQty   float64                      `json:"pending_scale_in_qty"`   // System-derived working-order reserve for active add levels; this is a subset of RemainingScaleInQty.
	RemainingScaleInQty float64                      `json:"remaining_scale_in_qty"` // System-derived total quantity still outstanding across the active plan, including working and not-yet-working levels.
	ExecutedScaleInQty  float64                      `json:"executed_scale_in_qty"`  // System-derived cumulative executed quantity.
	MaxScaleInCount     int                          `json:"max_scale_in_count"`     // Strategy-layer cap for how many add attempts are allowed.
	CurrentScaleInCount int                          `json:"current_scale_in_count"` // System-derived count of executed add attempts in the current plan snapshot.
	HasWorkingOrders    bool                         `json:"has_working_orders"`     // System-derived flag that at least one scale-in level is still working.
	WorkingLevelCount   int                          `json:"working_level_count"`    // System-derived count of working levels.
	ActiveLevelCount    int                          `json:"active_level_count"`     // System-derived count of non-terminal levels.
	CompletedLevelCount int                          `json:"completed_level_count"`  // System-derived count of terminal levels.
	ConsistencyStatus   string                       `json:"consistency_status"`     // Scale-in consistency enum: consistent, mismatch, pending, or unknown.
	HasStateMismatch    bool                         `json:"has_state_mismatch"`     // Reconcile result flag; true means truth layers disagree.
	MismatchReasons     []ScaleInStateMismatchReason `json:"mismatch_reasons"`       // Machine-readable mismatch explanation list.
	LastSyncedAt        time.Time                    `json:"last_synced_at"`         // Latest sync timestamp across the scale-in scope.
	ExecutionEligible   bool                         `json:"execution_eligible"`     // Runtime readiness flag; not a direct execution permit.
}

// initTables initializes the scale-in plan table and indexes.
func (s *ScaleInPlanStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'scale_in_plans'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&ScaleInPlan{}); err != nil {
		return fmt.Errorf("failed to migrate scale_in_plans table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *ScaleInPlanStore) ensureIndexes() error {
	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_scale_in_plans_scale_in_plan_id ON scale_in_plans(scale_in_plan_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_in_plans_trader_symbol_side_status ON scale_in_plans(trader_id, symbol, side, status)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_in_plans_linked_position_key ON scale_in_plans(linked_position_key)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create scale-in plan index: %w", err)
		}
	}
	return nil
}

// GetByKeys loads a scale-in plan row by the strongest available keys.
func (s *ScaleInPlanStore) GetByKeys(traderID, scaleInPlanID, linkedPositionKey string) (*ScaleInPlan, error) {
	var row ScaleInPlan
	query := s.db.Where("trader_id = ?", traderID)
	switch {
	case scaleInPlanID != "":
		query = query.Where("scale_in_plan_id = ?", scaleInPlanID)
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
		return nil, fmt.Errorf("failed to get scale-in plan row: %w", err)
	}
	return &row, nil
}

// ListByTraderID returns all scale-in plan rows for a trader ordered by the latest update.
func (s *ScaleInPlanStore) ListByTraderID(traderID string) ([]*ScaleInPlan, error) {
	var rows []*ScaleInPlan
	err := s.db.Where("trader_id = ?", traderID).
		Order("updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list scale-in plans: %w", err)
	}
	return rows, nil
}

// ListByTraderSymbolSide returns scale-in plan rows for one trader/symbol/side.
func (s *ScaleInPlanStore) ListByTraderSymbolSide(traderID, symbol, side string) ([]*ScaleInPlan, error) {
	var rows []*ScaleInPlan
	err := s.db.Where("trader_id = ? AND symbol = ? AND side = ?", traderID, normalizeAggregateSymbol(symbol), normalizeAggregateSide(side)).
		Order("updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list scale-in plans for symbol: %w", err)
	}
	return rows, nil
}

// Upsert stores or updates a scale-in plan row using the strongest available correlation keys.
func (s *ScaleInPlanStore) Upsert(row *ScaleInPlan) error {
	if row == nil {
		return fmt.Errorf("scale-in plan row is nil")
	}

	row.Symbol = normalizeAggregateSymbol(row.Symbol)
	row.Side = normalizeAggregateSide(row.Side)
	row.LinkedPositionKey = strings.TrimSpace(row.LinkedPositionKey)
	row.PlanMode = normalizeScaleInPlanMode(row.PlanMode)
	row.Status = normalizeScaleInPlanStatus(row.Status)
	if row.Source == "" {
		row.Source = "unknown"
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		existing, err := s.findExisting(tx, row.TraderID, row.ScaleInPlanID, row.LinkedPositionKey)
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

func (s *ScaleInPlanStore) findExisting(tx *gorm.DB, traderID, scaleInPlanID, linkedPositionKey string) (*ScaleInPlan, error) {
	var row ScaleInPlan
	query := tx.Where("trader_id = ?", traderID)
	switch {
	case scaleInPlanID != "":
		query = query.Where("scale_in_plan_id = ?", scaleInPlanID)
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
		return nil, fmt.Errorf("failed to find existing scale-in plan row: %w", err)
	}
	return &row, nil
}

// SummarizeForTraderSymbolSide returns the current scale-in summary for one trader/symbol/side scope.
func (s *ScaleInPlanStore) SummarizeForTraderSymbolSide(traderID, symbol, side string) (*ScaleInPlanSummary, error) {
	rows, err := s.ListByTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		return nil, err
	}
	return summarizeScaleInPlanRows(traderID, normalizeAggregateSymbol(symbol), normalizeAggregateSide(side), rows, NewScaleInPlanLevelStore(s.db))
}

// SummarizeForTraderSymbol returns the current scale-in summary for one trader/symbol scope.
func (s *ScaleInPlanStore) SummarizeForTraderSymbol(traderID, symbol string) (*ScaleInPlanSummary, error) {
	rows, err := s.ListByTraderID(traderID)
	if err != nil {
		return nil, err
	}
	filtered := make([]*ScaleInPlan, 0, len(rows))
	normalizedSymbol := normalizeAggregateSymbol(symbol)
	for _, row := range rows {
		if row == nil {
			continue
		}
		if normalizedSymbol == "" || strings.EqualFold(normalizeAggregateSymbol(row.Symbol), normalizedSymbol) {
			filtered = append(filtered, row)
		}
	}
	return summarizeScaleInPlanRows(traderID, normalizedSymbol, "", filtered, NewScaleInPlanLevelStore(s.db))
}

func summarizeScaleInPlanRows(traderID, symbol, side string, rows []*ScaleInPlan, levelStore *ScaleInPlanLevelStore) (*ScaleInPlanSummary, error) {
	summary := &ScaleInPlanSummary{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		MismatchReasons:   []ScaleInStateMismatchReason{},
		ConsistencyStatus: "unknown",
	}
	if len(rows) == 0 {
		return summary, nil
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i] == nil || rows[j] == nil {
			return rows[j] != nil
		}
		if rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
	})

	var target *ScaleInPlan
	for _, row := range rows {
		if row == nil {
			continue
		}
		if isActiveScaleInPlanStatus(row.Status) {
			target = row
			break
		}
	}
	if target == nil {
		target = rows[0]
	}
	if target == nil {
		return summary, nil
	}

	levels, err := levelStore.ListByPlanID(target.ScaleInPlanID)
	if err != nil {
		return nil, err
	}

	summary.ScaleInPlanID = target.ScaleInPlanID
	summary.PlanMode = target.PlanMode
	summary.ScaleInStatus = normalizeScaleInPlanStatus(target.Status)
	summary.HasScaleInPlan = isActiveScaleInPlanStatus(summary.ScaleInStatus)
	summary.LinkedPositionKey = target.LinkedPositionKey
	summary.MaxScaleInCount = target.MaxScaleInCount
	summary.CurrentScaleInCount = target.CurrentScaleInCount
	summary.LastSyncedAt = target.UpdatedAt
	derivedScaleInCount := 0

	for _, level := range levels {
		if level == nil {
			continue
		}
		summary.TotalPlannedQty += level.PlannedQty
		summary.ExecutedScaleInQty += level.ExecutedQty
		summary.RemainingScaleInQty += level.RemainingQty
		if level.ExecutedQty > 0 {
			derivedScaleInCount++
		}
		if isWorkingScaleInLevelStatus(level.Status) {
			summary.PendingScaleInQty += level.RemainingQty
			summary.HasWorkingOrders = true
			summary.WorkingLevelCount++
		}
		if isTerminalScaleInLevelStatus(level.Status) {
			summary.CompletedLevelCount++
		} else {
			summary.ActiveLevelCount++
		}
		if level.UpdatedAt.After(summary.LastSyncedAt) {
			summary.LastSyncedAt = level.UpdatedAt
		}
	}

	if derivedScaleInCount > summary.CurrentScaleInCount {
		summary.CurrentScaleInCount = derivedScaleInCount
	}
	if target.CurrentScaleInCount > 0 && derivedScaleInCount > 0 && target.CurrentScaleInCount != derivedScaleInCount {
		summary.ConsistencyStatus = "mismatch"
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ScaleInStateMismatchReason{
			Category:          "plan_state",
			Reason:            "stored scale-in count differs from derived executed levels",
			Symbol:            summary.Symbol,
			ScaleInPlanID:     summary.ScaleInPlanID,
			LinkedPositionKey: summary.LinkedPositionKey,
		})
	}

	if summary.RemainingScaleInQty <= 0 && summary.ExecutedScaleInQty > 0 {
		summary.ScaleInStatus = "completed"
		summary.HasScaleInPlan = false
	}

	if summary.ScaleInStatus == "completed" && summary.RemainingScaleInQty > 0 {
		summary.ConsistencyStatus = "mismatch"
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ScaleInStateMismatchReason{
			Category:          "plan_state",
			Reason:            "completed scale-in plan still has remaining quantity",
			Symbol:            summary.Symbol,
			ScaleInPlanID:     summary.ScaleInPlanID,
			LinkedPositionKey: summary.LinkedPositionKey,
		})
	}
	if !summary.HasScaleInPlan && summary.ScaleInStatus == "invalid" {
		summary.ConsistencyStatus = "mismatch"
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ScaleInStateMismatchReason{
			Category:          "plan_state",
			Reason:            "scale-in plan is invalid",
			Symbol:            summary.Symbol,
			ScaleInPlanID:     summary.ScaleInPlanID,
			LinkedPositionKey: summary.LinkedPositionKey,
		})
	}

	if len(summary.MismatchReasons) > 0 {
		summary.ConsistencyStatus = "mismatch"
	} else if summary.ScaleInStatus == "completed" {
		summary.ConsistencyStatus = "consistent"
	} else if summary.ScaleInStatus == "invalid" {
		summary.ConsistencyStatus = "mismatch"
		summary.HasStateMismatch = true
	} else if summary.HasScaleInPlan || summary.ScaleInStatus == "draft" || summary.WorkingLevelCount > 0 || summary.PendingScaleInQty > 0 || summary.ExecutedScaleInQty > 0 {
		summary.ConsistencyStatus = "pending"
	}

	summary.ConsistencyStatus = normalizeScaleInConsistencyStatus(summary.ConsistencyStatus)
	summary.ExecutionEligible = summary.HasScaleInPlan && summary.ConsistencyStatus != "mismatch"
	return summary, nil
}

func normalizeScaleInPlanMode(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "", "fixed_qty", "fixed_ratio":
		if normalized == "" {
			return "fixed_qty"
		}
		return normalized
	default:
		return normalized
	}
}

func normalizeScaleInPlanStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "", "draft":
		return "draft"
	case "armed", "partially_filled", "completed", "invalid":
		return normalized
	default:
		return "invalid"
	}
}

func normalizeScaleInConsistencyStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "consistent", "mismatch", "pending", "unknown":
		return normalized
	default:
		return "unknown"
	}
}

func isActiveScaleInPlanStatus(status string) bool {
	switch normalizeScaleInPlanStatus(status) {
	case "draft", "armed", "partially_filled":
		return true
	default:
		return false
	}
}

// NormalizeScaleInPlanStatus exposes the scale-in plan status normalizer for service-layer reconciliation logic.
func NormalizeScaleInPlanStatus(status string) string {
	return normalizeScaleInPlanStatus(status)
}

// NormalizeScaleInPlanMode exposes the scale-in plan mode normalizer for service-layer reconciliation logic.
func NormalizeScaleInPlanMode(mode string) string {
	return normalizeScaleInPlanMode(mode)
}

// NormalizeScaleInConsistencyStatus exposes the scale-in consistency normalizer for service-layer reconciliation logic.
func NormalizeScaleInConsistencyStatus(status string) string {
	return normalizeScaleInConsistencyStatus(status)
}

func isWorkingScaleInLevelStatus(status string) bool {
	switch normalizeScaleInLevelStatus(status) {
	case "armed", "partially_filled":
		return true
	default:
		return false
	}
}

func isTerminalScaleInLevelStatus(status string) bool {
	switch normalizeScaleInLevelStatus(status) {
	case "filled", "cancelled", "invalid":
		return true
	default:
		return false
	}
}
