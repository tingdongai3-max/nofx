package kernel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/provider/nofxos"
	"nofx/security"
	"nofx/store"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// ============================================================================
// Pre-compiled regular expressions (performance optimization)
// ============================================================================

var (
	// Safe regex: precisely match ```json code blocks
	reJSONFence      = regexp.MustCompile(`(?is)` + "```json\\s*(\\[\\s*\\{.*?\\}\\s*\\])\\s*```")
	reJSONArray      = regexp.MustCompile(`(?is)\[\s*\{.*?\}\s*\]`)
	reArrayHead      = regexp.MustCompile(`^\[\s*\{`)
	reArrayOpenSpace = regexp.MustCompile(`^\[\s+\{`)
	reInvisibleRunes = regexp.MustCompile("[\u200B\u200C\u200D\uFEFF]")

	// XML tag extraction (supports any characters in reasoning chain)
	reReasoningTag = regexp.MustCompile(`(?s)<reasoning>(.*?)</reasoning>`)
	reDecisionTag  = regexp.MustCompile(`(?s)<decision>(.*?)</decision>`)
	// 千分位逗号：仅匹配“数字,数字”，用于预处理时移除 reason/reasoning 等字符串内的 67,317 形式
	reThousandSep = regexp.MustCompile(`(\d),(\d)`)
)

// ============================================================================
// Type Definitions
// ============================================================================

// PositionInfo position information
type PositionInfo struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long" or "short"
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"`
	PeakPnLPct       float64 `json:"peak_pnl_pct"` // Historical peak profit percentage
	LiquidationPrice float64 `json:"liquidation_price"`
	MarginUsed       float64 `json:"margin_used"`
	UpdateTime       int64   `json:"update_time"` // Position update timestamp (milliseconds)
}

// AccountInfo account information
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // Account equity
	AvailableBalance float64 `json:"available_balance"` // Available balance
	UnrealizedPnL    float64 `json:"unrealized_pnl"`    // Unrealized profit/loss
	TotalPnL         float64 `json:"total_pnl"`         // Total profit/loss
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // Total profit/loss percentage
	MarginUsed       float64 `json:"margin_used"`       // Used margin
	MarginUsedPct    float64 `json:"margin_used_pct"`   // Margin usage rate
	PositionCount    int     `json:"position_count"`    // Number of positions
}

// CandidateCoin candidate coin (from coin pool)
type CandidateCoin struct {
	Symbol  string   `json:"symbol"`
	Sources []string `json:"sources"` // Sources: "ai500" and/or "oi_top"
}

// OITopData open interest growth top data (for AI decision reference)
type OITopData struct {
	Rank              int     // OI Top ranking
	OIDeltaPercent    float64 // Open interest change percentage (1 hour)
	OIDeltaValue      float64 // Open interest change value
	PriceDeltaPercent float64 // Price change percentage
}

// TradingStats trading statistics (for AI input)
type TradingStats struct {
	TotalTrades    int     `json:"total_trades"`     // Total number of trades (closed)
	WinRate        float64 `json:"win_rate"`         // Win rate (%)
	ProfitFactor   float64 `json:"profit_factor"`    // Profit factor
	SharpeRatio    float64 `json:"sharpe_ratio"`     // Sharpe ratio
	TotalPnL       float64 `json:"total_pnl"`        // Total profit/loss
	AvgWin         float64 `json:"avg_win"`          // Average win
	AvgLoss        float64 `json:"avg_loss"`         // Average loss
	MaxDrawdownPct float64 `json:"max_drawdown_pct"` // Maximum drawdown (%)
}

// RecentOrder recently completed order (for AI input)
type RecentOrder struct {
	Symbol       string  `json:"symbol"`        // Trading pair
	Side         string  `json:"side"`          // long/short
	EntryPrice   float64 `json:"entry_price"`   // Entry price
	ExitPrice    float64 `json:"exit_price"`    // Exit price
	RealizedPnL  float64 `json:"realized_pnl"`  // Realized profit/loss
	PnLPct       float64 `json:"pnl_pct"`       // Profit/loss percentage
	EntryTime    string  `json:"entry_time"`    // Entry time
	ExitTime     string  `json:"exit_time"`     // Exit time
	HoldDuration string  `json:"hold_duration"` // Hold duration, e.g. "2h30m"
}

// ReasoningOutcome 单笔开仓时的 AI 逻辑与结果，用于「Recent AI Reasoning History」注入与自我修正（原证复核，禁止 AI 改写）
type ReasoningOutcome struct {
	PositionID   int64  `json:"position_id"`   // 唯一仓位 ID，防串线
	EntryTimeStr string `json:"entry_time_str"` // 开仓时间展示，如 "03-01 11:13"
	Side         string `json:"side"`           // LONG / SHORT
	Symbol       string `json:"symbol"`
	Reasoning    string `json:"reasoning"`    // 开仓时 AI 思维链（仅来自 DB ai_reasoning_at_open）
	ResultStr    string `json:"result_str"`    // 如 "LOSS (-0.31%)" / "PROFIT (+0.5%)"
	CloseReason  string `json:"close_reason"`  // 系统平仓触发点：StopLoss / TakeProfit / sync 等，空表示 AI 主动平仓
}

// OpenPositionReasoning 当前持仓的开仓逻辑，提醒 AI「你正在为什么而坚持」
type OpenPositionReasoning struct {
	Symbol    string `json:"symbol"`
	Side      string `json:"side"` // LONG / SHORT
	Reasoning string `json:"reasoning"`
}

// Context trading context (complete information passed to AI)
type Context struct {
	CurrentTime     string                             `json:"current_time"`
	RuntimeMinutes  int                                `json:"runtime_minutes"`
	CallCount       int                                `json:"call_count"`
	Account         AccountInfo                        `json:"account"`
	Positions       []PositionInfo                     `json:"positions"`
	CandidateCoins  []CandidateCoin                    `json:"candidate_coins"`
	PromptVariant   string                             `json:"prompt_variant,omitempty"`
	TradingStats    *TradingStats                      `json:"trading_stats,omitempty"`
	RecentOrders                 []RecentOrder                      `json:"recent_orders,omitempty"`
	RecentReasoningHistory       []ReasoningOutcome                  `json:"recent_reasoning_history,omitempty"` // 近期开仓时的 AI 逻辑与盈亏（原证），供自我审阅
	OpenPositionReasoning        []OpenPositionReasoning             `json:"open_position_reasoning,omitempty"` // 当前持仓的开仓逻辑，防串线
	ClosedCountSinceLastDecision int                                 `json:"closed_count_since_last_decision"`   // 自上次决策以来系统平仓数，用于 ALERT
	MarketDataMap                map[string]*market.Data            `json:"-"`
	MultiTFMarket   map[string]map[string]*market.Data `json:"-"`
	OITopDataMap    map[string]*OITopData              `json:"-"`
	QuantDataMap    map[string]*QuantData              `json:"-"`
	CZSCLabelsMap   map[string]*CZSCLabels            `json:"-"` // 缠论标签（笔/线段/中枢/买卖点），仅当 EnableCZSC 时填充
	OIRankingData      *nofxos.OIRankingData      `json:"-"` // Market-wide OI ranking data
	NetFlowRankingData *nofxos.NetFlowRankingData `json:"-"` // Market-wide fund flow ranking data
	PriceRankingData   *nofxos.PriceRankingData   `json:"-"` // Market-wide price gainers/losers
	BTCETHLeverage     int                          `json:"-"`
	AltcoinLeverage int                                `json:"-"`
	Timeframes      []string                           `json:"-"`
}

// CZSCLabels 缠论分析结果：笔、线段、中枢、1/2/3类买卖点（由 CZSC 中间件返回，原样注入 Prompt）
type CZSCLabels struct {
	Bi            []CZSCBiSegment   `json:"bi,omitempty"`             // 笔
	Xd            []CZSCBiSegment   `json:"xd,omitempty"`             // 线段
	Zs            []CZSCZhongshu    `json:"zs,omitempty"`             // 中枢
	BuySellPoints []CZSCBuySellPoint `json:"buy_sell_points,omitempty"` // 买卖点（1买/2买/3买、1卖/2卖/3卖）
	Timeframe     string            `json:"timeframe,omitempty"`
}

// CZSCBiSegment 笔或线段的一段（起止时间与价格）
type CZSCBiSegment struct {
	StartTime int64   `json:"start_time"`
	EndTime   int64   `json:"end_time"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Direction string  `json:"direction,omitempty"` // up / down
}

// CZSCZhongshu 中枢（区间与高低点）
type CZSCZhongshu struct {
	StartTime int64   `json:"start_time"`
	EndTime   int64   `json:"end_time"`
	ZG        float64 `json:"zg"` // 中枢高点
	ZD        float64 `json:"zd"` // 中枢低点
	GG        float64 `json:"gg,omitempty"`
	DD        float64 `json:"dd,omitempty"`
}

// CZSCBuySellPoint 买卖点
type CZSCBuySellPoint struct {
	Type  string  `json:"type"`  // 1买 2买 3买 / 1卖 2卖 3卖
	Time  int64   `json:"time"`
	Price float64 `json:"price"`
}

// Decision AI trading decision
type Decision struct {
	Symbol string `json:"symbol"`
	Action string `json:"action"` // Allowed: "open_long", "open_short", "close_long", "close_short", "hold", "wait"
	// Grid actions: "place_buy_limit", "place_sell_limit", "cancel_order", "cancel_all_orders", "pause_grid", "resume_grid", "adjust_grid"

	// Opening position parameters
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`
	TakeProfitStages []TakeProfitStage `json:"take_profit_stages,omitempty"`

	// ATR 移动止盈止损（仅当策略开启 enable_atr_trailing 时使用）：不开交易所固定 TP/SL，由机器狗按价格监控触发
	ATRTrailingSlMult   float64             `json:"atr_sl_mult,omitempty"`   // 止损：entry ± atr_sl_mult * ATR
	ATRTrailingTpMult   float64             `json:"atr_tp_mult,omitempty"`   // 止盈：entry ± atr_tp_mult * ATR（可与 atr_tp_stages 二选一或同时用）
	ATRTrailingTpStages []ATRTrailingStage `json:"atr_tp_stages,omitempty"` // 分批止盈，最多 3 阶段：每阶段 atr_mult + close_pct(0-100)

	// Grid trading parameters
	Price      float64 `json:"price,omitempty"`       // Limit order price (for grid)
	Quantity   float64 `json:"quantity,omitempty"`    // Order quantity (for grid)
	LevelIndex int     `json:"level_index,omitempty"` // Grid level index
	OrderID    string  `json:"order_id,omitempty"`    // Order ID (for cancel)

	// Common parameters
	Confidence  int     `json:"confidence,omitempty"`  // Confidence level (0-100)
	RiskUSD     float64 `json:"risk_usd,omitempty"`    // Maximum USD risk
	Reasoning   string  `json:"reasoning"`
	QuantityPct float64 `json:"quantity_pct,omitempty"` // 比例平仓：0~1，如 0.4 表示平掉当前仓位的 40%（仅 close_long/close_short 时有效）
}

// TakeProfitStage 静态分批止盈阶段：到达指定 price 时平掉 close_pct 比例的仓位（使用交易所分批 TP 功能）
type TakeProfitStage struct {
	Price    float64 `json:"price"`     // 目标止盈价
	ClosePct float64 `json:"close_pct"` // 该阶段平仓比例 0-100，如 30 表示平 30% 仓位
}

// ATRTrailingStage 分批止盈阶段：达到 atr_mult 倍 ATR 时平仓 close_pct 比例（由 ATR 狗监控价格触发）
type ATRTrailingStage struct {
	AtrMult  float64 `json:"atr_mult"`  // ATR 倍数，如 1.2 表示入场价 + 1.2*ATR（多）或 入场价 - 1.2*ATR（空）
	ClosePct float64 `json:"close_pct"` // 该阶段平仓比例 0-100，如 50 表示平 50% 仓位
}

// FullDecision AI's complete decision (including chain of thought)
type FullDecision struct {
	SystemPrompt        string     `json:"system_prompt"`
	UserPrompt          string     `json:"user_prompt"`
	CoTTrace            string     `json:"cot_trace"`
	Decisions           []Decision `json:"decisions"`
	RawResponse         string     `json:"raw_response"`
	Timestamp           time.Time  `json:"timestamp"`
	AIRequestDurationMs int64      `json:"ai_request_duration_ms,omitempty"`
}

// QuantData quantitative data structure (fund flow, position changes, price changes)
type QuantData struct {
	Symbol      string             `json:"symbol"`
	Price       float64            `json:"price"`
	Netflow     *NetflowData       `json:"netflow,omitempty"`
	OI          map[string]*OIData `json:"oi,omitempty"`
	PriceChange map[string]float64 `json:"price_change,omitempty"`
}

type NetflowData struct {
	Institution *FlowTypeData `json:"institution,omitempty"`
	Personal    *FlowTypeData `json:"personal,omitempty"`
}

type FlowTypeData struct {
	Future map[string]float64 `json:"future,omitempty"`
	Spot   map[string]float64 `json:"spot,omitempty"`
}

type OIData struct {
	CurrentOI float64                 `json:"current_oi"`
	Delta     map[string]*OIDeltaData `json:"delta,omitempty"`
}

type OIDeltaData struct {
	OIDelta        float64 `json:"oi_delta"`
	OIDeltaValue   float64 `json:"oi_delta_value"`
	OIDeltaPercent float64 `json:"oi_delta_percent"`
}

// ============================================================================
// StrategyEngine - Core Strategy Execution Engine
// ============================================================================

// StrategyEngine strategy execution engine
type StrategyEngine struct {
	config       *store.StrategyConfig
	nofxosClient *nofxos.Client
}

// NewStrategyEngine creates strategy execution engine
func NewStrategyEngine(config *store.StrategyConfig) *StrategyEngine {
	// Create NofxOS client with API key from config
	apiKey := config.Indicators.NofxOSAPIKey
	if apiKey == "" {
		apiKey = nofxos.DefaultAuthKey
	}
	client := nofxos.NewClient(nofxos.DefaultBaseURL, apiKey)

	// Debug: 打印本次策略的关键指标开关，便于确认 Prompt 注入是否与配置一致。
	logger.Infof("Strategy indicator toggles: EnableLiquidation=%v, EnableVolumePOC=%v, EnableOrderBookDepth=%v",
		config.Indicators.EnableLiquidation,
		config.Indicators.EnableVolumePOC,
		config.Indicators.EnableOrderBookDepth,
	)

	return &StrategyEngine{
		config:       config,
		nofxosClient: client,
	}
}

// GetRiskControlConfig gets risk control configuration
func (e *StrategyEngine) GetRiskControlConfig() store.RiskControlConfig {
	return e.config.RiskControl
}

