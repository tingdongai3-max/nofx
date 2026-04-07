package trader

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
	"nofx/trader/types"
)

// ScaleInPlanConfig is the execution-side configuration for a fixed-qty / fixed-ratio same-symbol add plan.
// It is a service-layer input, not a persisted truth row.
type ScaleInPlanConfig struct {
	FixedQty     float64   // Absolute add quantity for the plan when fixed_qty is used.
	FixedRatio   float64   // Relative add quantity ratio against the current open position when fixed_ratio is used.
	LevelRatios  []float64 // Relative quantity split per level; normalized to 1.0 by the manager.
	TargetPrices []float64 // Optional target prices per level when limit_price is used.
	TargetType   string    // Level target type: market or limit_price.
	Leverage     int       // Leverage applied to the add order(s); defaults to 1 when omitted.
}

// scaleInTrader is the minimal execution surface required by the scale-in manager.
// It is intentionally narrower than the full exchange interface because add-position support is optional.
type scaleInTrader interface {
	SetLeverage(symbol string, leverage int) error
	AddLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error)
	AddShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error)
	PlaceLimitOrder(req *types.LimitOrderRequest) (*types.LimitOrderResult, error)
}

// ScaleInManager attaches and records Binance-only same-symbol add-position plans.
// It centralizes plan construction, risk checking, order attachment, and truth-layer recording.
type ScaleInManager struct {
	store                *store.Store
	trader               scaleInTrader
	orderRegistryBuilder *store.OrderRegistryBuilder
	scaleInPlanBuilder   *store.ScaleInPlanBuilder
	riskGuard            *ScaleInRiskGuard
}

// NewScaleInManager creates a new scale-in manager.
func NewScaleInManager(st *store.Store, client Trader, riskGuard *ScaleInRiskGuard) *ScaleInManager {
	var typedTrader scaleInTrader
	if client != nil {
		if candidate, ok := client.(scaleInTrader); ok {
			typedTrader = candidate
		}
	}
	if st == nil {
		return &ScaleInManager{
			trader:    typedTrader,
			riskGuard: riskGuard,
		}
	}
	return &ScaleInManager{
		store:                st,
		trader:               typedTrader,
		orderRegistryBuilder: st.OrderRegistryBuilder(),
		scaleInPlanBuilder:   st.ScaleInPlanBuilder(),
		riskGuard:            riskGuard,
	}
}

