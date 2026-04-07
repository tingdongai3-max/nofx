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

// ScaleOutPlanConfig is the execution-side configuration for a fixed-ratio scale-out plan.
// It is a service-layer input, not a persisted truth row.
type ScaleOutPlanConfig struct {
	LevelRatios    []float64 // Relative quantity split per level; normalized to 1.0 by the manager.
	TargetPercents  []float64 // Relative target offsets per level; positive values for LONG, negative for SHORT.
	TargetType      string    // Level target type: limit_price or reduce_market.
}

// ScaleOutManager attaches and records Binance-only fixed-ratio partial reduce plans.
// It centralizes plan construction, order attachment, and truth-layer recording.
type ScaleOutManager struct {
	store                 *store.Store
	trader                Trader
	orderRegistryBuilder  *store.OrderRegistryBuilder
	scaleOutPlanBuilder   *store.ScaleOutPlanBuilder
}

// NewScaleOutManager creates a new scale-out manager.
func NewScaleOutManager(st *store.Store, client Trader) *ScaleOutManager {
	if st == nil {
		return &ScaleOutManager{trader: client}
	}
	return &ScaleOutManager{
		store:                st,
		trader:               client,
		orderRegistryBuilder:  st.OrderRegistryBuilder(),
		scaleOutPlanBuilder:   st.ScaleOutPlanBuilder(),
	}
}

// EnsureScaleOutPlanForAggregate creates or reuses a fixed-ratio scale-out plan for one position.
// It is idempotent: an active plan is reused instead of duplicated.
func (m *ScaleOutManager) EnsureScaleOutPlanForAggregate(aggregate *store.PositionAggregate, profile *store.StrategyProfile, cfg *ScaleOutPlanConfig) (*store.ScaleOutPlan, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("scale-out manager store is not configured")
	}
	if aggregate == nil {
		return nil, fmt.Errorf("position aggregate is required")
	}
	if aggregate.TotalQty <= 0 {
		return nil, fmt.Errorf("position aggregate has no quantity to scale out")
	}
	if profile == nil {
		var err error
		profile, err = m.store.StrategyProfile().GetByTraderID(aggregate.TraderID)
		if err != nil {
			return nil, err
		}
	}
	if profile == nil {
		return nil, fmt.Errorf("strategy profile is required for scale-out plan creation")
	}
	if !profile.ExecutionEnabled {
		return nil, fmt.Errorf("strategy profile is not execution-enabled")
	}
	if !strings.EqualFold(profile.Exchange, "binance_usdm") || !strings.EqualFold(profile.Mode, "one_way") {
		return nil, fmt.Errorf("strategy profile is outside the binance_usdm one_way scope")
	}

	existingSummary, err := m.store.ScaleOutPlan().SummarizeForTraderSymbolSide(aggregate.TraderID, aggregate.Symbol, aggregate.Side)
	if err != nil {
		return nil, err
	}
	if existingSummary != nil && existingSummary.HasScaleOutPlan && existingSummary.ConsistencyStatus != "mismatch" {
		return m.store.ScaleOutPlan().GetByKeys(aggregate.TraderID, existingSummary.ScaleOutPlanID, existingSummary.LinkedPositionKey)
	}

	plan, levels, err := m.buildFixedRatioScaleOutPlan(aggregate, cfg)
	if err != nil {
		return nil, err
	}

	if _, err := m.recordScaleOutPlan(plan, levels, "draft", "scale_out_manager", "SCALE_OUT_PLAN_CREATED"); err != nil {
		return nil, err
	}
	logger.Infof("scale out plan built: trader=%s symbol=%s side=%s plan=%s qty=%.6f status=%s",
		plan.TraderID, plan.Symbol, plan.Side, plan.ScaleOutPlanID, plan.TotalPlannedQty, plan.Status)

	attachedLevels, err := m.attachScaleOutOrders(plan, levels)
	if err != nil {
		_, _ = m.recordScaleOutPlan(plan, levels, "invalid", "scale_out_manager", "SCALE_OUT_PLAN_INVALIDATED")
		return nil, err
	}

	armedPlan, err := m.recordScaleOutPlan(plan, attachedLevels, "armed", "scale_out_manager", "SCALE_OUT_ORDERS_ATTACHED")
	if err != nil {
		return nil, err
	}
	logger.Infof("scale out orders attached: trader=%s symbol=%s side=%s plan=%s levels=%d",
		armedPlan.TraderID, armedPlan.Symbol, armedPlan.Side, armedPlan.ScaleOutPlanID, len(attachedLevels))
	return armedPlan, nil
}

