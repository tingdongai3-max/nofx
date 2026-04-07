package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ProtectionGroupStore stores the current fixed-protection truth snapshot rows.
type ProtectionGroupStore struct {
	db *gorm.DB
}

// NewProtectionGroupStore creates a protection group store.
func NewProtectionGroupStore(db *gorm.DB) *ProtectionGroupStore {
	return &ProtectionGroupStore{db: db}
}

// ProtectionGroup is the system-owned truth model for a fixed stop-loss/take-profit protection pair.
// It is not an exchange raw order mirror: the fields below are the current protection snapshot.
type ProtectionGroup struct {
	ID                      int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                                      // System persistence row id; not an exchange identifier.
	TraderID                string    `gorm:"column:trader_id;not null;index:idx_protection_groups_trader_symbol_status,priority:1;index:idx_protection_groups_trader_symbol_side_group,priority:1" json:"trader_id"`              // System-owned trader key.
	Symbol                  string    `gorm:"column:symbol;not null;index:idx_protection_groups_trader_symbol_status,priority:2;index:idx_protection_groups_trader_symbol_side_group,priority:2" json:"symbol"`                    // Exchange-normalized symbol in the protection scope.
	Side                    string    `gorm:"column:side;not null;index:idx_protection_groups_trader_symbol_side,priority:3;index:idx_protection_groups_trader_symbol_side_group,priority:3" json:"side"`                          // One-way side in the protection scope: LONG or SHORT.
	LinkedPositionKey       string    `gorm:"column:linked_position_key;not null;default:'';index:idx_protection_groups_linked_position_key" json:"linked_position_key"` // System-owned position correlation key.
	ProtectionGroupID       string    `gorm:"column:protection_group_id;not null;default:'';uniqueIndex:idx_protection_groups_protection_group_id;index:idx_protection_groups_trader_symbol_side_group,priority:4" json:"protection_group_id"` // System-generated protection group identifier.
	ProtectionMode          string    `gorm:"column:protection_mode;not null;default:fixed" json:"protection_mode"`                                                  // Protection policy label: fixed, break_even, or trailing_segmented.
	StopLossOrderIntentID   string    `gorm:"column:stop_loss_order_intent_id;not null;default:''" json:"stop_loss_order_intent_id"`                                // System-local intent id for the stop-loss leg.
	TakeProfitOrderIntentID string    `gorm:"column:take_profit_order_intent_id;not null;default:''" json:"take_profit_order_intent_id"`                            // System-local intent id for the take-profit leg.
	StopLossExchangeOrderID string    `gorm:"column:stop_loss_exchange_order_id;not null;default:'';index:idx_protection_groups_stop_loss_exchange_order_id" json:"stop_loss_exchange_order_id"`    // Exchange raw stop-loss order id when available.
	TakeProfitExchangeOrderID string  `gorm:"column:take_profit_exchange_order_id;not null;default:'';index:idx_protection_groups_take_profit_exchange_order_id" json:"take_profit_exchange_order_id"` // Exchange raw take-profit order id when available.
	StopLossTriggerPrice    float64   `gorm:"column:stop_loss_trigger_price;not null;default:0" json:"stop_loss_trigger_price"`                                       // Backward-compatible current stop-loss trigger price alias.
	TakeProfitTriggerPrice  float64   `gorm:"column:take_profit_trigger_price;not null;default:0" json:"take_profit_trigger_price"`                                   // Backward-compatible current take-profit trigger price alias.
	StopLossInitialTriggerPrice float64 `gorm:"column:stop_loss_initial_trigger_price;not null;default:0" json:"stop_loss_initial_trigger_price"`                   // System-derived initial stop-loss trigger price.
	StopLossCurrentTriggerPrice float64 `gorm:"column:stop_loss_current_trigger_price;not null;default:0;index:idx_protection_groups_last_protection_action_at,priority:1" json:"stop_loss_current_trigger_price"` // System-derived current stop-loss trigger price.
	TakeProfitInitialTriggerPrice float64 `gorm:"column:take_profit_initial_trigger_price;not null;default:0" json:"take_profit_initial_trigger_price"`             // System-derived initial take-profit trigger price.
	TakeProfitCurrentTriggerPrice float64 `gorm:"column:take_profit_current_trigger_price;not null;default:0" json:"take_profit_current_trigger_price"`             // System-derived current take-profit trigger price.
	BreakEvenArmed          bool      `gorm:"column:break_even_armed;not null;default:false" json:"break_even_armed"`                                                  // System-derived flag showing the protection has moved to break-even.
	TrailingArmed           bool      `gorm:"column:trailing_armed;not null;default:false" json:"trailing_armed"`                                                      // System-derived flag showing the protection has entered segmented trailing.
	TrailingRuleID          string    `gorm:"column:trailing_rule_id;not null;default:'';index:idx_protection_groups_trailing_rule_id" json:"trailing_rule_id"`     // System-owned trailing rule identifier.
	TrailingAnchorPrice     float64   `gorm:"column:trailing_anchor_price;not null;default:0" json:"trailing_anchor_price"`                                           // System-derived price anchor used by the latest trailing move.
	TrailingLastMoveAt      time.Time `gorm:"column:trailing_last_move_at;index:idx_protection_groups_last_protection_action_at,priority:2" json:"trailing_last_move_at"` // Latest trailing move timestamp.
	TrailingMoveCount       int       `gorm:"column:trailing_move_count;not null;default:0" json:"trailing_move_count"`                                                // System-derived count of completed trailing moves.
	ProtectionRevision      int       `gorm:"column:protection_revision;not null;default:0;index:idx_protection_groups_protection_revision" json:"protection_revision"` // System-owned protection revision counter.
	LastProtectionAction    string    `gorm:"column:last_protection_action;not null;default:''" json:"last_protection_action"`                                          // Last protection mutation action label.
	LastProtectionActionAt  time.Time `gorm:"column:last_protection_action_at;index:idx_protection_groups_last_protection_action_at,priority:3" json:"last_protection_action_at"` // Last protection mutation timestamp.
	ProtectedQuantity       float64   `gorm:"column:protected_quantity;not null;default:0" json:"protected_quantity"`                                                  // System-derived active protected quantity.
	Status                  string    `gorm:"column:status;not null;default:pending_attach;index:idx_protection_groups_trader_symbol_status,priority:3" json:"status"` // System truth status: pending_attach, armed, partial_invalid, closed, cancel_pending.
	Source                  string    `gorm:"column:source;not null;default:''" json:"source"`                                                                         // Truth source label: fixed_protection_manager, user_stream, exchange_snapshot, bootstrap.
	LastSyncedAt            time.Time `gorm:"column:last_synced_at;index:idx_protection_groups_last_synced_at" json:"last_synced_at"`                                // Last exchange or reconciliation sync timestamp.
	CreatedAt               time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                                      // Persistence timestamp.
	UpdatedAt               time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`                                                                      // Persistence timestamp.
}

// TableName returns the table name for ProtectionGroup.
func (ProtectionGroup) TableName() string {
	return "protection_groups"
}

// ProtectionStateMismatchReason is a machine-readable protection-truth mismatch explanation.
// It is derived from reconciliation, not from a raw exchange event.
type ProtectionStateMismatchReason struct {
	Category          string `json:"category"`                    // Mismatch bucket used by preview/debug tooling.
	Reason            string `json:"reason"`                      // Human-readable mismatch explanation.
	Symbol            string `json:"symbol,omitempty"`            // Symbol scope for the mismatch.
	ProtectionGroupID string `json:"protection_group_id,omitempty"` // Protection group involved in the mismatch.
	OrderRole         string `json:"order_role,omitempty"`        // Registry role involved in the mismatch.
	LocalIntentID     string `json:"local_intent_id,omitempty"`   // Local intent id when available.
	ExchangeOrderID   string `json:"exchange_order_id,omitempty"` // Exchange order id when available.
	ClientOrderID     string `json:"client_order_id,omitempty"`   // Exchange client id when available.
}

// ProtectionGroupSummary is the system-derived rollup of the current protection truth for one trader/symbol/side scope.
// It is the single source of truth for protection gating, preview output, and position-aggregate protection fields.
type ProtectionGroupSummary struct {
	TraderID                   string                         `json:"trader_id"`                  // System-owned trader key.
	Symbol                     string                         `json:"symbol"`                     // Truth-layer symbol scope.
	Side                       string                         `json:"side"`                       // Truth-layer one-way side scope.
	LinkedPositionKey          string                         `json:"linked_position_key"`        // System-derived position correlation key.
	ProtectionGroupID          string                         `json:"protection_group_id"`        // System-generated protection group identifier.
	ProtectionMode             string                         `json:"protection_mode"`            // Protection policy label.
	ProtectionGroupStatus      string                         `json:"protection_group_status"`    // Current protection truth status.
	StopLossOrderIntentID      string                         `json:"stop_loss_order_intent_id"`  // System-local stop-loss intent id for the protection truth layer.
	TakeProfitOrderIntentID    string                         `json:"take_profit_order_intent_id"` // System-local take-profit intent id for the protection truth layer.
	StopLossExchangeOrderID    string                         `json:"stop_loss_exchange_order_id"` // Exchange raw stop-loss order id when available.
	TakeProfitExchangeOrderID  string                         `json:"take_profit_exchange_order_id"` // Exchange raw take-profit order id when available.
	HasProtection              bool                           `json:"has_protection"`             // System-derived flag that active protection exists.
	StopLossArmed              bool                           `json:"stop_loss_armed"`            // System-derived flag that the stop-loss leg is still working.
	TakeProfitArmed            bool                           `json:"take_profit_armed"`          // System-derived flag that the take-profit leg is still working.
	BreakEvenArmed             bool                           `json:"break_even_armed"`           // System-derived flag that the protection has moved to break-even.
	TrailingArmed              bool                           `json:"trailing_armed"`             // System-derived flag that segmented trailing is active.
	TrailingRuleID             string                         `json:"trailing_rule_id"`           // System-owned trailing rule identifier.
	TrailingAnchorPrice        float64                        `json:"trailing_anchor_price"`      // System-derived anchor price used by the latest trailing move.
	TrailingMoveCount          int                            `json:"trailing_move_count"`        // System-derived count of completed trailing moves.
	ProtectionRevision         int                            `json:"protection_revision"`        // System-owned protection revision counter.
	LastProtectionAction       string                         `json:"last_protection_action"`     // Last protection mutation action label.
	LastProtectionActionAt     time.Time                      `json:"last_protection_action_at"`  // Last protection mutation timestamp.
	ProtectedQuantity          float64                        `json:"protected_quantity"`         // System-derived protection-layer coverage target for the live position; not an order reservation metric.
	HasStateMismatch           bool                           `json:"has_state_mismatch"`         // Reconcile result flag; true means truth layers disagree.
	ConsistencyStatus          string                         `json:"consistency_status"`         // Protection consistency enum: consistent, mismatch, pending, or unknown.
	MismatchReasons            []ProtectionStateMismatchReason `json:"mismatch_reasons"`           // Machine-readable mismatch explanation list.
	HasWorkingOrders           bool                           `json:"has_working_orders"`         // Truth-layer flag derived from linked working protection rows.
	HasPendingCancelReplace    bool                           `json:"has_pending_cancel_replace"` // Truth-layer flag for any linked cancel_replace flow.
	CurrentStopLossPrice       float64                        `json:"current_stop_loss_price"`    // Current stop-loss trigger price from the protection group.
	InitialStopLossPrice       float64                        `json:"initial_stop_loss_price"`    // Initial stop-loss trigger price before any protection movement.
	CurrentTakeProfitPrice     float64                        `json:"current_take_profit_price"`  // Current take-profit trigger price from the protection group.
	InitialTakeProfitPrice     float64                        `json:"initial_take_profit_price"`  // Initial take-profit trigger price before any protection movement.
	StopLossTriggerPrice       float64                        `json:"stop_loss_trigger_price"`    // Backward-compatible alias for the current stop-loss trigger price.
	TakeProfitTriggerPrice     float64                        `json:"take_profit_trigger_price"`  // Backward-compatible alias for the current take-profit trigger price.
	LastSyncedAt               time.Time                      `json:"last_synced_at"`             // Latest sync timestamp across the protection scope.
	ExecutionEligible          bool                           `json:"execution_eligible"`         // Runtime readiness flag; not a direct execution permit.
}

// initTables initializes the protection group table and indexes.
func (s *ProtectionGroupStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'protection_groups'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&ProtectionGroup{}); err != nil {
		return fmt.Errorf("failed to migrate protection_groups table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *ProtectionGroupStore) ensureIndexes() error {
	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_protection_groups_protection_group_id ON protection_groups(protection_group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_groups_trader_symbol_status ON protection_groups(trader_id, symbol, status)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_groups_trader_symbol_side_group ON protection_groups(trader_id, symbol, side, protection_group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_groups_linked_position_key ON protection_groups(linked_position_key)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_groups_stop_loss_exchange_order_id ON protection_groups(stop_loss_exchange_order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_groups_take_profit_exchange_order_id ON protection_groups(take_profit_exchange_order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_groups_trailing_rule_id ON protection_groups(trailing_rule_id)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_groups_protection_revision ON protection_groups(protection_revision)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_groups_last_protection_action_at ON protection_groups(last_protection_action_at)`,
		`CREATE INDEX IF NOT EXISTS idx_protection_groups_last_synced_at ON protection_groups(last_synced_at)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create protection group index: %w", err)
		}
	}
	return nil
}

// GetByKeys loads a protection group row by the strongest available keys.
func (s *ProtectionGroupStore) GetByKeys(traderID, protectionGroupID, linkedPositionKey string) (*ProtectionGroup, error) {
	var row ProtectionGroup
	query := s.db.Where("trader_id = ?", traderID)
	switch {
	case protectionGroupID != "":
		query = query.Where("protection_group_id = ?", protectionGroupID)
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
		return nil, fmt.Errorf("failed to get protection group row: %w", err)
	}
	return &row, nil
}

// ListByTraderID returns all persisted protection group rows for a trader.
func (s *ProtectionGroupStore) ListByTraderID(traderID string) ([]*ProtectionGroup, error) {
	var rows []*ProtectionGroup
	err := s.db.Where("trader_id = ?", traderID).
		Order("last_synced_at DESC, updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list protection groups: %w", err)
	}
	return rows, nil
}

// ListByTraderSymbol returns protection group rows for one trader and symbol.
func (s *ProtectionGroupStore) ListByTraderSymbol(traderID, symbol string) ([]*ProtectionGroup, error) {
	var rows []*ProtectionGroup
	err := s.db.Where("trader_id = ? AND symbol = ?", traderID, normalizeAggregateSymbol(symbol)).
		Order("last_synced_at DESC, updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list protection groups for symbol: %w", err)
	}
	return rows, nil
}

// ListByTraderSymbolSide returns protection group rows for one trader/symbol/side.
func (s *ProtectionGroupStore) ListByTraderSymbolSide(traderID, symbol, side string) ([]*ProtectionGroup, error) {
	normalizedSide := normalizeAggregateSide(side)
	rows, err := s.ListByTraderSymbol(traderID, symbol)
	if err != nil {
		return nil, err
	}
	filtered := make([]*ProtectionGroup, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		if normalizedSide == "" || normalizeAggregateSide(row.Side) == normalizedSide {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

// ReplaceForTrader replaces all rows for one trader with the provided protection snapshot.
// It is used only by recovery/bootstrap flows so the store keeps one current truth snapshot.
func (s *ProtectionGroupStore) ReplaceForTrader(traderID string, rows []*ProtectionGroup) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("trader_id = ?", traderID).Delete(&ProtectionGroup{}).Error; err != nil {
			return fmt.Errorf("failed to clear protection group rows: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		if err := tx.CreateInBatches(rows, len(rows)).Error; err != nil {
			return fmt.Errorf("failed to store protection group rows: %w", err)
		}
		return nil
	})
}

// Upsert stores or updates a protection group row using the strongest available correlation keys.
func (s *ProtectionGroupStore) Upsert(row *ProtectionGroup) error {
	if row == nil {
		return fmt.Errorf("protection group row is nil")
	}

	row.Symbol = normalizeAggregateSymbol(row.Symbol)
	row.Side = normalizeAggregateSide(row.Side)
	row.LinkedPositionKey = strings.TrimSpace(row.LinkedPositionKey)
	row.ProtectionMode = normalizeProtectionMode(row.ProtectionMode)
	row.Status = normalizeProtectionGroupStatus(row.Status)
	if row.Source == "" {
		row.Source = "unknown"
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		existing, err := s.findExisting(tx, row.TraderID, row.ProtectionGroupID, row.LinkedPositionKey)
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

func (s *ProtectionGroupStore) findExisting(tx *gorm.DB, traderID, protectionGroupID, linkedPositionKey string) (*ProtectionGroup, error) {
	var row ProtectionGroup
	query := tx.Where("trader_id = ?", traderID)
	switch {
	case protectionGroupID != "":
		query = query.Where("protection_group_id = ?", protectionGroupID)
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
		return nil, fmt.Errorf("failed to find existing protection group row: %w", err)
	}
	return &row, nil
}

// SummarizeForTraderSymbol returns a sorted list of protection summaries for a trader/symbol scope.
func (s *ProtectionGroupStore) SummarizeForTraderSymbol(traderID, symbol string) ([]*ProtectionGroupSummary, error) {
	rows, err := s.ListByTraderSymbol(traderID, symbol)
	if err != nil {
		return nil, err
	}
	result := make([]*ProtectionGroupSummary, 0, len(rows))
	for _, row := range rows {
		summary, summaryErr := s.summarizeRow(row)
		if summaryErr != nil {
			return nil, summaryErr
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastSyncedAt.After(result[j].LastSyncedAt)
	})
	return result, nil
}

// SummarizeForTraderSymbolSide returns the system truth summary for a trader/symbol/side scope.
func (s *ProtectionGroupStore) SummarizeForTraderSymbolSide(traderID, symbol, side string) (*ProtectionGroupSummary, error) {
	rows, err := s.ListByTraderSymbolSide(traderID, symbol, side)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		summary := &ProtectionGroupSummary{
			TraderID:          traderID,
			Symbol:            normalizeAggregateSymbol(symbol),
			Side:              normalizeAggregateSide(side),
			ConsistencyStatus: "unknown",
			ExecutionEligible: false,
			MismatchReasons:   []ProtectionStateMismatchReason{},
		}

		var allRows []*OrderRegistry
		if err := s.db.Where("trader_id = ? AND symbol = ?", traderID, summary.Symbol).
			Order("last_exchange_update_at DESC, updated_at DESC, created_at DESC").
			Find(&allRows).Error; err != nil {
			return nil, fmt.Errorf("failed to query protection order rows for mismatch detection: %w", err)
		}

		for _, row := range allRows {
			if row == nil {
				continue
			}
			if normalizeAggregateSide(row.Side) != summary.Side {
				continue
			}
			role := normalizeOrderRegistryRole(row.OrderRole)
			if role != "stop_loss" && role != "take_profit" && role != "cancel_replace" {
				continue
			}
			summary.HasProtection = true
			summary.HasStateMismatch = true
			summary.HasWorkingOrders = summary.HasWorkingOrders || row.IsWorking
			if role == "stop_loss" {
				summary.StopLossArmed = row.IsWorking
			}
			if role == "take_profit" {
				summary.TakeProfitArmed = row.IsWorking
			}
			summary.ProtectedQuantity += row.RemainingQty
			summary.MismatchReasons = append(summary.MismatchReasons, ProtectionStateMismatchReason{
				Category:         "orphan",
				Reason:           "protection order exists without a protection group snapshot",
				Symbol:           summary.Symbol,
				OrderRole:        role,
				LocalIntentID:    row.LocalIntentID,
				ExchangeOrderID:  row.ExchangeOrderID,
				ClientOrderID:    row.ClientOrderID,
			})
		}
		if summary.HasStateMismatch {
			summary.ConsistencyStatus = "mismatch"
		} else if summary.HasProtection {
			summary.ConsistencyStatus = "pending"
		}
		return summary, nil
	}

	// The truth layer keeps one current snapshot per symbol/side; if more than one row exists,
	// use the freshest row and let the consistency summary expose the duplicate-snapshot mismatch.
	row := rows[0]
	summary, err := s.summarizeRow(row)
	if err != nil {
		return nil, err
	}

	if len(rows) > 1 {
		summary.HasStateMismatch = true
		summary.ConsistencyStatus = "mismatch"
		summary.MismatchReasons = append(summary.MismatchReasons, ProtectionStateMismatchReason{
			Category:         "snapshot",
			Reason:           "multiple protection snapshot rows exist for the same trader/symbol/side",
			Symbol:           summary.Symbol,
			ProtectionGroupID: summary.ProtectionGroupID,
		})
	}

	return summary, nil
}

func (s *ProtectionGroupStore) summarizeRow(row *ProtectionGroup) (*ProtectionGroupSummary, error) {
	if row == nil {
		return &ProtectionGroupSummary{
			ConsistencyStatus: "unknown",
			MismatchReasons:   []ProtectionStateMismatchReason{},
		}, nil
	}

	normalizedSymbol := normalizeAggregateSymbol(row.Symbol)
	normalizedSide := normalizeAggregateSide(row.Side)
	currentStopLossPrice := row.StopLossCurrentTriggerPrice
	if currentStopLossPrice <= 0 {
		currentStopLossPrice = row.StopLossTriggerPrice
	}
	initialStopLossPrice := row.StopLossInitialTriggerPrice
	if initialStopLossPrice <= 0 {
		initialStopLossPrice = row.StopLossTriggerPrice
	}
	currentTakeProfitPrice := row.TakeProfitCurrentTriggerPrice
	if currentTakeProfitPrice <= 0 {
		currentTakeProfitPrice = row.TakeProfitTriggerPrice
	}
	initialTakeProfitPrice := row.TakeProfitInitialTriggerPrice
	if initialTakeProfitPrice <= 0 {
		initialTakeProfitPrice = row.TakeProfitTriggerPrice
	}
	summary := &ProtectionGroupSummary{
		TraderID:                 row.TraderID,
		Symbol:                   normalizedSymbol,
		Side:                     normalizedSide,
		LinkedPositionKey:        strings.TrimSpace(row.LinkedPositionKey),
		ProtectionGroupID:        strings.TrimSpace(row.ProtectionGroupID),
		ProtectionMode:           normalizeProtectionMode(row.ProtectionMode),
		ProtectionGroupStatus:    normalizeProtectionGroupStatus(row.Status),
		StopLossOrderIntentID:    strings.TrimSpace(row.StopLossOrderIntentID),
		TakeProfitOrderIntentID:  strings.TrimSpace(row.TakeProfitOrderIntentID),
		StopLossExchangeOrderID:  strings.TrimSpace(row.StopLossExchangeOrderID),
		TakeProfitExchangeOrderID: strings.TrimSpace(row.TakeProfitExchangeOrderID),
		BreakEvenArmed:           row.BreakEvenArmed,
		TrailingArmed:            row.TrailingArmed,
		TrailingRuleID:           strings.TrimSpace(row.TrailingRuleID),
		TrailingAnchorPrice:      row.TrailingAnchorPrice,
		TrailingMoveCount:        row.TrailingMoveCount,
		ProtectionRevision:       row.ProtectionRevision,
		LastProtectionAction:     strings.TrimSpace(row.LastProtectionAction),
		LastProtectionActionAt:   row.LastProtectionActionAt,
		ProtectedQuantity:        row.ProtectedQuantity,
		CurrentStopLossPrice:     currentStopLossPrice,
		InitialStopLossPrice:     initialStopLossPrice,
		CurrentTakeProfitPrice:   currentTakeProfitPrice,
		InitialTakeProfitPrice:   initialTakeProfitPrice,
		StopLossTriggerPrice:     currentStopLossPrice,
		TakeProfitTriggerPrice:   currentTakeProfitPrice,
		LastSyncedAt:             row.LastSyncedAt,
		ExecutionEligible:        true,
		MismatchReasons:          []ProtectionStateMismatchReason{},
	}

	linkedOrders, err := s.loadLinkedProtectionOrders(row.TraderID, normalizedSymbol, normalizedSide, summary.ProtectionGroupID, summary.LinkedPositionKey)
	if err != nil {
		return nil, err
	}

	summary.HasWorkingOrders = false
	stopLossRows := 0
	takeProfitRows := 0
	workingQty := 0.0
	for _, order := range linkedOrders {
		if order == nil {
			continue
		}
		summary.LastSyncedAt = maxTime(summary.LastSyncedAt, order.LastExchangeUpdateAt)
		if order.Status == "PENDING_REPLACE" || normalizeOrderRegistryStatus(order.Status) == "PENDING_REPLACE" || normalizeOrderRegistryRole(order.OrderRole) == "cancel_replace" {
			summary.HasPendingCancelReplace = true
		}
		if !order.IsWorking {
			continue
		}
		summary.HasWorkingOrders = true
		workingQty += order.RemainingQty
		switch normalizeOrderRegistryRole(order.OrderRole) {
		case "stop_loss":
			stopLossRows++
		case "take_profit":
			takeProfitRows++
		}
	}

	summary.StopLossArmed = stopLossRows > 0
	summary.TakeProfitArmed = takeProfitRows > 0

	if summary.ProtectedQuantity <= 0 && workingQty > 0 {
		summary.ProtectedQuantity = workingQty
	}
	if summary.CurrentStopLossPrice <= 0 && summary.ProtectedQuantity > 0 && summary.StopLossArmed {
		summary.CurrentStopLossPrice = row.StopLossTriggerPrice
	}
	if summary.InitialStopLossPrice <= 0 && summary.CurrentStopLossPrice > 0 {
		summary.InitialStopLossPrice = summary.CurrentStopLossPrice
	}
	if summary.CurrentTakeProfitPrice <= 0 && summary.TakeProfitArmed {
		summary.CurrentTakeProfitPrice = row.TakeProfitTriggerPrice
	}
	if summary.InitialTakeProfitPrice <= 0 && summary.CurrentTakeProfitPrice > 0 {
		summary.InitialTakeProfitPrice = summary.CurrentTakeProfitPrice
	}
	if summary.TrailingArmed && summary.TrailingRuleID == "" {
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ProtectionStateMismatchReason{
			Category:         "trailing_rule",
			Reason:           "trailing protection is armed but trailing_rule_id is empty",
			Symbol:           summary.Symbol,
			ProtectionGroupID: summary.ProtectionGroupID,
		})
		summary.ConsistencyStatus = "mismatch"
	}
	if summary.ProtectionMode == "break_even" && !summary.BreakEvenArmed {
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ProtectionStateMismatchReason{
			Category:          "protection_mode",
			Reason:            "break-even protection mode is active but break_even_armed is false",
			Symbol:            summary.Symbol,
			ProtectionGroupID: summary.ProtectionGroupID,
		})
		summary.ConsistencyStatus = "mismatch"
	}
	if summary.ProtectionMode == "trailing_segmented" && (!summary.TrailingArmed || summary.TrailingRuleID == "") {
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ProtectionStateMismatchReason{
			Category:          "protection_mode",
			Reason:            "segmented trailing protection mode is active but trailing state is incomplete",
			Symbol:            summary.Symbol,
			ProtectionGroupID: summary.ProtectionGroupID,
		})
		summary.ConsistencyStatus = "mismatch"
	}
	if summary.BreakEvenArmed && summary.CurrentStopLossPrice <= 0 {
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ProtectionStateMismatchReason{
			Category:         "stop_loss",
			Reason:           "break-even protection is armed but current stop-loss price is missing",
			Symbol:           summary.Symbol,
			ProtectionGroupID: summary.ProtectionGroupID,
		})
		summary.ConsistencyStatus = "mismatch"
	}
	if summary.TrailingMoveCount < 0 {
		summary.TrailingMoveCount = 0
	}
	if summary.ProtectionRevision < 0 {
		summary.ProtectionRevision = 0
	}

	if row.Status == "closed" && summary.HasWorkingOrders {
		summary.HasStateMismatch = true
		summary.MismatchReasons = append(summary.MismatchReasons, ProtectionStateMismatchReason{
			Category:         "status",
			Reason:           "protection group is closed but working protection orders still exist",
			Symbol:           summary.Symbol,
			ProtectionGroupID: summary.ProtectionGroupID,
		})
		summary.ConsistencyStatus = "mismatch"
	}
	if row.Status == "armed" && (!summary.StopLossArmed || !summary.TakeProfitArmed) {
		summary.HasStateMismatch = true
		if !summary.StopLossArmed {
			summary.MismatchReasons = append(summary.MismatchReasons, ProtectionStateMismatchReason{
				Category:         "missing_leg",
				Reason:           "armed protection group is missing a working stop-loss leg",
				Symbol:           summary.Symbol,
				ProtectionGroupID: summary.ProtectionGroupID,
				OrderRole:        "stop_loss",
			})
		}
		if !summary.TakeProfitArmed {
			summary.MismatchReasons = append(summary.MismatchReasons, ProtectionStateMismatchReason{
				Category:         "missing_leg",
				Reason:           "armed protection group is missing a working take-profit leg",
				Symbol:           summary.Symbol,
				ProtectionGroupID: summary.ProtectionGroupID,
				OrderRole:        "take_profit",
			})
		}
		summary.ConsistencyStatus = "mismatch"
	}
	if row.Status == "cancel_pending" || row.Status == "pending_attach" || row.Status == "armed" || row.Status == "partial_invalid" {
		if summary.ConsistencyStatus != "mismatch" {
			summary.ConsistencyStatus = "pending"
		}
	}
	if row.Status == "closed" && !summary.HasWorkingOrders && !summary.HasStateMismatch {
		summary.ConsistencyStatus = "consistent"
	}
	if len(summary.MismatchReasons) > 0 {
		summary.ConsistencyStatus = "mismatch"
	}
	if summary.ConsistencyStatus == "" {
		summary.ConsistencyStatus = "unknown"
	}
	summary.HasProtection = row.Status != "closed" || summary.HasWorkingOrders
	if row.Status == "closed" && !summary.HasWorkingOrders {
		summary.HasProtection = false
	}
	if summary.LastProtectionAction == "" {
		summary.LastProtectionAction = row.LastProtectionAction
	}

	return summary, nil
}

func (s *ProtectionGroupStore) loadLinkedProtectionOrders(traderID, symbol, side, protectionGroupID, linkedPositionKey string) ([]*OrderRegistry, error) {
	var allRows []*OrderRegistry
	if err := s.db.Where("trader_id = ? AND symbol = ?", traderID, symbol).
		Order("last_exchange_update_at DESC, updated_at DESC, created_at DESC").
		Find(&allRows).Error; err != nil {
		return nil, fmt.Errorf("failed to query linked protection order rows: %w", err)
	}

	filtered := make([]*OrderRegistry, 0, len(allRows))
	for _, row := range allRows {
		if row == nil {
			continue
		}
		if normalizeAggregateSide(row.Side) != normalizeAggregateSide(side) {
			continue
		}
		role := normalizeOrderRegistryRole(row.OrderRole)
		if role != "stop_loss" && role != "take_profit" && role != "cancel_replace" {
			continue
		}
		if protectionGroupID != "" && strings.TrimSpace(row.LinkedGroupID) != protectionGroupID {
			if linkedPositionKey != "" && strings.TrimSpace(row.LinkedPositionKey) != linkedPositionKey {
				continue
			}
		}
		filtered = append(filtered, row)
	}
	return filtered, nil
}

func normalizeProtectionMode(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "", "fixed":
		return "fixed"
	case "break_even":
		return "break_even"
	case "trailing_segmented":
		return "trailing_segmented"
	default:
		return "fixed"
	}
}

func normalizeProtectionGroupStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "", "pending_attach", "armed", "partial_invalid", "closed", "cancel_pending":
		if normalized == "" {
			return "pending_attach"
		}
		return normalized
	default:
		return normalized
	}
}

func normalizeProtectionConsistencyStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "consistent", "mismatch", "pending", "unknown":
		return normalized
	default:
		return "unknown"
	}
}

func isNonTerminalProtectionGroupStatus(status string) bool {
	switch normalizeProtectionGroupStatus(status) {
	case "pending_attach", "armed", "cancel_pending", "partial_invalid":
		return true
	default:
		return false
	}
}

func maxTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.After(a) {
		return b
	}
	return a
}

// NormalizeProtectionGroupStatus exposes the protection status normalizer for service-layer reconciliation logic.
func NormalizeProtectionGroupStatus(status string) string {
	return normalizeProtectionGroupStatus(status)
}

// NormalizeProtectionConsistencyStatus exposes the protection consistency enum normalizer for service-layer reconciliation logic.
func NormalizeProtectionConsistencyStatus(status string) string {
	return normalizeProtectionConsistencyStatus(status)
}

// IsNonTerminalProtectionGroupStatus reports whether the protection lifecycle is still active or transitioning.
func IsNonTerminalProtectionGroupStatus(status string) bool {
	return isNonTerminalProtectionGroupStatus(status)
}

// NormalizeProtectionMode exposes the protection mode normalizer for service-layer reconciliation logic.
func NormalizeProtectionMode(mode string) string {
	return normalizeProtectionMode(mode)
}