// EnsureScaleInPlanForAggregate creates or reuses a same-symbol add-position plan for one position.
// It is idempotent: an active plan is reused instead of duplicated.
func (m *ScaleInManager) EnsureScaleInPlanForAggregate(aggregate *store.PositionAggregate, profile *store.StrategyProfile, cfg *ScaleInPlanConfig, accountEquity float64) (*store.ScaleInPlan, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("scale-in manager store is not configured")
	}
	if aggregate == nil {
		return nil, fmt.Errorf("position aggregate is required")
	}
	if aggregate.TotalQty <= 0 {
		return nil, fmt.Errorf("position aggregate has no quantity to scale in")
	}
	if cfg == nil {
		return nil, fmt.Errorf("scale-in config is required")
	}
	if profile == nil {
		var err error
		profile, err = m.store.StrategyProfile().GetByTraderID(aggregate.TraderID)
		if err != nil {
			return nil, err
		}
	}
	if profile == nil {
		return nil, fmt.Errorf("strategy profile is required for scale-in plan creation")
	}
	if !profile.ExecutionEnabled {
		return nil, fmt.Errorf("strategy profile is not execution-enabled")
	}
	if !strings.EqualFold(profile.Exchange, "binance_usdm") || !strings.EqualFold(profile.Mode, "one_way") {
		return nil, fmt.Errorf("strategy profile is outside the binance_usdm one_way scope")
	}
	maxScaleInCount := profile.MaxScaleInCount
	if maxScaleInCount <= 0 && profile.AllowAddPosition {
		maxScaleInCount = 1
	}

	existingSummary, err := m.store.ScaleInPlan().SummarizeForTraderSymbolSide(aggregate.TraderID, aggregate.Symbol, aggregate.Side)
	if err != nil {
		return nil, err
	}
	if existingSummary != nil && existingSummary.HasScaleInPlan && existingSummary.ConsistencyStatus != "mismatch" {
		return m.store.ScaleInPlan().GetByKeys(aggregate.TraderID, existingSummary.ScaleInPlanID, existingSummary.LinkedPositionKey)
	}

	plannedQty := m.estimatePlanQuantity(aggregate, cfg)
	if plannedQty <= 0 {
		return nil, fmt.Errorf("scale-in plan quantity must be greater than 0")
	}

	guard := m.riskGuard
	if guard == nil {
		guard = NewScaleInRiskGuard()
	}
	assessment, err := guard.Assess(&ScaleInRiskRequest{
		StrategyProfile:         profile,
		PositionAggregate:       aggregate,
		ScaleInSummary:          existingSummary,
		ExecutionMode:           "live",
		AllowOrderPlacement:     true,
		UserStreamReady:         true,
		AccountEquity:           accountEquity,
		ProposedAddQty:          plannedQty,
		ProposedAddPrice:        aggregate.AvgEntryPrice,
		HasStateMismatch:        existingSummary != nil && existingSummary.HasStateMismatch,
		HasProtectionMismatch:   false,
		HasPendingCancelReplace: false,
	})
	if err != nil {
		return nil, err
	}
	if !assessment.Allowed {
		return nil, fmt.Errorf("scale-in risk guard blocked plan creation")
	}

	plan, levels, err := m.buildScaleInPlan(aggregate, cfg, plannedQty, maxScaleInCount)
	if err != nil {
		return nil, err
	}

	if _, err := m.recordScaleInPlan(plan, levels, "draft", "scale_in_manager", "SCALE_IN_PLAN_CREATED"); err != nil {
		return nil, err
	}
	logger.Infof("scale in plan built: trader=%s symbol=%s side=%s plan=%s qty=%.6f status=%s", plan.TraderID, plan.Symbol, plan.Side, plan.ScaleInPlanID, plan.TotalPlannedQty, plan.Status)

	attachedLevels, err := m.attachScaleInOrders(plan, levels, cfg)
	if err != nil {
		_, _ = m.recordScaleInPlan(plan, levels, "invalid", "scale_in_manager", "SCALE_IN_PLAN_INVALIDATED")
		return nil, err
	}

	armedPlan, err := m.recordScaleInPlan(plan, attachedLevels, "armed", "scale_in_manager", "SCALE_IN_ORDERS_ATTACHED")
	if err != nil {
		return nil, err
	}
	logger.Infof("scale in orders attached: trader=%s symbol=%s side=%s plan=%s levels=%d", armedPlan.TraderID, armedPlan.Symbol, armedPlan.Side, armedPlan.ScaleInPlanID, len(attachedLevels))
	return armedPlan, nil
}

func (m *ScaleInManager) estimatePlanQuantity(aggregate *store.PositionAggregate, cfg *ScaleInPlanConfig) float64 {
	if aggregate == nil || cfg == nil {
		return 0
	}
	if cfg.FixedQty > 0 {
		return roundScaleInQty(cfg.FixedQty)
	}
	if cfg.FixedRatio > 0 {
		return roundScaleInQty(aggregate.TotalQty * cfg.FixedRatio)
	}
	ratios := normalizeScaleInRatios(cfg.LevelRatios)
	if len(ratios) == 0 {
		return 0
	}
	return roundScaleInQty(aggregate.TotalQty * ratios[0])
}

