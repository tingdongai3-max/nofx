package trader

import (
	"fmt"
	"nofx/logger"
	"nofx/store"
	"strconv"
	"strings"
	"time"
)

const fixedAutoCloseCheckInterval = 15 * time.Second

// startFixedAutoCloseMonitor enforces the new hard rule:
// live positions are closed by fixed timer only, never by dynamic exit logic.
func (at *AutoTrader) startFixedAutoCloseMonitor() {
	if at == nil || at.store == nil || at.trader == nil {
		return
	}

	at.monitorWg.Add(1)
	go func() {
		defer at.monitorWg.Done()

		ticker := time.NewTicker(fixedAutoCloseCheckInterval)
		defer ticker.Stop()

		hold := at.getFixedAutoCloseHoldDuration()
		logger.Infof("⏱ Started fixed auto-close monitor (hold=%s, check=%s)", hold, fixedAutoCloseCheckInterval)

		at.processFixedAutoCloseCycle(time.Now().UTC())

		for {
			select {
			case now := <-ticker.C:
				at.processFixedAutoCloseCycle(now.UTC())
			case <-at.stopMonitorCh:
				logger.Info("⏹ Stopped fixed auto-close monitor")
				return
			}
		}
	}()
}

func (at *AutoTrader) processFixedAutoCloseCycle(now time.Time) {
	if at == nil || at.store == nil || at.trader == nil {
		return
	}

	hold := at.getFixedAutoCloseHoldDuration()
	expired, err := at.store.Position().GetExpiredAutoClosePositions(
		at.id,
		now.UTC().UnixMilli(),
		int64(hold/time.Millisecond),
	)
	if err != nil {
		logger.Infof("⚠️ Fixed auto-close monitor failed to load positions: %v", err)
		return
	}

	for _, pos := range expired {
		if pos == nil || pos.ID <= 0 || !strings.EqualFold(pos.Status, "OPEN") {
			continue
		}
		if !at.claimFixedAutoClose(pos.ID) {
			continue
		}
		if err := at.executeFixedAutoClose(pos, now.UTC(), hold); err != nil {
			at.releaseFixedAutoClose(pos.ID)
			logger.Infof("⚠️ Fixed timed exit failed for %s %s: %v", pos.Symbol, pos.Side, err)
		}
	}
}

func (at *AutoTrader) claimFixedAutoClose(positionID int64) bool {
	if at == nil || positionID <= 0 {
		return false
	}

	at.fixedAutoCloseMu.Lock()
	defer at.fixedAutoCloseMu.Unlock()

	if at.fixedAutoClosingIDs == nil {
		at.fixedAutoClosingIDs = make(map[int64]struct{})
	}
	if _, exists := at.fixedAutoClosingIDs[positionID]; exists {
		return false
	}
	at.fixedAutoClosingIDs[positionID] = struct{}{}
	return true
}

func (at *AutoTrader) releaseFixedAutoClose(positionID int64) {
	if at == nil || positionID <= 0 {
		return
	}

	at.fixedAutoCloseMu.Lock()
	defer at.fixedAutoCloseMu.Unlock()

	delete(at.fixedAutoClosingIDs, positionID)
}

func (at *AutoTrader) executeFixedAutoClose(pos *store.TraderPosition, now time.Time, hold time.Duration) error {
	if at == nil || pos == nil {
		return nil
	}

	action := "close_long"
	closeFn := at.trader.CloseLong
	if strings.EqualFold(pos.Side, "SHORT") {
		action = "close_short"
		closeFn = at.trader.CloseShort
	}

	reasoning := buildFixedTimedExitReason(hold)
	actionRecord := store.DecisionAction{
		Action:        action,
		Symbol:        pos.Symbol,
		Quantity:      pos.Quantity,
		Leverage:      pos.Leverage,
		Reasoning:     reasoning,
		Timestamp:     now,
		Success:       false,
		ExecutionMode: "fixed_timed_exit",
	}

	if marketPrice, err := at.trader.GetMarketPrice(pos.Symbol); err == nil && marketPrice > 0 {
		actionRecord.Price = marketPrice
	} else {
		actionRecord.Price = pos.EntryPrice
	}

	order, err := closeFn(pos.Symbol, 0)
	if err != nil {
		return err
	}
	if orderID := extractOrderID(order); orderID != "" && orderID != "0" {
		if parsed, parseErr := strconv.ParseInt(orderID, 10, 64); parseErr == nil {
			actionRecord.OrderID = parsed
		}
	}

	actionRecord.Success = true
	record := &store.DecisionRecord{
		Timestamp:    now,
		Decisions:    []store.DecisionAction{actionRecord},
		ExecutionLog: []string{fmt.Sprintf("✓ %s %s executed by fixed timed exit", pos.Symbol, action)},
		Success:      true,
	}
	if err := at.saveDecision(record); err != nil {
		logger.Infof("⚠️ Failed to persist fixed timed exit record: %v", err)
	}

	logger.Infof(
		"⏱ Fixed timed exit executed: trader=%s symbol=%s side=%s auto_close_at=%d",
		at.id,
		pos.Symbol,
		pos.Side,
		pos.AutoCloseAt,
	)
	return nil
}

