package trader

import (
	"context"
	"fmt"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"sort"
	"strings"
	"sync"
	"time"
)

const watchdogPositionSyncInterval = 10 * time.Second
const watchdogPositionCacheMaxAge = 5 * time.Second
const defenseTimeframe = "15m"
const defenseBars = 60
const atrWatchdogFallbackInterval = 60 * time.Second // ATR 狗兜底：数据流无推送时最多 60s 检查一次，避免轮询封禁

// ATRTrailingState 单仓位 ATR 移动止盈止损状态（由 AI 输出，机器狗按价格监控触发）
type ATRTrailingState struct {
	Symbol         string
	Side           string   // "long" or "short"
	EntryPrice     float64
	OriginalQty    float64  // 开仓总仓位（不变）；每档平仓量按该数量的百分比计算，不是按当前剩余
	CurrentQty     float64  // 当前剩余仓位（分批止盈后会减少）
	AtrSlMult      float64
	AtrTpMult      float64
	TpStages       []kernel.ATRTrailingStage // 最多 3 阶段，atr_mult 必须严格递增
	TriggeredStage [3]bool                   // 已触发的阶段（每档只触发一次，更新 AI 参数也不重复触发）
	// 1R 首批止盈 + 动态追踪止损
	EntryATR        float64 // 开仓时 ATR，用于 1R 计算
	FirstBatchClosed bool    // 是否已执行 1R 首批 40% 止盈
	TrailingSLPrice  float64 // 1R 后追踪止损价（仅向有利方向移动）
	AIStopLoss       float64 // AI 通过 hold/wait 下发的 stop_loss 价；与 TrailingSLPrice 取并集（谁先触发听谁的）
}

type AIDynamicTrailingConfig struct {
	Symbol                string
	Side                  string
	TrailingActivationPct float64 // Take-profit target progress ratio (0-100)
	TrailingRetracePct    float64 // Allowed giveback ratio of earned profit (0-100)
}

type WatchdogHooks struct {
	GetPositions                func() ([]map[string]interface{}, error)
	ClosePosition               func(symbol, side string, quantity float64) (map[string]interface{}, error)
	GetAIDynamicTrailingConfigs func() map[string]*AIDynamicTrailingConfig
	GetTakeProfitTargets        func() map[string]float64
	GetStopLossTargets          func() map[string]float64
	OnSafetyFloorUpdate         func(symbol, side string, stop float64)
	OnSafetyFloorClear          func(symbol, side string)
	OnAIDynamicTrailingClear    func(symbol, side string)
}

var (
	watchdogLogMu     sync.Mutex
	watchdogLastPrint string // fingerprint of last logged params
)

