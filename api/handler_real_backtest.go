package api

import (
	"fmt"
	"net/http"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"nofx/trader"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const realBacktestMonitorHoldDuration = 15 * time.Minute

type realBacktestMonitorResponse struct {
	Armed         bool                           `json:"armed"`
	Mode          string                         `json:"mode"`
	DaemonRunning bool                           `json:"daemon_running"`
	TotalEquity   float64                        `json:"total_equity"`
	PositionCount int                            `json:"position_count"`
	LogCount      int                            `json:"log_count"`
	Positions     []*realBacktestMonitorPosition `json:"positions"`
	Logs          []*realBacktestLogEntry        `json:"logs"`
	Attribution   *kernel.RealFireWeights        `json:"attribution,omitempty"`
	UpdatedAt     int64                          `json:"updated_at"`
}

type realBacktestMonitorPosition struct {
	TraderID               string                       `json:"trader_id"`
	TraderName             string                       `json:"trader_name"`
	Symbol                 string                       `json:"symbol"`
	Side                   string                       `json:"side"`
	Sector                 string                       `json:"sector,omitempty"`
	Signal                 string                       `json:"signal,omitempty"`
	EntryPrice             float64                      `json:"entry_price"`
	MarkPrice              float64                      `json:"mark_price"`
	Quantity               float64                      `json:"quantity"`
	Leverage               int                          `json:"leverage"`
	MarginUsed             float64                      `json:"margin_used"`
	UnrealizedPnL          float64                      `json:"unrealized_pnl"`
	UnrealizedPnLPct       float64                      `json:"unrealized_pnl_pct"`
	LogicScore             float64                      `json:"logic_score"`
	BinCenter              int                          `json:"bin_center"`
	SmoothingHalfWidth     float64                      `json:"smoothing_half_width"`
	EntryTime              int64                        `json:"entry_time"`
	CloseAt                int64                        `json:"close_at"`
	CountdownSeconds       int64                        `json:"countdown_seconds"`
	GlobalEV               float64                      `json:"global_ev"`
	SectorEV               float64                      `json:"sector_ev"`
	SymbolEV               float64                      `json:"symbol_ev"`
	GlobalMedianEV         float64                      `json:"global_median_ev"`
	SectorMedianEV         float64                      `json:"sector_median_ev"`
	SymbolMedianEV         float64                      `json:"symbol_median_ev"`
	ResonanceStrength      float64                      `json:"resonance_strength"`
	MahalanobisDistance    float64                      `json:"mahalanobis_distance,omitempty"`
	MahalanobisThreshold   float64                      `json:"mahalanobis_threshold,omitempty"`
	MahalanobisResonant    bool                         `json:"mahalanobis_resonant,omitempty"`
	FeatureVectorFocus     string                       `json:"feature_vector_focus,omitempty"`
	FeatureVectorDeviation float64                      `json:"feature_vector_deviation,omitempty"`
	EntryAllowed           *bool                        `json:"entry_allowed,omitempty"`
	EntryBlockReason       string                       `json:"entry_block_reason,omitempty"`
	GlobalBin              *store.RealBacktestDimension `json:"global_bin,omitempty"`
	SectorBin              *store.RealBacktestDimension `json:"sector_bin,omitempty"`
	SymbolBin              *store.RealBacktestDimension `json:"symbol_bin,omitempty"`
}

type realBacktestLogEntry struct {
	TraderID               string             `json:"trader_id"`
	TraderName             string             `json:"trader_name"`
	Symbol                 string             `json:"symbol"`
	Side                   string             `json:"side"`
	Action                 string             `json:"action"`
	Signal                 string             `json:"signal,omitempty"`
	Price                  float64            `json:"price"`
	Quantity               float64            `json:"quantity"`
	LogicScore             float64            `json:"logic_score"`
	RealizedPnL            float64            `json:"realized_pnl,omitempty"`
	Success                bool               `json:"success"`
	AutoClosed             bool               `json:"auto_closed"`
	Message                string             `json:"message,omitempty"`
	Timestamp              int64              `json:"timestamp"`
	GlobalEV               float64            `json:"global_ev,omitempty"`
	SectorEV               float64            `json:"sector_ev,omitempty"`
	SymbolEV               float64            `json:"symbol_ev,omitempty"`
	FinalEntryEV           float64            `json:"final_entry_ev,omitempty"`
	AttributionWeights     map[string]float64 `json:"attribution_weights,omitempty"`
	MahalanobisDistance    float64            `json:"mahalanobis_distance,omitempty"`
	MahalanobisThreshold   float64            `json:"mahalanobis_threshold,omitempty"`
	MahalanobisResonant    bool               `json:"mahalanobis_resonant,omitempty"`
	FeatureVectorFocus     string             `json:"feature_vector_focus,omitempty"`
	FeatureVectorDeviation float64            `json:"feature_vector_deviation,omitempty"`
	EntryAllowed           *bool              `json:"entry_allowed,omitempty"`
	EntryBlockReason       string             `json:"entry_block_reason,omitempty"`
	ExecutionMode          string             `json:"execution_mode,omitempty"`
}

func (s *Server) handleRealBacktestPositions(c *gin.Context) {
	userID := c.GetString("user_id")
	if s.traderManager != nil {
		if loadErr := s.traderManager.LoadUserTradersFromStore(s.store, userID); loadErr != nil {
			logger.Warnf("⚠️ Failed to load user traders for real backtest monitor: %v", loadErr)
		}
	}

	traderConfigs, err := s.store.Trader().List(userID)
	if err != nil {
		SafeInternalError(c, "Load traders", err)
		return
	}

	armed, err := s.store.GetRealBacktestEnabled()
	if err != nil {
		SafeInternalError(c, "Load real backtest config", err)
		return
	}

	response := &realBacktestMonitorResponse{
		Armed:         armed,
		Mode:          "DORMANT",
		DaemonRunning: s.traderManager != nil && s.traderManager.IsGlobalResonanceSniperRunning(),
		Positions:     make([]*realBacktestMonitorPosition, 0, 3),
		Logs:          make([]*realBacktestLogEntry, 0, 24),
		UpdatedAt:     time.Now().UTC().UnixMilli(),
	}
	if armed {
		response.Mode = "ARMED"
	}

	now := time.Now().UTC()
	executors := make(map[string]*trader.AutoTrader, len(traderConfigs))
	if s.traderManager != nil {
		for _, cfg := range traderConfigs {
			at, getErr := s.traderManager.GetTrader(cfg.ID)
			if getErr != nil || at == nil {
				continue
			}
			key := at.GetExchangeID()
			if key == "" {
				key = at.GetID()
			}
			if _, exists := executors[key]; !exists {
				executors[key] = at
			}
		}
	}

	for key, at := range executors {
		if account, accountErr := at.GetAccountInfo(); accountErr != nil {
			logger.Warnf("⚠️ Failed to fetch monitor equity for exchange %s: %v", key, accountErr)
		} else {
			response.TotalEquity += floatFromAny(account["total_equity"])
		}
	}

	records, recordErr := s.store.Decision().GetLatestRecords(trader.GlobalRealSniperTraderID, 240)
	if recordErr != nil {
		logger.Warnf("⚠️ Failed to load real backtest decision records for trader %s: %v", trader.GlobalRealSniperTraderID, recordErr)
	}

	openActions := indexLatestRealBacktestOpenActions(records)
	openPositions, _ := s.store.Position().GetOpenPositions(trader.GlobalRealSniperTraderID)
	closedPositions, _ := s.store.Position().GetClosedPositions(trader.GlobalRealSniperTraderID, 64)
	livePositionsByExchange := s.realBacktestLivePositionsByExchange(executors)

	for _, pos := range openPositions {
		if pos == nil {
			continue
		}

		side := strings.ToLower(strings.TrimSpace(pos.Side))
		key := realBacktestPositionKey(pos.Symbol, side)
		action := openActions[key]
		meta := (*store.RealBacktestDecisionMeta)(nil)
		if action != nil {
			meta = action.RealBacktest
		}

		position := &realBacktestMonitorPosition{
			TraderID:         trader.GlobalRealSniperTraderID,
			TraderName:       trader.GlobalRealSniperDisplayName,
			Symbol:           pos.Symbol,
			Side:             side,
			EntryPrice:       pos.EntryPrice,
			MarkPrice:        pos.EntryPrice,
			Quantity:         pos.Quantity,
			Leverage:         pos.Leverage,
			MarginUsed:       marginUsedFromStoredPosition(pos),
			UnrealizedPnL:    0,
			UnrealizedPnLPct: 0,
			EntryTime:        pos.EntryTime,
		}

		if live := livePositionsByExchange[pos.ExchangeID][key]; live != nil {
			position.MarkPrice = floatFromAny(live["mark_price"])
			position.Quantity = floatFromAny(live["quantity"])
			if position.Quantity <= 0 {
				position.Quantity = pos.Quantity
			}
			if leverage := int(floatFromAny(live["leverage"])); leverage > 0 {
				position.Leverage = leverage
			}
			position.MarginUsed = floatFromAny(live["margin_used"])
			position.UnrealizedPnL = floatFromAny(live["unrealized_pnl"])
			position.UnrealizedPnLPct = floatFromAny(live["unrealized_pnl_pct"])
		} else {
			position.UnrealizedPnL = unrealizedPnLFromStoredPosition(pos, position.MarkPrice)
			position.UnrealizedPnLPct = calculateStoredPnLPct(position.UnrealizedPnL, position.MarginUsed)
		}

		closeAt, countdown := realBacktestCloseTimingFromPosition(pos, meta, now)
		position.CloseAt = closeAt
		position.CountdownSeconds = countdown
		applyRealBacktestMeta(position, meta)
		response.Positions = append(response.Positions, position)
	}

	response.Logs = append(response.Logs, buildRealBacktestLogEntries(
		trader.GlobalRealSniperTraderID,
		trader.GlobalRealSniperDisplayName,
		records,
		closedPositions,
	)...)
	if resonanceRecords, err := s.store.RealTradeStats().ListClosedByTrader(trader.GlobalRealSniperTraderID, store.PERFORMANCE_RECENT_SAMPLE_LIMIT); err != nil {
		logger.Warnf("⚠️ Failed to load real fire attribution records: %v", err)
	} else {
		response.Attribution = kernel.CalculateRealFireWeights(resonanceRecords)
	}

	sort.SliceStable(response.Positions, func(i, j int) bool {
		if response.Positions[i].LogicScore == response.Positions[j].LogicScore {
			return response.Positions[i].Symbol < response.Positions[j].Symbol
		}
		return response.Positions[i].LogicScore > response.Positions[j].LogicScore
	})

	sort.SliceStable(response.Logs, func(i, j int) bool {
		return response.Logs[i].Timestamp > response.Logs[j].Timestamp
	})
	if len(response.Logs) > 40 {
		response.Logs = response.Logs[:40]
	}

	response.PositionCount = len(response.Positions)
	response.LogCount = len(response.Logs)
	c.JSON(http.StatusOK, response)
}

func applyRealBacktestMeta(position *realBacktestMonitorPosition, meta *store.RealBacktestDecisionMeta) {
	if position == nil || meta == nil {
		return
	}

	threshold, resonant, entryAllowed, blockReason := alignRealBacktestMahalanobisMeta(
		meta.MahalanobisDistance,
		meta.EntryAllowed,
		meta.EntryBlockReason,
	)

	position.Sector = meta.Sector
	position.Signal = meta.Signal
	position.LogicScore = meta.LogicScore
	position.BinCenter = meta.BinCenter
	position.SmoothingHalfWidth = meta.SmoothingHalfWidth
	position.GlobalBin = meta.Global
	position.SectorBin = meta.SectorBin
	position.SymbolBin = meta.Symbol
	position.MahalanobisDistance = meta.MahalanobisDistance
	position.MahalanobisThreshold = threshold
	position.MahalanobisResonant = resonant
	position.FeatureVectorFocus = meta.FeatureVectorFocus
	position.FeatureVectorDeviation = meta.FeatureVectorDeviation
	position.EntryAllowed = entryAllowed
	position.EntryBlockReason = blockReason
	if meta.Global != nil {
		position.GlobalEV = meta.Global.ExpectedValue
		position.GlobalMedianEV = meta.Global.MedianExpectedValue
	}
	if meta.SectorBin != nil {
		position.SectorEV = meta.SectorBin.ExpectedValue
		position.SectorMedianEV = meta.SectorBin.MedianExpectedValue
	}
	if meta.Symbol != nil {
		position.SymbolEV = meta.Symbol.ExpectedValue
		position.SymbolMedianEV = meta.Symbol.MedianExpectedValue
	}
	position.ResonanceStrength = calculateResonanceStrength(meta)
}

func alignRealBacktestMahalanobisMeta(distance float64, entryAllowed *bool, blockReason string) (float64, bool, *bool, string) {
	threshold := kernel.DefaultMahalanobisThreshold
	resonant := distance <= threshold

	var alignedAllowed *bool
	if entryAllowed != nil {
		allowed := *entryAllowed && resonant
		alignedAllowed = &allowed
	}
	if !resonant {
		blockReason = fmt.Sprintf("D > %.1f", threshold)
	}
	return threshold, resonant, alignedAllowed, blockReason
}

func calculateResonanceStrength(meta *store.RealBacktestDecisionMeta) float64 {
	if meta == nil {
		return 0
	}

	dimensions := []*store.RealBacktestDimension{meta.Global, meta.SectorBin, meta.Symbol}
	total := 0.0
	count := 0.0
	for _, dimension := range dimensions {
		if dimension == nil {
			continue
		}
		component := (dimension.ExpectedValue + dimension.MedianExpectedValue) * 1200
		if dimension.ProfitFactor > 1 {
			component += (dimension.ProfitFactor - 1) * 12
		}
		if component < 0 {
			component = 0
		}
		if component > 100 {
			component = 100
		}
		total += component
		count++
	}
	if count == 0 {
		return 0
	}
	return total / count
}

func buildRealBacktestLogEntries(
	traderID string,
	traderName string,
	records []*store.DecisionRecord,
	closedPositions []*store.TraderPosition,
) []*realBacktestLogEntry {
	if len(records) == 0 {
		return nil
	}

	logs := make([]*realBacktestLogEntry, 0, len(records))
	for _, record := range records {
		if !isRealBacktestRecord(record) {
			continue
		}
		for _, action := range record.Decisions {
			if !isRealBacktestAction(&action) {
				continue
			}

			side := sideFromAction(action.Action)
			timestamp := decisionActionTimestamp(record, &action).UnixMilli()
			entry := &realBacktestLogEntry{
				TraderID:      traderID,
				TraderName:    traderName,
				Symbol:        action.Symbol,
				Side:          side,
				Action:        action.Action,
				Price:         action.Price,
				Quantity:      action.Quantity,
				LogicScore:    0,
				Success:       action.Success,
				AutoClosed:    isTimedExitAction(&action),
				Message:       action.Reasoning,
				Timestamp:     timestamp,
				ExecutionMode: action.ExecutionMode,
			}
			if action.RealBacktest != nil {
				entry.Signal = action.RealBacktest.Signal
				entry.LogicScore = action.RealBacktest.LogicScore
				if action.RealBacktest.Global != nil {
					entry.GlobalEV = action.RealBacktest.Global.ExpectedValue
				}
				if action.RealBacktest.SectorBin != nil {
					entry.SectorEV = action.RealBacktest.SectorBin.ExpectedValue
				}
				if action.RealBacktest.Symbol != nil {
					entry.SymbolEV = action.RealBacktest.Symbol.ExpectedValue
				}
				entry.FinalEntryEV = action.RealBacktest.FinalEntryEV
				entry.AttributionWeights = action.RealBacktest.AttributionWeights
				entry.MahalanobisDistance = action.RealBacktest.MahalanobisDistance
				entry.MahalanobisThreshold, entry.MahalanobisResonant, entry.EntryAllowed, entry.EntryBlockReason = alignRealBacktestMahalanobisMeta(
					action.RealBacktest.MahalanobisDistance,
					action.RealBacktest.EntryAllowed,
					action.RealBacktest.EntryBlockReason,
				)
				entry.FeatureVectorFocus = action.RealBacktest.FeatureVectorFocus
				entry.FeatureVectorDeviation = action.RealBacktest.FeatureVectorDeviation
			}
			if strings.HasPrefix(action.Action, "close_") {
				if closed := matchClosedPosition(closedPositions, action.Symbol, side, timestamp); closed != nil {
					entry.RealizedPnL = closed.RealizedPnL
				}
			}
			logs = append(logs, entry)
		}
	}
	return logs
}

func indexLatestRealBacktestOpenActions(records []*store.DecisionRecord) map[string]*store.DecisionAction {
	indexed := make(map[string]*store.DecisionAction)
	for _, record := range records {
		if !isRealBacktestRecord(record) {
			continue
		}
		for i := range record.Decisions {
			action := &record.Decisions[i]
			if !isRealBacktestAction(action) || !strings.HasPrefix(action.Action, "open_") || !action.Success {
				continue
			}
			key := realBacktestPositionKey(action.Symbol, sideFromAction(action.Action))
			indexed[key] = action
		}
	}
	return indexed
}

func isRealBacktestRecord(record *store.DecisionRecord) bool {
	if record == nil {
		return false
	}
	for _, line := range record.ExecutionLog {
		if strings.Contains(strings.ToLower(line), "real_backtest") {
			return true
		}
	}
	for i := range record.Decisions {
		if isRealBacktestAction(&record.Decisions[i]) {
			return true
		}
	}
	return false
}

func isRealBacktestAction(action *store.DecisionAction) bool {
	if action == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(action.ExecutionMode), "real_backtest") {
		return true
	}
	reason := strings.ToLower(strings.TrimSpace(action.Reasoning))
	return strings.Contains(reason, "real backtest") || strings.Contains(action.Reasoning, "实战回测")
}