func buildFixedTimedExitReason(hold time.Duration) string {
	minutes := int(hold.Round(time.Minute) / time.Minute)
	if minutes <= 0 {
		minutes = 15
	}
	return fmt.Sprintf("固定 %d 分钟平仓", minutes)
}

func (at *AutoTrader) getFixedAutoCloseHoldDuration() time.Duration {
	return defaultRealBacktestHoldDuration
}

func (at *AutoTrader) shouldSuppressDynamicCloseAction(action string) bool {
	switch action {
	case "close_long", "close_short":
		return true
	default:
		return false
	}
}

// ============================================================================
// Risk Control Helpers
// ============================================================================

// isBTCETH checks if a symbol is BTC or ETH
func isBTCETH(symbol string) bool {
	symbol = strings.ToUpper(symbol)
	return strings.HasPrefix(symbol, "BTC") || strings.HasPrefix(symbol, "ETH")
}

// enforcePositionValueRatio checks and enforces position value ratio limits (CODE ENFORCED)
// Returns the adjusted position size (capped if necessary) and whether the position was capped
// positionSizeUSD: the original position size in USD
// equity: the account equity
// symbol: the trading symbol
func (at *AutoTrader) enforcePositionValueRatio(positionSizeUSD float64, equity float64, symbol string) (float64, bool) {
	if at.config.StrategyConfig == nil {
		return positionSizeUSD, false
	}

	riskControl := at.config.StrategyConfig.RiskControl

	var maxPositionValueRatio float64
	if isBTCETH(symbol) {
		maxPositionValueRatio = riskControl.BTCETHMaxPositionValueRatio
		if maxPositionValueRatio <= 0 {
			maxPositionValueRatio = 5.0
		}
	} else {
		maxPositionValueRatio = riskControl.AltcoinMaxPositionValueRatio
		if maxPositionValueRatio <= 0 {
			maxPositionValueRatio = 1.0
		}
	}

	maxPositionValue := equity * maxPositionValueRatio

	if positionSizeUSD > maxPositionValue {
		logger.Infof("  ⚠️ [RISK CONTROL] Position %.2f USDT exceeds limit (equity %.2f × %.1fx = %.2f USDT max for %s), capping",
			positionSizeUSD, equity, maxPositionValueRatio, maxPositionValue, symbol)
		return maxPositionValue, true
	}

	return positionSizeUSD, false
}

// enforceMinPositionSize checks minimum position size (CODE ENFORCED)
func (at *AutoTrader) enforceMinPositionSize(positionSizeUSD float64) error {
	if at.config.StrategyConfig == nil {
		return nil
	}

	minSize := at.config.StrategyConfig.RiskControl.MinPositionSize
	if minSize <= 0 {
		minSize = 12
	}

	if positionSizeUSD < minSize {
		return fmt.Errorf("❌ [RISK CONTROL] Position %.2f USDT below minimum (%.2f USDT)", positionSizeUSD, minSize)
	}
	return nil
}

// enforceMaxPositions checks maximum positions count (CODE ENFORCED)
func (at *AutoTrader) enforceMaxPositions(currentPositionCount int) error {
	if at.config.StrategyConfig == nil {
		return nil
	}

	maxPositions := at.config.StrategyConfig.RiskControl.MaxPositions
	if maxPositions <= 0 {
		maxPositions = 3
	}

	if currentPositionCount >= maxPositions {
		return fmt.Errorf("❌ [RISK CONTROL] Already at max positions (%d/%d)", currentPositionCount, maxPositions)
	}
	return nil
}

// getSideFromAction converts order action to side (BUY/SELL)
func getSideFromAction(action string) string {
	switch action {
	case "open_long", "close_short":
		return "BUY"
	case "open_short", "close_long":
		return "SELL"
	default:
		return "BUY"
	}
}