// normalizeATRStages 排序并校验：atr_mult 严格递增，每档只触发一次；最多 3 档；基于开仓总仓位的百分比
func normalizeATRStages(stages []kernel.ATRTrailingStage) []kernel.ATRTrailingStage {
	if len(stages) == 0 {
		return nil
	}
	// 按 AtrMult 升序，保证第一目标最小、后续目标依次变大
	sorted := make([]kernel.ATRTrailingStage, len(stages))
	copy(sorted, stages)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].AtrMult < sorted[j].AtrMult })
	// 严格递增：后一档倍数必须大于前一档，否则丢弃
	out := make([]kernel.ATRTrailingStage, 0, 3)
	prev := -1.0
	for _, s := range sorted {
		if s.AtrMult <= prev {
			continue
		}
		prev = s.AtrMult
		out = append(out, s)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// OnWatchdogClose is called after the watchdog closes a position (for logging and DB record).
// symbol, action (close_long/close_short), orderResult from exchange, quantity, exitPrice, entryPrice.
type OnWatchdogClose func(symbol, action string, orderResult map[string]interface{}, quantity, exitPrice, entryPrice float64)

// RunRiskWatchdog starts the system defense watchdog goroutine.
// It listens to the exchange-bound kline stream and latest price feed, then force-closes
// positions immediately when enabled hard defense conditions are hit.
func RunRiskWatchdog(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	getExchange func() string,
	onClose OnWatchdogClose,
	hooks *WatchdogHooks,
) {
	go runRiskWatchdogLoop(ctx, trader, getConfig, getExchange, onClose, hooks)
}

type watchdogPosition struct {
	Symbol                string
	Side                  string
	Source                string
	Quantity              float64
	EntryPrice            float64
	Leverage              float64
	InitialStopLoss       float64
	SafetyBuffer          float64
	SafetyFloorSL         float64
	TrailingActivationPct float64
	TrailingRetracePct    float64
	TakeProfitPrice       float64
	HasNativeTP           bool
	TPOrderIDs            []string
	IsTrailingActive      bool
	ExtremePrice          float64
}

type recentWatchdogPositionCache interface {
	GetPositionsFromCacheMaxAge(maxAge time.Duration) ([]map[string]interface{}, bool)
}

func runRiskWatchdogLoop(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	getExchange func() string,
	onClose OnWatchdogClose,
	hooks *WatchdogHooks,
) {
	klineCh := market.SubscribeKlineUpdates()
	priceCh, cancelPriceSub := market.SubscribePriceUpdates(2048)
	defer cancelPriceSub()
	syncTicker := time.NewTicker(watchdogPositionSyncInterval)
	defer syncTicker.Stop()
	exchange := getWatchdogExchange(getExchange)
	tracked := make(map[string]*watchdogPosition)
	syncWatchdogPositions(ctx, trader, getConfig, exchange, hooks, tracked)

	for {
		select {
		case <-ctx.Done():
			logger.Infof("🛡️ System Risk Watchdog stopped")
			return
		case price, ok := <-priceCh:
			if !ok {
				return
			}
			if !strings.EqualFold(price.Exchange, exchange) {
				continue
			}
			for _, pos := range tracked {
				if pos == nil || pos.Symbol != price.Symbol {
					continue
				}
				if handleWatchdogPriceEvent(ctx, trader, getConfig, pos, price.Price, onClose, hooks) {
					clearWatchdogPositionState(pos.Symbol, pos.Side, exchange, tracked, hooks)
				}
			}
		case ev, ok := <-klineCh:
			if !ok {
				return
			}
			if !strings.EqualFold(ev.Exchange, exchange) || ev.Interval != defenseTimeframe {
				continue
			}
			syncWatchdogPositions(ctx, trader, getConfig, exchange, hooks, tracked)
			for _, pos := range tracked {
				if pos == nil || pos.Symbol != market.Normalize(ev.Symbol) {
					continue
				}
				if handleWatchdogDefenseEvent(ctx, trader, getConfig, exchange, pos, onClose, hooks) {
					clearWatchdogPositionState(pos.Symbol, pos.Side, exchange, tracked, hooks)
				}
			}
		case <-syncTicker.C:
			syncWatchdogPositions(ctx, trader, getConfig, exchange, hooks, tracked)
		}
	}
}

func syncWatchdogPositions(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	exchange string,
	hooks *WatchdogHooks,
	tracked map[string]*watchdogPosition,
) {
	if ctx.Err() != nil {
		return
	}
	cfg := getConfig()
	if cfg == nil {
		return
	}
	tryLogWatchdogParams(cfg, exchange)

	var (
		positions []map[string]interface{}
		err       error
	)
	if hooks != nil && hooks.GetPositions != nil {
		positions, err = hooks.GetPositions()
	} else if cachedTrader, ok := trader.(recentWatchdogPositionCache); ok {
		if cached, hit := cachedTrader.GetPositionsFromCacheMaxAge(watchdogPositionCacheMaxAge); hit {
			positions = cached
		}
	}
	if positions == nil {
		positions, err = trader.GetPositions()
		if err != nil {
			logger.Warnf("🛡️ [系统风控] GetPositions failed: %v", err)
			return
		}
	}
	aiConfigs := map[string]*AIDynamicTrailingConfig{}
	if hooks != nil && hooks.GetAIDynamicTrailingConfigs != nil {
		aiConfigs = hooks.GetAIDynamicTrailingConfigs()
	}
	takeProfitTargets := map[string]float64{}
	if hooks != nil && hooks.GetTakeProfitTargets != nil {
		takeProfitTargets = hooks.GetTakeProfitTargets()
	}
	stopLossTargets := map[string]float64{}
	if hooks != nil && hooks.GetStopLossTargets != nil {
		stopLossTargets = hooks.GetStopLossTargets()
	}
	active := make(map[string]struct{}, len(positions))
	for _, posMap := range positions {
		pos, ok := buildWatchdogPosition(posMap, stopLossTargets)
		if !ok {
			continue
		}
		key := watchdogPositionKey(pos.Symbol, pos.Side)
		active[key] = struct{}{}
		if existing := tracked[key]; existing != nil {
			pos.IsTrailingActive = existing.IsTrailingActive
			pos.ExtremePrice = existing.ExtremePrice
			pos.SafetyFloorSL = existing.SafetyFloorSL
			if pos.SafetyBuffer <= 0 {
				pos.SafetyBuffer = existing.SafetyBuffer
			}
		}
		if cfg.TrailingActivationPct > 0 {
			pos.TrailingActivationPct = cfg.TrailingActivationPct
		} else if aiCfg := aiConfigs[key]; aiCfg != nil {
			pos.TrailingActivationPct = aiCfg.TrailingActivationPct
		}
		if tp := takeProfitTargets[key]; tp > 0 {
			pos.TakeProfitPrice = tp
		}
		if cfg.TrailingRetracePct > 0 {
			pos.TrailingRetracePct = cfg.TrailingRetracePct
		} else if aiCfg := aiConfigs[key]; aiCfg != nil {
			pos.TrailingRetracePct = aiCfg.TrailingRetracePct
		}
		if pos.TrailingActivationPct <= 0 || pos.TrailingRetracePct <= 0 {
			pos.IsTrailingActive = false
			pos.ExtremePrice = 0
		}
		if pos.IsTrailingActive {
			pos.SafetyFloorSL = 0
		}
		if hooks != nil && hooks.OnSafetyFloorUpdate != nil {
			hooks.OnSafetyFloorUpdate(pos.Symbol, pos.Side, pos.SafetyFloorSL)
		}
		tracked[key] = &pos
		_, _ = market.GetWithTimeframesWithExchange(pos.Symbol, []string{defenseTimeframe}, defenseTimeframe, nil, nil, exchange)
	}
	for key, pos := range tracked {
		if _, ok := active[key]; ok {
			continue
		}
		clearWatchdogPositionState(pos.Symbol, pos.Side, exchange, tracked, hooks)
	}
}

func watchdogEnabled(cfg *store.StrategyConfig) bool {
	if cfg == nil {
		return false
	}
	return watchdogClassicDefenseEnabled(cfg) || cfg.TrailingActivationPct > 0 || cfg.TrailingRetracePct > 0
}

func watchdogClassicDefenseEnabled(cfg *store.StrategyConfig) bool {
	if cfg == nil {
		return false
	}
	return cfg.Indicators.EnableFractalDefense || cfg.Indicators.EnableEMA20GapDefense || cfg.Indicators.Enable3BarTrailing
}

func getWatchdogExchange(getExchange func() string) string {
	if getExchange != nil {
		if ex := strings.TrimSpace(getExchange()); ex != "" {
			return ex
		}
	}
	return "binance"
}

func watchdogPositionKey(symbol, side string) string {
	return market.Normalize(symbol) + "_" + strings.ToLower(strings.TrimSpace(side))
}

func clearWatchdogPositionState(symbol, side, exchange string, tracked map[string]*watchdogPosition, hooks *WatchdogHooks) {
	delete(tracked, watchdogPositionKey(symbol, side))
	market.ReleaseRealtimeKlineStream(symbol, defenseTimeframe, exchange)
	if hooks != nil && hooks.OnAIDynamicTrailingClear != nil {
		hooks.OnAIDynamicTrailingClear(symbol, side)
	}
	if hooks != nil && hooks.OnSafetyFloorClear != nil {
		hooks.OnSafetyFloorClear(symbol, side)
	}
	logger.Infof("🛡️ [毫秒风控] 清理订阅状态: symbol=%s side=%s", market.Normalize(symbol), side)
}

func buildWatchdogPosition(posMap map[string]interface{}, stopLossTargets map[string]float64) (watchdogPosition, bool) {
	rawSymbol, _ := posMap["symbol"].(string)
	symbol := market.Normalize(rawSymbol)
	if symbol == "" {
		return watchdogPosition{}, false
	}
	sideStr, _ := posMap["side"].(string)
	positionAmt, _ := posMap["positionAmt"].(float64)
	entryPrice, _ := posMap["entryPrice"].(float64)
	markPrice, _ := posMap["markPrice"].(float64)
	leverage, _ := posMap["leverage"].(float64)
	source, _ := posMap["source"].(string)
	takeProfitPrice, _ := posMap["take_profit"].(float64)
	stopLossPrice, _ := posMap["stop_loss"].(float64)
	hasNativeTP, _ := posMap["has_native_tp"].(bool)
	var tpOrderIDs []string
	switch ids := posMap["tp_order_ids"].(type) {
	case []string:
		tpOrderIDs = append(tpOrderIDs, ids...)
	case []interface{}:
		for _, raw := range ids {
			if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
				tpOrderIDs = append(tpOrderIDs, s)
			}
		}
	}
	if entryPrice == 0 {
		entryPrice = markPrice
	}
	if leverage <= 0 {
		leverage = 1
	}

	side := "short"
	quantity := -positionAmt
	switch strings.ToLower(strings.TrimSpace(sideStr)) {
	case "long":
		side = "long"
		if positionAmt > 0 {
			quantity = positionAmt
		}
	case "short":
		side = "short"
		if positionAmt < 0 {
			quantity = -positionAmt
		} else {
			quantity = positionAmt
		}
	default:
		if positionAmt >= 0 {
			side = "long"
			quantity = positionAmt
		}
	}
	if quantity <= 0 {
		return watchdogPosition{}, false
	}
	key := watchdogPositionKey(symbol, side)
	initialStopLoss := 0.0
	if stopLossTargets != nil {
		initialStopLoss = stopLossTargets[key]
	}
	if initialStopLoss <= 0 {
		initialStopLoss = stopLossPrice
	}
	safetyBuffer := 0.0
	if initialStopLoss > 0 && entryPrice > 0 {
		safetyBuffer = absFloat(entryPrice-initialStopLoss) * 1.5
	}
	return watchdogPosition{
		Symbol:          symbol,
		Side:            side,
		Source:          strings.ToLower(strings.TrimSpace(source)),
		Quantity:        quantity,
		EntryPrice:      entryPrice,
		Leverage:        leverage,
		InitialStopLoss: initialStopLoss,
		TakeProfitPrice: takeProfitPrice,
		HasNativeTP:     hasNativeTP || len(tpOrderIDs) > 0,
		TPOrderIDs:      tpOrderIDs,
		SafetyBuffer:    safetyBuffer,
	}, true
}

