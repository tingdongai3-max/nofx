package trader

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

// fixedProtectionAlgoOrderClient submits fixed protection algo orders with caller-provided correlation ids.
// Binance implements this interface so the protection truth layer can keep one deterministic correlation key.
type fixedProtectionAlgoOrderClient interface {
	CreateStopLossOrder(symbol, positionSide string, quantity, stopPrice float64, clientAlgoID string) (string, error)
	CreateTakeProfitOrder(symbol, positionSide string, quantity, takeProfitPrice float64, clientAlgoID string) (string, error)
}

// protectionCancelClient is the minimum exchange capability needed by the fixed protection manager.
// It is an execution-side dependency, not a protection truth layer.
type protectionCancelClient interface {
	CancelStopLossOrders(symbol string) error
	CancelTakeProfitOrders(symbol string) error
}

// FixedProtectionPlan is the runtime-fixed stop-loss / take-profit attach plan.
// It is a service-layer execution plan, not a persisted truth row.
type FixedProtectionPlan struct {
	TraderID                string    // System-owned trader key for this attach plan.
	Symbol                  string    // Truth-layer symbol scope.
	Side                    string    // One-way side: LONG or SHORT.
	LinkedPositionKey       string    // System-owned position correlation key.
	ProtectionGroupID       string    // System-generated protection group identifier.
	ProtectionMode          string    // Protection policy label; current phase only allows fixed.
	StopLossOrderIntentID   string    // System-local intent id for the stop-loss leg.
	TakeProfitOrderIntentID string    // System-local intent id for the take-profit leg.
	StopLossTriggerPrice    float64   // Calculated stop-loss trigger price.
	TakeProfitTriggerPrice  float64   // Calculated take-profit trigger price.
	ProtectedQuantity       float64   // Quantity covered by this fixed protection attach.
	SourceDecisionCycle     int       // AI decision cycle used as the price source.
	SourceDecisionTime      time.Time // AI decision timestamp used as the price source.
	EntryPrice              float64   // Best-effort entry price used for debug output.
}

// FixedProtectionManager attaches and records Binance-only fixed protection orders.
// It centralizes protection price selection, order registration, and group persistence.
type FixedProtectionManager struct {
	store          *store.Store
	exchange       protectionCancelClient
	algoClient     fixedProtectionAlgoOrderClient
	protectionBld  *store.ProtectionGroupBuilder
	orderRegistryB *store.OrderRegistryBuilder
}

// NewFixedProtectionManager creates a fixed protection manager.
func NewFixedProtectionManager(st *store.Store, client Trader) *FixedProtectionManager {
	var algoClient fixedProtectionAlgoOrderClient
	if typed, ok := client.(fixedProtectionAlgoOrderClient); ok {
		algoClient = typed
	}
	var cancelClient protectionCancelClient
	if typed, ok := client.(protectionCancelClient); ok {
		cancelClient = typed
	}
	return &FixedProtectionManager{
		store:          st,
		exchange:       cancelClient,
		algoClient:     algoClient,
		protectionBld:  st.ProtectionGroupBuilder(),
		orderRegistryB: st.OrderRegistryBuilder(),
	}
}