func realBacktestEntryTime(pos *store.TraderPosition, action *store.DecisionAction) int64 {
	if pos != nil && pos.EntryTime > 0 {
		return pos.EntryTime
	}
	if action == nil {
		return 0
	}
	return decisionActionTimestamp(nil, action).UnixMilli()
}

func realBacktestCloseTiming(entryTime int64, meta *store.RealBacktestDecisionMeta, now time.Time) (int64, int64) {
	if entryTime <= 0 {
		return 0, 0
	}

	holdDuration := realBacktestMonitorHoldDuration
	if meta != nil && meta.HoldDurationSeconds > 0 {
		holdDuration = time.Duration(meta.HoldDurationSeconds) * time.Second
	}

	closeAt := entryTime + holdDuration.Milliseconds()
	remainingMs := closeAt - now.UTC().UnixMilli()
	if remainingMs <= 0 {
		return closeAt, 0
	}
	countdown := remainingMs / 1000
	if remainingMs%1000 != 0 {
		countdown++
	}
	return closeAt, countdown
}

func realBacktestCloseTimingFromPosition(pos *store.TraderPosition, meta *store.RealBacktestDecisionMeta, now time.Time) (int64, int64) {
	if pos == nil {
		return 0, 0
	}
	if pos.AutoCloseAt > 0 {
		remainingMs := pos.AutoCloseAt - now.UTC().UnixMilli()
		if remainingMs <= 0 {
			return pos.AutoCloseAt, 0
		}
		countdown := remainingMs / 1000
		if remainingMs%1000 != 0 {
			countdown++
		}
		return pos.AutoCloseAt, countdown
	}
	return realBacktestCloseTiming(pos.EntryTime, meta, now)
}