func handleWatchdogPriceEvent(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	pos *watchdogPosition,
	currentPrice float64,
	onClose OnWatchdogClose,
	hooks *WatchdogHooks,
) bool {
	if pos == nil || currentPrice <= 0 || ctx.Err() != nil {
		return false
	}
	if handleDryRunHardTPSL(trader, pos, currentPrice, onClose, hooks) {
		return true
	}
	if pos.HasNativeTP {
		return handleDynamicTrailingStop(ctx, trader, getConfig(), pos, currentPrice, onClose, hooks)
	}
	return handleDynamicTrailingStop(ctx, trader, getConfig(), pos, currentPrice, onClose, hooks)
}

func handleDryRunHardTPSL(
	trader Trader,
	pos *watchdogPosition,
	currentPrice float64,
	onClose OnWatchdogClose,
	hooks *WatchdogHooks,
) bool {
	if pos == nil || pos.Source != "dry_run" {
		return false
	}
	isLong := pos.Side == "long"
	triggered := false
	logMsg := ""

	switch {
	case isLong && pos.TakeProfitPrice > 0 && currentPrice >= pos.TakeProfitPrice:
		triggered = true
		logMsg = "[模拟盘虚拟撮合] 多单触发硬止盈"
	case isLong && pos.InitialStopLoss > 0 && currentPrice <= pos.InitialStopLoss:
		triggered = true
		logMsg = "[模拟盘虚拟撮合] 多单触发硬止损"
	case !isLong && pos.TakeProfitPrice > 0 && currentPrice <= pos.TakeProfitPrice:
		triggered = true
		logMsg = "[模拟盘虚拟撮合] 空单触发硬止盈"
	case !isLong && pos.InitialStopLoss > 0 && currentPrice >= pos.InitialStopLoss:
		triggered = true
		logMsg = "[模拟盘虚拟撮合] 空单触发硬止损"
	}
	if !triggered {
		return false
	}

	order, err := closeWatchdogPosition(trader, pos.Symbol, pos.Side, 0, hooks)
	if err != nil {
		logger.Warnf("🛡️ %s，但平仓失败: %s %s: %v", logMsg, pos.Symbol, closeAction(pos.Side), err)
		return false
	}
	logger.Infof("🛡️ %s。symbol=%s side=%s price=%.6f tp=%.6f sl=%.6f",
		logMsg, pos.Symbol, pos.Side, currentPrice, pos.TakeProfitPrice, pos.InitialStopLoss)
	if onClose != nil {
		onClose(pos.Symbol, closeAction(pos.Side), order, pos.Quantity, currentPrice, pos.EntryPrice)
	}
	return true
}