func (m *ScaleOutManager) buildFixedRatioScaleOutPlan(aggregate *store.PositionAggregate, cfg *ScaleOutPlanConfig) (*store.ScaleOutPlan, []*store.ScaleOutPlanLevel, error) {
	if aggregate == nil {
		return nil, nil, fmt.Errorf("position aggregate is required")
	}

	levelRatios := []float64{0.5, 0.5}
	targetPercents := []float64{0.03, 0.06}
	targetType := "limit_price"
	if cfg != nil {
		if len(cfg.LevelRatios) > 0 {
			levelRatios = cfg.LevelRatios
		}
		if len(cfg.TargetPercents) > 0 {
			targetPercents = cfg.TargetPercents
		}
		if t := strings.TrimSpace(cfg.TargetType); t != "" {
			targetType = t
		}
	}

	ratios := normalizeScaleOutRatios(levelRatios)
	if len(ratios) == 0 {
		ratios = []float64{1}
	}
	percs := normalizeScaleOutRatios(targetPercents)
	if len(percs) < len(ratios) {
		for len(percs) < len(ratios) {
			percs = append(percs, percs[len(percs)-1])
		}
	}

	planID := buildScaleOutPlanID(aggregate.TraderID, aggregate.Symbol, aggregate.Side)
	plan := &store.ScaleOutPlan{
		TraderID:            aggregate.TraderID,
		Symbol:              aggregate.Symbol,
		Side:                aggregate.Side,
		LinkedPositionKey:   scaleOutPositionKey(aggregate.Symbol, aggregate.Side),
		ScaleOutPlanID:      planID,
		PlanMode:            "fixed_ratio",
		Status:              "draft",
		TotalPlannedQty:     aggregate.TotalQty,
		RemainingPlannedQty: aggregate.TotalQty,
		ExecutedQty:         0,
		Source:              "scale_out_manager",
	}

	remaining := aggregate.TotalQty
	levels := make([]*store.ScaleOutPlanLevel, 0, len(ratios))
	for idx, ratio := range ratios {
		plannedQty := roundScaleOutQty(aggregate.TotalQty * ratio)
		if idx == len(ratios)-1 {
			plannedQty = roundScaleOutQty(remaining)
		}
		if plannedQty > remaining {
			plannedQty = remaining
		}
		remaining -= plannedQty
		if remaining < 0 {
			remaining = 0
		}

		targetPrice := computeScaleOutTargetPrice(aggregate.AvgEntryPrice, aggregate.Side, percs[idx])
		levels = append(levels, &store.ScaleOutPlanLevel{
			TraderID:              aggregate.TraderID,
			Symbol:                aggregate.Symbol,
			Side:                  aggregate.Side,
			LinkedPositionKey:     scaleOutPositionKey(aggregate.Symbol, aggregate.Side),
			ScaleOutPlanID:        planID,
			LevelIndex:            idx + 1,
			TargetType:            normalizeScaleOutTargetType(targetType),
			TargetPrice:           targetPrice,
			PlannedQty:            plannedQty,
			ExecutedQty:           0,
			RemainingQty:          plannedQty,
			LinkedOrderIntentID:   buildScaleOutLevelIntentID(planID, idx+1),
			LinkedExchangeOrderID: "",
			Status:                "draft",
		})
	}

	return plan, levels, nil
}