// GetLanguage returns the language from config or falls back to auto-detection
func (e *StrategyEngine) GetLanguage() Language {
	switch e.config.Language {
	case "zh":
		return LangChinese
	case "en":
		return LangEnglish
	default:
		// Fall back to auto-detection from prompt content for backward compatibility
		return detectLanguage(e.config.PromptSections.RoleDefinition)
	}
}

// GetConfig gets complete strategy configuration
func (e *StrategyEngine) GetConfig() *store.StrategyConfig {
	return e.config
}

// ============================================================================
// Entry Functions - Main API
// ============================================================================

// GetFullDecision gets AI's complete trading decision (batch analysis of all coins and positions)
// Uses default strategy configuration - for production use GetFullDecisionWithStrategy with explicit config
func GetFullDecision(ctx *Context, mcpClient mcp.AIClient) (*FullDecision, error) {
	defaultConfig := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&defaultConfig)
	return GetFullDecisionWithStrategy(ctx, mcpClient, engine, "")
}

// GetFullDecisionWithStrategy uses StrategyEngine to get AI decision (unified prompt generation)
func GetFullDecisionWithStrategy(ctx *Context, mcpClient mcp.AIClient, engine *StrategyEngine, variant string) (*FullDecision, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is nil")
	}
	if engine == nil {
		defaultConfig := store.GetDefaultStrategyConfig("en")
		engine = NewStrategyEngine(&defaultConfig)
	}

	// 1. Fetch market data using strategy config
	if len(ctx.MarketDataMap) == 0 {
		if err := fetchMarketDataWithStrategy(ctx, engine); err != nil {
			return nil, fmt.Errorf("failed to fetch market data: %w", err)
		}
	}

	// Ensure OITopDataMap is initialized
	if ctx.OITopDataMap == nil {
		ctx.OITopDataMap = make(map[string]*OITopData)
		oiPositions, err := engine.nofxosClient.GetOITopPositions()
		if err == nil {
			for _, pos := range oiPositions {
				ctx.OITopDataMap[pos.Symbol] = &OITopData{
					Rank:              pos.Rank,
					OIDeltaPercent:    pos.OIDeltaPercent,
					OIDeltaValue:      pos.OIDeltaValue,
					PriceDeltaPercent: pos.PriceDeltaPercent,
				}
			}
		}
	}

	// 2. Build System Prompt (static = cacheable, dynamic = equity-dependent, never cached)
	riskConfig := engine.GetRiskControlConfig()
	systemStatic := engine.BuildSystemPromptStatic(variant)
	systemDynamic := engine.BuildSystemPromptDynamic(ctx.Account.TotalEquity)

	// 3. Build User Prompt using strategy engine (always dynamic: market data, positions)
	userPrompt := engine.BuildUserPrompt(ctx)

	// 3.5 熔断风控：若传给 AI 的最后一根 K 线收盘时间落后当前时间超过 10 分钟，中止交易，绝不让 AI 拿陈旧数据做判断
	if err := ensureKlineDataFreshness(ctx, engine); err != nil {
		return nil, err
	}

	// 4. Call AI API (Claude uses prompt caching for systemStatic; others get concatenated system)
	aiCallStart := time.Now()
	aiResponse, err := mcpClient.CallWithCacheableSystem(systemStatic, systemDynamic, userPrompt)
	aiCallDuration := time.Since(aiCallStart)
	if err != nil {
		return nil, fmt.Errorf("AI API call failed: %w", err)
	}

	// 5. Parse AI response
	allowStagedTP := true
	if engine != nil && engine.config != nil && !engine.config.Indicators.EnableStagedTakeProfit {
		allowStagedTP = false
	}
	decision, err := parseFullDecisionResponse(
		aiResponse,
		ctx.Account.TotalEquity,
		riskConfig.BTCETHMaxLeverage,
		riskConfig.AltcoinMaxLeverage,
		riskConfig.BTCETHMaxPositionValueRatio,
		riskConfig.AltcoinMaxPositionValueRatio,
		allowStagedTP,
	)

	if decision != nil {
		decision.SystemPrompt = systemStatic + systemDynamic
		decision.UserPrompt = userPrompt
		decision.Timestamp = time.Now()
		decision.AIRequestDurationMs = aiCallDuration.Milliseconds()
		decision.RawResponse = aiResponse
	}

	if err != nil {
		return decision, fmt.Errorf("failed to parse AI response: %w", err)
	}

	return decision, nil
}

// ============================================================================
// Market Data Fetching
// ============================================================================

// ensureKlineDataFreshness 熔断风控：若任一击中标的的 K 线最后一根收盘时间落后当前超过 2 分钟，报错中止。
// 校验公式：CloseTime = 最后一根 OpenTime + 周期；若 now - CloseTime > 2min 则熔断。
// 防负数：当前未闭合的 K 线时 now < CloseTime，差值为负，直接放行不熔断。
func ensureKlineDataFreshness(ctx *Context, engine *StrategyEngine) error {
	if ctx == nil || engine == nil || engine.config == nil {
		return nil
	}
	primaryTF := engine.config.Indicators.Klines.PrimaryTimeframe
	if primaryTF == "" && len(engine.config.Indicators.Klines.SelectedTimeframes) > 0 {
		primaryTF = engine.config.Indicators.Klines.SelectedTimeframes[0]
	}
	if primaryTF == "" {
		primaryTF = "5m"
	}
	nowMs := time.Now().UTC().UnixMilli()
	const maxStalenessMs = 2 * 60 * 1000 // 2 minutes

	for symbol, data := range ctx.MarketDataMap {
		if data == nil {
			continue
		}
		lastCloseMs, ok := market.DataLastCloseTimeMs(data, primaryTF)
		if !ok {
			continue
		}
		lagMs := nowMs - lastCloseMs
		if lagMs <= 0 {
			continue // 负数或零：当前未闭合的 K 线，数据非常新鲜，放行
		}
		if lagMs > maxStalenessMs {
			logger.Infof("[ERROR] K-line data is stale, aborting trade: %s primary %s lastClose=%d now=%d lag=%d ms",
				symbol, primaryTF, lastCloseMs, nowMs, lagMs)
			return fmt.Errorf("[ERROR] K-line data is stale, aborting trade")
		}
	}
	return nil
}