func handleWatchdogDefenseEvent(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	exchange string,
	pos *watchdogPosition,
	onClose OnWatchdogClose,
	hooks *WatchdogHooks,
) bool {
	cfg := getConfig()
	if cfg == nil || !watchdogClassicDefenseEnabled(cfg) {
		return false
	}
	if pos == nil || ctx.Err() != nil {
		return false
	}
	klines, ok := market.GetRealtimeKlines(pos.Symbol, defenseTimeframe, exchange, defenseBars)
	if !ok || len(klines) < 25 {
		return false
	}
	currentPrice, _, ok := market.GetLatestPrice(pos.Symbol, exchange, 0)
	if !ok || currentPrice <= 0 {
		currentPrice = klines[len(klines)-1].Close
	}
	reason := evaluateDefenseTrigger(cfg, *pos, klines, currentPrice)
	if reason == "" {
		return false
	}
	order, err := closeWatchdogPosition(trader, pos.Symbol, pos.Side, 0, hooks)
	if err != nil {
		logger.Warnf("🛡️ [系统风控] %s 触发后平仓失败: %s %s: %v", reason, pos.Symbol, closeAction(pos.Side), err)
		return false
	}
	logger.Infof("🛡️ [系统风控] 触发 %s，强制平%s单！symbol=%s price=%.4f tf=%s",
		reason, ternary(pos.Side == "long", "多", "空"), pos.Symbol, currentPrice, defenseTimeframe)
	if onClose != nil {
		onClose(pos.Symbol, closeAction(pos.Side), order, pos.Quantity, currentPrice, pos.EntryPrice)
	}
	return true
}

