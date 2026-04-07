package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// OrderRegistryStore stores the system-owned Binance USDⓈ-M Futures one-way order truth rows.
type OrderRegistryStore struct {
	db *gorm.DB
}

// NewOrderRegistryStore creates an order registry store.
func NewOrderRegistryStore(db *gorm.DB) *OrderRegistryStore {
	return &OrderRegistryStore{db: db}
}

// OrderRegistry is the system-owned order truth model.
// It is not an exchange raw order mirror: the fields below are reconciled and system-derived.
type OrderRegistry struct {
	ID                   int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                               // System persistence row id; not an exchange identifier.
	TraderID             string    `gorm:"column:trader_id;not null;index:idx_order_registry_trader_symbol_status,priority:1" json:"trader_id"`              // System-owned trader key.
	Symbol               string    `gorm:"column:symbol;not null;index:idx_order_registry_trader_symbol_status,priority:2" json:"symbol"`                    // Exchange-normalized symbol from the truth layer.
	Side                 string    `gorm:"column:side;not null" json:"side"`                                                                                 // One-way side on the truth layer: LONG or SHORT.
	PositionSideMode     string    `gorm:"column:position_side_mode;not null;default:one_way" json:"position_side_mode"`                                     // Binance futures position mode; fixed to one_way in this scope.
	OrderRole            string    `gorm:"column:order_role;not null;default:entry" json:"order_role"`                                                       // System-derived role: entry, reduce, stop_loss, take_profit, trailing_protection, cancel_replace.
	LocalIntentID        string    `gorm:"column:local_intent_id;not null;default:'';index:idx_order_registry_local_intent_id" json:"local_intent_id"`       // System-generated local intent identifier used for recovery and correlation.
	LinkedGroupID        string    `gorm:"column:linked_group_id;not null;default:'';index:idx_order_registry_linked_group_id" json:"linked_group_id"`       // System-generated group id for mutually-aware orders.
	LinkedPositionKey    string    `gorm:"column:linked_position_key;not null;default:''" json:"linked_position_key"`                                        // System-derived position correlation key used by the truth layer.
	ExchangeOrderID      string    `gorm:"column:exchange_order_id;not null;default:'';index:idx_order_registry_exchange_order_id" json:"exchange_order_id"` // Exchange raw order id when available.
	ClientOrderID        string    `gorm:"column:client_order_id;not null;default:'';index:idx_order_registry_client_order_id" json:"client_order_id"`       // Exchange raw client order id, if supplied.
	OrderType            string    `gorm:"column:order_type;not null;default:''" json:"order_type"`                                                          // Exchange raw order type, normalized into the truth layer.
	TimeInForce          string    `gorm:"column:time_in_force;not null;default:''" json:"time_in_force"`                                                    // Exchange raw time-in-force value.
	ReduceOnly           bool      `gorm:"column:reduce_only;default:false" json:"reduce_only"`                                                              // Exchange raw reduce-only flag; participates in future execution gating.
	ClosePosition        bool      `gorm:"column:close_position;default:false" json:"close_position"`                                                        // Exchange raw close-position flag; participates in future execution gating.
	OrigQty              float64   `gorm:"column:orig_qty;not null;default:0" json:"orig_qty"`                                                               // Exchange raw original order quantity.
	ExecutedQty          float64   `gorm:"column:executed_qty;not null;default:0" json:"executed_qty"`                                                       // System-tracked executed quantity from exchange/user-stream events.
	RemainingQty         float64   `gorm:"column:remaining_qty;not null;default:0" json:"remaining_qty"`                                                     // System-derived remaining quantity.
	AvgPrice             float64   `gorm:"column:avg_price;not null;default:0" json:"avg_price"`                                                             // Exchange raw average fill price when available.
	TriggerPrice         float64   `gorm:"column:trigger_price;not null;default:0" json:"trigger_price"`                                                     // Exchange raw trigger price or stop price.
	ActivationPrice      float64   `gorm:"column:activation_price;not null;default:0" json:"activation_price"`                                               // Exchange raw activation price for trailing orders.
	CallbackRate         float64   `gorm:"column:callback_rate;not null;default:0" json:"callback_rate"`                                                     // Exchange raw callback rate for trailing orders.
	Status               string    `gorm:"column:status;not null;default:NEW;index:idx_order_registry_trader_symbol_status,priority:3" json:"status"`        // Exchange/order-state status after reconciliation.
	Source               string    `gorm:"column:source;not null;default:''" json:"source"`                                                                  // System truth source: user_stream, local_submit, exchange_snapshot, bootstrap.
	IsWorking            bool      `gorm:"column:is_working;default:false" json:"is_working"`                                                                // System-derived flag: order still occupies working state on exchange.
	LastExchangeUpdateAt time.Time `gorm:"column:last_exchange_update_at;index:idx_order_registry_last_exchange_update_at" json:"last_exchange_update_at"`   // Latest exchange/user-stream timestamp that touched this truth row.
	CreatedAt            time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                               // System persistence timestamp.
	UpdatedAt            time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`                                                               // System persistence timestamp.
}

// TableName returns the table name for OrderRegistry.
func (OrderRegistry) TableName() string {
	return "order_registry"
}

// OrderRegistrySummary is a system-derived rollup of the registry rows for one trader/symbol/side.
// It is used for truth-layer previews and capability clipping, not as a raw exchange record.
type OrderRegistrySummary struct {
	TraderID                string    `json:"trader_id"`                  // System-owned trader key.
	Symbol                  string    `json:"symbol"`                     // Truth-layer symbol scope.
	Side                    string    `json:"side"`                       // Truth-layer side scope.
	HasWorkingOrders        bool      `json:"has_working_orders"`         // System-derived working-order flag.
	HasPendingCancelReplace bool      `json:"has_pending_cancel_replace"` // System-derived indicator that a cancel_replace flow is still active.
	PendingAddQty           float64   `json:"pending_add_qty"`            // System-derived add quantity reserved by working entry orders.
	PendingReduceQty        float64   `json:"pending_reduce_qty"`         // System-derived order-layer reduce reserve, including working reduce-only and protection rows; this is not the remaining position size.
	ProtectionCoverageQty   float64   `json:"protection_coverage_qty"`    // System-derived protection-order coverage from stop/take-profit/trailing rows; compare this against ProtectedQuantity for rebalance checks.
	WorkingOrderCount       int       `json:"working_order_count"`        // System-derived count of working rows.
	WorkingOrderIDs         []string  `json:"working_order_ids"`          // System-derived exchange order ids for the working rows.
	PendingOrderIDs         []string  `json:"pending_order_ids"`          // System-derived client order ids for the working rows.
	LastExchangeUpdateAt    time.Time `json:"last_exchange_update_at"`    // Latest exchange/user-stream update time among rows in scope.
	ExecutionEligible       bool      `json:"execution_eligible"`         // System-derived execution readiness flag; not a direct order permit.
}

// initTables initializes the order registry table and its indexes.
func (s *OrderRegistryStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'order_registry'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&OrderRegistry{}); err != nil {
		return fmt.Errorf("failed to migrate order_registry table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *OrderRegistryStore) ensureIndexes() error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_order_registry_trader_symbol_status ON order_registry(trader_id, symbol, status)`,
		`CREATE INDEX IF NOT EXISTS idx_order_registry_local_intent_id ON order_registry(local_intent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_registry_exchange_order_id ON order_registry(exchange_order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_registry_client_order_id ON order_registry(client_order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_registry_linked_group_id ON order_registry(linked_group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_registry_last_exchange_update_at ON order_registry(last_exchange_update_at)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create order registry index: %w", err)
		}
	}
	return nil
}

// GetByKeys loads an order registry row by the strongest available keys.
func (s *OrderRegistryStore) GetByKeys(traderID, exchangeOrderID, clientOrderID, localIntentID string) (*OrderRegistry, error) {
	var row OrderRegistry
	query := s.db.Where("trader_id = ?", traderID)
	switch {
	case exchangeOrderID != "":
		query = query.Where("exchange_order_id = ?", exchangeOrderID)
	case clientOrderID != "":
		query = query.Where("client_order_id = ?", clientOrderID)
	case localIntentID != "":
		query = query.Where("local_intent_id = ?", localIntentID)
	default:
		return nil, nil
	}

	err := query.First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get order registry row: %w", err)
	}
	return &row, nil
}

// ListByTraderID returns all registry rows for a trader ordered by latest exchange update.
func (s *OrderRegistryStore) ListByTraderID(traderID string) ([]*OrderRegistry, error) {
	var rows []*OrderRegistry
	err := s.db.Where("trader_id = ?", traderID).
		Order("last_exchange_update_at DESC, updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list order registry rows: %w", err)
	}
	return rows, nil
}

// ListByTraderSymbol returns registry rows for one trader and symbol.
func (s *OrderRegistryStore) ListByTraderSymbol(traderID, symbol string) ([]*OrderRegistry, error) {
	normalizedSymbol := normalizeAggregateSymbol(symbol)
	var rows []*OrderRegistry
	err := s.db.Where("trader_id = ? AND symbol = ?", traderID, normalizedSymbol).
		Order("last_exchange_update_at DESC, updated_at DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list order registry rows for symbol: %w", err)
	}
	return rows, nil
}

// ListByTraderSymbolSide returns registry rows for one trader/symbol/side scope.
// It is a store-level truth query, not an execution permit.
func (s *OrderRegistryStore) ListByTraderSymbolSide(traderID, symbol, side string) ([]*OrderRegistry, error) {
	rows, err := s.ListByTraderSymbol(traderID, symbol)
	if err != nil {
		return nil, err
	}

	normalizedSide := normalizeAggregateSide(side)
	if normalizedSide == "" {
		return rows, nil
	}

	filtered := make([]*OrderRegistry, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		if normalizeAggregateSide(row.Side) == normalizedSide {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

// ReplaceForTrader replaces all rows for one trader with the provided registry snapshot.
// It is used only by recovery/bootstrap flows so the store keeps one truth snapshot.
func (s *OrderRegistryStore) ReplaceForTrader(traderID string, rows []*OrderRegistry) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("trader_id = ?", traderID).Delete(&OrderRegistry{}).Error; err != nil {
			return fmt.Errorf("failed to clear order registry rows: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		if err := tx.CreateInBatches(rows, len(rows)).Error; err != nil {
			return fmt.Errorf("failed to store order registry rows: %w", err)
		}
		return nil
	})
}

// Upsert stores or updates a registry row using the strongest available correlation keys.
func (s *OrderRegistryStore) Upsert(row *OrderRegistry) error {
	if row == nil {
		return fmt.Errorf("order registry row is nil")
	}

	row.Symbol = normalizeAggregateSymbol(row.Symbol)
	row.Side = normalizeAggregateSide(row.Side)
	row.PositionSideMode = normalizePositionSideMode(row.PositionSideMode)
	row.OrderRole = normalizeOrderRegistryRole(row.OrderRole)
	row.Status = normalizeOrderRegistryStatus(row.Status)
	if row.Source == "" {
		row.Source = "unknown"
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		existing, err := s.findExisting(tx, row.TraderID, row.ExchangeOrderID, row.ClientOrderID, row.LocalIntentID)
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

func (s *OrderRegistryStore) findExisting(tx *gorm.DB, traderID, exchangeOrderID, clientOrderID, localIntentID string) (*OrderRegistry, error) {
	var row OrderRegistry
	query := tx.Where("trader_id = ?", traderID)
	switch {
	case exchangeOrderID != "":
		query = query.Where("exchange_order_id = ?", exchangeOrderID)
	case clientOrderID != "":
		query = query.Where("client_order_id = ?", clientOrderID)
	case localIntentID != "":
		query = query.Where("local_intent_id = ?", localIntentID)
	default:
		return nil, nil
	}

	err := query.First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find existing order registry row: %w", err)
	}
	return &row, nil
}

// SummarizeForTraderSymbol returns the system truth summary for a trader/symbol scope.
func (s *OrderRegistryStore) SummarizeForTraderSymbol(traderID, symbol string) (*OrderRegistrySummary, error) {
	rows, err := s.ListByTraderSymbol(traderID, symbol)
	if err != nil {
		return nil, err
	}
	return summarizeOrderRegistryRows(traderID, normalizeAggregateSymbol(symbol), rows), nil
}

// SummarizeForTraderSymbolSide returns the system truth summary for a trader/symbol/side scope.
func (s *OrderRegistryStore) SummarizeForTraderSymbolSide(traderID, symbol, side string) (*OrderRegistrySummary, error) {
	rows, err := s.ListByTraderSymbol(traderID, symbol)
	if err != nil {
		return nil, err
	}
	normalizedSide := normalizeAggregateSide(side)
	filtered := make([]*OrderRegistry, 0, len(rows))
	for _, row := range rows {
		if normalizedSide == "" || normalizeAggregateSide(row.Side) == normalizedSide {
			filtered = append(filtered, row)
		}
	}
	return summarizeOrderRegistryRows(traderID, normalizeAggregateSymbol(symbol), filtered), nil
}

// SummarizeForTrader returns a sorted list of symbol/side summaries for a trader.
func (s *OrderRegistryStore) SummarizeForTrader(traderID string) ([]*OrderRegistrySummary, error) {
	rows, err := s.ListByTraderID(traderID)
	if err != nil {
		return nil, err
	}

	type summaryKey struct {
		symbol string
		side   string
	}

	summaryMap := make(map[summaryKey][]*OrderRegistry)
	orderKeys := make([]summaryKey, 0)
	for _, row := range rows {
		key := summaryKey{symbol: normalizeAggregateSymbol(row.Symbol), side: normalizeAggregateSide(row.Side)}
		if _, exists := summaryMap[key]; !exists {
			orderKeys = append(orderKeys, key)
		}
		summaryMap[key] = append(summaryMap[key], row)
	}

	sort.Slice(orderKeys, func(i, j int) bool {
		if orderKeys[i].symbol == orderKeys[j].symbol {
			return orderKeys[i].side < orderKeys[j].side
		}
		return orderKeys[i].symbol < orderKeys[j].symbol
	})

	result := make([]*OrderRegistrySummary, 0, len(orderKeys))
	for _, key := range orderKeys {
		result = append(result, summarizeOrderRegistryRows(traderID, key.symbol, summaryMap[key]))
	}
	return result, nil
}

func summarizeOrderRegistryRows(traderID, symbol string, rows []*OrderRegistry) *OrderRegistrySummary {
	summary := &OrderRegistrySummary{
		TraderID:         traderID,
		Symbol:           symbol,
		HasWorkingOrders: false,
		WorkingOrderIDs:  []string{},
		PendingOrderIDs:  []string{},
	}

	if len(rows) == 0 {
		return summary
	}

	side := ""
	for _, row := range rows {
		if row == nil {
			continue
		}
		if side == "" && normalizeAggregateSide(row.Side) != "" {
			side = normalizeAggregateSide(row.Side)
		}
		if row.LastExchangeUpdateAt.After(summary.LastExchangeUpdateAt) {
			summary.LastExchangeUpdateAt = row.LastExchangeUpdateAt
		}
		if !row.IsWorking {
			continue
		}

		impact := classifyOrderRegistryImpact(row)
		summary.HasWorkingOrders = true
		summary.WorkingOrderCount++
		if row.ExchangeOrderID != "" {
			summary.WorkingOrderIDs = append(summary.WorkingOrderIDs, row.ExchangeOrderID)
		} else if row.ClientOrderID != "" {
			summary.PendingOrderIDs = append(summary.PendingOrderIDs, row.ClientOrderID)
		}
		summary.PendingAddQty += impact.AddQty
		summary.PendingReduceQty += impact.ReduceQty
		summary.ProtectionCoverageQty += impact.ProtectionQty
		if impact.CancelReplace {
			summary.HasPendingCancelReplace = true
		}
	}

	summary.Side = side
	summary.ExecutionEligible = len(rows) > 0
	if summary.PendingReduceQty < 0 {
		summary.PendingReduceQty = 0
	}
	if summary.PendingAddQty < 0 {
		summary.PendingAddQty = 0
	}
	return summary
}

// classifyOrderRegistryImpact converts a single registry row into add/reduce/protection impact.
func classifyOrderRegistryImpact(row *OrderRegistry) registryImpact {
	if row == nil {
		return registryImpact{}
	}

	remaining := row.RemainingQty
	if remaining <= 0 {
		return registryImpact{}
	}

	role := normalizeOrderRegistryRole(row.OrderRole)
	impact := registryImpact{}
	switch role {
	case "reduce":
		impact.ReduceQty = remaining
	case "stop_loss", "take_profit", "trailing_protection":
		impact.ReduceQty = remaining
		impact.ProtectionQty = remaining
	case "cancel_replace":
		impact.ReduceQty = remaining
		impact.ProtectionQty = remaining
		impact.CancelReplace = true
	default:
		if row.ReduceOnly || row.ClosePosition {
			impact.ReduceQty = remaining
			impact.ProtectionQty = remaining
		} else {
			impact.AddQty = remaining
		}
	}

	if row.ReduceOnly || row.ClosePosition {
		if impact.ReduceQty == 0 {
			impact.ReduceQty = remaining
		}
	}

	return impact
}

type registryImpact struct {
	AddQty        float64
	ReduceQty     float64
	ProtectionQty float64
	CancelReplace bool
}

func normalizePositionSideMode(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "", "one_way", "one-way":
		return "one_way"
	case "hedge", "dual_side", "dual-side":
		return "hedge"
	default:
		return normalized
	}
}

func normalizeOrderRegistryRole(role string) string {
	normalized := strings.ToLower(strings.TrimSpace(role))
	switch normalized {
	case "", "entry":
		return "entry"
	case "reduce", "stop_loss", "take_profit", "trailing_protection", "cancel_replace":
		return normalized
	default:
		return "entry"
	}
}

func normalizeOrderRegistryStatus(status string) string {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	switch normalized {
	case "":
		return "NEW"
	case "NEW", "PARTIALLY_FILLED", "FILLED", "CANCELED", "EXPIRED", "REJECTED", "PENDING_CANCEL", "PENDING_NEW", "WORKING", "OPEN", "PENDING_REPLACE", "REPLACED":
		return normalized
	default:
		return normalized
	}
}

// NormalizeOrderRegistryRole exposes the registry role normalizer for service-layer reconciliation logic.
func NormalizeOrderRegistryRole(role string) string {
	return normalizeOrderRegistryRole(role)
}

// NormalizeOrderRegistryStatus exposes the registry status normalizer for service-layer reconciliation logic.
func NormalizeOrderRegistryStatus(status string) string {
	return normalizeOrderRegistryStatus(status)
}

// NormalizeOneWaySide exposes the one-way side normalizer for service-layer reconciliation logic.
func NormalizeOneWaySide(side string) string {
	return normalizeAggregateSide(side)
}