// EnsureFixedProtectionForAggregate ensures a fixed protection pair exists for the provided position aggregate.
// It is idempotent: an armed or cancel-pending protection group is left unchanged.
func (m *FixedProtectionManager) EnsureFixedProtectionForAggregate(aggregate *store.PositionAggregate, profile *store.StrategyProfile) (*store.ProtectionGroup, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("fixed protection manager store is not configured")
	}
	if aggregate == nil {
		return nil, fmt.Errorf("position aggregate is required")
	}
	if aggregate.TotalQty <= 0 {
		return nil, fmt.Errorf("position aggregate has no quantity to protect")
	}
	if m.algoClient == nil {
		return nil, fmt.Errorf("binance fixed protection client is not configured")
	}

	if profile == nil {
		var err error
		profile, err = m.store.StrategyProfile().GetByTraderID(aggregate.TraderID)
		if err != nil {
			return nil, err
		}
	}
	if profile == nil {
		return nil, fmt.Errorf("strategy profile is required for fixed protection attach")
	}
	if !profile.ExecutionEnabled {
		return nil, fmt.Errorf("strategy profile is not execution-enabled")
	}

	summary, err := m.store.ProtectionGroup().SummarizeForTraderSymbolSide(aggregate.TraderID, aggregate.Symbol, aggregate.Side)
	if err != nil {
		return nil, err
	}
	var existingGroup *store.ProtectionGroup
	if summary != nil && summary.ProtectionGroupID != "" {
		existingGroup, err = m.store.ProtectionGroup().GetByKeys(aggregate.TraderID, summary.ProtectionGroupID, summary.LinkedPositionKey)
		if err != nil {
			return nil, err
		}
	}

	if summary != nil && summary.HasProtection {
		switch summary.ProtectionGroupStatus {
		case "armed", "cancel_pending", "partial_invalid":
			return existingGroup, nil
		case "pending_attach":
			if summary.StopLossArmed && summary.TakeProfitArmed {
				return existingGroup, nil
			}
		}
	}

	plan, err := m.buildFixedProtectionPlan(aggregate, profile, existingGroup)
	if err != nil {
		return nil, err
	}
	logger.Infof("fixed protection plan built: trader=%s symbol=%s side=%s group=%s qty=%.6f stop=%.6f take=%.6f",
		plan.TraderID, plan.Symbol, plan.Side, plan.ProtectionGroupID, plan.ProtectedQuantity, plan.StopLossTriggerPrice, plan.TakeProfitTriggerPrice)

	group, err := m.recordFixedProtectionGroup(plan, "pending_attach", "fixed_protection_manager", "PROTECTION_GROUP_CREATED")
	if err != nil {
		return nil, err
	}
	logger.Infof("protection group created: trader=%s symbol=%s side=%s group=%s status=%s",
		group.TraderID, group.Symbol, group.Side, group.ProtectionGroupID, group.Status)

	stopIntentID, err := m.algoClient.CreateStopLossOrder(plan.Symbol, plan.Side, plan.ProtectedQuantity, plan.StopLossTriggerPrice, plan.StopLossOrderIntentID)
	if err != nil {
		if m.exchange != nil {
			_ = m.exchange.CancelStopLossOrders(plan.Symbol)
		}
		if _, recErr := m.recordFixedProtectionGroup(plan, "partial_invalid", "fixed_protection_manager", "PROTECTION_GROUP_FAILED"); recErr != nil {
			logger.Infof("⚠️ Failed to record partial protection failure: %v", recErr)
		}
		return nil, err
	}

	takeIntentID, err := m.algoClient.CreateTakeProfitOrder(plan.Symbol, plan.Side, plan.ProtectedQuantity, plan.TakeProfitTriggerPrice, plan.TakeProfitOrderIntentID)
	if err != nil {
		if m.exchange != nil {
			_ = m.exchange.CancelStopLossOrders(plan.Symbol)
		}
		_, recErr := m.recordFixedProtectionGroup(plan, "partial_invalid", "fixed_protection_manager", "PROTECTION_GROUP_FAILED")
		if recErr != nil {
			logger.Infof("⚠️ Failed to record partial protection failure: %v", recErr)
		}
		return nil, err
	}

	if _, err := m.orderRegistryB.ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          plan.TraderID,
		Symbol:            plan.Symbol,
		Side:              plan.Side,
		PositionSideMode:  "one_way",
		OrderRole:         "stop_loss",
		LocalIntentID:     stopIntentID,
		LinkedGroupID:     plan.ProtectionGroupID,
		LinkedPositionKey: plan.LinkedPositionKey,
		ClientOrderID:     stopIntentID,
		OrderType:         "STOP_MARKET",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     true,
		OrigQty:           plan.ProtectedQuantity,
		ExecutedQty:       0,
		TriggerPrice:      plan.StopLossTriggerPrice,
		Status:            "NEW",
		Source:            "fixed_protection_manager",
		EventType:         "FIXED_PROTECTION_ATTACH",
		EventSource:       "fixed_protection_manager",
		PayloadJSON:       mustJSON(map[string]interface{}{"leg": "stop_loss", "group_id": plan.ProtectionGroupID, "quantity": plan.ProtectedQuantity, "trigger_price": plan.StopLossTriggerPrice}),
		EventTime:         time.Now().UTC(),
	}); err != nil {
		if m.exchange != nil {
			_ = m.exchange.CancelStopLossOrders(plan.Symbol)
			_ = m.exchange.CancelTakeProfitOrders(plan.Symbol)
		}
		return nil, err
	}

	if _, err := m.orderRegistryB.ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          plan.TraderID,
		Symbol:            plan.Symbol,
		Side:              plan.Side,
		PositionSideMode:  "one_way",
		OrderRole:         "take_profit",
		LocalIntentID:     takeIntentID,
		LinkedGroupID:     plan.ProtectionGroupID,
		LinkedPositionKey: plan.LinkedPositionKey,
		ClientOrderID:     takeIntentID,
		OrderType:         "TAKE_PROFIT_MARKET",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     true,
		OrigQty:           plan.ProtectedQuantity,
		ExecutedQty:       0,
		TriggerPrice:      plan.TakeProfitTriggerPrice,
		Status:            "NEW",
		Source:            "fixed_protection_manager",
		EventType:         "FIXED_PROTECTION_ATTACH",
		EventSource:       "fixed_protection_manager",
		PayloadJSON:       mustJSON(map[string]interface{}{"leg": "take_profit", "group_id": plan.ProtectionGroupID, "quantity": plan.ProtectedQuantity, "trigger_price": plan.TakeProfitTriggerPrice}),
		EventTime:         time.Now().UTC(),
	}); err != nil {
		if m.exchange != nil {
			_ = m.exchange.CancelStopLossOrders(plan.Symbol)
			_ = m.exchange.CancelTakeProfitOrders(plan.Symbol)
		}
		return nil, err
	}

	group, err = m.recordFixedProtectionGroup(plan, "armed", "fixed_protection_manager", "FIXED_PROTECTION_ORDERS_ATTACHED")
	if err != nil {
		return nil, err
	}
	logger.Infof("fixed protection orders attached: trader=%s symbol=%s side=%s group=%s stop_intent=%s take_intent=%s",
		plan.TraderID, plan.Symbol, plan.Side, plan.ProtectionGroupID, stopIntentID, takeIntentID)

	updated, err := m.store.ProtectionGroup().GetByKeys(plan.TraderID, plan.ProtectionGroupID, plan.LinkedPositionKey)
	if err != nil {
		return nil, err
	}
	if updated != nil {
		return updated, nil
	}
	return group, nil
}