// fetchMarketDataWithStrategy fetches market data using strategy config (multiple timeframes)
func fetchMarketDataWithStrategy(ctx *Context, engine *StrategyEngine) error {
	config := engine.GetConfig()
	ctx.MarketDataMap = make(map[string]*market.Data)

	timeframes := config.Indicators.Klines.SelectedTimeframes
	primaryTimeframe := config.Indicators.Klines.PrimaryTimeframe
	if len(timeframes) == 0 {
		if primaryTimeframe != "" {
			timeframes = append(timeframes, primaryTimeframe)
		} else {
			timeframes = append(timeframes, "3m")
		}
		if config.Indicators.Klines.LongerTimeframe != "" {
			timeframes = append(timeframes, config.Indicators.Klines.LongerTimeframe)
		}
	}
	if primaryTimeframe == "" {
		primaryTimeframe = timeframes[0]
	}

	// 各周期 K 线数量：优先用 TimeframeCounts，否则用默认梯队（小周期多、大周期少）
	counts := make(map[string]int)
	for _, tf := range timeframes {
		if n, ok := config.Indicators.Klines.TimeframeCounts[tf]; ok && n > 0 {
			counts[tf] = n
		} else {
			counts[tf] = market.DefaultCountForTimeframe(tf)
		}
	}
	logger.Infof("📊 Strategy timeframes: %v, Primary: %s, counts: %v", timeframes, primaryTimeframe, counts)

	opts := IndicatorParamsFromConfig(engine.config.Indicators)
	for _, pos := range ctx.Positions {
		data, err := market.GetWithTimeframes(pos.Symbol, timeframes, primaryTimeframe, counts, opts)
		if err != nil {
			logger.Infof("⚠️  Failed to fetch market data for position %s: %v", pos.Symbol, err)
			continue
		}
		ctx.MarketDataMap[pos.Symbol] = data
	}

	// 2. Fetch data for all candidate coins（已废弃 OI 过滤：配置中的币种无条件拉取 K 线与指标）
	for _, coin := range ctx.CandidateCoins {
		if _, exists := ctx.MarketDataMap[coin.Symbol]; exists {
			continue
		}

		data, err := market.GetWithTimeframes(coin.Symbol, timeframes, primaryTimeframe, counts, opts)
		if err != nil {
			logger.Infof("⚠️  Failed to fetch market data for %s: %v", coin.Symbol, err)
			continue
		}

		ctx.MarketDataMap[coin.Symbol] = data
	}

	// 缠论 CZSC 预处理：并发请求中间件，避免 20–30 币串行造成秒级延迟
	if config.Indicators.EnableCZSC && config.Indicators.CZSCServiceURL != "" {
		ctx.CZSCLabelsMap = make(map[string]*CZSCLabels)
		var mu sync.Mutex
		var g errgroup.Group
		for symbol, data := range ctx.MarketDataMap {
			if data == nil || data.TimeframeData == nil {
				continue
			}
			tfData, ok := data.TimeframeData[primaryTimeframe]
			if !ok || len(tfData.Klines) < 20 {
				continue
			}
			symbol := symbol
			klines := tfData.Klines
			serviceURL := config.Indicators.CZSCServiceURL
			g.Go(func() error {
				labels, err := FetchCZSCLabels(symbol, primaryTimeframe, klines, serviceURL)
				if err != nil {
					logger.Warnf("czsc: %s %s: %v", symbol, primaryTimeframe, err)
					return nil
				}
				mu.Lock()
				ctx.CZSCLabelsMap[symbol] = labels
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			logger.Warnf("czsc: errgroup: %v", err)
		}
		logger.Infof("📊 CZSC labels fetched for %d symbols (concurrent)", len(ctx.CZSCLabelsMap))
	}

	logger.Infof("📊 Successfully fetched multi-timeframe market data for %d coins", len(ctx.MarketDataMap))
	return nil
}

// ============================================================================
// Candidate Coins
// ============================================================================

// GetCandidateCoins gets candidate coins based on strategy configuration
func (e *StrategyEngine) GetCandidateCoins() ([]CandidateCoin, error) {
	var candidates []CandidateCoin
	symbolSources := make(map[string][]string)

	coinSource := e.config.CoinSource

	// 量化优选：从 Screener 内存缓存按条件筛选，作为候选池喂给 AI
	if coinSource.EnableQuantFilter && len(coinSource.QuantFilterConditions) > 0 {
		conditions := make([]market.QuantFilterCondition, 0, len(coinSource.QuantFilterConditions))
		for _, c := range coinSource.QuantFilterConditions {
			conditions = append(conditions, market.QuantFilterCondition{
				Timeframe: c.Timeframe,
				Indicator: c.Indicator,
				Operator:  c.Operator,
				Value:     c.Value,
				Value2:    c.Value2,
			})
		}
		symbols := market.FilterSymbolsByConditions(conditions)
		for _, sym := range symbols {
			sym = market.Normalize(sym)
			candidates = append(candidates, CandidateCoin{
				Symbol:  sym,
				Sources: []string{"quant_screener"},
			})
		}
		return e.filterExcludedCoins(candidates), nil
	}

	switch coinSource.SourceType {
	case "static":
		for _, symbol := range coinSource.StaticCoins {
			symbol = market.Normalize(symbol)
			candidates = append(candidates, CandidateCoin{
				Symbol:  symbol,
				Sources: []string{"static"},
			})
		}

		return e.filterExcludedCoins(candidates), nil

	case "ai500":
		// 检查 use_ai500 标志，如果为 false 则回退到静态币种
		if !coinSource.UseAI500 {
			logger.Infof("⚠️  source_type is 'ai500' but use_ai500 is false, falling back to static coins")
			for _, symbol := range coinSource.StaticCoins {
				symbol = market.Normalize(symbol)
				candidates = append(candidates, CandidateCoin{
					Symbol:  symbol,
					Sources: []string{"static"},
				})
			}
			return e.filterExcludedCoins(candidates), nil
		}
		coins, err := e.getAI500Coins(coinSource.AI500Limit)
		if err != nil {
			return nil, err
		}
		// 空列表是正常情况，直接返回
		return e.filterExcludedCoins(coins), nil

	case "oi_top":
		// 检查 use_oi_top 标志，如果为 false 则回退到静态币种
		if !coinSource.UseOITop {
			logger.Infof("⚠️  source_type is 'oi_top' but use_oi_top is false, falling back to static coins")
			for _, symbol := range coinSource.StaticCoins {
				symbol = market.Normalize(symbol)
				candidates = append(candidates, CandidateCoin{
					Symbol:  symbol,
					Sources: []string{"static"},
				})
			}
			return e.filterExcludedCoins(candidates), nil
		}
		coins, err := e.getOITopCoins(coinSource.OITopLimit)
		if err != nil {
			return nil, err
		}
		// 空列表是正常情况，直接返回
		return e.filterExcludedCoins(coins), nil

	case "oi_low":
		// 持仓减少榜，适合做空
		if !coinSource.UseOILow {
			logger.Infof("⚠️  source_type is 'oi_low' but use_oi_low is false, falling back to static coins")
			for _, symbol := range coinSource.StaticCoins {
				symbol = market.Normalize(symbol)
				candidates = append(candidates, CandidateCoin{
					Symbol:  symbol,
					Sources: []string{"static"},
				})
			}
			return e.filterExcludedCoins(candidates), nil
		}
		coins, err := e.getOILowCoins(coinSource.OILowLimit)
		if err != nil {
			return nil, err
		}
		// 空列表是正常情况，直接返回
		return e.filterExcludedCoins(coins), nil

	case "mixed":
		if coinSource.UseAI500 {
			poolCoins, err := e.getAI500Coins(coinSource.AI500Limit)
			if err != nil {
				logger.Infof("⚠️  Failed to get AI500 coins: %v", err)
			} else {
				for _, coin := range poolCoins {
					symbolSources[coin.Symbol] = append(symbolSources[coin.Symbol], "ai500")
				}
			}
		}

		if coinSource.UseOITop {
			oiCoins, err := e.getOITopCoins(coinSource.OITopLimit)
			if err != nil {
				logger.Infof("⚠️  Failed to get OI Top: %v", err)
			} else {
				for _, coin := range oiCoins {
					symbolSources[coin.Symbol] = append(symbolSources[coin.Symbol], "oi_top")
				}
			}
		}

		if coinSource.UseOILow {
			oiLowCoins, err := e.getOILowCoins(coinSource.OILowLimit)
			if err != nil {
				logger.Infof("⚠️  Failed to get OI Low: %v", err)
			} else {
				for _, coin := range oiLowCoins {
					symbolSources[coin.Symbol] = append(symbolSources[coin.Symbol], "oi_low")
				}
			}
		}

		for _, symbol := range coinSource.StaticCoins {
			symbol = market.Normalize(symbol)
			if _, exists := symbolSources[symbol]; !exists {
				symbolSources[symbol] = []string{"static"}
			} else {
				symbolSources[symbol] = append(symbolSources[symbol], "static")
			}
		}

		for symbol, sources := range symbolSources {
			candidates = append(candidates, CandidateCoin{
				Symbol:  symbol,
				Sources: sources,
			})
		}
		return e.filterExcludedCoins(candidates), nil

	default:
		return nil, fmt.Errorf("unknown coin source type: %s", coinSource.SourceType)
	}
}

// filterExcludedCoins removes excluded coins from the candidates list
func (e *StrategyEngine) filterExcludedCoins(candidates []CandidateCoin) []CandidateCoin {
	if len(e.config.CoinSource.ExcludedCoins) == 0 {
		return candidates
	}

	// Build excluded set for O(1) lookup
	excluded := make(map[string]bool)
	for _, coin := range e.config.CoinSource.ExcludedCoins {
		normalized := market.Normalize(coin)
		excluded[normalized] = true
	}

	// Filter out excluded coins
	filtered := make([]CandidateCoin, 0, len(candidates))
	for _, c := range candidates {
		if !excluded[c.Symbol] {
			filtered = append(filtered, c)
		} else {
			logger.Infof("🚫 Excluded coin: %s", c.Symbol)
		}
	}

	return filtered
}

func (e *StrategyEngine) getAI500Coins(limit int) ([]CandidateCoin, error) {
	if limit <= 0 {
		limit = 30
	}

	symbols, err := e.nofxosClient.GetTopRatedCoins(limit)
	if err != nil {
		return nil, err
	}

	var candidates []CandidateCoin
	for _, symbol := range symbols {
		candidates = append(candidates, CandidateCoin{
			Symbol:  symbol,
			Sources: []string{"ai500"},
		})
	}
	return candidates, nil
}

func (e *StrategyEngine) getOITopCoins(limit int) ([]CandidateCoin, error) {
	if limit <= 0 {
		limit = 10
	}

	positions, err := e.nofxosClient.GetOITopPositions()
	if err != nil {
		return nil, err
	}

	var candidates []CandidateCoin
	for i, pos := range positions {
		if i >= limit {
			break
		}
		symbol := market.Normalize(pos.Symbol)
		candidates = append(candidates, CandidateCoin{
			Symbol:  symbol,
			Sources: []string{"oi_top"},
		})
	}
	return candidates, nil
}

func (e *StrategyEngine) getOILowCoins(limit int) ([]CandidateCoin, error) {
	if limit <= 0 {
		limit = 10
	}

	positions, err := e.nofxosClient.GetOILowPositions()
	if err != nil {
		return nil, err
	}

	var candidates []CandidateCoin
	for i, pos := range positions {
		if i >= limit {
			break
		}
		symbol := market.Normalize(pos.Symbol)
		candidates = append(candidates, CandidateCoin{
			Symbol:  symbol,
			Sources: []string{"oi_low"},
		})
	}
	return candidates, nil
}

// ============================================================================
// External & Quant Data
// ============================================================================

// IndicatorParamsFromConfig 将策略指标配置转为 market.IndicatorParams，供 Get/GetWithTimeframes 动态计算指标（供 debate/api 等调用）
// 注意：只有在对应 Enable* 为 true 时，才会填充该类指标的参数，避免在策略关闭 EMA/RSI/BOLL 等时仍计算并暴露给 AI。
func IndicatorParamsFromConfig(c store.IndicatorConfig) *market.IndicatorParams {
	opts := &market.IndicatorParams{}

	// EMA
	if c.EnableEMA {
		opts.EMAPeriods = append([]int{}, c.EMAPeriods...)
		if len(opts.EMAPeriods) == 0 {
			opts.EMAPeriods = []int{20, 50}
		}
	}

	// RSI
	if c.EnableRSI {
		opts.RSIPeriods = append([]int{}, c.RSIPeriods...)
		if len(opts.RSIPeriods) == 0 {
			opts.RSIPeriods = []int{7, 14}
		}
	}

	// ATR
	if c.EnableATR {
		opts.ATRPeriods = append([]int{}, c.ATRPeriods...)
		if len(opts.ATRPeriods) == 0 {
			opts.ATRPeriods = []int{14}
		}
	}

	// ADX
	if c.EnableADX {
		opts.ADXPeriods = append([]int{}, c.ADXPeriods...)
		if len(opts.ADXPeriods) == 0 {
			opts.ADXPeriods = []int{14}
		}
	}

	// BOLL
	if c.EnableBOLL {
		opts.BOLLPeriods = append([]int{}, c.BOLLPeriods...)
		if len(opts.BOLLPeriods) == 0 {
			opts.BOLLPeriods = []int{20}
		}
	}

	// MACD：通过将周期设为 0/负数来关闭，在 fillDynamicIndicators 中检测 fast/slow/sig 是否有效
	if c.EnableMACD {
		opts.MACDFast = 12
		opts.MACDSlow = 26
		opts.MACDSignal = 9
	}

	// BIAS
	if c.EnableBIAS {
		opts.BIASPeriods = append([]int{}, c.BIASPeriods...)
		if len(opts.BIASPeriods) == 0 {
			opts.BIASPeriods = []int{6, 12, 24}
		}
	}

	// 放量
	if c.EnableVolMult {
		n := c.VolMultBars
		if n <= 0 {
			n = 5
		}
		opts.VolMultBars = n
	}

	return opts
}

// FetchMarketData fetches market data based on strategy configuration (uses DynamicIndicators from strategy params)
func (e *StrategyEngine) FetchMarketData(symbol string) (*market.Data, error) {
	return market.Get(symbol, IndicatorParamsFromConfig(e.config.Indicators))
}

// FetchExternalData fetches external data sources
func (e *StrategyEngine) FetchExternalData() (map[string]interface{}, error) {
	externalData := make(map[string]interface{})

	for _, source := range e.config.Indicators.ExternalDataSources {
		data, err := e.fetchSingleExternalSource(source)
		if err != nil {
			logger.Infof("⚠️  Failed to fetch external data source [%s]: %v", source.Name, err)
			continue
		}
		externalData[source.Name] = data
	}

	return externalData, nil
}

func (e *StrategyEngine) fetchSingleExternalSource(source store.ExternalDataSource) (interface{}, error) {
	// SSRF Protection: Validate URL before making request
	if err := security.ValidateURL(source.URL); err != nil {
		return nil, fmt.Errorf("external source URL validation failed: %w", err)
	}

	timeout := time.Duration(source.RefreshSecs) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// Use SSRF-safe HTTP client
	client := security.SafeHTTPClient(timeout)

	req, err := http.NewRequest(source.Method, source.URL, nil)
	if err != nil {
		return nil, err
	}

	for k, v := range source.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if source.DataPath != "" {
		result = extractJSONPath(result, source.DataPath)
	}

	return result, nil
}

func extractJSONPath(data interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		if m, ok := current.(map[string]interface{}); ok {
			current = m[part]
		} else {
			return nil
		}
	}

	return current
}

// FetchQuantData fetches quantitative data for a single coin
func (e *StrategyEngine) FetchQuantData(symbol string) (*QuantData, error) {
	if !e.config.Indicators.EnableQuantData {
		return nil, nil
	}

	// Use nofxos client with unified API key
	include := "oi,price"
	if e.config.Indicators.EnableQuantNetflow {
		include = "netflow,oi,price"
	}

	nofxosData, err := e.nofxosClient.GetCoinData(symbol, include)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch quant data: %w", err)
	}

	if nofxosData == nil {
		return nil, nil
	}

	// Convert nofxos.QuantData to kernel.QuantData
	quantData := &QuantData{
		Symbol:      nofxosData.Symbol,
		Price:       nofxosData.Price,
		PriceChange: nofxosData.PriceChange,
	}

	// Convert OI data
	if nofxosData.OI != nil {
		quantData.OI = make(map[string]*OIData)
		for exchange, oiData := range nofxosData.OI {
			if oiData != nil {
				kData := &OIData{
					CurrentOI: oiData.CurrentOI,
				}
				if oiData.Delta != nil {
					kData.Delta = make(map[string]*OIDeltaData)
					for dur, delta := range oiData.Delta {
						if delta != nil {
							kData.Delta[dur] = &OIDeltaData{
								OIDelta:        delta.OIDelta,
								OIDeltaValue:   delta.OIDeltaValue,
								OIDeltaPercent: delta.OIDeltaPercent,
							}
						}
					}
				}
				quantData.OI[exchange] = kData
			}
		}
	}

	// Convert Netflow data
	if nofxosData.Netflow != nil {
		quantData.Netflow = &NetflowData{}
		if nofxosData.Netflow.Institution != nil {
			quantData.Netflow.Institution = &FlowTypeData{
				Future: nofxosData.Netflow.Institution.Future,
				Spot:   nofxosData.Netflow.Institution.Spot,
			}
		}
		if nofxosData.Netflow.Personal != nil {
			quantData.Netflow.Personal = &FlowTypeData{
				Future: nofxosData.Netflow.Personal.Future,
				Spot:   nofxosData.Netflow.Personal.Spot,
			}
		}
	}

	return quantData, nil
}

// FetchQuantDataBatch batch fetches quantitative data
func (e *StrategyEngine) FetchQuantDataBatch(symbols []string) map[string]*QuantData {
	result := make(map[string]*QuantData)

	if !e.config.Indicators.EnableQuantData {
		return result
	}

	for _, symbol := range symbols {
		data, err := e.FetchQuantData(symbol)
		if err != nil {
			logger.Infof("⚠️  Failed to fetch quantitative data for %s: %v", symbol, err)
			continue
		}
		if data != nil {
			result[symbol] = data
		}
	}

	return result
}

// FetchOIRankingData fetches market-wide OI ranking data
func (e *StrategyEngine) FetchOIRankingData() *nofxos.OIRankingData {
	indicators := e.config.Indicators
	if !indicators.EnableOIRanking {
		return nil
	}

	duration := indicators.OIRankingDuration
	if duration == "" {
		duration = "1h"
	}

	limit := indicators.OIRankingLimit
	if limit <= 0 {
		limit = 10
	}

	logger.Infof("📊 Fetching OI ranking data (duration: %s, limit: %d)", duration, limit)

	data, err := e.nofxosClient.GetOIRanking(duration, limit)
	if err != nil {
		logger.Warnf("⚠️  Failed to fetch OI ranking data: %v", err)
		return nil
	}

	logger.Infof("✓ OI ranking data ready: %d top, %d low positions",
		len(data.TopPositions), len(data.LowPositions))

	return data
}

// FetchNetFlowRankingData fetches market-wide NetFlow ranking data
func (e *StrategyEngine) FetchNetFlowRankingData() *nofxos.NetFlowRankingData {
	indicators := e.config.Indicators
	if !indicators.EnableNetFlowRanking {
		return nil
	}

	duration := indicators.NetFlowRankingDuration
	if duration == "" {
		duration = "1h"
	}

	limit := indicators.NetFlowRankingLimit
	if limit <= 0 {
		limit = 10
	}

	logger.Infof("💰 Fetching NetFlow ranking data (duration: %s, limit: %d)", duration, limit)

	data, err := e.nofxosClient.GetNetFlowRanking(duration, limit)
	if err != nil {
		logger.Warnf("⚠️  Failed to fetch NetFlow ranking data: %v", err)
		return nil
	}

	logger.Infof("✓ NetFlow ranking data ready: inst_in=%d, inst_out=%d, retail_in=%d, retail_out=%d",
		len(data.InstitutionFutureTop), len(data.InstitutionFutureLow),
		len(data.PersonalFutureTop), len(data.PersonalFutureLow))

	return data
}

// FetchPriceRankingData fetches market-wide price ranking data (gainers/losers)
func (e *StrategyEngine) FetchPriceRankingData() *nofxos.PriceRankingData {
	indicators := e.config.Indicators
	if !indicators.EnablePriceRanking {
		return nil
	}

	durations := indicators.PriceRankingDuration
	if durations == "" {
		durations = "1h"
	}

	limit := indicators.PriceRankingLimit
	if limit <= 0 {
		limit = 10
	}

	logger.Infof("📈 Fetching Price ranking data (durations: %s, limit: %d)", durations, limit)

	data, err := e.nofxosClient.GetPriceRanking(durations, limit)
	if err != nil {
		logger.Warnf("⚠️  Failed to fetch Price ranking data: %v", err)
		return nil
	}

	logger.Infof("✓ Price ranking data ready for %d durations", len(data.Durations))

	return data
}

// ============================================================================
// Prompt Building - System Prompt
// ============================================================================

// BuildSystemPrompt builds full System Prompt (static + dynamic). Use BuildSystemPromptStatic + BuildSystemPromptDynamic for prompt caching.
func (e *StrategyEngine) BuildSystemPrompt(accountEquity float64, variant string) string {
	return e.BuildSystemPromptStatic(variant) + e.BuildSystemPromptDynamic(accountEquity)
}