func matchClosedPosition(closedPositions []*store.TraderPosition, symbol, side string, eventTimeMs int64) *store.TraderPosition {
	if len(closedPositions) == 0 || symbol == "" || side == "" || eventTimeMs <= 0 {
		return nil
	}

	key := realBacktestPositionKey(symbol, side)
	var matched *store.TraderPosition
	bestDiff := int64(5 * time.Minute / time.Millisecond)
	for _, pos := range closedPositions {
		if pos == nil {
			continue
		}
		if realBacktestPositionKey(pos.Symbol, pos.Side) != key {
			continue
		}
		diff := pos.ExitTime - eventTimeMs
		if diff < 0 {
			diff = -diff
		}
		if matched == nil || diff < bestDiff {
			bestDiff = diff
			matched = pos
		}
	}
	return matched
}

func decisionActionTimestamp(record *store.DecisionRecord, action *store.DecisionAction) time.Time {
	if action != nil && !action.Timestamp.IsZero() {
		return action.Timestamp.UTC()
	}
	if record != nil && !record.Timestamp.IsZero() {
		return record.Timestamp.UTC()
	}
	return time.Now().UTC()
}

func sideFromAction(action string) string {
	switch {
	case strings.HasSuffix(action, "_long"):
		return "long"
	case strings.HasSuffix(action, "_short"):
		return "short"
	default:
		return ""
	}
}