// RebalanceFixedProtectionForAggregate refreshes an existing fixed protection pair to match the current position size.
// It keeps the protection mode fixed and only rebalances quantity, not price.
func (m *FixedProtectionManager) RebalanceFixedProtectionForAggregate(aggregate *store.PositionAggregate, profile *store.StrategyProfile) (*store.ProtectionGroup, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("fixed protection manager store is not configured")
	}
	if aggregate == nil {
		return nil, fmt.Errorf("position aggregate is required")
	}
	if aggregate.TotalQty <= 0 {
		return nil, fmt.Errorf("position aggregate has no quantity to protect")
	}
	if m.algoClient == nil {
		return nil, fmt.Errorf("binance fixed protection client is not configured")
	}

	if profile == nil {
		var err error
		profile, err = m.store.StrategyProfile().GetByTraderID(aggregate.TraderID)
		if err != nil {
			return nil, err
		}
	}
	if profile == nil {
		return nil, fmt.Errorf("strategy profile is required for fixed protection rebalance")
	}

	summary, err := m.store.ProtectionGroup().SummarizeForTraderSymbolSide(aggregate.TraderID, aggregate.Symbol, aggregate.Side)
	if err != nil {
		return nil, err
	}
	var existingGroup *store.ProtectionGroup
	if summary != nil && summary.ProtectionGroupID != "" {
		existingGroup, err = m.store.ProtectionGroup().GetByKeys(aggregate.TraderID, summary.ProtectionGroupID, summary.LinkedPositionKey)
		if err != nil {
			return nil, err
		}
	}

	if summary != nil && summary.HasProtection && math.Abs(summary.ProtectedQuantity-aggregate.TotalQty) < 0.000001 && summary.ProtectionGroupStatus == "armed" {
		return existingGroup, nil
	}

	plan, err := m.buildFixedProtectionPlan(aggregate, profile, existingGroup)
	if err != nil {
		return nil, err
	}
	plan.ProtectedQuantity = aggregate.TotalQty
	logger.Infof("fixed protection plan built: trader=%s symbol=%s side=%s group=%s qty=%.6f stop=%.6f take=%.6f",
		plan.TraderID, plan.Symbol, plan.Side, plan.ProtectionGroupID, plan.ProtectedQuantity, plan.StopLossTriggerPrice, plan.TakeProfitTriggerPrice)

	if summary != nil && summary.HasProtection {
		_, _ = m.recordFixedProtectionGroup(plan, "cancel_pending", "fixed_protection_manager", "PROTECTION_QTY_REBALANCE_REQUESTED")
		if m.exchange != nil {
			_ = m.exchange.CancelStopLossOrders(plan.Symbol)
			_ = m.exchange.CancelTakeProfitOrders(plan.Symbol)
		}
	}

	stopIntentID, err := m.algoClient.CreateStopLossOrder(plan.Symbol, plan.Side, plan.ProtectedQuantity, plan.StopLossTriggerPrice, plan.StopLossOrderIntentID)
	if err != nil {
		if m.exchange != nil {
			_ = m.exchange.CancelStopLossOrders(plan.Symbol)
		}
		if _, recErr := m.recordFixedProtectionGroup(plan, "partial_invalid", "fixed_protection_manager", "PROTECTION_GROUP_FAILED"); recErr != nil {
			logger.Infof("⚠️ Failed to record partial protection failure: %v", recErr)
		}
		return nil, err
	}

	takeIntentID, err := m.algoClient.CreateTakeProfitOrder(plan.Symbol, plan.Side, plan.ProtectedQuantity, plan.TakeProfitTriggerPrice, plan.TakeProfitOrderIntentID)
	if err != nil {
		if m.exchange != nil {
			_ = m.exchange.CancelStopLossOrders(plan.Symbol)
		}
		_, recErr := m.recordFixedProtectionGroup(plan, "partial_invalid", "fixed_protection_manager", "PROTECTION_GROUP_FAILED")
		if recErr != nil {
			logger.Infof("⚠️ Failed to record partial protection failure: %v", recErr)
		}
		return nil, err
	}

	if _, err := m.orderRegistryB.ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          plan.TraderID,
		Symbol:            plan.Symbol,
		Side:              plan.Side,
		PositionSideMode:  "one_way",
		OrderRole:         "stop_loss",
		LocalIntentID:     stopIntentID,
		LinkedGroupID:     plan.ProtectionGroupID,
		LinkedPositionKey: plan.LinkedPositionKey,
		ClientOrderID:     stopIntentID,
		OrderType:         "STOP_MARKET",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     true,
		OrigQty:           plan.ProtectedQuantity,
		ExecutedQty:       0,
		TriggerPrice:      plan.StopLossTriggerPrice,
		Status:            "NEW",
		Source:            "fixed_protection_manager",
		EventType:         "FIXED_PROTECTION_QTY_REBALANCED",
		EventSource:       "fixed_protection_manager",
		PayloadJSON:       mustJSON(map[string]interface{}{"leg": "stop_loss", "group_id": plan.ProtectionGroupID, "quantity": plan.ProtectedQuantity, "trigger_price": plan.StopLossTriggerPrice}),
		EventTime:         time.Now().UTC(),
	}); err != nil {
		return nil, err
	}

	if _, err := m.orderRegistryB.ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          plan.TraderID,
		Symbol:            plan.Symbol,
		Side:              plan.Side,
		PositionSideMode:  "one_way",
		OrderRole:         "take_profit",
		LocalIntentID:     takeIntentID,
		LinkedGroupID:     plan.ProtectionGroupID,
		LinkedPositionKey: plan.LinkedPositionKey,
		ClientOrderID:     takeIntentID,
		OrderType:         "TAKE_PROFIT_MARKET",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     true,
		OrigQty:           plan.ProtectedQuantity,
		ExecutedQty:       0,
		TriggerPrice:      plan.TakeProfitTriggerPrice,
		Status:            "NEW",
		Source:            "fixed_protection_manager",
		EventType:         "FIXED_PROTECTION_QTY_REBALANCED",
		EventSource:       "fixed_protection_manager",
		PayloadJSON:       mustJSON(map[string]interface{}{"leg": "take_profit", "group_id": plan.ProtectionGroupID, "quantity": plan.ProtectedQuantity, "trigger_price": plan.TakeProfitTriggerPrice}),
		EventTime:         time.Now().UTC(),
	}); err != nil {
		return nil, err
	}

	group, err := m.recordFixedProtectionGroup(plan, "armed", "fixed_protection_manager", "FIXED_PROTECTION_QTY_REBALANCED")
	if err != nil {
		return nil, err
	}
	logger.Infof("protection quantity rebalanced: trader=%s symbol=%s side=%s group=%s protected_qty=%.6f",
		group.TraderID, group.Symbol, group.Side, group.ProtectionGroupID, group.ProtectedQuantity)

	updated, err := m.store.ProtectionGroup().GetByKeys(plan.TraderID, plan.ProtectionGroupID, plan.LinkedPositionKey)
	if err != nil {
		return nil, err
	}
	if updated != nil {
		return updated, nil
	}
	return group, nil
}

