package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ScaleOutPlanStore stores the system-owned partial reduce / batch take-profit plan truth rows.
type ScaleOutPlanStore struct {
	db *gorm.DB
}

// NewScaleOutPlanStore creates a scale-out plan store.
func NewScaleOutPlanStore(db *gorm.DB) *ScaleOutPlanStore {
	return &ScaleOutPlanStore{db: db}
}

// ScaleOutPlan is the system-owned truth model for a partial reduce / batch take-profit plan.
// It is a plan-layer snapshot, not a raw exchange order mirror.
type ScaleOutPlan struct {
	ID                  int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                                         // System persistence row id; not an execution identifier.
	TraderID            string    `gorm:"column:trader_id;not null;index:idx_scale_out_plans_trader_symbol_side_status,priority:1" json:"trader_id"`              // System-owned trader key.
	Symbol              string    `gorm:"column:symbol;not null;index:idx_scale_out_plans_trader_symbol_side_status,priority:2" json:"symbol"`                    // Exchange-normalized symbol in the scale-out scope.
	Side                string    `gorm:"column:side;not null;index:idx_scale_out_plans_trader_symbol_side_status,priority:3" json:"side"`                       // One-way side in the scale-out scope: LONG or SHORT.
	LinkedPositionKey   string    `gorm:"column:linked_position_key;not null;default:'';index:idx_scale_out_plans_linked_position_key" json:"linked_position_key"` // System-owned position correlation key.
	ScaleOutPlanID      string    `gorm:"column:scale_out_plan_id;not null;default:'';uniqueIndex:idx_scale_out_plans_scale_out_plan_id" json:"scale_out_plan_id"` // System-generated plan identifier.
	PlanMode            string    `gorm:"column:plan_mode;not null;default:fixed_ratio" json:"plan_mode"`                                                          // Plan policy label; current scope is fixed_ratio only.
	Status              string    `gorm:"column:status;not null;default:draft;index:idx_scale_out_plans_trader_symbol_side_status,priority:4" json:"status"`       // Plan truth status: draft, armed, partially_filled, completed, invalid.
	TotalPlannedQty     float64   `gorm:"column:total_planned_qty;not null;default:0" json:"total_planned_qty"`                                                     // System-derived total quantity planned for the scale-out.
	RemainingPlannedQty float64   `gorm:"column:remaining_planned_qty;not null;default:0" json:"remaining_planned_qty"`                                             // System-derived quantity still reserved by the current plan.
	ExecutedQty         float64   `gorm:"column:executed_qty;not null;default:0" json:"executed_qty"`                                                               // System-derived cumulative executed quantity across the plan.
	Source              string    `gorm:"column:source;not null;default:''" json:"source"`                                                                         // Truth source label: scale_out_manager, user_stream, exchange_snapshot, bootstrap.
	CreatedAt           time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                                       // Persistence timestamp.
	UpdatedAt           time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`                                                                       // Persistence timestamp.
}

// TableName returns the table name for ScaleOutPlan.
func (ScaleOutPlan) TableName() string {
	return "scale_out_plans"
}

// ScaleOutStateMismatchReason is a machine-readable scale-out reconcile explanation.
// It is derived from the system truth layers, not from a raw exchange event.
type ScaleOutStateMismatchReason struct {
	Category          string `json:"category"`                    // Mismatch bucket used by preview/debug tooling.
	Reason            string `json:"reason"`                      // Human-readable mismatch explanation.
	Symbol            string `json:"symbol,omitempty"`            // Symbol scope for the mismatch.
	ScaleOutPlanID    string `json:"scale_out_plan_id,omitempty"` // Scale-out plan involved in the mismatch.
	LevelIndex        int    `json:"level_index,omitempty"`       // Scale-out level involved in the mismatch.
	LinkedPositionKey string `json:"linked_position_key,omitempty"` // System-owned position correlation key.
	LinkedOrderIntentID string `json:"linked_order_intent_id,omitempty"` // Local intent id involved in the mismatch.
	ExchangeOrderID   string `json:"exchange_order_id,omitempty"` // Exchange order id when available.
	ClientOrderID     string `json:"client_order_id,omitempty"`   // Exchange client id when available.
}

// ScaleOutPlanSummary is the system-derived rollup for a trader/symbol/side scale-out scope.
// It drives preview output, capability clipping, and position-aggregate scale-out fields.
type ScaleOutPlanSummary struct {
	TraderID            string                      `json:"trader_id"`              // System-owned trader key.
	Symbol              string                      `json:"symbol"`                 // Truth-layer symbol scope.
	Side                string                      `json:"side"`                   // Truth-layer one-way side scope.
	LinkedPositionKey   string                      `json:"linked_position_key"`    // System-owned position correlation key.
	ScaleOutPlanID      string                      `json:"scale_out_plan_id"`      // System-generated scale-out plan identifier.
	PlanMode            string                      `json:"plan_mode"`              // Plan policy label.
	ScaleOutStatus      string                      `json:"scale_out_status"`       // Current plan truth status.
	HasScaleOutPlan     bool                        `json:"has_scale_out_plan"`     // System-derived flag that an active plan exists.
	TotalPlannedQty     float64                     `json:"total_planned_qty"`      // System-derived total quantity planned by the active plan.
	PendingScaleOutQty  float64                     `json:"pending_scale_out_qty"`  // System-derived working-order reserve for active scale-out levels; this is a subset of RemainingScaleOutQty.
	RemainingScaleOutQty float64                     `json:"remaining_scale_out_qty"` // System-derived total quantity still outstanding across the active plan, including working and not-yet-working levels.
	ExecutedScaleOutQty float64                     `json:"executed_scale_out_qty"`  // System-derived cumulative executed quantity.
	HasWorkingOrders    bool                        `json:"has_working_orders"`     // System-derived flag that at least one scale-out level is still working.
	WorkingLevelCount   int                         `json:"working_level_count"`    // System-derived count of working levels.
	ActiveLevelCount     int                         `json:"active_level_count"`     // System-derived count of non-terminal levels.
	CompletedLevelCount  int                         `json:"completed_level_count"`  // System-derived count of terminal levels.
	ConsistencyStatus   string                      `json:"consistency_status"`     // Scale-out consistency enum: consistent, mismatch, pending, or unknown.
	HasStateMismatch    bool                        `json:"has_state_mismatch"`     // Reconcile result flag; true means truth layers disagree.
	MismatchReasons     []ScaleOutStateMismatchReason `json:"mismatch_reasons"`       // Machine-readable mismatch explanation list.
	LastSyncedAt        time.Time                   `json:"last_synced_at"`         // Latest sync timestamp across the scale-out scope.
	ExecutionEligible   bool                        `json:"execution_eligible"`     // Runtime readiness flag; not a direct execution permit.
}

// initTables initializes the scale-out plan table and indexes.
func (s *ScaleOutPlanStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'scale_out_plans'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&ScaleOutPlan{}); err != nil {
		return fmt.Errorf("failed to migrate scale_out_plans table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *ScaleOutPlanStore) ensureIndexes() error {
	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_scale_out_plans_scale_out_plan_id ON scale_out_plans(scale_out_plan_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_out_plans_trader_symbol_side_status ON scale_out_plans(trader_id, symbol, side, status)`,
		`CREATE INDEX IF NOT EXISTS idx_scale_out_plans_linked_position_key ON scale_out_plans(linked_position_key)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create scale-out plan index: %w", err)
		}
	}
	return nil
}

// GetByKeys loads a scale-out plan row by the strongest available keys.
func (s *ScaleOutPlanStore) GetByKeys(traderID, scaleOutPlanID, linkedPositionKey string) (*ScaleOutPlan, error) {
	var row ScaleOutPlan
	query := s.db.Where("trader_id = ?", traderID)
	switch {
	case scaleOutPlanID != "":
		query = query.Where("scale_out_plan_id = ?", scaleOutPlanID)
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
		return nil, fmt.Errorf("failed to get scale-out plan row: %w", err)
	}
	return &row, nil
}

// ListByTraderID returns all scale-out plan rows for a trader ordered by the latest update.
func (s *ScaleOutPlanStore) ListByTraderID(traderID string) ([]*ScaleOutPlan, error) {
	var rows []*ScaleOutPlan
	err := s.db.Where("trader_id = ?", traderID).
		Order("updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list scale-out plans: %w", err)
	}
	return rows, nil
}

// ListByTraderSymbolSide returns scale-out plan rows for one trader/symbol/side.
func (s *ScaleOutPlanStore) ListByTraderSymbolSide(traderID, symbol, side string) ([]*ScaleOutPlan, error) {
	var rows []*ScaleOutPlan
	err := s.db.Where("trader_id = ? AND symbol = ? AND side = ?", traderID, normalizeAggregateSymbol(symbol), normalizeAggregateSide(side)).
		Order("updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list scale-out plans for symbol: %w", err)
	}
	return rows, nil
}

// Upsert stores or updates a scale-out plan row using the strongest available correlation keys.
func (s *ScaleOutPlanStore) Upsert(row *ScaleOutPlan) error {
	if row == nil {
		return fmt.Errorf("scale-out plan row is nil")
	}

	row.Symbol = normalizeAggregateSymbol(row.Symbol)
	row.Side = normalizeAggregateSide(row.Side)
	row.LinkedPositionKey = strings.TrimSpace(row.LinkedPositionKey)
	row.PlanMode = normalizeScaleOutPlanMode(row.PlanMode)
	row.Status = normalizeScaleOutPlanStatus(row.Status)
	if row.Source == "" {
		row.Source = "unknown"
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		existing, err := s.findExisting(tx, row.TraderID, row.ScaleOutPlanID, row.LinkedPositionKey)
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

func (s *ScaleOutPlanStore) findExisting(tx *gorm.DB, traderID, scaleOutPlanID, linkedPositionKey string) (*ScaleOutPlan, error) {
	var row ScaleOutPlan
	query := tx.Where("trader_id = ?", traderID)
	switch {
	case scaleOutPlanID != "":
		query = query.Where("scale_out_plan_id = ?", scaleOutPlanID)
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
		return nil, fmt.Errorf("failed to find existing scale-out plan row: %w", err)
	}
	return &row, nil
}

// SummarizeForTraderSymbolSide returns the current scale-out summary for one trader/symbol/side scope.
func (s *ScaleOutPlanStore) SummarizeForTraderSymbolSide(traderID, symbol, side string) (*ScaleOutPlanSummary, error) {
	rows, err := s.ListByTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		return nil, err
	}
	return summarizeScaleOutPlanRows(traderID, normalizeAggregateSymbol(symbol), normalizeAggregateSide(side), rows, NewScaleOutPlanLevelStore(s.db))
}

// SummarizeForTraderSymbol returns the current scale-out summary for one trader/symbol scope.
func (s *ScaleOutPlanStore) SummarizeForTraderSymbol(traderID, symbol string) (*ScaleOutPlanSummary, error) {
	rows, err := s.ListByTraderID(traderID)
	if err != nil {
		return nil, err
	}
	filtered := make([]*ScaleOutPlan, 0, len(rows))
	normalizedSymbol := normalizeAggregateSymbol(symbol)
	for _, row := range rows {
		if row == nil {
			continue
		}
		if normalizedSymbol == "" || strings.EqualFold(normalizeAggregateSymbol(row.Symbol), normalizedSymbol) {
			filtered = append(filtered, row)
		}
	}
	return summarizeScaleOutPlanRows(traderID, normalizedSymbol, "", filtered, NewScaleOutPlanLevelStore(s.db))
}

func summarizeScaleOutPlanRows(traderID, symbol, side string, rows []*ScaleOutPlan, levelStore *ScaleOutPlanLevelStore) (*ScaleOutPlanSummary, error) {
	summary := &ScaleOutPlanSummary{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              side,
		MismatchReasons:   []ScaleOutStateMismatchReason{},
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

	var target *ScaleOutPlan
	for _, row := range rows {
		if row == nil {
			continue
		}
		if isActiveScaleOutPlanStatus(row.Status) {
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

	levels, err := levelStore.ListByPlanID(target.ScaleOutPlanID)
	if err != nil {
		return nil, err
	}

	summary.ScaleOutPlanID = target.ScaleOutPlanID
	summary.PlanMode = target.PlanMode
	summary.ScaleOutStatus = normalizeScaleOutPlanStatus(target.Status)
	summary.HasScaleOutPlan = isActiveScaleOutPlanStatus(summary.ScaleOutStatus)
	summary.LinkedPositionKey = target.LinkedPositionKey
	summary.LastSyncedAt = target.UpdatedAt

	for _, level := range levels {
		if level == nil {
			continue
		}
		summary.TotalPlannedQty += level.PlannedQty
		summary.ExecutedScaleOutQty += level.ExecutedQty
		summary.RemainingScaleOutQty += level.RemainingQty
		if isWorkingScaleOutLevelStatus(level.Status) {
			summary.PendingScaleOutQty += level.RemainingQty
			summary.HasWorkingOrders = true
			summary.WorkingLevelCount++
		}
		if isTerminalScaleOutLevelStatus(level.Status) {
			summary.CompletedLevelCount++
		} else {
			summary.ActiveLevelCount++
		}
		if level.UpdatedAt.After(summary.LastSyncedAt) {
			summary.LastSyncedAt = level.UpdatedAt
		}
	}

	if summary.RemainingScaleOutQty <= 0 && summary.ExecutedScaleOutQty > 0 {
		summary.ScaleOutStatus = "completed"
		summary.HasScaleOutPlan = false
	}

	if summary.ScaleOutStatus == "completed" && summary.RemainingScaleOutQty > 0 {
		summary.ConsistencyStatus = "mismatch"
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ScaleOutStateMismatchReason{
			Category:          "plan_state",
			Reason:            "completed scale-out plan still has remaining quantity",
			Symbol:            summary.Symbol,
			ScaleOutPlanID:    summary.ScaleOutPlanID,
			LinkedPositionKey: summary.LinkedPositionKey,
		})
	}
	if !summary.HasScaleOutPlan && summary.ScaleOutStatus == "invalid" {
		summary.ConsistencyStatus = "mismatch"
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ScaleOutStateMismatchReason{
			Category:          "plan_state",
			Reason:            "scale-out plan is invalid",
			Symbol:            summary.Symbol,
			ScaleOutPlanID:    summary.ScaleOutPlanID,
			LinkedPositionKey: summary.LinkedPositionKey,
		})
	}

	if len(summary.MismatchReasons) > 0 {
		summary.ConsistencyStatus = "mismatch"
	} else if summary.ScaleOutStatus == "completed" {
		summary.ConsistencyStatus = "consistent"
	} else if summary.ScaleOutStatus == "invalid" {
		summary.ConsistencyStatus = "mismatch"
		summary.HasStateMismatch = true
	} else if summary.HasScaleOutPlan || summary.ScaleOutStatus == "draft" || summary.WorkingLevelCount > 0 || summary.PendingScaleOutQty > 0 || summary.ExecutedScaleOutQty > 0 {
		summary.ConsistencyStatus = "pending"
	}

	summary.ConsistencyStatus = normalizeScaleOutConsistencyStatus(summary.ConsistencyStatus)
	summary.ExecutionEligible = summary.HasScaleOutPlan && summary.ConsistencyStatus != "mismatch"
	return summary, nil
}

func normalizeScaleOutPlanMode(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "", "fixed_ratio":
		return "fixed_ratio"
	default:
		return normalized
	}
}

func normalizeScaleOutPlanStatus(status string) string {
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

func normalizeScaleOutConsistencyStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "consistent", "mismatch", "pending", "unknown":
		return normalized
	default:
		return "unknown"
	}
}

func isActiveScaleOutPlanStatus(status string) bool {
	switch normalizeScaleOutPlanStatus(status) {
	case "draft", "armed", "partially_filled":
		return true
	default:
		return false
	}
}

func NormalizeScaleOutPlanStatus(status string) string {
	return normalizeScaleOutPlanStatus(status)
}

func NormalizeScaleOutPlanMode(mode string) string {
	return normalizeScaleOutPlanMode(mode)
}

// NormalizeScaleOutConsistencyStatus exposes the scale-out consistency normalizer for service-layer reconciliation logic.
func NormalizeScaleOutConsistencyStatus(status string) string {
	return normalizeScaleOutConsistencyStatus(status)
}

func isWorkingScaleOutLevelStatus(status string) bool {
	switch normalizeScaleOutLevelStatus(status) {
	case "armed", "partially_filled":
		return true
	default:
		return false
	}
}

func isTerminalScaleOutLevelStatus(status string) bool {
	switch normalizeScaleOutLevelStatus(status) {
	case "filled", "cancelled", "invalid":
		return true
	default:
		return false
	}
}
