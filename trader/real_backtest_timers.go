package trader

import (
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"strconv"
	"strings"
	"time"
)

func (at *AutoTrader) ensureRealBacktestTimerMaps() {
	if at == nil {
		return
	}
	at.realBacktestTimerMu.Lock()
	defer at.realBacktestTimerMu.Unlock()

	if at.realBacktestCloseTimers == nil {
		at.realBacktestCloseTimers = make(map[int64]*time.Timer)
	}
	if at.realBacktestClosingIDs == nil {
		at.realBacktestClosingIDs = make(map[int64]struct{})
	}
}

func (at *AutoTrader) cancelRealBacktestAutoCloseTimer(positionID int64) {
	if at == nil || positionID <= 0 {
		return
	}

	at.realBacktestTimerMu.Lock()
	defer at.realBacktestTimerMu.Unlock()

	if at.realBacktestCloseTimers == nil {
		return
	}

	if timer, exists := at.realBacktestCloseTimers[positionID]; exists {
		timer.Stop()
		delete(at.realBacktestCloseTimers, positionID)
	}
}

func (at *AutoTrader) cancelAllRealBacktestAutoCloseTimers() {
	if at == nil {
		return
	}

	at.realBacktestTimerMu.Lock()
	defer at.realBacktestTimerMu.Unlock()

	for id, timer := range at.realBacktestCloseTimers {
		timer.Stop()
		delete(at.realBacktestCloseTimers, id)
	}
	for id := range at.realBacktestClosingIDs {
		delete(at.realBacktestClosingIDs, id)
	}
}

func (at *AutoTrader) claimRealBacktestClose(positionID int64) bool {
	if at == nil || positionID <= 0 {
		return false
	}

	at.ensureRealBacktestTimerMaps()

	at.realBacktestTimerMu.Lock()
	defer at.realBacktestTimerMu.Unlock()

	if _, exists := at.realBacktestClosingIDs[positionID]; exists {
		return false
	}
	at.realBacktestClosingIDs[positionID] = struct{}{}
	return true
}

func (at *AutoTrader) releaseRealBacktestClose(positionID int64) {
	if at == nil || positionID <= 0 {
		return
	}

	at.realBacktestTimerMu.Lock()
	defer at.realBacktestTimerMu.Unlock()

	delete(at.realBacktestClosingIDs, positionID)
}

func (at *AutoTrader) armRealBacktestAutoCloseTimer(pos *store.TraderPosition, autoCloseAt int64) {
	if at == nil || pos == nil || pos.ID <= 0 {
		return
	}

	at.ensureRealBacktestTimerMaps()
	at.cancelRealBacktestAutoCloseTimer(pos.ID)

	delay := time.Until(time.UnixMilli(autoCloseAt).UTC())
	snapshot := *pos
	snapshot.AutoCloseAt = autoCloseAt

	timer := time.AfterFunc(delay, func() {
		if err := at.forceCloseExpiredRealBacktestPositionByID(snapshot.ID); err != nil {
			logger.Warnf("⚠️ Real backtest auto-close timer failed for %s %s: %v", snapshot.Symbol, snapshot.Side, err)
		}
	})

	at.realBacktestTimerMu.Lock()
	at.realBacktestCloseTimers[pos.ID] = timer
	at.realBacktestTimerMu.Unlock()

	logger.Infof(
		"🎯 Real backtest auto-close timer armed: %s %s positionID=%d auto_close_at=%d remaining=%s",
		snapshot.Symbol,
		snapshot.Side,
		snapshot.ID,
		autoCloseAt,
		time.Until(time.UnixMilli(autoCloseAt).UTC()).Round(time.Millisecond),
	)
}

func (at *AutoTrader) lookupOpenPositionRecordForTrader(traderID, symbol, side string) (*store.TraderPosition, error) {
	normalizedSymbol := market.Normalize(symbol)
	if at != nil && at.store != nil {
		if openPos, err := at.store.Position().GetOpenPositionBySymbol(traderID, normalizedSymbol, side); err != nil {
			return nil, err
		} else if openPos != nil {
			return openPos, nil
		}
	}

	if at == nil || at.trader == nil {
		return nil, nil
	}

	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, err
	}
	for _, pos := range positions {
		posSymbol, _ := pos["symbol"].(string)
		posSide, _ := pos["side"].(string)
		if market.Normalize(posSymbol) != normalizedSymbol {
			continue
		}
		if side == "LONG" && posSide != "long" {
			continue
		}
		if side == "SHORT" && posSide != "short" {
			continue
		}

		entryPrice, _ := pos["entryPrice"].(float64)
		return &store.TraderPosition{
			TraderID:     traderID,
			ExchangeID:   at.exchangeID,
			ExchangeType: at.exchange,
			Symbol:       normalizedSymbol,
			Side:         strings.ToUpper(strings.TrimSpace(side)),
			Quantity:     absPositionQuantity(pos),
			EntryPrice:   entryPrice,
			Status:       "OPEN",
			Source:       "real_backtest",
		}, nil
	}
	return nil, nil
}