func (m *ScaleOutManager) attachScaleOutOrders(plan *store.ScaleOutPlan, levels []*store.ScaleOutPlanLevel) ([]*store.ScaleOutPlanLevel, error) {
	if plan == nil {
		return nil, fmt.Errorf("scale-out plan is required")
	}
	if len(levels) == 0 {
		return nil, fmt.Errorf("scale-out plan has no levels")
	}
	if m.trader == nil {
		return nil, fmt.Errorf("scale-out trader is not configured")
	}

	attached := make([]*store.ScaleOutPlanLevel, 0, len(levels))
	for _, level := range levels {
		if level == nil {
			continue
		}

		quantity := level.PlannedQty
		if quantity <= 0 {
			continue
		}

		exchangeOrderID, clientOrderID, status, err := m.placeScaleOutOrder(plan, level)
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
		level.Status = normalizeScaleOutLevelStatus(strings.ToLower(strings.TrimSpace(status)))
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
			Side:              scaleOutExchangeSide(plan.Side),
			PositionSideMode:  "one_way",
			OrderRole:         "reduce",
			LocalIntentID:     level.LinkedOrderIntentID,
			LinkedGroupID:     plan.ScaleOutPlanID,
			LinkedPositionKey: plan.LinkedPositionKey,
			ExchangeOrderID:   exchangeOrderID,
			ClientOrderID:     clientOrderID,
			OrderType:         scaleOutOrderType(level.TargetType),
			TimeInForce:       "GTC",
			ReduceOnly:        true,
			ClosePosition:     false,
			OrigQty:           quantity,
			ExecutedQty:       level.ExecutedQty,
			AvgPrice:          level.TargetPrice,
			TriggerPrice:      level.TargetPrice,
			Status:            scaleOutRegistryStatus(status),
			Source:            "scale_out_manager",
			EventType:         "SCALE_OUT_LEVEL_ARMED",
			EventSource:       "scale_out_manager",
			PayloadJSON:       mustJSON(map[string]interface{}{"plan_id": plan.ScaleOutPlanID, "level_index": level.LevelIndex, "target_type": level.TargetType, "target_price": level.TargetPrice, "planned_qty": quantity}),
			EventTime:         time.Now().UTC(),
		}); err != nil {
			return nil, err
		}

		logger.Infof("scale out level armed: trader=%s symbol=%s side=%s plan=%s level=%d qty=%.6f target=%.6f status=%s",
			plan.TraderID, plan.Symbol, plan.Side, plan.ScaleOutPlanID, level.LevelIndex, quantity, level.TargetPrice, level.Status)
		attached = append(attached, level)
	}

	return attached, nil
}

func (m *ScaleOutManager) placeScaleOutOrder(plan *store.ScaleOutPlan, level *store.ScaleOutPlanLevel) (exchangeOrderID, clientOrderID, status string, err error) {
	quantity := level.PlannedQty
	if quantity <= 0 {
		return "", "", "", fmt.Errorf("scale-out level has no quantity")
	}

	switch normalizeScaleOutTargetType(level.TargetType) {
	case "reduce_market":
		if reducer, ok := m.trader.(interface {
			ReduceLong(string, float64) (map[string]interface{}, error)
			ReduceShort(string, float64) (map[string]interface{}, error)
		}); ok {
			var result map[string]interface{}
			if normalizeOneWaySide(plan.Side) == "LONG" {
				result, err = reducer.ReduceLong(plan.Symbol, quantity)
			} else {
				result, err = reducer.ReduceShort(plan.Symbol, quantity)
			}
			if err != nil {
				return "", "", "", err
			}
			return parseScaleOutOrderResult(result)
		}
		return "", "", "", fmt.Errorf("scale-out trader does not support reduce-market execution")
	default:
		placer, ok := m.trader.(types.GridTrader)
		if !ok {
			return "", "", "", fmt.Errorf("scale-out trader does not support limit order placement")
		}
		result, err := placer.PlaceLimitOrder(&types.LimitOrderRequest{
			Symbol:       plan.Symbol,
			Side:         scaleOutLimitSide(plan.Side),
			PositionSide: normalizeOneWaySide(plan.Side),
			Price:        level.TargetPrice,
			Quantity:     quantity,
			Leverage:     0,
			ReduceOnly:   true,
			ClientID:     level.LinkedOrderIntentID,
		})
		if err != nil {
			return "", "", "", err
		}
		return result.OrderID, result.ClientID, result.Status, nil
	}
}