func handleDynamicTrailingStop(
	ctx context.Context,
	trader Trader,
	cfg *store.StrategyConfig,
	pos *watchdogPosition,
	currentPrice float64,
	onClose OnWatchdogClose,
	hooks *WatchdogHooks,
) bool {
	if cfg == nil || pos == nil || ctx.Err() != nil {
		return false
	}
	if !pos.IsTrailingActive {
		updateWatchdogSafetyFloor(pos, currentPrice)
		if hooks != nil && hooks.OnSafetyFloorUpdate != nil {
			hooks.OnSafetyFloorUpdate(pos.Symbol, pos.Side, pos.SafetyFloorSL)
		}
		effectiveSafetyStop := effectiveWatchdogSafetyStop(*pos)
		if effectiveSafetyStop > 0 && stopHit(pos.Side == "long", currentPrice, effectiveSafetyStop) {
			order, err := closeWatchdogPosition(trader, pos.Symbol, pos.Side, 0, hooks)
			if err != nil {
				logger.Warnf("🛡️ [底层护盘] 被动动态止损触发但平仓失败: %s %s: %v", pos.Symbol, closeAction(pos.Side), err)
				return false
			}
			logger.Infof("🛡️ [底层护盘] 1.5x 初始风险被动护盘触发。symbol=%s side=%s price=%.6f stop=%.6f initial_sl=%.6f floor=%.6f",
				pos.Symbol, pos.Side, currentPrice, effectiveSafetyStop, pos.InitialStopLoss, pos.SafetyFloorSL)
			if onClose != nil {
				onClose(pos.Symbol, closeAction(pos.Side), order, pos.Quantity, currentPrice, pos.EntryPrice)
			}
			return true
		}
	}
	activationPct := pos.TrailingActivationPct
	retracePct := pos.TrailingRetracePct
	if activationPct <= 0 || retracePct <= 0 || pos.EntryPrice <= 0 || currentPrice <= 0 {
		return false
	}
	if !watchdogHasValidTakeProfitTarget(*pos) {
		return false
	}

	currentMovePct := calculateWatchdogFavorableMovePct(*pos, currentPrice)
	activationMovePct := calculateWatchdogActivationMovePctFromTargetProgress(*pos)
	if activationMovePct <= 0 {
		return false
	}
	if currentMovePct >= activationMovePct && !pos.IsTrailingActive {
		pos.IsTrailingActive = true
		pos.ExtremePrice = currentPrice
		if hooks != nil && hooks.OnSafetyFloorClear != nil {
			hooks.OnSafetyFloorClear(pos.Symbol, pos.Side)
		}
		logger.Infof("🛡️ [追踪止盈已激活] symbol=%s side=%s favorable_move=%.2f%% activation_progress=%.2f%% activation_move=%.2f%% tp=%.6f retrace=%.2f%% extreme=%.6f",
			pos.Symbol, pos.Side, currentMovePct, activationPct, activationMovePct, pos.TakeProfitPrice, retracePct, pos.ExtremePrice)
	}
	if !pos.IsTrailingActive {
		return false
	}

	if pos.Side == "long" {
		if currentPrice > pos.ExtremePrice {
			pos.ExtremePrice = currentPrice
		}
		profitDistance := pos.ExtremePrice - pos.EntryPrice
		dynamicRetraceAmount := profitDistance * (retracePct / 100)
		closeLine := pos.ExtremePrice - dynamicRetraceAmount
		if profitDistance > 0 && currentPrice <= closeLine {
			order, err := closeWatchdogPosition(trader, pos.Symbol, pos.Side, 0, hooks)
			if err != nil {
				logger.Warnf("🛡️ [毫秒风控] 追踪止盈触发但平仓失败: %s %s: %v", pos.Symbol, closeAction(pos.Side), err)
				return false
			}
			logger.Infof("🚨 [毫秒风控] 触发追踪止盈利润回吐线，以市价强制抢跑平仓！ symbol=%s side=%s price=%.6f extreme=%.6f profit_distance=%.6f giveback=%.2f%% close_line=%.6f",
				pos.Symbol, pos.Side, currentPrice, pos.ExtremePrice, profitDistance, retracePct, closeLine)
			if onClose != nil {
				onClose(pos.Symbol, closeAction(pos.Side), order, pos.Quantity, currentPrice, pos.EntryPrice)
			}
			return true
		}
		return false
	}

	if pos.ExtremePrice == 0 || currentPrice < pos.ExtremePrice {
		pos.ExtremePrice = currentPrice
	}
	profitDistance := pos.EntryPrice - pos.ExtremePrice
	dynamicRetraceAmount := profitDistance * (retracePct / 100)
	closeLine := pos.ExtremePrice + dynamicRetraceAmount
	if profitDistance > 0 && currentPrice >= closeLine {
		order, err := closeWatchdogPosition(trader, pos.Symbol, pos.Side, 0, hooks)
		if err != nil {
			logger.Warnf("🛡️ [毫秒风控] 追踪止盈触发但平仓失败: %s %s: %v", pos.Symbol, closeAction(pos.Side), err)
			return false
		}
		logger.Infof("🚨 [毫秒风控] 触发追踪止盈利润回吐线，以市价强制抢跑平仓！ symbol=%s side=%s price=%.6f extreme=%.6f profit_distance=%.6f giveback=%.2f%% close_line=%.6f",
			pos.Symbol, pos.Side, currentPrice, pos.ExtremePrice, profitDistance, retracePct, closeLine)
		if onClose != nil {
			onClose(pos.Symbol, closeAction(pos.Side), order, pos.Quantity, currentPrice, pos.EntryPrice)
		}
		return true
	}
	return false
}

