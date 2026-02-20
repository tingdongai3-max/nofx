package trader

import (
	"context"
	"fmt"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"strings"
	"sync"
	"time"
)

const watchdogInterval = 5 * time.Second

var (
	watchdogLogMu     sync.Mutex
	watchdogLastPrint string // fingerprint of last logged params
)

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