func (m *ScaleInManager) buildScaleInPlan(aggregate *store.PositionAggregate, cfg *ScaleInPlanConfig, totalQty float64, maxScaleInCount int) (*store.ScaleInPlan, []*store.ScaleInPlanLevel, error) {
	if aggregate == nil {
		return nil, nil, fmt.Errorf("position aggregate is required")
	}
	if cfg == nil {
		return nil, nil, fmt.Errorf("scale-in config is required")
	}

	targetType := normalizeScaleInTargetType(cfg.TargetType)
	ratios := normalizeScaleInRatios(cfg.LevelRatios)
	if len(ratios) == 0 {
		ratios = []float64{1}
	}
	if totalQty <= 0 {
		totalQty = m.estimatePlanQuantity(aggregate, cfg)
	}
	if totalQty <= 0 {
		return nil, nil, fmt.Errorf("scale-in quantity is required")
	}
	targetPrices := normalizeScaleInTargetPrices(cfg.TargetPrices, len(ratios), aggregate.AvgEntryPrice)

	planID := buildScaleInPlanID(aggregate.TraderID, aggregate.Symbol, aggregate.Side)
	plan := &store.ScaleInPlan{
		TraderID:            aggregate.TraderID,
		Symbol:              aggregate.Symbol,
		Side:                aggregate.Side,
		LinkedPositionKey:   scaleInPositionKey(aggregate.Symbol, aggregate.Side),
		ScaleInPlanID:       planID,
		PlanMode:            chooseScaleInPlanMode(cfg, totalQty),
		Status:              "draft",
		TotalPlannedQty:     totalQty,
		RemainingPlannedQty: totalQty,
		ExecutedQty:         0,
		MaxScaleInCount:     maxScaleInCount,
		CurrentScaleInCount: 0,
		Source:              "scale_in_manager",
	}

	remaining := totalQty
	levels := make([]*store.ScaleInPlanLevel, 0, len(ratios))
	for idx, ratio := range ratios {
		plannedQty := roundScaleInQty(totalQty * ratio)
		if idx == len(ratios)-1 {
			plannedQty = roundScaleInQty(remaining)
		}
		if plannedQty > remaining {
			plannedQty = remaining
		}
		remaining -= plannedQty
		if remaining < 0 {
			remaining = 0
		}

		levels = append(levels, &store.ScaleInPlanLevel{
			TraderID:              aggregate.TraderID,
			Symbol:                aggregate.Symbol,
			Side:                  aggregate.Side,
			LinkedPositionKey:     scaleInPositionKey(aggregate.Symbol, aggregate.Side),
			ScaleInPlanID:         planID,
			LevelIndex:            idx + 1,
			TargetType:            targetType,
			TargetPrice:           targetPrices[idx],
			PlannedQty:            plannedQty,
			ExecutedQty:           0,
			RemainingQty:          plannedQty,
			LinkedOrderIntentID:   buildScaleInLevelIntentID(planID, idx+1),
			LinkedExchangeOrderID: "",
			Status:                "draft",
		})
	}

	return plan, levels, nil
}

func (m *ScaleInManager) attachScaleInOrders(plan *store.ScaleInPlan, levels []*store.ScaleInPlanLevel, cfg *ScaleInPlanConfig) ([]*store.ScaleInPlanLevel, error) {
	if plan == nil {
		return nil, fmt.Errorf("scale-in plan is required")
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("scale-in plan has no levels")
	}
	if m.trader == nil {
		return nil, fmt.Errorf("scale-in trader is not configured")
	}

	attached := make([]*store.ScaleInPlanLevel, 0, len(levels))
	for _, level := range levels {
		if level == nil {
			continue
		}
		quantity := level.PlannedQty
		if quantity <= 0 {
			continue
		}

		exchangeOrderID, clientOrderID, status, err := m.placeScaleInOrder(plan, level, cfg)
		if err != nil {
			return nil, err
		}

		level.LinkedExchangeOrderID = exchangeOrderID
		if level.LinkedOrderIntentID == "" {
			level.LinkedOrderIntentID = clientOrderID
		}
		if status == "" {
			status = "NEW"
		}
		level.Status = normalizeScaleInLevelStatus(strings.ToLower(strings.TrimSpace(status)))
		if level.Status == "filled" {
			level.ExecutedQty = quantity
			level.RemainingQty = 0
		} else {
			level.ExecutedQty = 0
			level.RemainingQty = quantity
		}

		if _, err := m.orderRegistryBuilder.ApplyOrderEvent(&store.OrderRegistryEventInput{
			TraderID:          plan.TraderID,
			Symbol:            plan.Symbol,
			Side:              scaleInExchangeSide(plan.Side),
			PositionSideMode:  "one_way",
			OrderRole:         "entry",
			LocalIntentID:     level.LinkedOrderIntentID,
			LinkedGroupID:     plan.ScaleInPlanID,
			LinkedPositionKey: plan.LinkedPositionKey,
			ExchangeOrderID:   exchangeOrderID,
			ClientOrderID:     clientOrderID,
			OrderType:         scaleInOrderType(level.TargetType),
			TimeInForce:       scaleInTimeInForce(level.TargetType),
			ReduceOnly:        false,
			ClosePosition:     false,
			OrigQty:           quantity,
			ExecutedQty:       level.ExecutedQty,
			AvgPrice:          level.TargetPrice,
			TriggerPrice:      level.TargetPrice,
			Status:            scaleInRegistryStatus(status),
			Source:            "scale_in_manager",
			EventType:         "SCALE_IN_LEVEL_ARMED",
			EventSource:       "scale_in_manager",
			PayloadJSON:       mustJSON(map[string]interface{}{"plan_id": plan.ScaleInPlanID, "level_index": level.LevelIndex, "target_type": level.TargetType, "target_price": level.TargetPrice, "planned_qty": quantity}),
			EventTime:         time.Now().UTC(),
		}); err != nil {
			return nil, err
		}

		attached = append(attached, level)
	}

	return attached, nil
}