func calculateWatchdogFavorableMovePct(pos watchdogPosition, currentPrice float64) float64 {
	if pos.EntryPrice <= 0 || currentPrice <= 0 {
		return 0
	}
	movePct := (currentPrice - pos.EntryPrice) / pos.EntryPrice * 100
	if pos.Side == "short" {
		movePct = -movePct
	}
	return movePct
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func updateWatchdogSafetyFloor(pos *watchdogPosition, currentPrice float64) {
	if pos == nil || currentPrice <= 0 || pos.SafetyBuffer <= 0 {
		return
	}
	if pos.Side == "long" {
		potentialSL := currentPrice - pos.SafetyBuffer
		if potentialSL > pos.SafetyFloorSL {
			pos.SafetyFloorSL = potentialSL
		}
		return
	}
	potentialSL := currentPrice + pos.SafetyBuffer
	if pos.SafetyFloorSL <= 0 || potentialSL < pos.SafetyFloorSL {
		pos.SafetyFloorSL = potentialSL
	}
}

func effectiveWatchdogSafetyStop(pos watchdogPosition) float64 {
	if pos.Side == "long" {
		return maxStopForState(true, pos.InitialStopLoss, pos.SafetyFloorSL)
	}
	return maxStopForState(false, pos.InitialStopLoss, pos.SafetyFloorSL)
}

func maxStopForState(isLong bool, a, b float64) float64 {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if isLong {
		if a > b {
			return a
		}
		return b
	}
	if a < b {
		return a
	}
	return b
}

func watchdogHasValidTakeProfitTarget(pos watchdogPosition) bool {
	if pos.EntryPrice <= 0 || pos.TakeProfitPrice <= 0 {
		return false
	}
	if pos.Side == "short" {
		return pos.TakeProfitPrice < pos.EntryPrice
	}
	return pos.TakeProfitPrice > pos.EntryPrice
}

func calculateWatchdogActivationMovePctFromTargetProgress(pos watchdogPosition) float64 {
	if !watchdogHasValidTakeProfitTarget(pos) || pos.TrailingActivationPct <= 0 {
		return 0
	}
	targetMovePct := calculateWatchdogFavorableMovePct(pos, pos.TakeProfitPrice)
	if targetMovePct <= 0 {
		return 0
	}
	return targetMovePct * pos.TrailingActivationPct / 100
}

func stopHit(isLong bool, price, stop float64) bool {
	if stop <= 0 {
		return false
	}
	if isLong {
		return price <= stop
	}
	return price >= stop
}

func closeWatchdogPosition(trader Trader, symbol, side string, quantity float64, hooks *WatchdogHooks) (map[string]interface{}, error) {
	if err := trader.CancelAllOrders(symbol); err != nil {
		logger.Warnf("🛡️ [系统风控] cancel all orders before close failed: %s: %v", symbol, err)
	}
	if hooks != nil && hooks.ClosePosition != nil {
		return hooks.ClosePosition(symbol, side, quantity)
	}
	if side == "long" {
		return trader.CloseLong(symbol, quantity)
	}
	return trader.CloseShort(symbol, quantity)
}

func closeAction(side string) string {
	if side == "long" {
		return "close_long"
	}
	return "close_short"
}

func evaluateDefenseTrigger(cfg *store.StrategyConfig, pos watchdogPosition, klines []market.Kline, currentPrice float64) string {
	if len(klines) < 25 {
		return ""
	}
	last := klines[len(klines)-1]

	if cfg.Indicators.Enable3BarTrailing && len(klines) >= 4 {
		low3, high3 := trailingWindowExtremes(klines[len(klines)-4 : len(klines)-1])
		if pos.Side == "long" && currentPrice < low3 {
			return fmt.Sprintf("3K线动量追踪防守(现价 %.4f 跌破近3K低点 %.4f)", currentPrice, low3)
		}
		if pos.Side == "short" && currentPrice > high3 {
			return fmt.Sprintf("3K线动量追踪防守(现价 %.4f 突破近3K高点 %.4f)", currentPrice, high3)
		}
	}

	if cfg.Indicators.EnableEMA20GapDefense {
		ema20 := market.ExportCalculateEMA(klines, 20)
		if ema20 > 0 {
			if pos.Side == "long" && last.High < ema20 {
				return fmt.Sprintf("EMA20 缺口防守(High %.4f < EMA20 %.4f)", last.High, ema20)
			}
			if pos.Side == "short" && last.Low > ema20 {
				return fmt.Sprintf("EMA20 缺口防守(Low %.4f > EMA20 %.4f)", last.Low, ema20)
			}
		}
	}

	if cfg.Indicators.EnableFractalDefense && len(klines) >= 5 {
		if pos.Side == "long" {
			if trigger, ref := detectLongFractalDefense(klines); trigger {
				return fmt.Sprintf("2B 假突破防守(最高 %.4f 刺破前高 %.4f 后收回)", last.High, ref)
			}
		} else {
			if trigger, ref := detectShortFractalDefense(klines); trigger {
				return fmt.Sprintf("2B 假突破防守(最低 %.4f 跌破前低 %.4f 后收回)", last.Low, ref)
			}
		}
	}

	return ""
}

func trailingWindowExtremes(klines []market.Kline) (low float64, high float64) {
	if len(klines) == 0 {
		return 0, 0
	}
	low = klines[0].Low
	high = klines[0].High
	for _, k := range klines[1:] {
		if k.Low < low {
			low = k.Low
		}
		if k.High > high {
			high = k.High
		}
	}
	return low, high
}

func detectLongFractalDefense(klines []market.Kline) (bool, float64) {
	last := klines[len(klines)-1]
	refHigh := 0.0
	for i := 1; i <= 3 && len(klines)-1-i >= 0; i++ {
		h := klines[len(klines)-1-i].High
		if h > refHigh {
			refHigh = h
		}
	}
	if refHigh <= 0 || last.High <= refHigh || last.Close >= refHigh {
		return false, 0
	}
	body := absFloat(last.Close - last.Open)
	upperWick := last.High - maxFloat(last.Open, last.Close)
	if upperWick <= 0 || upperWick < body {
		return false, 0
	}
	return true, refHigh
}

func detectShortFractalDefense(klines []market.Kline) (bool, float64) {
	last := klines[len(klines)-1]
	refLow := 0.0
	for i := 1; i <= 3 && len(klines)-1-i >= 0; i++ {
		l := klines[len(klines)-1-i].Low
		if i == 1 || l < refLow {
			refLow = l
		}
	}
	if refLow <= 0 || last.Low >= refLow || last.Close <= refLow {
		return false, 0
	}
	body := absFloat(last.Close - last.Open)
	lowerWick := minFloat(last.Open, last.Close) - last.Low
	if lowerWick <= 0 || lowerWick < body {
		return false, 0
	}
	return true, refLow
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// tryLogWatchdogParams 仅首次检测到持仓或配置参数变更时打印，避免刷屏
func tryLogWatchdogParams(cfg *store.StrategyConfig, exchange string) {
	fp := fmt.Sprintf("fractal=%t|ema_gap=%t|bar3=%t|trail_act=%.4f|trail_ret=%.4f|tf=%s|ex=%s",
		cfg.Indicators.EnableFractalDefense,
		cfg.Indicators.EnableEMA20GapDefense,
		cfg.Indicators.Enable3BarTrailing,
		cfg.TrailingActivationPct,
		cfg.TrailingRetracePct,
		defenseTimeframe,
		exchange,
	)
	watchdogLogMu.Lock()
	if watchdogLastPrint == fp {
		watchdogLogMu.Unlock()
		return
	}
	watchdogLastPrint = fp
	watchdogLogMu.Unlock()
	logger.Infof("🛡️ System Risk Watchdog armed | fractal=%t | ema20_gap=%t | 3bar=%t | trailing_activation(tp_progress)=%.2f%% | trailing_retrace(profit_giveback)=%.2f%% | tf=%s | exchange=%s",
		cfg.Indicators.EnableFractalDefense,
		cfg.Indicators.EnableEMA20GapDefense,
		cfg.Indicators.Enable3BarTrailing,
		cfg.TrailingActivationPct,
		cfg.TrailingRetracePct,
		defenseTimeframe,
		exchange,
	)
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// RunATRTrailingWatchdog 当策略开启 ATR 移动止盈止损时，按价格流（周期拉取 K 线/ATR）监控，触发时向交易所发止盈/止损/分批止盈。
// getATRState 返回 symbol_side -> *ATRTrailingState；onPartialClose(symbol, side, closedQty, stageIndex)；onFullClose(symbol, side)。
func RunATRTrailingWatchdog(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	getExchange func() string,
	getATRState func() map[string]*ATRTrailingState,
	onPartialClose func(symbol, side string, closedQty float64, stageIndex int),
	onFullClose func(symbol, side string),
) {
	go runATRTrailingWatchdogLoop(ctx, trader, getConfig, getExchange, getATRState, onPartialClose, onFullClose)
}

func runATRTrailingWatchdogLoop(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	getExchange func() string,
	getATRState func() map[string]*ATRTrailingState,
	onPartialClose func(symbol, side string, closedQty float64, stageIndex int),
	onFullClose func(symbol, side string),
) {
	// 数据流驱动：交易所 K 线 WebSocket 有推送时才计算，避免轮询与封禁
	klineCh := market.SubscribeKlineUpdates()
	fallbackTicker := time.NewTicker(atrWatchdogFallbackInterval)
	defer fallbackTicker.Stop()

	// 启动时跑一轮：确保所需 symbol 的 K 线流已订阅（ensureKlineStream），并做一次检查
	runATRTrailingCycle(ctx, trader, getConfig, getExchange, getATRState, onPartialClose, onFullClose)

	for {
		select {
		case <-ctx.Done():
			logger.Infof("🛡️ ATR Trailing Watchdog stopped")
			return
		case ev, ok := <-klineCh:
			if !ok {
				return
			}
			cfg := getConfig()
			if cfg == nil || !cfg.Indicators.EnableATRTrailing {
				continue
			}
			tf := cfg.Indicators.TrailingTimeframe
			if tf == "" {
				tf = "5m"
			}
			exchange := "binance"
			if getExchange != nil {
				if ex := getExchange(); ex != "" {
					exchange = ex
				}
			}
			// 仅当推送的周期与交易所匹配、且该 symbol 在 ATR 状态中时才跑周期
			if ev.Interval != tf || !strings.EqualFold(ev.Exchange, exchange) {
				continue
			}
			stateMap := getATRState()
			if stateMap == nil || len(stateMap) == 0 {
				continue
			}
			symNorm := market.Normalize(ev.Symbol)
			hasSymbol := false
			for _, state := range stateMap {
				if state != nil && market.Normalize(state.Symbol) == symNorm {
					hasSymbol = true
					break
				}
			}
			if !hasSymbol {
				continue
			}
			runATRTrailingCycle(ctx, trader, getConfig, getExchange, getATRState, onPartialClose, onFullClose)
		case <-fallbackTicker.C:
			// 兜底：长时间无推送时仍检查一次（读 WS 缓冲，不额外拉 REST）
			runATRTrailingCycle(ctx, trader, getConfig, getExchange, getATRState, onPartialClose, onFullClose)
		}
	}
}

func runATRTrailingCycle(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	getExchange func() string,
	getATRState func() map[string]*ATRTrailingState,
	onPartialClose func(symbol, side string, closedQty float64, stageIndex int),
	onFullClose func(symbol, side string),
) {
	cfg := getConfig()
	if cfg == nil || !cfg.Indicators.EnableATRTrailing {
		return
	}
	stateMap := getATRState()
	if stateMap == nil || len(stateMap) == 0 {
		return
	}
	exchange := "binance"
	if getExchange != nil {
		if ex := getExchange(); ex != "" {
			exchange = ex
		}
	}
	opts := kernel.IndicatorParamsFromConfig(cfg.Indicators)
	tf := "5m"
	if cfg.Indicators.TrailingTimeframe != "" {
		tf = cfg.Indicators.TrailingTimeframe
	}

	for _, state := range stateMap {
		if ctx.Err() != nil {
			return
		}
		if state == nil || state.CurrentQty <= 0 || state.EntryPrice <= 0 {
			continue
		}
		// OriginalQty 未设时用 CurrentQty 兼容旧状态
		originalQty := state.OriginalQty
		if originalQty <= 0 {
			originalQty = state.CurrentQty
		}
		symbol := market.Normalize(state.Symbol)
		data, err := market.GetWithTimeframesWithExchange(symbol, []string{tf}, tf, nil, opts, exchange)
		if err != nil {
			logger.Warnf("🛡️ ATR Watchdog: market.Get %s failed: %v", symbol, err)
			continue
		}
		atr := 0.0
		if data.DynamicIndicators != nil {
			if v, ok := data.DynamicIndicators["atr_14"]; ok && v > 0 {
				atr = v
			}
		}
		if atr <= 0 {
			logger.Warnf("🛡️ ATR Watchdog: %s %s skip (ATR=0 or missing in DynamicIndicators, enable ATR in strategy)", state.Symbol, state.Side)
			continue
		}
		price := data.CurrentPrice
		isLong := strings.ToLower(state.Side) == "long"

		// 预设指令并计算触发价，便于日志核对机器狗是否在等待信号
		slPrice := 0.0
		if state.AtrSlMult > 0 {
			slPrice = state.EntryPrice - state.AtrSlMult*atr
			if !isLong {
				slPrice = state.EntryPrice + state.AtrSlMult*atr
			}
		}
		tpPrice := 0.0
		if state.AtrTpMult > 0 {
			tpPrice = state.EntryPrice + state.AtrTpMult*atr
			if !isLong {
				tpPrice = state.EntryPrice - state.AtrTpMult*atr
			}
		}
		stagePrices := ""
		for i := 0; i < len(state.TpStages) && i < 3; i++ {
			if state.TriggeredStage[i] {
				continue
			}
			sp := state.EntryPrice + state.TpStages[i].AtrMult*atr
			if !isLong {
				sp = state.EntryPrice - state.TpStages[i].AtrMult*atr
			}
			if stagePrices != "" {
				stagePrices += ", "
			}
			stagePrices += fmt.Sprintf("stage%d=%.4f(%.0f%%)", i, sp, state.TpStages[i].ClosePct)
		}
		if stagePrices == "" && state.AtrTpMult > 0 {
			stagePrices = fmt.Sprintf("tp=%.4f", tpPrice)
		}
		logger.Infof("🛡️ ATR Watchdog: %s %s 预设 entry=%.4f ATR=%.4f sl=%.2f×→%.4f | %s | current=%.4f (等待信号)",
			state.Symbol, state.Side, state.EntryPrice, atr, state.AtrSlMult, slPrice, stagePrices, price)

		// 止损：多单 price <= entry - atr_sl*ATR；空单 price >= entry + atr_sl*ATR
		if state.AtrSlMult > 0 {
			slPrice := state.EntryPrice - state.AtrSlMult*atr
			if !isLong {
				slPrice = state.EntryPrice + state.AtrSlMult*atr
			}
			hitSL := (isLong && price <= slPrice) || (!isLong && price >= slPrice)
			if hitSL {
			var err error
			if isLong {
				_, err = trader.CloseLong(state.Symbol, 0)
			} else {
				_, err = trader.CloseShort(state.Symbol, 0)
			}
			if err != nil {
				logger.Warnf("🛡️ ATR Watchdog: SL close %s %s failed: %v", state.Symbol, state.Side, err)
					continue
				}
				action := "close_long"
				if !isLong {
					action = "close_short"
				}
				logger.Infof("🛡️ ATR Watchdog: %s %s SL triggered price=%.4f slPrice=%.4f (%.2f×ATR)", state.Symbol, action, price, slPrice, state.AtrSlMult)
				if onFullClose != nil {
					onFullClose(state.Symbol, state.Side)
				}
				continue
			}
		}

		// 分批止盈阶段（最多 3 个）
		for i := 0; i < len(state.TpStages) && i < 3; i++ {
			if state.TriggeredStage[i] {
				continue
			}
			stage := state.TpStages[i]
			tpPrice := state.EntryPrice + stage.AtrMult*atr
			if !isLong {
				tpPrice = state.EntryPrice - stage.AtrMult*atr
			}
			hitStage := (isLong && price >= tpPrice) || (!isLong && price <= tpPrice)
			if !hitStage {
				continue
			}
			closePct := stage.ClosePct
			if closePct <= 0 || closePct > 100 {
				closePct = 100
			}
			// 每档平仓量 = 开仓总仓位的 closePct%，不是当前剩余的百分比；每档只触发一次
			closeQty := originalQty * (closePct / 100)
			if closeQty > state.CurrentQty {
				closeQty = state.CurrentQty // 剩余不足时全平
			}
			if closeQty <= 0 {
				continue
			}
			var err error
			if isLong {
				_, err = trader.CloseLong(state.Symbol, closeQty)
			} else {
				_, err = trader.CloseShort(state.Symbol, closeQty)
			}
			if err != nil {
				logger.Warnf("🛡️ ATR Watchdog: stage %d partial close %s failed: %v", i, state.Symbol, err)
				continue
			}
			logger.Infof("🛡️ ATR Watchdog: %s %s stage %d triggered price=%.4f tpPrice=%.4f close %.0f%% qty=%.4f", state.Symbol, state.Side, i, price, tpPrice, closePct, closeQty)
			if onPartialClose != nil {
				onPartialClose(state.Symbol, state.Side, closeQty, i)
			}
			state.TriggeredStage[i] = true
			state.CurrentQty -= closeQty
			if state.CurrentQty <= 0 && onFullClose != nil {
				onFullClose(state.Symbol, state.Side)
			}
		}

		// 统一止盈线（atr_tp_mult）：多单 price >= entry + atr_tp*ATR；空单 price <= entry - atr_tp*ATR
		if state.AtrTpMult > 0 && state.CurrentQty > 0 {
			tpPrice := state.EntryPrice + state.AtrTpMult*atr
			if !isLong {
				tpPrice = state.EntryPrice - state.AtrTpMult*atr
			}
			hitTP := (isLong && price >= tpPrice) || (!isLong && price <= tpPrice)
			if hitTP {
			var err error
			if isLong {
				_, err = trader.CloseLong(state.Symbol, 0)
			} else {
				_, err = trader.CloseShort(state.Symbol, 0)
			}
			if err != nil {
				logger.Warnf("🛡️ ATR Watchdog: TP close %s %s failed: %v", state.Symbol, state.Side, err)
					continue
				}
				action := "close_long"
				if !isLong {
					action = "close_short"
				}
				logger.Infof("🛡️ ATR Watchdog: %s %s TP triggered price=%.4f tpPrice=%.4f (%.2f×ATR)", state.Symbol, action, price, tpPrice, state.AtrTpMult)
				if onFullClose != nil {
					onFullClose(state.Symbol, state.Side)
				}
			}
		}
	}
}
