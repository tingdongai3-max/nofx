package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"strings"
	"time"
)

const (
	dryRunSlippagePct = 0.0005 // 0.05% 滑点：做多上浮、做空下沉
	dryRunFeePct      = 0.0005 // 0.05% 每边手续费（开仓+平仓各 0.05%）
)

// executeDryRunOrder 模拟盘统一入口：不调用交易所，用当前价+滑点撮合，直接落库并更新虚拟资金
// aiReasoning 为本轮 AI 思维链，用于开仓时写入 TraderPosition.ai_reasoning_at_open，供复盘与自我修正
func (at *AutoTrader) executeDryRunOrder(decision *kernel.Decision, actionRecord *store.DecisionAction, action string, aiReasoning string) error {
	switch action {
	case "open_long":
		return at.executeDryRunOpenLong(decision, actionRecord, aiReasoning)
	case "open_short":
		return at.executeDryRunOpenShort(decision, actionRecord, aiReasoning)
	case "close_long":
		return at.executeDryRunCloseLong(decision, actionRecord)
	case "close_short":
		return at.executeDryRunCloseShort(decision, actionRecord)
	default:
		return fmt.Errorf("dry run unsupported action: %s", action)
	}
}

func (at *AutoTrader) dryRunOrderID() string {
	return fmt.Sprintf("dry_run_%d", time.Now().UnixNano())
}

// getDryRunPrice 从行情取当前价并施加 0.05% 滑点（做多上浮、做空下沉）
func (at *AutoTrader) getDryRunPrice(symbol string, isLong bool) (float64, error) {
	data, err := market.GetWithExchange(symbol, at.exchange, nil)
	if err != nil {
		return 0, err
	}
	price := data.CurrentPrice
	if isLong {
		price = price * (1 + dryRunSlippagePct)
	} else {
		price = price * (1 - dryRunSlippagePct)
	}
	return price, nil
}

func (at *AutoTrader) executeDryRunOpenLong(decision *kernel.Decision, actionRecord *store.DecisionAction, aiReasoning string) error {
	logger.Infof("  📈 [Dry-Run] Open long: %s", decision.Symbol)

	price, err := at.getDryRunPrice(decision.Symbol, true)
	if err != nil {
		return err
	}

	// 可用资金检查用 VirtualEquity
	equity := at.config.VirtualEquity
	if equity <= 0 {
		equity = 10000
		if at.store != nil {
			_ = at.store.Trader().UpdateVirtualEquity(at.userID, at.id, equity)
		}
	}
	adjustedSize, _ := at.enforcePositionValueRatio(decision.PositionSizeUSD, equity, decision.Symbol)
	decision.PositionSizeUSD = adjustedSize
	quantity := adjustedSize / price
	if quantity <= 0 {
		return fmt.Errorf("dry run position size too small")
	}

	orderID := at.dryRunOrderID()
	notional := quantity * price
	fee := notional * dryRunFeePct

	actionRecord.Quantity = quantity
	actionRecord.Price = price
	actionRecord.OrderID = 0

	nowMs := time.Now().UTC().UnixMilli()
	normSymbol := market.Normalize(decision.Symbol)
	pos := &store.TraderPosition{
		TraderID:            at.id,
		ExchangeID:          at.exchangeID,
		ExchangeType:        at.exchange,
		ExchangePositionID:  fmt.Sprintf("dry_run_%s_%s_%d", normSymbol, "LONG", nowMs),
		Symbol:              normSymbol,
		Side:                "LONG",
		Quantity:            quantity,
		EntryQuantity:       quantity,
		EntryPrice:          price,
		EntryOrderID:        orderID,
		EntryTime:           nowMs,
		Leverage:            decision.Leverage,
		Status:              "OPEN",
		Source:              "dry_run",
		Fee:                 fee,
		CreatedAt:           nowMs,
		UpdatedAt:           nowMs,
	}
	pos.AiReasoningAtOpen = aiReasoning
	if err := at.store.Position().CreateOpenPosition(pos); err != nil {
		return err
	}
	logger.Infof("  ✓ [Dry-Run] Long opened: %s qty=%.6f @ %.4f (fee %.4f)", decision.Symbol, quantity, price, fee)
	return nil
}