func (m *FixedProtectionManager) buildFixedProtectionPlan(aggregate *store.PositionAggregate, profile *store.StrategyProfile, existingGroup *store.ProtectionGroup) (*FixedProtectionPlan, error) {
	if aggregate == nil {
		return nil, fmt.Errorf("position aggregate is required")
	}
	if aggregate.TotalQty <= 0 {
		return nil, fmt.Errorf("position aggregate has no quantity to protect")
	}
	if profile != nil {
		if !strings.EqualFold(profile.Exchange, "binance_usdm") {
			return nil, fmt.Errorf("strategy profile is outside the binance_usdm scope")
		}
		if !strings.EqualFold(profile.Mode, "one_way") {
			return nil, fmt.Errorf("strategy profile is outside one_way mode")
		}
	}
	decision, record, err := m.findLatestOpenDecisionAction(aggregate.TraderID, aggregate.Symbol, aggregate.Side)
	if err != nil {
		return nil, err
	}
	if decision == nil {
		return nil, fmt.Errorf("no open decision action with fixed protection prices found for %s %s", aggregate.Symbol, aggregate.Side)
	}
	if decision.StopLoss <= 0 || decision.TakeProfit <= 0 {
		return nil, fmt.Errorf("decision action does not contain valid fixed protection prices for %s %s", aggregate.Symbol, aggregate.Side)
	}
	decisionCycle := 0
	var decisionTime time.Time
	if record != nil {
		decisionCycle = record.CycleNumber
		decisionTime = record.Timestamp
	}

	groupID := generateProtectionGroupID(aggregate.TraderID, aggregate.Symbol, aggregate.Side)
	stopIntentID := generateProtectionIntentID(groupID, "sl")
	takeIntentID := generateProtectionIntentID(groupID, "tp")
	if existingGroup != nil {
		if trimmed := strings.TrimSpace(existingGroup.ProtectionGroupID); trimmed != "" {
			groupID = trimmed
		}
		if trimmed := strings.TrimSpace(existingGroup.StopLossOrderIntentID); trimmed != "" {
			stopIntentID = trimmed
		}
		if trimmed := strings.TrimSpace(existingGroup.TakeProfitOrderIntentID); trimmed != "" {
			takeIntentID = trimmed
		}
	}

	return &FixedProtectionPlan{
		TraderID:                aggregate.TraderID,
		Symbol:                  aggregate.Symbol,
		Side:                    aggregate.Side,
		LinkedPositionKey:       protectionPositionKey(aggregate.Symbol, aggregate.Side),
		ProtectionGroupID:       groupID,
		ProtectionMode:          "fixed",
		StopLossOrderIntentID:   stopIntentID,
		TakeProfitOrderIntentID: takeIntentID,
		StopLossTriggerPrice:    decision.StopLoss,
		TakeProfitTriggerPrice:  decision.TakeProfit,
		ProtectedQuantity:       aggregate.TotalQty,
		SourceDecisionCycle:     decisionCycle,
		SourceDecisionTime:      decisionTime,
		EntryPrice:              aggregate.AvgEntryPrice,
	}, nil
}