func (at *AutoTrader) persistRealBacktestClosePositionByPosition(openPos *store.TraderPosition, orderID string, exitPrice, quantity float64, closeReason string, entryPriceHint float64) {
	if at == nil || at.store == nil || openPos == nil {
		return
	}
	if openPos.ID <= 0 {
		logger.Infof("  ⚠️ Failed to close real backtest position: invalid position id for %s %s", openPos.Symbol, openPos.Side)
		return
	}

	normalizedSymbol := market.Normalize(openPos.Symbol)
	closeQty := openPos.Quantity
	if quantity > 0 {
		closeQty = quantity
	}
	entryPrice := openPos.EntryPrice
	if entryPrice <= 0 {
		entryPrice = entryPriceHint
	}

	realizedPnL := openPos.RealizedPnL
	if closeQty > 0 && entryPrice > 0 {
		if openPos.Side == "LONG" {
			realizedPnL += (exitPrice - entryPrice) * closeQty
		} else {
			realizedPnL += (entryPrice - exitPrice) * closeQty
		}
	}
	if closeReason == "" {
		closeReason = buildRealBacktestTimedExitReason(at.getRealBacktestHoldDuration())
	}
	exitTimeMs := time.Now().UTC().UnixMilli()

	if err := at.store.Position().ClosePositionFully(
		openPos.ID,
		exitPrice,
		orderID,
		exitTimeMs,
		realizedPnL,
		openPos.Fee,
		closeReason,
	); err != nil {
		logger.Infof("  ⚠️ Failed to close real backtest position: %v", err)
		return
	}
	if openPos.EntryOrderID != "" {
		if err := at.store.RealTradeStats().CloseRecordByOrderID(GlobalRealSniperTraderID, at.exchangeID, openPos.EntryOrderID, realizedPnL, exitTimeMs); err != nil {
			logger.Infof("  ⚠️ Failed to finalize real trade resonance record: %v", err)
		}
	}
	logger.Infof("  ✅ Real backtest position closed [%s] %s %s @ %.6f",
		GlobalRealSniperTraderID,
		normalizedSymbol,
		openPos.Side,
		exitPrice,
	)
}

func (at *AutoTrader) forceCloseExpiredRealBacktestPositionByID(positionID int64) error {
	if at == nil || at.store == nil || positionID <= 0 {
		return nil
	}

	if !at.claimRealBacktestClose(positionID) {
		return nil
	}
	defer at.releaseRealBacktestClose(positionID)
	defer at.cancelRealBacktestAutoCloseTimer(positionID)

	openPos, err := at.store.Position().GetOpenPositionByID(positionID)
	if err != nil {
		return err
	}
	if openPos == nil {
		return nil
	}

	closeAction := "close_long"
	if openPos.Side == "SHORT" {
		closeAction = "close_short"
	}

	now := time.Now().UTC()
	autoCloseAt := openPos.AutoCloseAt
	if autoCloseAt <= 0 {
		autoCloseAt = openPos.EntryTime + int64(at.getRealBacktestHoldDuration()/time.Millisecond)
	}

	actionRecord := store.DecisionAction{
		Action:        closeAction,
		Symbol:        openPos.Symbol,
		Timestamp:     now,
		Reasoning:     buildRealBacktestTimedExitReason(at.getRealBacktestHoldDuration()),
		Success:       false,
		ExecutionMode: "real_backtest",
		RealBacktest:  at.loadLatestRealBacktestMeta(openPos.Symbol, closeAction),
	}

	logger.Infof(
		"🎯 Real backtest timed exit executed: %s %s auto_close_at=%d now=%d delta_ms=%d",
		closeAction,
		openPos.Symbol,
		autoCloseAt,
		now.UnixMilli(),
		now.UnixMilli()-autoCloseAt,
	)

	live, marketPrice, err := at.hasPhysicalPosition(openPos.Symbol, openPos.Side)
	if err != nil {
		actionRecord.Error = err.Error()
		at.saveRealBacktestDecisionRecord(now, &actionRecord, false, err)
		return err
	}
	if !live {
		if marketPrice > 0 {
			actionRecord.Price = marketPrice
		} else {
			actionRecord.Price = openPos.EntryPrice
		}
		actionRecord.Quantity = openPos.Quantity
		at.persistRealBacktestClosePositionByPosition(openPos, "", actionRecord.Price, openPos.Quantity, actionRecord.Reasoning, openPos.EntryPrice)
		actionRecord.Success = true
		at.saveRealBacktestDecisionRecord(now, &actionRecord, true, nil)
		return nil
	}

	var order map[string]interface{}
	switch closeAction {
	case "close_long":
		order, err = at.trader.CloseLong(openPos.Symbol, 0)
	case "close_short":
		order, err = at.trader.CloseShort(openPos.Symbol, 0)
	default:
		return nil
	}
	if err != nil {
		actionRecord.Error = err.Error()
		at.saveRealBacktestDecisionRecord(now, &actionRecord, false, err)
		return err
	}

	if orderID := extractOrderID(order); orderID != "" && orderID != "0" {
		if parsed, err := strconv.ParseInt(orderID, 10, 64); err == nil {
			actionRecord.OrderID = parsed
		}
	}
	if marketPrice > 0 {
		actionRecord.Price = marketPrice
	} else {
		actionRecord.Price = openPos.EntryPrice
	}
	actionRecord.Quantity = openPos.Quantity
	at.recordRealBacktestExecution(order, openPos.Symbol, closeAction, openPos.Quantity, actionRecord.Price, 0, openPos.EntryPrice, openPos, 0, actionRecord.Reasoning, actionRecord.RealBacktest)
	actionRecord.Success = true
	at.saveRealBacktestDecisionRecord(now, &actionRecord, true, nil)
	return nil
}