func (at *AutoTrader) executeDryRunOpenShort(decision *kernel.Decision, actionRecord *store.DecisionAction, aiReasoning string) error {
	logger.Infof("  📉 [Dry-Run] Open short: %s", decision.Symbol)

	price, err := at.getDryRunPrice(decision.Symbol, false)
	if err != nil {
		return err
	}

	equity := at.config.VirtualEquity
	if equity <= 0 {
		equity = 10000
		if at.store != nil {
			_ = at.store.Trader().UpdateVirtualEquity(at.userID, at.id, equity)
		}
	}
	adjustedSize, _ := at.enforcePositionValueRatio(decision.PositionSizeUSD, equity, decision.Symbol)
	decision.PositionSizeUSD = adjustedSize
	quantity := adjustedSize / price
	if quantity <= 0 {
		return fmt.Errorf("dry run position size too small")
	}

	orderID := at.dryRunOrderID()
	notional := quantity * price
	fee := notional * dryRunFeePct

	actionRecord.Quantity = quantity
	actionRecord.Price = price
	actionRecord.OrderID = 0

	nowMs := time.Now().UTC().UnixMilli()
	normSymbol := market.Normalize(decision.Symbol)
	pos := &store.TraderPosition{
		TraderID:            at.id,
		ExchangeID:          at.exchangeID,
		ExchangeType:        at.exchange,
		ExchangePositionID:  fmt.Sprintf("dry_run_%s_%s_%d", normSymbol, "SHORT", nowMs),
		Symbol:              normSymbol,
		Side:                "SHORT",
		Quantity:            quantity,
		EntryQuantity:       quantity,
		EntryPrice:          price,
		EntryOrderID:        orderID,
		EntryTime:           nowMs,
		Leverage:            decision.Leverage,
		Status:              "OPEN",
		Source:              "dry_run",
		Fee:                 fee,
		CreatedAt:           nowMs,
		UpdatedAt:           nowMs,
	}
	pos.AiReasoningAtOpen = aiReasoning
	if err := at.store.Position().CreateOpenPosition(pos); err != nil {
		return err
	}
	logger.Infof("  ✓ [Dry-Run] Short opened: %s qty=%.6f @ %.4f (fee %.4f)", decision.Symbol, quantity, price, fee)
	return nil
}

func (at *AutoTrader) executeDryRunCloseLong(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  🔄 [Dry-Run] Close long: %s", decision.Symbol)

	normSymbol := market.Normalize(decision.Symbol)
	// 四维隔离：仅查询 source = 'dry_run'，绝不误平实盘仓位
	openPos, err := at.store.Position().GetOpenPositionBySymbolAndSource(at.id, normSymbol, "LONG", "dry_run")
	if err != nil || openPos == nil {
		logger.Infof("  ⚠️ [Dry-Run] No open long for %s, skip", decision.Symbol)
		return nil
	}

	exitPrice, err := at.getDryRunPrice(decision.Symbol, false) // 平多 = 卖出，价格下沉
	if err != nil {
		return err
	}

	closeQty := openPos.Quantity
	if decision.Quantity > 0 && decision.Quantity < closeQty {
		closeQty = decision.Quantity
	} else if decision.QuantityPct > 0 && decision.QuantityPct <= 1.0 {
		closeQty = openPos.Quantity * decision.QuantityPct
	}

	notional := closeQty * exitPrice
	closeFee := notional * dryRunFeePct
	realizedPnL := (exitPrice - openPos.EntryPrice) * closeQty
	totalFee := openPos.Fee*(closeQty/openPos.Quantity) + closeFee
	realizedPnL -= totalFee

	closedNotional := closeQty * openPos.EntryPrice
	mfe, mae := at.getDryRunMfeMae(normSymbol, "long", closedNotional)
	orderID := at.dryRunOrderID()
	nowMs := time.Now().UTC().UnixMilli()
	pb := store.NewPositionBuilder(at.store.Position())
	// 按仓位 ID 平仓，确保只关闭本条 dry_run 记录，不触碰实盘
	if err := pb.ProcessTradeCloseByPositionID(openPos.ID, closeQty, exitPrice, closeFee, realizedPnL, nowMs, orderID, mfe, mae, "", "dry_run"); err != nil {
		return err
	}

	// 复利结算：RealizedPnL 写入 VirtualEquity 并持久化到 traders 表，下次 buildDryRunTradingContext 可见
	at.updateVirtualEquityAfterClose(realizedPnL)
	actionRecord.Price = exitPrice
	actionRecord.Quantity = closeQty
	actionRecord.OrderID = 0
	logger.Infof("  ✓ [Dry-Run] Long closed: %s qty=%.6f @ %.4f, PnL=%.2f fee=%.4f", decision.Symbol, closeQty, exitPrice, realizedPnL, totalFee)
	return nil
}