func (m *ScaleInManager) placeScaleInOrder(plan *store.ScaleInPlan, level *store.ScaleInPlanLevel, cfg *ScaleInPlanConfig) (string, string, string, error) {
	if plan == nil || level == nil {
		return "", "", "", fmt.Errorf("scale-in plan level is required")
	}
	if m.trader == nil {
		return "", "", "", fmt.Errorf("scale-in trader is not configured")
	}

	quantity := level.PlannedQty
	if quantity <= 0 {
		return "", "", "", fmt.Errorf("scale-in level quantity must be greater than 0")
	}

	if cfg != nil && cfg.Leverage > 0 {
		if err := m.trader.SetLeverage(plan.Symbol, cfg.Leverage); err != nil {
			return "", "", "", err
		}
	}

	switch normalizeScaleInTargetType(level.TargetType) {
	case "limit_price":
		req := &types.LimitOrderRequest{
			Symbol:       plan.Symbol,
			Side:         scaleInExchangeSide(plan.Side),
			PositionSide: plan.Side,
			Price:        level.TargetPrice,
			Quantity:     quantity,
			Leverage:     cfgLeverage(cfg),
			ReduceOnly:   false,
			ClientID:     level.LinkedOrderIntentID,
		}
		result, err := m.trader.PlaceLimitOrder(req)
		if err != nil {
			return "", "", "", err
		}
		return result.OrderID, result.ClientID, result.Status, nil
	default:
		switch normalizeAggregateSide(plan.Side) {
		case "LONG":
			order, err := m.trader.AddLong(plan.Symbol, quantity, cfgLeverage(cfg))
			if err != nil {
				return "", "", "", err
			}
			return orderIDFromMap(order), clientOrderIDFromMap(order), statusFromMap(order), nil
		case "SHORT":
			order, err := m.trader.AddShort(plan.Symbol, quantity, cfgLeverage(cfg))
			if err != nil {
				return "", "", "", err
			}
			return orderIDFromMap(order), clientOrderIDFromMap(order), statusFromMap(order), nil
		default:
			return "", "", "", fmt.Errorf("unknown scale-in side: %s", plan.Side)
		}
	}
}

func (m *ScaleInManager) recordScaleInPlan(plan *store.ScaleInPlan, levels []*store.ScaleInPlanLevel, status, source, eventType string) (*store.ScaleInPlan, error) {
	if m == nil || m.scaleInPlanBuilder == nil {
		return nil, fmt.Errorf("scale-in plan builder is not configured")
	}
	if plan == nil {
		return nil, fmt.Errorf("scale-in plan is required")
	}
	payload := mustJSON(map[string]interface{}{
		"plan_id": plan.ScaleInPlanID,
		"status":  status,
		"levels":  levels,
	})
	return m.scaleInPlanBuilder.ApplyScaleInEvent(&store.ScaleInPlanEventInput{
		TraderID:            plan.TraderID,
		Symbol:              plan.Symbol,
		Side:                plan.Side,
		LinkedPositionKey:   plan.LinkedPositionKey,
		ScaleInPlanID:       plan.ScaleInPlanID,
		PlanMode:            plan.PlanMode,
		Status:              status,
		TotalPlannedQty:     plan.TotalPlannedQty,
		RemainingPlannedQty: plan.RemainingPlannedQty,
		ExecutedQty:         plan.ExecutedQty,
		MaxScaleInCount:     plan.MaxScaleInCount,
		CurrentScaleInCount: plan.CurrentScaleInCount,
		Source:              source,
		EventType:           eventType,
		EventSource:         source,
		PayloadJSON:         payload,
		EventTime:           time.Now().UTC(),
		Levels:              scaleInPlanLevelsToInputs(plan, levels),
	})
}

func scaleInPlanLevelsToInputs(plan *store.ScaleInPlan, levels []*store.ScaleInPlanLevel) []*store.ScaleInPlanLevelInput {
	inputs := make([]*store.ScaleInPlanLevelInput, 0, len(levels))
	for _, level := range levels {
		if level == nil {
			continue
		}
		inputs = append(inputs, &store.ScaleInPlanLevelInput{
			TraderID:              level.TraderID,
			Symbol:                level.Symbol,
			Side:                  level.Side,
			LinkedPositionKey:     level.LinkedPositionKey,
			ScaleInPlanID:         level.ScaleInPlanID,
			LevelIndex:            level.LevelIndex,
			TargetType:            level.TargetType,
			TargetPrice:           level.TargetPrice,
			PlannedQty:            level.PlannedQty,
			ExecutedQty:           level.ExecutedQty,
			RemainingQty:          level.RemainingQty,
			LinkedOrderIntentID:   level.LinkedOrderIntentID,
			LinkedExchangeOrderID: level.LinkedExchangeOrderID,
			Status:                level.Status,
		})
	}
	return inputs
}