// BuildSystemPromptStatic returns the cacheable part of the system prompt (no account equity). Safe to cache across requests.
func (e *StrategyEngine) BuildSystemPromptStatic(variant string) string {
	var sb strings.Builder
	riskControl := e.config.RiskControl
	promptSections := e.config.PromptSections
	enableAIClose := riskControl.EnableAIClose

	// 1. Role definition (editable)
	if promptSections.RoleDefinition != "" {
		sb.WriteString(promptSections.RoleDefinition)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("# You are a professional cryptocurrency trading AI\n\n")
		sb.WriteString("Your task is to make trading decisions based on provided market data.\n\n")
	}

	// 2. Trading mode variant
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "aggressive":
		sb.WriteString("## Mode: Aggressive\n- Prioritize capturing trend breakouts, can build positions in batches when confidence ≥ 70\n- Allow higher positions, but must strictly set stop-loss and explain risk-reward ratio\n\n")
	case "conservative":
		sb.WriteString("## Mode: Conservative\n- Only open positions when multiple signals resonate\n- Prioritize cash preservation, must pause for multiple periods after consecutive losses\n\n")
	case "scalping":
		sb.WriteString("## Mode: Scalping\n- Focus on short-term momentum, smaller profit targets but require quick action\n- If price doesn't move as expected within two bars, immediately reduce position or stop-loss\n\n")
	}

	// 3. Hard constraints (risk control) — static structure only; actual limits go in BuildSystemPromptDynamic
	btcEthPosValueRatio := riskControl.BTCETHMaxPositionValueRatio
	if btcEthPosValueRatio <= 0 {
		btcEthPosValueRatio = 5.0
	}
	altcoinPosValueRatio := riskControl.AltcoinMaxPositionValueRatio
	if altcoinPosValueRatio <= 0 {
		altcoinPosValueRatio = 1.0
	}

	sb.WriteString("# Hard Constraints (Risk Control)\n\n")
	sb.WriteString("## CODE ENFORCED (Backend validation, cannot be bypassed):\n")
	sb.WriteString(fmt.Sprintf("- Max Positions: %d coins simultaneously\n", riskControl.MaxPositions))
	sb.WriteString("- Position Value Limit (Altcoins): max USDT = equity × ratio — see **This period** section below for actual numbers.\n")
	sb.WriteString("- Position Value Limit (BTC/ETH): max USDT = equity × ratio — see **This period** section below for actual numbers.\n")
	sb.WriteString(fmt.Sprintf("- Max Margin Usage: ≤%.0f%%\n", riskControl.MaxMarginUsage*100))
	sb.WriteString(fmt.Sprintf("- Min Position Size: ≥%.0f USDT\n\n", riskControl.MinPositionSize))

	sb.WriteString("## AI GUIDED (Recommended, you should follow):\n")
	sb.WriteString(fmt.Sprintf("- Trading Leverage: Altcoins max %dx | BTC/ETH max %dx\n",
		riskControl.AltcoinMaxLeverage, riskControl.BTCETHMaxLeverage))
	sb.WriteString("- Risk-Reward Ratio: No minimum. You may set any take_profit/stop_loss (e.g. 1:1 or even <1:1 for scalping). This is guidance only; no validation block.\n")
	sb.WriteString(fmt.Sprintf("- Min Confidence: ≥%d to open position\n\n", riskControl.MinConfidence))

	// Position sizing guidance (no equity numbers here)
	sb.WriteString("## Position Sizing Guidance\n")
	sb.WriteString("Calculate `position_size_usd` based on your confidence and the Position Value Limits in the **This period** section:\n")
	sb.WriteString("- High confidence (≥85): Use 80-100%% of max position value limit\n")
	sb.WriteString("- Medium confidence (70-84): Use 50-80%% of max position value limit\n")
	sb.WriteString("- Low confidence (60-69): Use 30-50%% of max position value limit\n")
	sb.WriteString("- **DO NOT** just use available_balance as position_size_usd. Use the Position Value Limits!\n\n")

	// 4. Trading frequency (editable)
	if promptSections.TradingFrequency != "" {
		sb.WriteString(promptSections.TradingFrequency)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("# ⏱️ Trading Frequency Awareness\n\n")
		sb.WriteString("- Excellent traders: 2-4 trades/day ≈ 0.1-0.2 trades/hour\n")
		sb.WriteString("- >2 trades/hour = Overtrading\n")
		sb.WriteString("- Single position hold time ≥ 30-60 minutes\n")
		sb.WriteString("If you find yourself trading every period → standards too low; if closing positions < 30 minutes → too impatient.\n\n")
	}

	// 5. Entry standards (editable)
	if promptSections.EntryStandards != "" {
		sb.WriteString(promptSections.EntryStandards)
		sb.WriteString("\n\nYou have the following indicator data:\n")
		e.writeAvailableIndicators(&sb)
		sb.WriteString(fmt.Sprintf("\n**Confidence ≥ %d** required to open positions.\n\n", riskControl.MinConfidence))
	} else {
		sb.WriteString("# 🎯 Entry Standards (Strict)\n\n")
		sb.WriteString("Only open positions when multiple signals resonate. You have:\n")
		e.writeAvailableIndicators(&sb)
		sb.WriteString(fmt.Sprintf("\nFeel free to use any effective analysis method, but **confidence ≥ %d** required to open positions; avoid low-quality behaviors such as single indicators, contradictory signals, sideways consolidation, reopening immediately after closing, etc.\n\n", riskControl.MinConfidence))
	}

	// 6. Decision process (editable)
	if promptSections.DecisionProcess != "" {
		sb.WriteString(promptSections.DecisionProcess)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("# 📋 Decision Process\n\n")
		sb.WriteString("1. Check positions → Should we take profit/stop-loss\n")
		sb.WriteString("2. Scan candidate coins + multi-timeframe → Are there strong signals\n")
		sb.WriteString("3. Write chain of thought first, then output structured JSON\n\n")
	}

	// 7. Output format & critical system rules
	sb.WriteString("# SYSTEM OVERRIDE (Action Configuration)\n\n")
	if enableAIClose {
		// Hybrid guardrail: AI may actively close positions, but still must respect watchdog/ATR rules.
		sb.WriteString("## CRITICAL_SYSTEM_RULES — Hybrid Guardrail Mode (混合护盘架构)\n\n")
		sb.WriteString("You MAY close positions manually. Allowed actions: `close_long`, `close_short` (full or partial), or `hold`/`wait` with TP/SL / ATR updates. Use closes for **professional scaling-out / risk reduction**, not for emotional over-trading.\n\n")
		sb.WriteString("## CLOSE & REDUCE POSITION (平仓与减仓)\n\n")
		sb.WriteString("- **Full close**: Use `close_long` or `close_short` with no `quantity` (or quantity=0) to close the entire position.\n")
		sb.WriteString("- **Partial close / 分批止盈**: Use `close_long` or `close_short` with `quantity` set to the amount (in base asset, e.g. BTC amount) you want to close. Example: position 0.5 BTC, take profit 50% → output `{\"action\": \"close_long\", \"symbol\": \"BTCUSDT\", \"quantity\": 0.25}`. You can close in multiple steps (e.g. 1/3 at first target, 1/3 at second, rest at trailing).\n")
		sb.WriteString("- When in doubt, you can still use only TP/SL orders and `hold`/`wait` to move them; closing is optional.\n\n")
	} else {
		// Fully automated kill-switch: AI has no permission to close; backend watchdog/ATR owns all exits.
		sb.WriteString("## CRITICAL_SYSTEM_RULES — Automated Kill-Switch Mode (物理断头台 / 全自动护盘)\n\n")
		sb.WriteString("- **System Notice**: Your manual close permission has been REVOKED at the engine level. You are an **entry decision engine only**.\n")
		sb.WriteString("- **DO NOT** output `close_long` or `close_short` under any circumstances. Any such actions will be ignored by the backend.\n")
		sb.WriteString("- Position exits (take profit / stop loss / emergency kill) are fully managed by the backend watchdog / ATR auto-cruise system. You focus on selecting high-quality entries and updating TP/SL parameters via `hold` / `wait` only.\n\n")
	}
	sb.WriteString("## TRAILING_STOP_PROTOCOL (Take Profit Iron Rule)\n\n")
	sb.WriteString("When moving a trailing stop (action `hold` or `wait` with a new `stop_loss`): You may update ONLY the stop loss. Do NOT automatically move take_profit up together with the trailing stop. The initial risk-reward ratio applies only to **opening** positions; when trailing, the existing take_profit remains unchanged unless you explicitly output a new `take_profit` value. To update only the stop: set `take_profit` to 0 or omit it — the system will then leave the current TP order intact and only modify the SL order.\n\n")
	sb.WriteString("# Output Format (Strictly Follow)\n\n")
	sb.WriteString("**Must use XML tags <reasoning> and <decision> to separate chain of thought and decision JSON, avoiding parsing errors**\n\n")
	sb.WriteString("## Format Requirements\n\n")
	sb.WriteString("<reasoning>\n")
	sb.WriteString("Your chain of thought analysis...\n")
	sb.WriteString("- Briefly analyze your thinking process \n")
	sb.WriteString("</reasoning>\n\n")
	sb.WriteString("<decision>\n")
	sb.WriteString("Step 2: JSON decision array\n\n")
	sb.WriteString("```json\n[\n")
	if e.config.Indicators.EnableATRTrailing {
		if e.config.Indicators.EnableStagedTakeProfit {
			sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": 5000, \"atr_sl_mult\": 1.2, \"atr_tp_mult\": 2, \"atr_tp_stages\": [{\"atr_mult\": 1.2, \"close_pct\": 50}, {\"atr_mult\": 1.5, \"close_pct\": 50}], \"confidence\": 85, \"risk_usd\": 300},\n",
				riskControl.BTCETHMaxLeverage))
		} else {
			sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": 5000, \"atr_sl_mult\": 1.2, \"atr_tp_mult\": 2, \"confidence\": 85, \"risk_usd\": 300},\n",
				riskControl.BTCETHMaxLeverage))
		}
	} else {
		sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": 5000, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300},\n",
			riskControl.BTCETHMaxLeverage))
		if e.config.Indicators.EnableStagedTakeProfit {
			sb.WriteString("  // 或使用多档静态分批止盈（不开 ATR），例如:\n")
			sb.WriteString("  // {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": 10, \"position_size_usd\": 5000, \"stop_loss\": 97000,\n")
			sb.WriteString("  //   \"take_profit_stages\": [\n")
			sb.WriteString("  //     {\"price\": 91000, \"close_pct\": 30},\n")
			sb.WriteString("  //     {\"price\": 90000, \"close_pct\": 30},\n")
			sb.WriteString("  //     {\"price\": 89000, \"close_pct\": 40}\n")
			sb.WriteString("  //   ], \"confidence\": 85, \"risk_usd\": 300},\n")
		}
	}
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"wait\", \"confidence\": 90}\n")
	sb.WriteString("]\n```\n")
	sb.WriteString("</decision>\n\n")

	// 7a. 自我审阅 + 反幻觉审计：原证复核，区分「逻辑错误」与「概率亏损」
	sb.WriteString("## Review Recent AI Reasoning History (Self-Correction & No Hallucination)\n\n")
	sb.WriteString("Before making the current decision, you **MUST** review the **## Recent AI Reasoning History** section in the User Prompt (if present). That section contains **only raw text from the database** (ai_reasoning_at_open). Do **not** reinterpret or invent past reasoning; treat it as audit evidence.\n\n")
	sb.WriteString("- If you find that previous **losses** were due to **repeated logic** (e.g. multiple times believing a breakout that turned out to be fake), you **MUST** either explain why **this time** the logic is different, or choose to **avoid** the same setup.\n")
	sb.WriteString("- **When auditing history, stay neutral and distinguish:**\n")
	sb.WriteString("  - **概率亏损 (Probability loss)**: The opening logic was consistent with the strategy (e.g. volume breakout, multi-signal confluence), but normal market volatility (wick, washout, liquidity spike) triggered stop. Mark in your chain of thought as **「符合策略的必要损耗」** — no need to change strategy parameters.\n")
	sb.WriteString("  - **逻辑错误 (Logic error)**: The opening logic had blind spots (e.g. did not notice BTC had broken support, chased at resistance, ignored divergence). Mark as **「认知失效」** and in subsequent decisions **force avoidance** of that pattern.\n")
	sb.WriteString("- Do **not** conflate the two: probability loss is acceptable; logic error requires correction.\n\n")

	// 7a2. 存在未平仓位时：强制「三要素」对照分析（开仓预期 vs 当前现实 vs 坚持/纠偏）
	sb.WriteString("## Mandatory Three-Element Check (When You Have Open Positions)\n\n")
	sb.WriteString("If **## Current Open Positions — Your Reasoning at Open** is present, you **MUST** include the following in your chain of thought for each open position (or state \"no open positions\"):\n\n")
	sb.WriteString("- **当初预期 (Expectation)**: What move did you expect at open? (Quote or paraphrase from the opening reasoning above.)\n")
	sb.WriteString("- **当前现实 (Reality)**: Does current price, volume, and market environment (e.g. BTC trend) still support that expectation?\n")
	sb.WriteString("- **逻辑修正 (Pivot or Persevere)**: If reality contradicts expectation, is it **normal probability drawdown** (hold) or **logic invalidated** (close or reduce)?\n\n")

	// 7a3. 平仓幻觉拦截：输出 close 时必须明确「逻辑失效」而非「情绪波动」
	if enableAIClose {
		sb.WriteString("## Close Decision — No Hallucination Rule\n\n")
		sb.WriteString("When you output **close_long** or **close_short**, you **MUST** state in your <reasoning> in one sentence:\n")
		sb.WriteString("**\"I decide to close because [specific condition from the original opening logic] has been invalidated, not due to fear of short-term price fluctuation.\"**\n")
		sb.WriteString("If you cannot point to a concrete condition from the opening thesis that is no longer true, do not close; use hold/wait instead.\n\n")
	}

	// 7a4. 系统平仓后的认知同步：若 Recent AI Reasoning History 中有「系统平仓」记录，必须在 CoT 中回应
	sb.WriteString("## System-Closed Positions — Mandatory Acknowledgment\n\n")
	sb.WriteString("If **## Recent AI Reasoning History** shows any line with **系统平仓触发点** (e.g. StopLoss, TakeProfit, drawdown protection), you **MUST** write in your current chain of thought, for each such position:\n")
	sb.WriteString("**\"My [SYMBOL] [LONG/SHORT] position was closed by system risk control. I [agree / do not agree] with this action, because ...\"**\n")
	sb.WriteString("This ensures cognitive sync after automatic exits and avoids ignoring system protector outcomes.\n\n")

	// 7b. 错误案例库：Negative Examples（从 config/error_patterns 加载，可人工维护）
	if patterns := loadErrorPatterns(); len(patterns) > 0 {
		sb.WriteString("## Negative Examples (Error Patterns to Avoid)\n\n")
		sb.WriteString("The following patterns have been marked as **high-risk or typical failure modes**. Consider them as negative examples when making decisions:\n\n")
		for _, p := range patterns {
			sb.WriteString(fmt.Sprintf("- %s\n", p))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Field Description\n\n")
	if enableAIClose {
		sb.WriteString("- `action`: open_long | open_short | close_long | close_short | hold | wait\n")
	} else {
		sb.WriteString("- `action`: open_long | open_short | hold | wait\n")
		sb.WriteString("- **You MUST NOT** output `close_long` or `close_short`; closing is handled by the backend watchdog / ATR engine. Any close_* actions will be discarded.\n")
	}
	sb.WriteString(fmt.Sprintf("- `confidence`: 0-100 (opening recommended ≥ %d)\n", riskControl.MinConfidence))
	if !e.config.Indicators.EnableStagedTakeProfit {
		sb.WriteString("- **Staged take profit is OFF**: Do NOT use `take_profit_stages` or `atr_tp_stages`. Use only single `take_profit` (or ATR mode: only `atr_tp_mult`) for full position close.\n")
	}
	if e.config.Indicators.EnableATRTrailing {
		sb.WriteString("- **ATR trailing is ON**: Set `atr_sl_mult`, `atr_tp_mult`, and/or `atr_tp_stages` (max 3) for the watchdog to monitor and trigger. You may **also** set `stop_loss` and/or `take_profit`/`take_profit_stages` to place **exchange fixed orders** (e.g. hard stop or backup TP); ATR and exchange orders are **not mutually exclusive**.\n")
		if e.config.Indicators.EnableStagedTakeProfit {
			sb.WriteString("- Required when opening (ATR mode): leverage, position_size_usd, atr_sl_mult, atr_tp_mult and/or atr_tp_stages (max 3 entries), confidence, risk_usd. Optional: stop_loss, take_profit, or take_profit_stages for exchange orders.\n")
		} else {
			sb.WriteString("- Required when opening (ATR mode): leverage, position_size_usd, atr_sl_mult, atr_tp_mult (do NOT use atr_tp_stages), confidence, risk_usd. Optional: stop_loss, take_profit for exchange orders.\n")
		}
	} else {
		sb.WriteString("- Required when opening: leverage, position_size_usd (use max from **This period** section; example shows 5000 as placeholder), stop_loss, **either** take_profit **or** take_profit_stages, confidence, risk_usd\n")
		if e.config.Indicators.EnableStagedTakeProfit {
			sb.WriteString("- Static multi-stage TP (分批挂单止盈，非 ATR): use `take_profit_stages` = [{\"price\": x, \"close_pct\": y}, ...], prices strictly increasing for long positions and strictly decreasing for short positions; total close_pct ≤ 100 (percent of current position size).\n")
		} else {
			sb.WriteString("- Use only single `take_profit` (one price for full position close); do NOT use take_profit_stages.\n")
		}
	}
	if enableAIClose {
		sb.WriteString("- When close_long/close_short: you have **full permission** to close or reduce positions. Use optional `quantity` (base asset amount) or `quantity_pct` (0~1, e.g. 0.4 = close 40%% of current position). Omit both or 0 = close all; set `quantity` = partial close by amount, or `quantity_pct` = partial close by ratio (减仓/分批止盈).\n")
	}
	sb.WriteString("- When hold/wait to update TP/SL or ATR: use `stop_loss` and/or `take_profit` / `take_profit_stages` and/or ATR fields. If you only want to update the stop (trailing stop), set `take_profit` and `take_profit_stages` to 0/empty or omit them — the system will keep the existing TP orders and only update SL.\n")
	if e.config.Indicators.EnableATRTrailing {
		sb.WriteString("- When hold/wait with ATR trailing: you may update `atr_sl_mult`, `atr_tp_mult`, or `atr_tp_stages`; you may also set `stop_loss`/`take_profit`/`take_profit_stages` to update exchange fixed orders (both can coexist).\n")
	}
	sb.WriteString("- **IMPORTANT**: All numeric values must be calculated numbers, NOT formulas/expressions (e.g., use `27.76` not `3000 * 0.01`)\n")
	sb.WriteString("- **STRICT RULE**: In the JSON object, all numbers (including those inside the \"reason\" or \"reasoning\" string) MUST NOT contain any thousand separators (e.g., use 67317 instead of 67,317). Commas are ONLY allowed as delimiters between JSON fields.\n\n")

	// 8. Custom Prompt
	if e.config.CustomPrompt != "" {
		sb.WriteString("# 📌 Personalized Trading Strategy\n\n")
		sb.WriteString(e.config.CustomPrompt)
		sb.WriteString("\n\n")
		sb.WriteString("Note: The above personalized strategy is a supplement to the basic rules and cannot violate the basic risk control principles.\n")
	}

	// 9. Data Dictionary & Schema（放在 System 结尾，保持前缀为交易规则与风控协议，利于上下文缓存命中）
	lang := e.GetLanguage()
	schemaPrompt := GetSchemaPrompt(lang)
	sb.WriteString("\n---\n\n")
	sb.WriteString(schemaPrompt)
	sb.WriteString("\n")

	return sb.String()
}

// BuildSystemPromptDynamic returns the non-cacheable part (equity-dependent). Must be sent fresh each request; do not cache.
func (e *StrategyEngine) BuildSystemPromptDynamic(accountEquity float64) string {
	riskControl := e.config.RiskControl
	btcEthPosValueRatio := riskControl.BTCETHMaxPositionValueRatio
	if btcEthPosValueRatio <= 0 {
		btcEthPosValueRatio = 5.0
	}
	altcoinPosValueRatio := riskControl.AltcoinMaxPositionValueRatio
	if altcoinPosValueRatio <= 0 {
		altcoinPosValueRatio = 1.0
	}
	altcoinMax := accountEquity * altcoinPosValueRatio
	btcEthMax := accountEquity * btcEthPosValueRatio
	examplePositionSize := accountEquity * btcEthPosValueRatio
	var sb strings.Builder
	sb.WriteString("# This period (current session — use these numbers)\n\n")
	sb.WriteString(fmt.Sprintf("- Equity: %.0f USDT\n", accountEquity))
	sb.WriteString(fmt.Sprintf("- Position Value Limit (Altcoins): max %.0f USDT (= equity × %.1fx)\n", altcoinMax, altcoinPosValueRatio))
	sb.WriteString(fmt.Sprintf("- Position Value Limit (BTC/ETH): max %.0f USDT (= equity × %.1fx)\n", btcEthMax, btcEthPosValueRatio))
	sb.WriteString(fmt.Sprintf("- Example position_size_usd for BTC/ETH (e.g. open_short): %.0f\n", examplePositionSize))
	return sb.String()
}

func (e *StrategyEngine) writeAvailableIndicators(sb *strings.Builder) {
	indicators := e.config.Indicators
	kline := indicators.Klines

	sb.WriteString(fmt.Sprintf("- %s price series", kline.PrimaryTimeframe))
	if kline.EnableMultiTimeframe {
		sb.WriteString(fmt.Sprintf(" + %s K-line series\n", kline.LongerTimeframe))
	} else {
		sb.WriteString("\n")
	}

	if indicators.EnableEMA {
		sb.WriteString("- EMA indicators")
		if len(indicators.EMAPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.EMAPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableMACD {
		sb.WriteString("- MACD indicators\n")
	}

	if indicators.EnableRSI {
		sb.WriteString("- RSI indicators")
		if len(indicators.RSIPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.RSIPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableATR {
		sb.WriteString("- ATR indicators")
		if len(indicators.ATRPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.ATRPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableBOLL {
		sb.WriteString("- Bollinger Bands (BOLL) - Upper/Middle/Lower bands")
		if len(indicators.BOLLPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.BOLLPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableBIAS {
		sb.WriteString("- BIAS (Bias Ratio / 乖离率)")
		if len(indicators.BIASPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.BIASPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableVolume {
		sb.WriteString("- Volume data\n")
	}

	if indicators.EnableVolMult {
		n := indicators.VolMultBars
		if n <= 0 {
			n = 5
		}
		sb.WriteString(fmt.Sprintf("- 放量 (realtime_rolling_volmult): 当前 5 分钟滚动成交量 / 近约 24 小时平均每 5 分钟成交量（window=%d秒）\n", 300))
		sb.WriteString("  该指标基于连续 5 分钟滚动窗口计算成交量动能，独立于 K 线开盘/收盘，避免新 K 线启动时的缩量误判导致错误平仓。\n")
	}

	if indicators.EnableVolumePOC {
		sb.WriteString("- Volume POC: 主筹码密集区价格（Volume Point of Control）及当前价格相对 POC 的偏离百分比，用于判断是在筹码上方追高还是在筹码下方抄底。\n")
	}

	if indicators.EnableLiquidation {
		sb.WriteString("- Liquidation Heat: 最近一段时间内的多空爆仓金额与多空爆仓比(多/空)，用于识别是否存在“杀多/杀空”型流动性收集。\n")
	}

	if indicators.EnableOrderBookDepth {
		sb.WriteString("- Order Book Depth: Top20 档订单簿 1% 买卖深度以及最近的大单墙（Wall），需结合爆仓数据判断是否存在虚假挂单 (Spoofing)。\n")
	}

	if indicators.EnableOI {
		sb.WriteString("- Open Interest (OI) data\n")
	}

	if indicators.EnableFundingRate {
		sb.WriteString("- Funding rate\n")
	}

	if len(e.config.CoinSource.StaticCoins) > 0 || e.config.CoinSource.UseAI500 || e.config.CoinSource.UseOITop {
		sb.WriteString("- AI500 / OI_Top filter tags (if available)\n")
	}

	if indicators.EnableQuantData {
		sb.WriteString("- Quantitative data (institutional/retail fund flow, position changes, multi-period price changes)\n")
	}
}

// ============================================================================
// Prompt Building - User Prompt
// ============================================================================

// truncateReasoning 截断过长思维链，避免撑爆 prompt。按 rune 截断，防止中文等多字节字符被切断产生乱码（如 \xe6\x9c）。
func truncateReasoning(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// loadErrorPatterns 从配置文件加载「标志性错误」列表，作为 Negative Examples 注入 System Prompt。
// 尝试路径：config/error_patterns.yaml、config/error_patterns.txt、error_patterns.txt（每行一条，# 开头为注释）
func loadErrorPatterns() []string {
	for _, path := range []string{"config/error_patterns.yaml", "config/error_patterns.txt", "error_patterns.txt"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var out []string
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			out = append(out, line)
		}
		if err := sc.Err(); err != nil {
			logger.Warnf("error_patterns scan: %v", err)
			return nil
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// BuildUserPrompt builds User Prompt based on strategy configuration
func (e *StrategyEngine) BuildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// 未决策期间空档复盘：强制 AI 先看结果再做新动作
	if ctx.ClosedCountSinceLastDecision > 0 {
		sb.WriteString(fmt.Sprintf("!! ALERT: Since your last decision, %d position(s) have been closed by the system protector. Review the outcomes in **Recent AI Reasoning History** below before taking new actions.\n\n",
			ctx.ClosedCountSinceLastDecision))
	}

	// Account information（相对稳定的账户层数据，放在 User Prompt 开头）
	sb.WriteString(fmt.Sprintf("Account: Equity %.2f | Balance %.2f (%.1f%%) | PnL %+.2f%% | Margin %.1f%% | Positions %d\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// 开仓逻辑优先：紧接 Account 之后、Candidate Coins 之前，强制与当前市场事实对标
	if len(ctx.OpenPositionReasoning) > 0 {
		sb.WriteString("**CRITICAL: You MUST evaluate if the initial THESIS for each open position still holds true under current market data.**\n\n")
		sb.WriteString("## Current Open Positions — Your Reasoning at Open\n\n")
		for _, o := range ctx.OpenPositionReasoning {
			reasoning := o.Reasoning
			if reasoning == "" {
				reasoning = "(no reasoning recorded)"
			}
			sb.WriteString(fmt.Sprintf("- %s %s: 开仓时逻辑: %q\n", o.Symbol, o.Side, truncateReasoning(reasoning, 300)))
		}
		sb.WriteString("\n")
	}

	// Recently completed orders (placed before positions to ensure visibility)
	if len(ctx.RecentOrders) > 0 {
		sb.WriteString("## Recent Completed Trades\n")
		for i, order := range ctx.RecentOrders {
			resultStr := "Profit"
			if order.RealizedPnL < 0 {
				resultStr = "Loss"
			}
			sb.WriteString(fmt.Sprintf("%d. %s %s | Entry %.4f Exit %.4f | %s: %+.2f USDT (%+.2f%%) | %s→%s (%s)\n",
				i+1, order.Symbol, order.Side,
				order.EntryPrice, order.ExitPrice,
				resultStr, order.RealizedPnL, order.PnLPct,
				order.EntryTime, order.ExitTime, order.HoldDuration))
		}
		sb.WriteString("\n")
	}

	// Recent AI Reasoning History：原证复核（仅来自 trader_positions.ai_reasoning_at_open，禁止 AI 自由发挥或改写）
	if len(ctx.RecentReasoningHistory) > 0 {
		sb.WriteString("## Recent AI Reasoning History (No Hallucination: raw from DB only)\n\n")
		for _, r := range ctx.RecentReasoningHistory {
			reasoning := r.Reasoning
			if reasoning == "" {
				reasoning = "(no reasoning recorded)"
			}
			reasoningTrunc := truncateReasoning(reasoning, 400)
			if r.CloseReason != "" {
				sb.WriteString(fmt.Sprintf("[已平仓] 品种: %s %s | 结果: %s | 开仓原始逻辑: %q | 系统平仓触发点: %s\n",
					r.Symbol, r.Side, r.ResultStr, reasoningTrunc, r.CloseReason))
			} else {
				sb.WriteString(fmt.Sprintf("[%s] 决策: %s %s | 开仓原始逻辑: %q | 结果: %s\n",
					r.EntryTimeStr, r.Side, r.Symbol, reasoningTrunc, r.ResultStr))
			}
		}
		sb.WriteString("\n")
	}

	// Historical trading statistics (helps AI understand past performance)
	if ctx.TradingStats != nil && ctx.TradingStats.TotalTrades > 0 {
		// Get language from strategy config
		lang := e.GetLanguage()

		// Win/Loss ratio
		var winLossRatio float64
		if ctx.TradingStats.AvgLoss > 0 {
			winLossRatio = ctx.TradingStats.AvgWin / ctx.TradingStats.AvgLoss
		}

		if lang == LangChinese {
			sb.WriteString("## 历史交易统计\n")
			sb.WriteString(fmt.Sprintf("总交易: %d 笔 | 盈利因子: %.2f | 夏普比率: %.2f | 盈亏比: %.2f\n",
				ctx.TradingStats.TotalTrades,
				ctx.TradingStats.ProfitFactor,
				ctx.TradingStats.SharpeRatio,
				winLossRatio))
			sb.WriteString(fmt.Sprintf("总盈亏: %+.2f USDT | 平均盈利: +%.2f | 平均亏损: -%.2f | 最大回撤: %.1f%%\n",
				ctx.TradingStats.TotalPnL,
				ctx.TradingStats.AvgWin,
				ctx.TradingStats.AvgLoss,
				ctx.TradingStats.MaxDrawdownPct))

			// Performance hints based on profit factor, sharpe, and drawdown
			if ctx.TradingStats.ProfitFactor >= 1.5 && ctx.TradingStats.SharpeRatio >= 1 {
				sb.WriteString("表现: 良好 - 保持当前策略\n")
			} else if ctx.TradingStats.ProfitFactor < 1 {
				sb.WriteString("表现: 需改进 - 提高盈亏比，优化止盈止损\n")
			} else if ctx.TradingStats.MaxDrawdownPct > 30 {
				sb.WriteString("表现: 风险偏高 - 减少仓位，控制回撤\n")
			} else {
				sb.WriteString("表现: 正常 - 有优化空间\n")
			}
		} else {
			sb.WriteString("## Historical Trading Statistics\n")
			sb.WriteString(fmt.Sprintf("Total Trades: %d | Profit Factor: %.2f | Sharpe: %.2f | Win/Loss Ratio: %.2f\n",
				ctx.TradingStats.TotalTrades,
				ctx.TradingStats.ProfitFactor,
				ctx.TradingStats.SharpeRatio,
				winLossRatio))
			sb.WriteString(fmt.Sprintf("Total PnL: %+.2f USDT | Avg Win: +%.2f | Avg Loss: -%.2f | Max Drawdown: %.1f%%\n",
				ctx.TradingStats.TotalPnL,
				ctx.TradingStats.AvgWin,
				ctx.TradingStats.AvgLoss,
				ctx.TradingStats.MaxDrawdownPct))

			// Performance hints based on profit factor, sharpe, and drawdown
			if ctx.TradingStats.ProfitFactor >= 1.5 && ctx.TradingStats.SharpeRatio >= 1 {
				sb.WriteString("Performance: GOOD - maintain current strategy\n")
			} else if ctx.TradingStats.ProfitFactor < 1 {
				sb.WriteString("Performance: NEEDS IMPROVEMENT - improve win/loss ratio, optimize TP/SL\n")
			} else if ctx.TradingStats.MaxDrawdownPct > 30 {
				sb.WriteString("Performance: HIGH RISK - reduce position size, control drawdown\n")
			} else {
				sb.WriteString("Performance: NORMAL - room for optimization\n")
			}
		}
		sb.WriteString("\n")
	}

	// Position information
	if len(ctx.Positions) > 0 {
		sb.WriteString("## Current Positions\n")
		sb.WriteString("For these symbols you may: wait, hold (with optional stop_loss/take_profit to update TP/SL), or close_long/close_short (optional quantity for partial close / 分批止盈).\n\n")
		for i, pos := range ctx.Positions {
			sb.WriteString(e.formatPositionInfo(i+1, pos, ctx))
		}
	} else {
		sb.WriteString("Current Positions: None\n\n")
	}

	// Candidate coins (exclude coins already in positions to avoid duplicate data)
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		// Normalize symbol to handle both "ETH" and "ETHUSDT" formats
		normalizedSymbol := market.Normalize(pos.Symbol)
		positionSymbols[normalizedSymbol] = true
	}

	sb.WriteString(fmt.Sprintf("## Candidate Coins (%d coins)\n\n", len(ctx.MarketDataMap)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		// Skip if this coin is already a position (data already shown in positions section)
		normalizedCoinSymbol := market.Normalize(coin.Symbol)
		if positionSymbols[normalizedCoinSymbol] {
			continue
		}

		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := e.formatCoinSourceTag(coin.Sources)
		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
		var czscLabels *CZSCLabels
		if ctx.CZSCLabelsMap != nil {
			czscLabels = ctx.CZSCLabelsMap[coin.Symbol]
		}
		sb.WriteString(e.formatMarketData(marketData, czscLabels))

		if ctx.QuantDataMap != nil {
			if quantData, hasQuant := ctx.QuantDataMap[coin.Symbol]; hasQuant {
				sb.WriteString(e.formatQuantData(quantData))
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Get language for market data formatting
	nofxosLang := nofxos.LangEnglish
	if e.GetLanguage() == LangChinese {
		nofxosLang = nofxos.LangChinese
	}

	// OI Ranking data (market-wide open interest changes)
	if ctx.OIRankingData != nil {
		sb.WriteString(nofxos.FormatOIRankingForAI(ctx.OIRankingData, nofxosLang))
	}

	// NetFlow Ranking data (market-wide fund flow)
	if ctx.NetFlowRankingData != nil {
		sb.WriteString(nofxos.FormatNetFlowRankingForAI(ctx.NetFlowRankingData, nofxosLang))
	}

	// Price Ranking data (market-wide gainers/losers)
	if ctx.PriceRankingData != nil {
		sb.WriteString(nofxos.FormatPriceRankingForAI(ctx.PriceRankingData, nofxosLang))
	}

	sb.WriteString("---\n\n")
	sb.WriteString("Now please analyze and output your decision (Chain of Thought + JSON)\n")

	return sb.String()
}

func (e *StrategyEngine) formatPositionInfo(index int, pos PositionInfo, ctx *Context) string {
	var sb strings.Builder

	holdingDuration := ""
	if pos.UpdateTime > 0 {
		durationMs := time.Now().UnixMilli() - pos.UpdateTime
		durationMin := durationMs / (1000 * 60)
		if durationMin < 60 {
			holdingDuration = fmt.Sprintf(" | Holding Duration %d min", durationMin)
		} else {
			durationHour := durationMin / 60
			durationMinRemainder := durationMin % 60
			holdingDuration = fmt.Sprintf(" | Holding Duration %dh %dm", durationHour, durationMinRemainder)
		}
	}

	positionValue := pos.Quantity * pos.MarkPrice
	if positionValue < 0 {
		positionValue = -positionValue
	}

	sb.WriteString(fmt.Sprintf("%d. %s %s | Entry %.4f Current %.4f | Qty %.4f | Position Value %.2f USDT | PnL%+.2f%% | PnL Amount%+.2f USDT | Peak PnL%.2f%% | Leverage %dx | Margin %.0f | Liq Price %.4f%s\n\n",
		index, pos.Symbol, strings.ToUpper(pos.Side),
		pos.EntryPrice, pos.MarkPrice, pos.Quantity, positionValue, pos.UnrealizedPnLPct, pos.UnrealizedPnL, pos.PeakPnLPct,
		pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

	if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
		var czscLabels *CZSCLabels
		if ctx.CZSCLabelsMap != nil {
			czscLabels = ctx.CZSCLabelsMap[pos.Symbol]
		}
		sb.WriteString(e.formatMarketData(marketData, czscLabels))

		if ctx.QuantDataMap != nil {
			if quantData, hasQuant := ctx.QuantDataMap[pos.Symbol]; hasQuant {
				sb.WriteString(e.formatQuantData(quantData))
			}
		}
		sb.WriteString("\n")
	}

	// System status & BTC 市场快照（高度动态的数据放在 User Prompt 尾部，减少对缓存前缀的影响）
	sb.WriteString(fmt.Sprintf("Time: %s | Period: #%d | Runtime: %d minutes\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		macd, rsi := 0.0, 0.0
		if btcData.DynamicIndicators != nil {
			if v, ok := btcData.DynamicIndicators["macd"]; ok {
				macd = v
			}
			if v, ok := btcData.DynamicIndicators["rsi_7"]; ok {
				rsi = v
			} else if v, ok := btcData.DynamicIndicators["rsi_14"]; ok {
				rsi = v
			}
		}
		sb.WriteString(fmt.Sprintf("BTC Snapshot: Price %.2f | 1h %+.2f%% | 4h %+.2f%% | MACD %.4f | RSI %.2f\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h, macd, rsi))
	}

	return sb.String()
}

func (e *StrategyEngine) formatCoinSourceTag(sources []string) string {
	if len(sources) > 1 {
		// 多信号源组合
		hasAI500 := false
		hasOITop := false
		hasOILow := false
		for _, s := range sources {
			switch s {
			case "ai500":
				hasAI500 = true
			case "oi_top":
				hasOITop = true
			case "oi_low":
				hasOILow = true
			}
		}
		if hasAI500 && hasOITop {
			return " (AI500+OI_Top dual signal)"
		}
		if hasAI500 && hasOILow {
			return " (AI500+OI_Low dual signal)"
		}
		if hasOITop && hasOILow {
			return " (OI_Top+OI_Low)"
		}
		return " (Multiple sources)"
	} else if len(sources) == 1 {
		switch sources[0] {
		case "ai500":
			return " (AI500)"
		case "oi_top":
			return " (OI_Top 持仓增加)"
		case "oi_low":
			return " (OI_Low 持仓减少)"
		case "static":
			return " (Manual selection)"
		}
	}
	return ""
}

// ============================================================================
// Market Data Formatting
// ============================================================================

// appendVolumeAuxForCZSC 在缠论标签模式下追加核心量能指标（VolMult、OBV），供 AI 辅助确认回抽是否有资金承接
func (e *StrategyEngine) appendVolumeAuxForCZSC(sb *strings.Builder, data *market.Data, indicators store.IndicatorConfig) {
	if data.DynamicIndicators == nil {
		return
	}
	if flag, ok := data.DynamicIndicators["volume_data_stale_flag"]; ok && flag > 0 {
		sb.WriteString("⚠️ Volume data might be stale，请将 vol_mult 仅作弱提示。\n\n")
	}
	if indicators.EnableVolMult {
		if v, ok := data.DynamicIndicators["vol_mult"]; ok {
			sb.WriteString(fmt.Sprintf("VolMult (放量倍数) = %.3f（>1 为放量，辅助确认该笔/回抽是否有资金承接）。\n", v))
		}
		if v, ok := data.DynamicIndicators["realtime_rolling_volmult"]; ok {
			sb.WriteString(fmt.Sprintf("RealtimeRollingVolMult = %.3f\n", v))
		}
		sb.WriteString("\n")
	}
	tf := indicators.Klines.PrimaryTimeframe
	if tf == "" {
		tf = "5m"
	}
	if data.TimeframeData != nil {
		if tfData, ok := data.TimeframeData[tf]; ok && len(tfData.Klines) >= 3 {
			kl := tfData.Klines
			n := len(kl)
			obvVals := make([]float64, n)
			prevClose := kl[0].Close
			for i := 1; i < n; i++ {
				obvVals[i] = obvVals[i-1]
				switch {
				case kl[i].Close > prevClose:
					obvVals[i] += kl[i].Volume
				case kl[i].Close < prevClose:
					obvVals[i] -= kl[i].Volume
				}
				prevClose = kl[i].Close
			}
			startIdx := n - 5
			if startIdx < 0 {
				startIdx = 0
			}
			deltaObv := obvVals[n-1] - obvVals[startIdx]
			denom := math.Abs(obvVals[startIdx])
			if denom < 1e-8 {
				denom = 1.0
			}
			relChange := deltaObv / denom
			trend := "Neutral"
			if relChange > 0.03 {
				trend = "Rising"
			} else if relChange < -0.03 {
				trend = "Falling"
			}
			priceStart := kl[startIdx].Close
			priceEnd := kl[n-1].Close
			obvUp := obvVals[n-1] > obvVals[startIdx]*1.001
			obvDown := obvVals[n-1] < obvVals[startIdx]*0.999
			priceUp := priceEnd > priceStart*1.001
			priceDown := priceEnd < priceStart*0.999
			divergence := "None"
			if priceUp && obvDown {
				divergence = "Bearish (Price up, Volume down)"
			} else if priceDown && obvUp {
				divergence = "Bullish (Price down, Volume up)"
			}
			sb.WriteString(fmt.Sprintf("OBV Trend (%s): %s. Divergence: %s.\n\n", strings.ToUpper(tf), trend, divergence))
		}
	}
}

func (e *StrategyEngine) formatMarketData(data *market.Data, czscLabels *CZSCLabels) string {
	var sb strings.Builder
	indicators := e.config.Indicators

	// 明确标注币种
	sb.WriteString(fmt.Sprintf("=== %s Market Data ===\n\n", data.Symbol))
	sb.WriteString(fmt.Sprintf("current_price = %.4f", data.CurrentPrice))

	// 缠论开关开启且本币有标签：以标签为主，并保留核心量能指标（VolMult、OBV）辅助背驰/回抽确认
	if indicators.EnableCZSC && czscLabels != nil {
		sb.WriteString("\n\n")
		sb.WriteString("## CZSC 缠论标签（笔 / 线段 / 中枢 / 1/2/3类买卖点）\n\n")
		jsonBytes, _ := json.MarshalIndent(czscLabels, "", "  ")
		sb.WriteString("```json\n")
		sb.Write(jsonBytes)
		sb.WriteString("\n```\n\n")
		sb.WriteString("**请主要依据以上缠论标签执行浪浪交易法，直接对笔、线段、中枢与买卖点做出反应，无需从原始K线自行推断结构。**\n\n")
		// 量价不完全分家：保留 VolMult / OBV 等量能指标，供 AI 确认回抽是否有资金承接
		e.appendVolumeAuxForCZSC(&sb, data, indicators)
		return sb.String()
	}
	// 使用 DynamicIndicators 透传用户配置的指标（如 EMA200、RSI14）给 AI
	if len(data.DynamicIndicators) > 0 {
		keys := make([]string, 0, len(data.DynamicIndicators))
		for k := range data.DynamicIndicators {
			keys = append(keys, k)
		}
		for _, k := range keys {
			// 根据策略配置有选择地暴露指标，避免在 Prompt 中出现用户未勾选的高级指标字段名。
			switch k {
			case "volume_data_stale_flag":
				// 内部标记，不作为数值指标暴露给 AI
				continue
			case "volume_poc", "poc_deviation_pct":
				if !indicators.EnableVolumePOC {
					continue
				}
			case "long_liq_usd", "short_liq_usd", "liq_long_short_ratio":
				if !indicators.EnableLiquidation {
					continue
				}
			default:
				// 将来若有 depth_* 一类键名，可在此按 EnableOrderBookDepth 开关过滤
				if strings.HasPrefix(k, "depth_") && !indicators.EnableOrderBookDepth {
					continue
				}
			}
			v := data.DynamicIndicators[k]
			readable := strings.ReplaceAll(k, "_", "")
			sb.WriteString(fmt.Sprintf(", current_%s = %.3f", readable, v))
		}
	}
	sb.WriteString("\n\n")

	// 专门为 POC 与爆仓热度写一段自然语言解释，避免 AI 误解字段含义（受策略开关控制）。
	if indicators.EnableVolumePOC {
		if poc, ok := data.DynamicIndicators["volume_poc"]; ok && poc > 0 {
			dev := data.DynamicIndicators["poc_deviation_pct"]
			sb.WriteString(fmt.Sprintf("POC (Volume Point of Control): 主筹码密集区价格约为 %.4f，当前价格相对 POC 偏离 %.2f%%（正值=在筹码上方，负值=在筹码下方）。\n\n", poc, dev))
		}
	}
	if indicators.EnableLiquidation {
		if longLiq, okL := data.DynamicIndicators["long_liq_usd"]; okL {
			if shortLiq, okS := data.DynamicIndicators["short_liq_usd"]; okS {
				ratio := data.DynamicIndicators["liq_long_short_ratio"]
				sb.WriteString(fmt.Sprintf("爆仓热度：最近一段时间内，多头爆仓约 %.0f USDT，空头爆仓约 %.0f USDT，多空爆仓比(多/空)=%.2f，用于判断是否存在“杀多/杀空”型流动性收集。\n\n",
					longLiq, shortLiq, ratio))
			}
		}
	}

	// 成交量数据新鲜度提示：若标记为 stale，提醒 AI 谨慎使用 vol_mult
	if flag, ok := data.DynamicIndicators["volume_data_stale_flag"]; ok && flag > 0 {
		sb.WriteString("⚠️ Volume data might be stale：当前最近 1 分钟成交量数据可能存在延迟或缓存问题，请将 vol_mult / realtime_rolling_volmult 仅作为弱提示，而不要当成强过滤条件。\n\n")
	}

	// 深度图：仅在策略勾选 EnableOrderBookDepth 时才接入，避免对所有策略增加额外网络开销。
	if indicators.EnableOrderBookDepth {
		if depth, err := market.AnalyzeMarketDepth(data.Symbol, "binance"); err == nil && depth != nil && depth.MidPrice > 0 {
			bidUSD := depth.BidNotional1Pct
			askUSD := depth.AskNotional1Pct
			sb.WriteString(fmt.Sprintf("订单簿 1%% 深度：在 mid≈%.4f 附近，买盘深度≈%.0f USDT，卖盘深度≈%.0f USDT，买卖深度失衡度≈%.2f（>0=买盘更厚，<0=卖盘更厚）。\n",
				depth.MidPrice, bidUSD, askUSD, depth.DepthImbalance))
			if depth.NearestBidWall != nil {
				sb.WriteString(fmt.Sprintf("最近买盘大墙：价格≈%.4f，挂单≈%.0f USDT，距离当前价约 %.2f%% 下方。\n",
					depth.NearestBidWall.Price, depth.NearestBidWall.NotionalUSD, depth.NearestBidWall.DistancePct))
			}
			if depth.NearestAskWall != nil {
				sb.WriteString(fmt.Sprintf("最近卖盘大墙：价格≈%.4f，挂单≈%.0f USDT，距离当前价约 %.2f%% 上方。\n",
					depth.NearestAskWall.Price, depth.NearestAskWall.NotionalUSD, depth.NearestAskWall.DistancePct))
			}
			sb.WriteString("警告：注意虚假挂单（Spoofing）——当看到巨大买单/卖单墙同时旁边方向的爆仓数据并未明显放大时，该墙更可能是真实支撑/压力；若爆仓金额持续激增而墙体价格附近挂单频繁撤单，则更可能是诱导市场情绪的假墙，应降低信任度。\n\n")
		}
	}

	// OBV Trend & Divergence（仅基于主周期 K 线，给 AI 语义化量价结构）
	{
		tf := indicators.Klines.PrimaryTimeframe
		if tf == "" {
			tf = "5m"
		}
		if data.TimeframeData != nil {
			if tfData, ok := data.TimeframeData[tf]; ok && len(tfData.Klines) >= 3 {
				kl := tfData.Klines
				n := len(kl)

				// 计算 OBV 全序列
				obvVals := make([]float64, n)
				prevClose := kl[0].Close
				for i := 1; i < n; i++ {
					obvVals[i] = obvVals[i-1]
					switch {
					case kl[i].Close > prevClose:
						obvVals[i] += kl[i].Volume
					case kl[i].Close < prevClose:
						obvVals[i] -= kl[i].Volume
					}
					prevClose = kl[i].Close
				}

				startIdx := n - 5
				if startIdx < 0 {
					startIdx = 0
				}
				obvStart := obvVals[startIdx]
				obvEnd := obvVals[n-1]
				priceStart := kl[startIdx].Close
				priceEnd := kl[n-1].Close

				// OBV 趋势：根据近 5 根的变化方向与幅度判断 Rising / Falling / Neutral
				deltaObv := obvEnd - obvStart
				denom := math.Abs(obvStart)
				if denom < 1e-8 {
					denom = 1.0
				}
				relChange := deltaObv / denom
				trend := "Neutral"
				// 对 5 根 K 线采用稍高敏感度（约 3%）以捕捉更多资金流趋势提示
				if relChange > 0.03 {
					trend = "Rising"
				} else if relChange < -0.03 {
					trend = "Falling"
				}

				// 价与 OBV 的背离检测：价格涨而 OBV 跌 / 价格跌而 OBV 涨
				priceUp := priceEnd > priceStart*1.001
				priceDown := priceEnd < priceStart*0.999
				obvUp := obvEnd > obvStart*1.001
				obvDown := obvEnd < obvStart*0.999
				divergence := "None"
				if priceUp && obvDown {
					divergence = "Bearish (Price up, Volume down)"
				} else if priceDown && obvUp {
					divergence = "Bullish (Price down, Volume up)"
				}

				sb.WriteString(fmt.Sprintf("OBV Trend (%s): %s. Divergence: %s.\n\n", strings.ToUpper(tf), trend, divergence))
			}
		}
	}

	if indicators.EnableFibonacci && len(data.Fibonacci) > 0 {
		sb.WriteString("Fibonacci levels (resistance/support from recent range): ")
		if v, ok := data.Fibonacci["high"]; ok {
			sb.WriteString(fmt.Sprintf("high = %.4f, ", v))
		}
		if v, ok := data.Fibonacci["low"]; ok {
			sb.WriteString(fmt.Sprintf("low = %.4f", v))
		}
		for _, k := range []string{"0.236", "0.382", "0.5", "0.618", "0.786"} {
			if v, ok := data.Fibonacci[k]; ok {
				sb.WriteString(fmt.Sprintf(", %s = %.4f", k, v))
			}
		}
		sb.WriteString("\n\n")
	}

	if indicators.EnableOI || indicators.EnableFundingRate {
		sb.WriteString(fmt.Sprintf("Additional data for %s:\n\n", data.Symbol))

		if indicators.EnableOI {
			if data.OpenInterest != nil && (data.OpenInterest.Latest != 0 || data.OpenInterest.Average != 0) {
				sb.WriteString(fmt.Sprintf("Open Interest: Latest: %.2f Average: %.2f\n\n",
					data.OpenInterest.Latest, data.OpenInterest.Average))
			} else {
				sb.WriteString("[DATA_STALE] Open Interest data is missing or zero; do not rely on it for trading decisions.\n\n")
			}
		}

		if indicators.EnableFundingRate {
			sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))
		}
	}

	if len(data.TimeframeData) > 0 {
		timeframeOrder := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w"}
		for _, tf := range timeframeOrder {
			if tfData, ok := data.TimeframeData[tf]; ok {
				sb.WriteString(fmt.Sprintf("=== %s Timeframe (oldest → latest) ===\n\n", strings.ToUpper(tf)))
				e.formatTimeframeSeriesData(&sb, tfData, indicators)
			}
		}
	} else {
		// Compatible with old data format
		if data.IntradaySeries != nil {
			klineConfig := indicators.Klines
			sb.WriteString(fmt.Sprintf("Intraday series (%s intervals, oldest → latest):\n\n", klineConfig.PrimaryTimeframe))

			if len(data.IntradaySeries.MidPrices) > 0 {
				sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
			}

			if indicators.EnableEMA && len(data.IntradaySeries.EMA20Values) > 0 {
				sb.WriteString(fmt.Sprintf("EMA indicators (20-period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
			}

			if indicators.EnableMACD && len(data.IntradaySeries.MACDValues) > 0 {
				sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
			}

			if indicators.EnableRSI {
				if len(data.IntradaySeries.RSI7Values) > 0 {
					sb.WriteString(fmt.Sprintf("RSI indicators (7-Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
				}
				if len(data.IntradaySeries.RSI14Values) > 0 {
					sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
				}
			}

			if indicators.EnableVolume && len(data.IntradaySeries.Volume) > 0 {
				sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.IntradaySeries.Volume)))
			}

			if indicators.EnableATR {
				sb.WriteString(fmt.Sprintf("3m ATR (14-period): %.3f\n\n", data.IntradaySeries.ATR14))
			}
		}

		if data.LongerTermContext != nil && indicators.Klines.EnableMultiTimeframe {
			sb.WriteString(fmt.Sprintf("Longer-term context (%s timeframe):\n\n", indicators.Klines.LongerTimeframe))

			if indicators.EnableEMA {
				sb.WriteString(fmt.Sprintf("20-Period EMA: %.3f vs. 50-Period EMA: %.3f\n\n",
					data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))
			}

			if indicators.EnableATR {
				sb.WriteString(fmt.Sprintf("3-Period ATR: %.3f vs. 14-Period ATR: %.3f\n\n",
					data.LongerTermContext.ATR3, data.LongerTermContext.ATR14))
			}

			if indicators.EnableVolume {
				sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
					data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))
			}

			if indicators.EnableMACD && len(data.LongerTermContext.MACDValues) > 0 {
				sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
			}

			if indicators.EnableRSI && len(data.LongerTermContext.RSI14Values) > 0 {
				sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
			}
		}
	}

	return sb.String()
}

func (e *StrategyEngine) formatTimeframeSeriesData(sb *strings.Builder, data *market.TimeframeSeriesData, indicators store.IndicatorConfig) {
	if len(data.Klines) > 0 {
		sb.WriteString("Time(UTC)      Open      High      Low       Close     Volume\n")
		for i, k := range data.Klines {
			t := time.UnixMilli(k.Time).UTC()
			timeStr := t.Format("01-02 15:04")
			marker := ""
			if i == len(data.Klines)-1 {
				marker = "  <- current"
			}
			sb.WriteString(fmt.Sprintf("%-14s %-9.4f %-9.4f %-9.4f %-9.4f %-12.2f%s\n",
				timeStr, k.Open, k.High, k.Low, k.Close, k.Volume, marker))
		}
		sb.WriteString("\n")
	} else if len(data.MidPrices) > 0 {
		sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.MidPrices)))
		if indicators.EnableVolume && len(data.Volume) > 0 {
			sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.Volume)))
		}
	}

	// 按策略开关与用户配置的周期输出 EMA/RSI：用户配置什么，AI 获取什么（从 DynamicIndicatorSeries 按 EMAPeriods/RSIPeriods 输出）
	if indicators.EnableEMA && len(data.DynamicIndicatorSeries) > 0 {
		periods := indicators.EMAPeriods
		if len(periods) == 0 {
			periods = []int{20, 50}
		}
		for _, p := range periods {
			key := fmt.Sprintf("ema_%d", p)
			if vals, ok := data.DynamicIndicatorSeries[key]; ok && len(vals) > 0 {
				sb.WriteString(fmt.Sprintf("EMA%d: %s\n", p, formatFloatSlice(vals)))
			}
		}
	}
	if indicators.EnableEMA && len(data.DynamicIndicatorSeries) == 0 && len(data.EMA20Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA20: %s\n", formatFloatSlice(data.EMA20Values)))
	}
	if indicators.EnableEMA && len(data.DynamicIndicatorSeries) == 0 && len(data.EMA50Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA50: %s\n", formatFloatSlice(data.EMA50Values)))
	}
	if indicators.EnableMACD && len(data.MACDValues) > 0 {
		sb.WriteString(fmt.Sprintf("MACD: %s\n", formatFloatSlice(data.MACDValues)))
	}
	if indicators.EnableRSI && len(data.DynamicIndicatorSeries) > 0 {
		periods := indicators.RSIPeriods
		if len(periods) == 0 {
			periods = []int{14}
		}
		for _, p := range periods {
			key := fmt.Sprintf("rsi_%d", p)
			if vals, ok := data.DynamicIndicatorSeries[key]; ok && len(vals) > 0 {
				sb.WriteString(fmt.Sprintf("RSI%d: %s\n", p, formatFloatSlice(vals)))
			}
		}
	}
	if indicators.EnableRSI && len(data.DynamicIndicatorSeries) == 0 {
		if len(data.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI7: %s\n", formatFloatSlice(data.RSI7Values)))
		}
		if len(data.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI14: %s\n", formatFloatSlice(data.RSI14Values)))
		}
	}
	if indicators.EnableATR && data.ATR14 > 0 {
		sb.WriteString(fmt.Sprintf("ATR14: %.4f\n", data.ATR14))
	}
	if indicators.EnableBOLL && len(data.BOLLUpper) > 0 {
		sb.WriteString(fmt.Sprintf("BOLL Upper: %s\n", formatFloatSlice(data.BOLLUpper)))
		sb.WriteString(fmt.Sprintf("BOLL Middle: %s\n", formatFloatSlice(data.BOLLMiddle)))
		sb.WriteString(fmt.Sprintf("BOLL Lower: %s\n", formatFloatSlice(data.BOLLLower)))
	}

	sb.WriteString("\n")
}

func (e *StrategyEngine) formatQuantData(data *QuantData) string {
	if data == nil {
		return ""
	}

	indicators := e.config.Indicators
	if !indicators.EnableQuantOI && !indicators.EnableQuantNetflow {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 %s Quantitative Data:\n", data.Symbol))

	if len(data.PriceChange) > 0 {
		sb.WriteString("Price Change: ")
		timeframes := []string{"5m", "15m", "1h", "4h", "12h", "24h"}
		parts := []string{}
		for _, tf := range timeframes {
			if v, ok := data.PriceChange[tf]; ok {
				parts = append(parts, fmt.Sprintf("%s: %+.4f%%", tf, v*100))
			}
		}
		sb.WriteString(strings.Join(parts, " | "))
		sb.WriteString("\n")
	}

	if indicators.EnableQuantNetflow && data.Netflow != nil {
		sb.WriteString("Fund Flow (Netflow):\n")
		timeframes := []string{"5m", "15m", "1h", "4h", "12h", "24h"}

		if data.Netflow.Institution != nil {
			if data.Netflow.Institution.Future != nil && len(data.Netflow.Institution.Future) > 0 {
				sb.WriteString("  Institutional Futures:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Institution.Future[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
			if data.Netflow.Institution.Spot != nil && len(data.Netflow.Institution.Spot) > 0 {
				sb.WriteString("  Institutional Spot:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Institution.Spot[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
		}

		if data.Netflow.Personal != nil {
			if data.Netflow.Personal.Future != nil && len(data.Netflow.Personal.Future) > 0 {
				sb.WriteString("  Retail Futures:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Personal.Future[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
			if data.Netflow.Personal.Spot != nil && len(data.Netflow.Personal.Spot) > 0 {
				sb.WriteString("  Retail Spot:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Personal.Spot[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
		}
	}

	if indicators.EnableQuantOI && len(data.OI) > 0 {
		for exchange, oiData := range data.OI {
			if len(oiData.Delta) > 0 {
				sb.WriteString(fmt.Sprintf("Open Interest (%s):\n", exchange))
				for _, tf := range []string{"5m", "15m", "1h", "4h", "12h", "24h"} {
					if d, ok := oiData.Delta[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %+.4f%% (%s)\n", tf, d.OIDeltaPercent, formatFlowValue(d.OIDeltaValue)))
					}
				}
			}
		}
	}

	return sb.String()
}

func formatFlowValue(v float64) string {
	sign := ""
	if v >= 0 {
		sign = "+"
	}
	absV := v
	if absV < 0 {
		absV = -absV
	}
	if absV >= 1e9 {
		return fmt.Sprintf("%s%.2fB", sign, v/1e9)
	} else if absV >= 1e6 {
		return fmt.Sprintf("%s%.2fM", sign, v/1e6)
	} else if absV >= 1e3 {
		return fmt.Sprintf("%s%.2fK", sign, v/1e3)
	}
	return fmt.Sprintf("%s%.2f", sign, v)
}

func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = fmt.Sprintf("%.4f", v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}

// ============================================================================
// AI Response Parsing
// ============================================================================

func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int, btcEthPosRatio, altcoinPosRatio float64, allowStagedTakeProfit bool) (*FullDecision, error) {
	cotTrace := extractCoTTrace(aiResponse)

	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("failed to extract decisions: %w", err)
	}

	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage, btcEthPosRatio, altcoinPosRatio, allowStagedTakeProfit); err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}, fmt.Errorf("decision validation failed: %w", err)
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: decisions,
	}, nil
}

func extractCoTTrace(response string) string {
	if match := reReasoningTag.FindStringSubmatch(response); match != nil && len(match) > 1 {
		logger.Infof("✓ Extracted reasoning chain using <reasoning> tag")
		return strings.TrimSpace(match[1])
	}

	if decisionIdx := strings.Index(response, "<decision>"); decisionIdx > 0 {
		logger.Infof("✓ Extracted content before <decision> tag as reasoning chain")
		return strings.TrimSpace(response[:decisionIdx])
	}

	jsonStart := strings.Index(response, "[")
	if jsonStart > 0 {
		logger.Infof("⚠️  Extracted reasoning chain using old format ([ character separator)")
		return strings.TrimSpace(response[:jsonStart])
	}

	return strings.TrimSpace(response)
}

func extractDecisions(response string) ([]Decision, error) {
	s := removeInvisibleRunes(response)
	s = strings.TrimSpace(s)
	s = fixMissingQuotes(s)

	var jsonPart string
	if match := reDecisionTag.FindStringSubmatch(s); match != nil && len(match) > 1 {
		jsonPart = strings.TrimSpace(match[1])
		logger.Infof("✓ Extracted JSON using <decision> tag")
	} else {
		jsonPart = s
		logger.Infof("⚠️  <decision> tag not found, searching JSON in full text")
	}

	jsonPart = fixMissingQuotes(jsonPart)

	if m := reJSONFence.FindStringSubmatch(jsonPart); m != nil && len(m) > 1 {
		jsonContent := strings.TrimSpace(m[1])
		jsonContent = compactArrayOpen(jsonContent)
		jsonContent = fixMissingQuotes(jsonContent)
		return parseJSONDecisions(jsonContent, response)
	}

	jsonContent := strings.TrimSpace(reJSONArray.FindString(jsonPart))
	if jsonContent == "" {
		logger.Infof("⚠️  [SafeFallback] AI didn't output JSON decision, entering safe wait mode")

		cotSummary := jsonPart
		if len(cotSummary) > 240 {
			cotSummary = cotSummary[:240] + "..."
		}

		fallbackDecision := Decision{
			Symbol:    "ALL",
			Action:    "wait",
			Reasoning: fmt.Sprintf("Model didn't output structured JSON decision, entering safe wait; summary: %s", cotSummary),
		}

		return []Decision{fallbackDecision}, nil
	}

	jsonContent = compactArrayOpen(jsonContent)
	jsonContent = fixMissingQuotes(jsonContent)

	return parseJSONDecisions(jsonContent, response)
}

// repairTruncatedJSON 在 AI 返回被截断的 JSON 时补全缺失的 ] }，便于解析出已完整的前若干条决策
func repairTruncatedJSON(s string) string {
	var stack []byte
	inString := false
	escape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == '[' || c == '{' {
			stack = append(stack, c)
			continue
		}
		if c == ']' || c == '}' {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) == 0 {
		return s
	}
	var suffix []byte
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '[' {
			suffix = append(suffix, ']')
		} else {
			suffix = append(suffix, '}')
		}
	}
	return s + string(suffix)
}

func fixMissingQuotes(jsonStr string) string {
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"")
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"")
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")

	jsonStr = strings.ReplaceAll(jsonStr, "［", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "］", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "｛", "{")
	jsonStr = strings.ReplaceAll(jsonStr, "｝", "}")
	jsonStr = strings.ReplaceAll(jsonStr, "：", ":")
	jsonStr = strings.ReplaceAll(jsonStr, "，", ",")

	jsonStr = strings.ReplaceAll(jsonStr, "【", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "】", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "〔", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "〕", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "、", ",")

	jsonStr = strings.ReplaceAll(jsonStr, "　", " ")

	return jsonStr
}

// removeThousandSeparatorsInJSON 移除 JSON 中数字之间的千分位逗号（如 67,317 -> 67317），
// 仅影响「数字,数字」模式，不破坏字段间的逗号分隔符。循环直到无匹配。
func removeThousandSeparatorsInJSON(s string) string {
	for {
		cleaned := reThousandSep.ReplaceAllString(s, "$1$2")
		if cleaned == s {
			return s
		}
		s = cleaned
	}
}

// parseJSONDecisions 解析 JSON 并支持千分位清洗、截断补全与 wait 降级。
func parseJSONDecisions(jsonContent string, response string) ([]Decision, error) {
	jsonContent = removeThousandSeparatorsInJSON(jsonContent)
	if err := validateJSONFormat(jsonContent); err != nil {
		return nil, fmt.Errorf("JSON format validation failed: %w\nJSON content: %s\nFull response:\n%s", err, jsonContent, response)
	}
	var decisions []Decision
	err := json.Unmarshal([]byte(jsonContent), &decisions)
	if err != nil && strings.Contains(err.Error(), "unexpected end of JSON input") {
		if repaired := repairTruncatedJSON(jsonContent); repaired != jsonContent {
			if err2 := json.Unmarshal([]byte(repaired), &decisions); err2 == nil {
				logger.Infof("✓ Repaired truncated JSON and parsed %d decisions", len(decisions))
				return decisions, nil
			}
		}
	}
	if err != nil {
		errMsg := err.Error()
		if syntaxErr, ok := err.(*json.SyntaxError); ok {
			offset := int(syntaxErr.Offset)
			start := offset - 40
			if start < 0 {
				start = 0
			}
			end := offset + 40
			if end > len(jsonContent) {
				end = len(jsonContent)
			}
			errMsg = fmt.Sprintf("%s (offset %d, snippet: ...%s<<<HERE>>>%s...)", errMsg, offset, jsonContent[start:offset], jsonContent[offset:end])
		}
		if strings.Contains(jsonContent, "wait") && strings.Contains(jsonContent, "action") {
			logger.Infof("⚠️  [WaitFallback] JSON parse failed (e.g. bad chars in reason), treating as single wait decision. Parse error: %s", errMsg)
			return []Decision{{
				Symbol:    "ALL",
				Action:    "wait",
				Reasoning: "Parse fallback: response indicated wait but JSON had invalid characters; treated as wait.",
			}}, nil
		}
		return nil, fmt.Errorf("JSON parsing failed: %s\nJSON content: %s", errMsg, jsonContent)
	}
	return decisions, nil
}

func validateJSONFormat(jsonStr string) error {
	trimmed := strings.TrimSpace(jsonStr)

	if !reArrayHead.MatchString(trimmed) {
		if strings.HasPrefix(trimmed, "[") && !strings.Contains(trimmed[:min(20, len(trimmed))], "{") {
			return fmt.Errorf("not a valid decision array (must contain objects {}), actual content: %s", trimmed[:min(50, len(trimmed))])
		}
		return fmt.Errorf("JSON must start with [{ (whitespace allowed), actual: %s", trimmed[:min(20, len(trimmed))])
	}

	if strings.Contains(jsonStr, "~") {
		return fmt.Errorf("JSON cannot contain range symbol ~, all numbers must be precise single values")
	}

	for i := 0; i < len(jsonStr)-4; i++ {
		if jsonStr[i] >= '0' && jsonStr[i] <= '9' &&
			jsonStr[i+1] == ',' &&
			jsonStr[i+2] >= '0' && jsonStr[i+2] <= '9' &&
			jsonStr[i+3] >= '0' && jsonStr[i+3] <= '9' &&
			jsonStr[i+4] >= '0' && jsonStr[i+4] <= '9' {
			return fmt.Errorf("JSON numbers cannot contain thousand separator comma, found: %s", jsonStr[i:min(i+10, len(jsonStr))])
		}
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func removeInvisibleRunes(s string) string {
	return reInvisibleRunes.ReplaceAllString(s, "")
}

func compactArrayOpen(s string) string {
	return reArrayOpenSpace.ReplaceAllString(strings.TrimSpace(s), "[{")
}

// ============================================================================
// Decision Validation
// ============================================================================

func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, btcEthPosRatio, altcoinPosRatio float64, allowStagedTakeProfit bool) error {
	for i := range decisions {
		if err := validateDecision(&decisions[i], accountEquity, btcEthLeverage, altcoinLeverage, btcEthPosRatio, altcoinPosRatio, allowStagedTakeProfit); err != nil {
			return fmt.Errorf("decision #%d validation failed: %w", i+1, err)
		}
	}
	return nil
}

func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, btcEthPosRatio, altcoinPosRatio float64, allowStagedTakeProfit bool) error {
	validActions := map[string]bool{
		"open_long":  true,
		"open_short": true,
		"hold":       true,
		"wait":       true,
		"close_long":  true,
		"close_short": true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("invalid action: %s", d.Action)
	}

	if d.Action == "open_long" || d.Action == "open_short" {
		maxLeverage := altcoinLeverage
		posRatio := altcoinPosRatio
		maxPositionValue := accountEquity * posRatio
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage
			posRatio = btcEthPosRatio
			maxPositionValue = accountEquity * posRatio
		}

		if d.Leverage <= 0 {
			return fmt.Errorf("leverage must be greater than 0: %d", d.Leverage)
		}
		if d.Leverage > maxLeverage {
			logger.Infof("⚠️  [Leverage Fallback] %s leverage exceeded (%dx > %dx), auto-adjusting to limit %dx",
				d.Symbol, d.Leverage, maxLeverage, maxLeverage)
			d.Leverage = maxLeverage
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("position size must be greater than 0: %.2f", d.PositionSizeUSD)
		}

		const minPositionSizeGeneral = 12.0
		const minPositionSizeBTCETH = 60.0

		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			if d.PositionSizeUSD < minPositionSizeBTCETH {
				return fmt.Errorf("%s opening amount too small (%.2f USDT), must be ≥%.2f USDT", d.Symbol, d.PositionSizeUSD, minPositionSizeBTCETH)
			}
		} else {
			if d.PositionSizeUSD < minPositionSizeGeneral {
				return fmt.Errorf("opening amount too small (%.2f USDT), must be ≥%.2f USDT", d.PositionSizeUSD, minPositionSizeGeneral)
			}
		}

		tolerance := maxPositionValue * 0.01
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
				return fmt.Errorf("BTC/ETH single coin position value cannot exceed %.0f USDT (%.1fx account equity), actual: %.0f", maxPositionValue, posRatio, d.PositionSizeUSD)
			} else {
				return fmt.Errorf("altcoin single coin position value cannot exceed %.0f USDT (%.1fx account equity), actual: %.0f", maxPositionValue, posRatio, d.PositionSizeUSD)
			}
		}
		useATRTrailing := d.ATRTrailingSlMult > 0 || d.ATRTrailingTpMult > 0 || len(d.ATRTrailingTpStages) > 0
		if !allowStagedTakeProfit {
			if len(d.TakeProfitStages) > 0 {
				return fmt.Errorf("staged take profit is disabled; use only take_profit (single price for full position close)")
			}
			if len(d.ATRTrailingTpStages) > 0 {
				return fmt.Errorf("staged ATR take profit is disabled; use only atr_tp_mult for full position close")
			}
		}
		if !useATRTrailing {
			// 静态 TP/SL 模式：必须至少提供一个止损 +（单一 TP 或分批 TP，后者需允许分批止盈）
			if d.StopLoss <= 0 {
				return fmt.Errorf("stop loss must be greater than 0")
			}

			hasSingleTP := d.TakeProfit > 0
			hasStages := len(d.TakeProfitStages) > 0
			if !hasSingleTP && !hasStages {
				return fmt.Errorf("either take_profit or take_profit_stages must be provided when ATR trailing is disabled")
			}
			if hasSingleTP && hasStages {
				return fmt.Errorf("use either take_profit or take_profit_stages, not both")
			}
			if hasStages && !allowStagedTakeProfit {
				return fmt.Errorf("staged take profit is disabled; use only take_profit for full position close")
			}

			if hasSingleTP {
				if d.Action == "open_long" {
					if d.StopLoss >= d.TakeProfit {
						return fmt.Errorf("for long positions, stop loss price must be less than take profit price")
					}
				} else {
					if d.StopLoss <= d.TakeProfit {
						return fmt.Errorf("for short positions, stop loss price must be greater than take profit price")
					}
				}
			}

			if hasStages {
				var sumPct float64
				var prevPrice float64
				for idx, st := range d.TakeProfitStages {
					if st.Price <= 0 {
						return fmt.Errorf("take_profit_stages[%d].price must be > 0", idx)
					}
					if st.ClosePct <= 0 || st.ClosePct > 100 {
						return fmt.Errorf("take_profit_stages[%d].close_pct must be in (0,100]", idx)
					}
					sumPct += st.ClosePct

					if idx == 0 {
						prevPrice = st.Price
						continue
					}
					if d.Action == "open_long" {
						if st.Price <= prevPrice {
							return fmt.Errorf("for long positions, take_profit_stages prices must be strictly increasing")
						}
					} else { // open_short
						if st.Price >= prevPrice {
							return fmt.Errorf("for short positions, take_profit_stages prices must be strictly decreasing")
						}
					}
					prevPrice = st.Price
				}
				if sumPct > 100.01 {
					return fmt.Errorf("sum of take_profit_stages.close_pct must be ≤ 100, got %.2f", sumPct)
				}
			}
		}

		// No minimum risk-reward ratio enforced: 1:1 or below is allowed (user may prefer tight TP for scalping).
	}

	return nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// detectLanguage detects language from text content
// Returns LangChinese if text contains Chinese characters, otherwise LangEnglish
func detectLanguage(text string) Language {
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			return LangChinese
		}
	}
	return LangEnglish
}