func (at *AutoTrader) executeDryRunCloseShort(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  🔄 [Dry-Run] Close short: %s", decision.Symbol)

	normSymbol := market.Normalize(decision.Symbol)
	// 四维隔离：仅查询 source = 'dry_run'，绝不误平实盘仓位
	openPos, err := at.store.Position().GetOpenPositionBySymbolAndSource(at.id, normSymbol, "SHORT", "dry_run")
	if err != nil || openPos == nil {
		logger.Infof("  ⚠️ [Dry-Run] No open short for %s, skip", decision.Symbol)
		return nil
	}

	exitPrice, err := at.getDryRunPrice(decision.Symbol, true) // 平空 = 买入，价格上浮
	if err != nil {
		return err
	}

	closeQty := openPos.Quantity
	if decision.Quantity > 0 && decision.Quantity < closeQty {
		closeQty = decision.Quantity
	} else if decision.QuantityPct > 0 && decision.QuantityPct <= 1.0 {
		closeQty = openPos.Quantity * decision.QuantityPct
	}

	notional := closeQty * exitPrice
	closeFee := notional * dryRunFeePct
	realizedPnL := (openPos.EntryPrice - exitPrice) * closeQty
	totalFee := openPos.Fee*(closeQty/openPos.Quantity) + closeFee
	realizedPnL -= totalFee

	closedNotional := closeQty * openPos.EntryPrice
	mfe, mae := at.getDryRunMfeMae(normSymbol, "short", closedNotional)
	orderID := at.dryRunOrderID()
	nowMs := time.Now().UTC().UnixMilli()
	pb := store.NewPositionBuilder(at.store.Position())
	// 按仓位 ID 平仓，确保只关闭本条 dry_run 记录，不触碰实盘
	if err := pb.ProcessTradeCloseByPositionID(openPos.ID, closeQty, exitPrice, closeFee, realizedPnL, nowMs, orderID, mfe, mae, "", "dry_run"); err != nil {
		return err
	}

	// 复利结算：RealizedPnL 写入 VirtualEquity 并持久化到 traders 表，下次 buildDryRunTradingContext 可见
	at.updateVirtualEquityAfterClose(realizedPnL)
	actionRecord.Price = exitPrice
	actionRecord.Quantity = closeQty
	actionRecord.OrderID = 0
	logger.Infof("  ✓ [Dry-Run] Short closed: %s qty=%.6f @ %.4f, PnL=%.2f fee=%.4f", decision.Symbol, closeQty, exitPrice, realizedPnL, totalFee)
	return nil
}

func (at *AutoTrader) getDryRunMfeMae(symbol, side string, notional float64) (mfe, mae float64) {
	posKey := symbol + "_" + strings.ToLower(side)
	at.peakPnLCacheMutex.RLock()
	peakPct := at.peakPnLCache[posKey]
	at.peakPnLCacheMutex.RUnlock()
	at.bottomPnLCacheMutex.RLock()
	bottomPct := at.bottomPnLCache[posKey]
	at.bottomPnLCacheMutex.RUnlock()
	mfe = notional * (peakPct / 100)
	mae = notional * (bottomPct / 100)
	return mfe, mae
}

// updateVirtualEquityAfterClose 将平仓后的 RealizedPnL（已扣 0.05% 模拟手续费）复利结算到 VirtualEquity，
// 并通过 GORM Update 持久化到 traders 表，保证再次 buildDryRunTradingContext 时 AI 看到变动后的真实余额。
func (at *AutoTrader) updateVirtualEquityAfterClose(realizedPnL float64) {
	if at.store == nil {
		return
	}
	fullConfig, err := at.store.Trader().GetFullConfig(at.userID, at.id)
	if err != nil || fullConfig == nil || fullConfig.Trader == nil {
		return
	}
	current := fullConfig.Trader.VirtualEquity
	if current <= 0 {
		current = 10000
	}
	newEquity := current + realizedPnL
	if newEquity < 0 {
		newEquity = 0
	}
	if err := at.store.Trader().UpdateVirtualEquity(at.userID, at.id, newEquity); err != nil {
		logger.Infof("  ⚠️ [Dry-Run] UpdateVirtualEquity failed: %v", err)
		return
	}
	at.config.VirtualEquity = newEquity
	logger.Infof("  💰 [Dry-Run] Virtual equity: %.2f -> %.2f (%.2f)", current, newEquity, realizedPnL)
}