func (m *FixedProtectionManager) findLatestOpenDecisionAction(traderID, symbol, side string) (*store.DecisionAction, *store.DecisionRecord, error) {
	records, err := m.store.Decision().GetLatestRecords(traderID, 50)
	if err != nil {
		return nil, nil, err
	}

	normalizedSymbol := strings.ToUpper(strings.TrimSpace(symbol))
	normalizedSide := normalizeOneWaySide(side)
	targetActions := []string{}
	switch normalizedSide {
	case "LONG":
		targetActions = []string{"open_long"}
	case "SHORT":
		targetActions = []string{"open_short"}
	default:
		targetActions = []string{"open_long", "open_short"}
	}

	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if record == nil {
			continue
		}
		for j := len(record.Decisions) - 1; j >= 0; j-- {
			decision := record.Decisions[j]
			if !strings.EqualFold(strings.TrimSpace(decision.Symbol), normalizedSymbol) {
				continue
			}
			if decision.Action == "" {
				continue
			}
			matched := false
			for _, target := range targetActions {
				if strings.EqualFold(decision.Action, target) {
					matched = true
					break
				}
			}
			if !matched || decision.StopLoss <= 0 || decision.TakeProfit <= 0 {
				continue
			}
			return &decision, record, nil
		}
	}

	return nil, nil, nil
}