func (m *ScaleOutManager) recordScaleOutPlan(plan *store.ScaleOutPlan, levels []*store.ScaleOutPlanLevel, status, eventSource, eventType string) (*store.ScaleOutPlan, error) {
	if plan == nil {
		return nil, fmt.Errorf("scale-out plan is required")
	}
	levelInputs := make([]*store.ScaleOutPlanLevelInput, 0, len(levels))
	remaining := 0.0
	executed := 0.0
	for _, level := range levels {
		if level == nil {
			continue
		}
		remaining += level.RemainingQty
		executed += level.ExecutedQty
		levelInputs = append(levelInputs, &store.ScaleOutPlanLevelInput{
			TraderID:              level.TraderID,
			Symbol:                level.Symbol,
			Side:                  level.Side,
			LinkedPositionKey:     level.LinkedPositionKey,
			ScaleOutPlanID:        level.ScaleOutPlanID,
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
	updated, err := m.scaleOutPlanBuilder.ApplyScaleOutEvent(&store.ScaleOutPlanEventInput{
		TraderID:            plan.TraderID,
		Symbol:              plan.Symbol,
		Side:                plan.Side,
		LinkedPositionKey:   plan.LinkedPositionKey,
		ScaleOutPlanID:      plan.ScaleOutPlanID,
		PlanMode:            plan.PlanMode,
		Status:              status,
		TotalPlannedQty:     plan.TotalPlannedQty,
		RemainingPlannedQty: remaining,
		ExecutedQty:         executed,
		Source:              "scale_out_manager",
		EventType:           eventType,
		EventSource:         eventSource,
		PayloadJSON:         mustJSON(map[string]interface{}{"plan": plan, "levels": levelInputs, "status": status}),
		EventTime:           time.Now().UTC(),
		Levels:              levelInputs,
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func buildScaleOutPlanID(traderID, symbol, side string) string {
	return sanitizeScaleOutIdentifier("so-" + traderID + "-" + symbol + "-" + side)
}

func sanitizeScaleOutIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "so"
	}
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "|", "_")
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func scaleOutPositionKey(symbol, side string) string {
	return strings.ToUpper(strings.TrimSpace(symbol)) + "|" + normalizeOneWaySide(side)
}

func normalizeScaleOutRatios(values []float64) []float64 {
	if len(values) == 0 {
		return nil
	}
	result := make([]float64, 0, len(values))
	sum := 0.0
	for _, value := range values {
		value = math.Abs(value)
		if value <= 0 {
			continue
		}
		result = append(result, value)
		sum += value
	}
	if len(result) == 0 {
		return nil
	}
	for i := range result {
		result[i] = result[i] / sum
	}
	return result
}

func roundScaleOutQty(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return math.Round(value*1e6) / 1e6
}

func computeScaleOutTargetPrice(entryPrice float64, side string, offsetPct float64) float64 {
	if entryPrice <= 0 {
		return 0
	}
	if offsetPct <= 0 {
		offsetPct = 0.03
	}
	switch normalizeOneWaySide(side) {
	case "SHORT":
		return roundScaleOutQty(entryPrice * (1 - offsetPct))
	default:
		return roundScaleOutQty(entryPrice * (1 + offsetPct))
	}
}

func scaleOutExchangeSide(side string) string {
	switch normalizeOneWaySide(side) {
	case "SHORT":
		return "BUY"
	default:
		return "SELL"
	}
}

func scaleOutLimitSide(side string) string {
	return scaleOutExchangeSide(side)
}

func scaleOutOrderType(targetType string) string {
	switch normalizeScaleOutTargetType(targetType) {
	case "reduce_market":
		return "MARKET"
	default:
		return "LIMIT"
	}
}

func scaleOutRegistryStatus(status string) string {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	switch normalized {
	case "FILLED", "PARTIALLY_FILLED", "CANCELED", "EXPIRED", "REJECTED":
		return normalized
	case "NEW", "WORKING", "OPEN":
		return "NEW"
	default:
		return "NEW"
	}
}

func parseScaleOutOrderResult(result map[string]interface{}) (exchangeOrderID, clientOrderID, status string, err error) {
	if result == nil {
		return "", "", "", nil
	}
	if v, ok := result["orderId"]; ok {
		switch value := v.(type) {
		case string:
			exchangeOrderID = strings.TrimSpace(value)
		case int64:
			exchangeOrderID = strconv.FormatInt(value, 10)
		case int:
			exchangeOrderID = strconv.Itoa(value)
		case float64:
			exchangeOrderID = strconv.FormatInt(int64(value), 10)
		default:
			exchangeOrderID = strings.TrimSpace(fmt.Sprintf("%v", value))
		}
	}
	if v, ok := result["clientOrderId"]; ok {
		clientOrderID = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	if v, ok := result["status"]; ok {
		status = strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	return exchangeOrderID, clientOrderID, status, nil
}

func buildScaleOutLevelIntentID(planID string, levelIndex int) string {
	trimmedPlanID := strings.TrimSpace(planID)
	if trimmedPlanID == "" {
		trimmedPlanID = "so"
	}
	return fmt.Sprintf("%s-l%d", trimmedPlanID, levelIndex)
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