func normalizeScaleInRatios(values []float64) []float64 {
	if len(values) == 0 {
		return nil
	}
	total := 0.0
	for _, v := range values {
		if v > 0 {
			total += v
		}
	}
	if total <= 0 {
		return nil
	}
	result := make([]float64, 0, len(values))
	for _, v := range values {
		if v <= 0 {
			continue
		}
		result = append(result, v/total)
	}
	return result
}

func normalizeScaleInTargetPrices(values []float64, count int, fallback float64) []float64 {
	if count <= 0 {
		return []float64{}
	}
	result := make([]float64, count)
	for i := 0; i < count; i++ {
		if i < len(values) && values[i] > 0 {
			result[i] = values[i]
		} else {
			result[i] = fallback
		}
	}
	return result
}

func chooseScaleInPlanMode(cfg *ScaleInPlanConfig, totalQty float64) string {
	if cfg != nil && totalQty > 0 {
		if cfg.FixedRatio > 0 {
			return "fixed_ratio"
		}
		return "fixed_qty"
	}
	return "fixed_qty"
}

func cfgLeverage(cfg *ScaleInPlanConfig) int {
	if cfg == nil || cfg.Leverage <= 0 {
		return 1
	}
	return cfg.Leverage
}

func roundScaleInQty(qty float64) float64 {
	return math.Round(qty*1e6) / 1e6
}

func buildScaleInPlanID(traderID, symbol, side string) string {
	return sanitizeScaleInIdentifier("si-" + traderID + "-" + symbol + "-" + side)
}

func sanitizeScaleInIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "si"
	}
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "|", "_")
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func buildScaleInLevelIntentID(planID string, levelIndex int) string {
	trimmedPlanID := strings.TrimSpace(planID)
	if trimmedPlanID == "" {
		trimmedPlanID = "si"
	}
	return fmt.Sprintf("%s-l%d", trimmedPlanID, levelIndex)
}

func scaleInPositionKey(symbol, side string) string {
	return normalizeAggregateSymbol(symbol) + "|" + normalizeAggregateSide(side)
}

func scaleInExchangeSide(side string) string {
	switch normalizeAggregateSide(side) {
	case "LONG":
		return "BUY"
	case "SHORT":
		return "SELL"
	default:
		return "BUY"
	}
}

func scaleInOrderType(targetType string) string {
	switch normalizeScaleInTargetType(targetType) {
	case "limit_price":
		return "LIMIT"
	default:
		return "MARKET"
	}
}

func scaleInRegistryStatus(status string) string {
	return scaleInRegistryStatusFromExchange(status)
}

func scaleInRegistryStatusFromExchange(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FILLED":
		return "FILLED"
	case "PARTIALLY_FILLED":
		return "PARTIALLY_FILLED"
	case "CANCELED", "EXPIRED", "REJECTED":
		return "CANCELED"
	default:
		return "NEW"
	}
}

func scaleInTimeInForce(targetType string) string {
	if normalizeScaleInTargetType(targetType) == "limit_price" {
		return "GTC"
	}
	return "GTC"
}

func orderIDFromMap(order map[string]interface{}) string {
	if order == nil {
		return ""
	}
	switch v := order["orderId"].(type) {
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func clientOrderIDFromMap(order map[string]interface{}) string {
	if order == nil {
		return ""
	}
	if v, ok := order["clientOrderId"].(string); ok {
		return v
	}
	return ""
}

func statusFromMap(order map[string]interface{}) string {
	if order == nil {
		return ""
	}
	if v, ok := order["status"].(string); ok {
		return v
	}
	return ""
}

func normalizeAggregateSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func normalizeAggregateSide(side string) string {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "BUY", "LONG":
		return "LONG"
	case "SELL", "SHORT":
		return "SHORT"
	default:
		return strings.ToUpper(strings.TrimSpace(side))
	}
}

func normalizeScaleInTargetType(targetType string) string {
	normalized := strings.ToLower(strings.TrimSpace(targetType))
	switch normalized {
	case "", "market", "limit_price":
		if normalized == "" {
			return "market"
		}
		return normalized
	default:
		return "market"
	}
}

func normalizeScaleInLevelStatus(status string) string {
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