func (m *FixedProtectionManager) recordFixedProtectionGroup(plan *FixedProtectionPlan, status, source, eventType string) (*store.ProtectionGroup, error) {
	return m.protectionBld.ApplyProtectionEvent(&store.ProtectionGroupEventInput{
		TraderID:                plan.TraderID,
		Symbol:                  plan.Symbol,
		Side:                    plan.Side,
		LinkedPositionKey:       plan.LinkedPositionKey,
		ProtectionGroupID:       plan.ProtectionGroupID,
		ProtectionMode:          plan.ProtectionMode,
		StopLossOrderIntentID:   plan.StopLossOrderIntentID,
		TakeProfitOrderIntentID: plan.TakeProfitOrderIntentID,
		StopLossTriggerPrice:    plan.StopLossTriggerPrice,
		TakeProfitTriggerPrice:  plan.TakeProfitTriggerPrice,
		ProtectedQuantity:       plan.ProtectedQuantity,
		Status:                  status,
		Source:                  source,
		EventType:               eventType,
		EventSource:             source,
		PayloadJSON:             mustJSON(plan),
		EventTime:               time.Now().UTC(),
	})
}

func generateProtectionGroupID(traderID, symbol, side string) string {
	return generateProtectionID("pg")
}

func generateProtectionIntentID(groupID, suffix string) string {
	trimmedGroup := strings.TrimSpace(groupID)
	if trimmedGroup == "" {
		trimmedGroup = generateProtectionID("pg")
	}
	intent := trimmedGroup + "-" + suffix
	if len(intent) > 32 {
		intent = intent[:32]
	}
	return intent
}

func generateProtectionID(prefix string) string {
	timestamp := time.Now().UnixNano() % 10000000000000
	randomBytes := make([]byte, 4)
	_, _ = rand.Read(randomBytes)
	randomHex := hex.EncodeToString(randomBytes)
	id := fmt.Sprintf("%s-%d%s", prefix, timestamp, randomHex)
	if len(id) > 32 {
		id = id[:32]
	}
	return id
}

func protectionPositionKey(symbol, side string) string {
	return normalizeCapabilitySymbol(symbol) + "|" + normalizeOneWaySide(side)
}

func mustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
