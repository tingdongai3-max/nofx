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

const watchdogInterval = 5 * time.Second  // 仅用于指标追踪狗
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

// RunRiskWatchdog starts the indicator-trailing risk watchdog goroutine.
// When EnableIndicatorTrailing is true, it periodically checks positions and closes them
// when price breaks the configured TrailingIndicator (e.g. long: price < EMA20 → close; short: price > EMA20 → close).
// getExchange must return the trader's connected exchange (okx, binance, etc.) - K-line data is fetched from that exchange
// to avoid cross-exchange data pollution. ctx.Done() stops the loop.
func RunRiskWatchdog(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	getExchange func() string,
	onClose OnWatchdogClose,
) {
	go runRiskWatchdogLoop(ctx, trader, getConfig, getExchange, onClose)
}

func runRiskWatchdogLoop(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	getExchange func() string,
	onClose OnWatchdogClose,
) {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Infof("🛡️ Risk Watchdog stopped")
			return
		case <-ticker.C:
			runWatchdogCycle(ctx, trader, getConfig, getExchange, onClose)
		}
	}
}

func runWatchdogCycle(
	ctx context.Context,
	trader Trader,
	getConfig func() *store.StrategyConfig,
	getExchange func() string,
	onClose OnWatchdogClose,
) {
	cfg := getConfig()
	if cfg == nil || !cfg.Indicators.EnableIndicatorTrailing {
		return
	}
	indicatorKey := strings.TrimSpace(cfg.Indicators.TrailingIndicator)
	if indicatorKey == "" {
		indicatorKey = "ema_20"
	}
	trailingTf := strings.TrimSpace(cfg.Indicators.TrailingTimeframe)
	if trailingTf == "" {
		trailingTf = "5m"
	}
	// 原样使用偏移量，支持正数（跌破后延迟平仓）与负数（未触及提前抢跑），严禁使用 math.Abs
	offsetPct := cfg.Indicators.TrailingOffsetPercent
	// 加锁读取持仓，避免与主循环并发写
	positions, err := trader.GetPositions()
	if err != nil {
		logger.Warnf("🛡️ Risk Watchdog: GetPositions failed: %v", err)
		return
	}
	if len(positions) == 0 {
		return
	}
	exchange := "binance"
	if getExchange != nil {
		if ex := getExchange(); ex != "" {
			exchange = ex
		}
	}
	tryLogWatchdogParams(indicatorKey, trailingTf, offsetPct, exchange)

	opts := kernel.IndicatorParamsFromConfig(cfg.Indicators)
	for _, posMap := range positions {
		if ctx.Err() != nil {
			return
		}
		rawSymbol, _ := posMap["symbol"].(string)
		symbol := market.Normalize(rawSymbol)
		sideStr, _ := posMap["side"].(string)
		positionAmt, _ := posMap["positionAmt"].(float64)
		entryPrice, _ := posMap["entryPrice"].(float64)
		markPrice, _ := posMap["markPrice"].(float64)
		if entryPrice == 0 {
			entryPrice = markPrice
		}

		var quantity float64
		var isLong bool
		switch strings.ToLower(sideStr) {
		case "long":
			if positionAmt > 0 {
				quantity = positionAmt
				isLong = true
			} else {
				quantity = -positionAmt
				isLong = false
			}
		case "short":
			if positionAmt < 0 {
				quantity = -positionAmt
				isLong = false
			} else {
				quantity = positionAmt
				isLong = true
			}
		default:
			if positionAmt > 0 {
				quantity = positionAmt
				isLong = true
			} else {
				quantity = -positionAmt
				isLong = false
			}
		}
		if quantity <= 0 {
			continue
		}

		// 使用 Trader 绑定的交易所拉取 K 线与指标，严禁用 Binance 数据平 OKX 仓位
		data, err := market.GetWithTimeframesWithExchange(symbol, []string{trailingTf}, trailingTf, nil, opts, exchange)
		if err != nil {
			logger.Warnf("🛡️ Risk Watchdog: market.GetWithTimeframes %s %s failed: %v", symbol, trailingTf, err)
			continue
		}
		if data.DynamicIndicators == nil {
			continue
		}
		indicatorValue, ok := data.DynamicIndicators[indicatorKey]
		if !ok {
			// 兼容前端可能传的 "EMA20" -> ema_20
			indicatorValue, ok = data.DynamicIndicators[normalizeIndicatorKey(indicatorKey)]
		}
		if !ok || indicatorValue <= 0 {
			continue
		}

		// 触发线 = 指标值 ± 偏移%；多单：价格 < 指标*(1 - offset%) 才平；空单：价格 > 指标*(1 + offset%) 才平
		var triggerLine float64
		if isLong {
			triggerLine = indicatorValue * (1 - offsetPct/100)
		} else {
			triggerLine = indicatorValue * (1 + offsetPct/100)
		}

		price := data.CurrentPrice
		shouldCloseLong := isLong && price < triggerLine
		shouldCloseShort := !isLong && price > triggerLine
		if !shouldCloseLong && !shouldCloseShort {
			continue
		}

		var order map[string]interface{}
		var action string
		if shouldCloseLong {
			action = "close_long"
			order, err = trader.CloseLong(symbol, 0)
		} else {
			action = "close_short"
			order, err = trader.CloseShort(symbol, 0)
		}
		if err != nil {
			logger.Warnf("🛡️ Risk Watchdog (Trailing Indicator): close %s %s failed: %v", symbol, action, err)
			continue
		}
		if shouldCloseLong {
			logger.Infof("🛡️ Risk Watchdog (Trailing Indicator): closed %s %s | Price %.4f < Trigger %.4f (%s %.4f - %.2f%%)",
				symbol, action, price, triggerLine, indicatorKey, indicatorValue, offsetPct)
		} else {
			logger.Infof("🛡️ Risk Watchdog (Trailing Indicator): closed %s %s | Price %.4f > Trigger %.4f (%s %.4f + %.2f%%)",
				symbol, action, price, triggerLine, indicatorKey, indicatorValue, offsetPct)
		}
		if onClose != nil {
			onClose(symbol, action, order, quantity, price, entryPrice)
		}
	}
}

// tryLogWatchdogParams 仅首次检测到持仓或配置参数变更时打印，避免每 5 秒刷屏
func tryLogWatchdogParams(indicator, timeframe string, offsetPct float64, exchange string) {
	fp := fmt.Sprintf("%s|%s|%.2f|%s", indicator, timeframe, offsetPct, exchange)
	watchdogLogMu.Lock()
	if watchdogLastPrint == fp {
		watchdogLogMu.Unlock()
		return
	}
	watchdogLastPrint = fp
	watchdogLogMu.Unlock()
	logger.Infof("🛡️ Risk Watchdog: TrailingOffsetPercent=%.2f%% (positive=delay, negative=early) | indicator=%s | tf=%s | exchange=%s",
		offsetPct, indicator, timeframe, exchange)
}

func normalizeIndicatorKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "ema_20"
	}
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "_")
	// "ema20" -> "ema_20", "middleband" -> "boll_middle_20"
	if strings.HasPrefix(s, "ema") && len(s) > 3 && s[3] != '_' {
		return "ema_" + s[3:]
	}
	if strings.Contains(s, "middle") || s == "middleband" {
		return "boll_middle_20"
	}
	return s
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