func isTimedExitAction(action *store.DecisionAction) bool {
	if action == nil {
		return false
	}
	reason := strings.ToLower(strings.TrimSpace(action.Reasoning))
	return strings.Contains(reason, "timed exit") || strings.Contains(action.Reasoning, "强制到点离场")
}

func normalizePositionSide(side string) string {
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "long":
		return "long"
	case "short":
		return "short"
	default:
		return ""
	}
}

func realBacktestPositionKey(symbol, side string) string {
	return market.Normalize(symbol) + ":" + strings.ToLower(strings.TrimSpace(side))
}

func floatFromAny(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	default:
		return 0
	}
}

func stringFromAny(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func (s *Server) realBacktestLivePositionsByExchange(executors map[string]*trader.AutoTrader) map[string]map[string]map[string]interface{} {
	result := make(map[string]map[string]map[string]interface{}, len(executors))
	for exchangeID, at := range executors {
		if at == nil {
			continue
		}
		rows, err := at.GetPositions()
		if err != nil {
			logger.Warnf("⚠️ Failed to fetch live positions for exchange %s: %v", exchangeID, err)
			continue
		}

		positionMap := make(map[string]map[string]interface{}, len(rows))
		for _, row := range rows {
			symbol := stringFromAny(row["symbol"])
			side := normalizePositionSide(stringFromAny(row["side"]))
			quantity := floatFromAny(row["quantity"])
			if symbol == "" || side == "" || quantity <= 0 {
				continue
			}
			positionMap[realBacktestPositionKey(symbol, side)] = row
		}
		result[exchangeID] = positionMap
	}
	return result
}

func marginUsedFromStoredPosition(pos *store.TraderPosition) float64 {
	if pos == nil || pos.Leverage <= 0 {
		return 0
	}
	return (pos.Quantity * pos.EntryPrice) / float64(pos.Leverage)
}

func unrealizedPnLFromStoredPosition(pos *store.TraderPosition, markPrice float64) float64 {
	if pos == nil || markPrice <= 0 {
		return 0
	}
	if strings.EqualFold(pos.Side, "SHORT") {
		return (pos.EntryPrice - markPrice) * pos.Quantity
	}
	return (markPrice - pos.EntryPrice) * pos.Quantity
}

func calculateStoredPnLPct(unrealizedPnL, marginUsed float64) float64 {
	if marginUsed <= 0 {
		return 0
	}
	return (unrealizedPnL / marginUsed) * 100
}
