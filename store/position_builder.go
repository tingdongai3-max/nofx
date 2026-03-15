package store

import (
	"fmt"
	"math"
	"nofx/logger"
	"nofx/market"
	"strings"
	"time"
)

// PositionBuilder handles position creation and updates with support for:
// - Position averaging (merging multiple opens)
// - Partial closes (reducing quantity)
// - FIFO matching
// - Time-ordered processing
type PositionBuilder struct {
	positionStore *PositionStore
}

// NewPositionBuilder creates a new PositionBuilder
func NewPositionBuilder(positionStore *PositionStore) *PositionBuilder {
	return &PositionBuilder{
		positionStore: positionStore,
	}
}

// ProcessTrade processes a single trade and updates position accordingly
// tradeTimeMs is Unix milliseconds UTC; mfe/mae are Max Favorable / Max Adverse Excursion in USD (optional, use 0 if not tracked)
// entryIndicatorsJSON/exitIndicatorsJSON are optional; pre-computed indicator snapshots for 指标分析 秒开 (empty string allowed)
func (pb *PositionBuilder) ProcessTrade(
	traderID, exchangeID, exchangeType, symbol, side, action string,
	quantity, price, fee, realizedPnL float64,
	tradeTimeMs int64,
	orderID string,
	mfe, mae float64,
	entryIndicatorsJSON, exitIndicatorsJSON string,
) error {
	if strings.HasPrefix(action, "open_") {
		return pb.handleOpen(traderID, exchangeID, exchangeType, symbol, side, quantity, price, fee, tradeTimeMs, orderID, entryIndicatorsJSON)
	} else if strings.HasPrefix(action, "close_") {
		return pb.handleClose(traderID, exchangeID, exchangeType, symbol, side, quantity, price, fee, realizedPnL, tradeTimeMs, orderID, mfe, mae, exitIndicatorsJSON)
	}
	return nil
}

// handleOpen handles opening positions (create new or average into existing)
// tradeTimeMs is Unix milliseconds UTC; entryIndicatorsJSON optional for Pre-computed 指标分析
func (pb *PositionBuilder) handleOpen(
	traderID, exchangeID, exchangeType, symbol, side string,
	quantity, price, fee float64,
	tradeTimeMs int64,
	orderID string,
	entryIndicatorsJSON string,
) error {
	// Get existing OPEN position for (symbol, side)
	existing, err := pb.positionStore.GetOpenPositionBySymbol(traderID, symbol, side)
	if err != nil {
		return fmt.Errorf("failed to get open position: %w", err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	if existing == nil {
		// 实盘决策记忆：OrderSync 创建新仓位时，从 pending_reasonings 取出最近一条匹配的 CoT 填入
		aiReasoning, _ := pb.positionStore.TakeLatestPendingReasoning(traderID, symbol, side)
		if aiReasoning == "" {
			logger.Warnf("[WARN] No reasoning found in pending table for %s %s", symbol, side)
		}
		// Create new position
		position := &TraderPosition{
			TraderID:            traderID,
			ExchangeID:          exchangeID,
			ExchangeType:        exchangeType,
			ExchangePositionID:  fmt.Sprintf("sync_%s_%s_%d", symbol, side, tradeTimeMs),
			Symbol:              symbol,
			Side:                side,
			Quantity:            quantity,
			EntryPrice:          price,
			EntryOrderID:        orderID,
			EntryTime:           tradeTimeMs,
			EntryIndicatorsJSON: entryIndicatorsJSON,
			Leverage:            1,
			Status:              "OPEN",
			Source:              "sync",
			Fee:                 fee,
			CreatedAt:           nowMs,
			UpdatedAt:           nowMs,
			AiReasoningAtOpen:   aiReasoning,
		}
		return pb.positionStore.CreateOpenPosition(position)
	}

	// Merge: Calculate weighted average entry price and update position
	logger.Infof("  📊 Averaging position: %s %s %.6f @ %.2f + %.6f @ %.2f",
		symbol, side, existing.Quantity, existing.EntryPrice, quantity, price)

	// Also update exchange_id and exchange_type if they were empty
	if existing.ExchangeID == "" || existing.ExchangeType == "" {
		if err := pb.positionStore.UpdatePositionExchangeInfo(existing.ID, exchangeID, exchangeType); err != nil {
			logger.Infof("  ⚠️  Failed to update exchange info: %v", err)
		}
	}

	return pb.positionStore.UpdatePositionQuantityAndPrice(existing.ID, quantity, price, fee)
}

// handleClose handles closing positions (partial or full)
// tradeTimeMs is Unix milliseconds UTC; mfe/mae in USD; exitIndicatorsJSON optional for Pre-computed 指标分析
func (pb *PositionBuilder) handleClose(
	traderID, exchangeID, exchangeType, symbol, side string,
	quantity, price, fee, realizedPnL float64,
	tradeTimeMs int64,
	orderID string,
	mfe, mae float64,
	exitIndicatorsJSON string,
) error {
	position, err := pb.positionStore.GetOpenPositionBySymbol(traderID, symbol, side)
	if err != nil {
		return fmt.Errorf("failed to get open position: %w", err)
	}
	if position == nil {
		logger.Infof("  ⚠️  No matching open position for %s %s (orderID: %s), skipping", symbol, side, orderID)
		return nil
	}
	return pb.handleCloseWithPosition(position, quantity, price, fee, realizedPnL, tradeTimeMs, orderID, mfe, mae, exitIndicatorsJSON, "sync")
}

// ProcessTradeCloseByPositionID 按仓位 ID 平仓，用于模拟盘等需严格隔离的场景（只关闭指定 id 的 OPEN 仓位，绝不误动同 symbol/side 的实盘）
// closeReason 如 "dry_run" / "sync"
func (pb *PositionBuilder) ProcessTradeCloseByPositionID(
	positionID int64,
	quantity, price, fee, realizedPnL float64,
	tradeTimeMs int64,
	orderID string,
	mfe, mae float64,
	exitIndicatorsJSON, closeReason string,
) error {
	position, err := pb.positionStore.GetOpenPositionByID(positionID)
	if err != nil {
		return fmt.Errorf("failed to get open position by id: %w", err)
	}
	if position == nil {
		logger.Infof("  ⚠️  No OPEN position for id %d (orderID: %s), skipping", positionID, orderID)
		return nil
	}
	return pb.handleCloseWithPosition(position, quantity, price, fee, realizedPnL, tradeTimeMs, orderID, mfe, mae, exitIndicatorsJSON, closeReason)
}

func (pb *PositionBuilder) handleCloseWithPosition(
	position *TraderPosition,
	quantity, price, fee, realizedPnL float64,
	tradeTimeMs int64,
	orderID string,
	mfe, mae float64,
	exitIndicatorsJSON, closeReason string,
) error {
	symbol := position.Symbol
	side := position.Side
	const QUANTITY_TOLERANCE = 0.0001

	if realizedPnL == 0 && position.EntryPrice > 0 {
		if side == "LONG" {
			realizedPnL = (price - position.EntryPrice) * quantity
		} else {
			realizedPnL = (position.EntryPrice - price) * quantity
		}
		realizedPnL = math.Round(realizedPnL*100) / 100
	}

	if quantity < position.Quantity-QUANTITY_TOLERANCE {
		logger.Infof("  📉 Partial close: %s %s %.6f → %.6f (closed %.6f @ %.2f, PnL: %.2f)",
			symbol, side, position.Quantity, position.Quantity-quantity, quantity, price, realizedPnL)
		return pb.positionStore.ReducePositionQuantity(position.ID, quantity, price, fee, realizedPnL)
	}

	closeQty := quantity
	if quantity > position.Quantity {
		logger.Infof("  ⚠️  Over-close detected: %s %s trying to close %.6f but only %.6f open, closing full position",
			symbol, side, quantity, position.Quantity)
		closeQty = position.Quantity
	}
	closedBefore := position.EntryQuantity - position.Quantity
	totalClosed := closedBefore + closeQty
	var finalExitPrice float64
	if totalClosed > 0 {
		finalExitPrice = (position.ExitPrice*closedBefore + price*closeQty) / totalClosed
		finalExitPrice = adaptivePriceRound(finalExitPrice, position.ExitPrice, price, position.EntryPrice)
	} else {
		finalExitPrice = price
	}
	totalPnL := position.RealizedPnL + realizedPnL
	totalFee := position.Fee + fee
	logger.Infof("  ✅ Full close: %s %s %.6f @ %.2f (avg exit: %.2f, entry: %.2f, PnL: %.2f)",
		symbol, side, closeQty, price, finalExitPrice, position.EntryPrice, totalPnL)

	mfeFinal := position.MaxFavorableExcursion
	maeFinal := position.MaxAdverseExcursion
	if mfeFinal == 0 && maeFinal == 0 {
		if computedMFE, computedMAE, ok := computeExcursionsFromKlines(
			symbol, side, position.EntryPrice, position.EntryQuantity, position.EntryTime, tradeTimeMs,
		); ok {
			mfeFinal = computedMFE
			maeFinal = computedMAE
		} else {
			mfeFinal, maeFinal = estimateExcursionsFromPrice(position, finalExitPrice)
		}
	}

	return pb.positionStore.ClosePositionFully(
		position.ID,
		finalExitPrice,
		orderID,
		tradeTimeMs,
		totalPnL,
		totalFee,
		closeReason,
		mfeFinal,
		maeFinal,
		exitIndicatorsJSON,
	)
}

func estimateExcursionsFromPrice(pos *TraderPosition, price float64) (float64, float64) {
	if pos == nil || pos.EntryPrice <= 0 {
		return 0, 0
	}
	qty := pos.EntryQuantity
	if qty <= 0 {
		qty = pos.Quantity
	}
	if qty <= 0 {
		return 0, 0
	}
	diff := price - pos.EntryPrice
	side := strings.ToUpper(strings.TrimSpace(pos.Side))
	var mfe, mae float64
	if side == "SHORT" {
		mfe = -diff * qty
		mae = -diff * qty
	} else {
		mfe = diff * qty
		mae = diff * qty
	}
	if mfe < 0 {
		mfe = 0
	}
	if mae > 0 {
		mae = 0
	}
	return mfe, mae
}

func computeExcursionsFromKlines(symbol, side string, entryPrice, qty float64, entryTimeMs, exitTimeMs int64) (float64, float64, bool) {
	if entryPrice <= 0 || qty <= 0 || entryTimeMs <= 0 || exitTimeMs <= 0 {
		return 0, 0, false
	}
	if exitTimeMs < entryTimeMs {
		return 0, 0, false
	}
	duration := time.Duration(exitTimeMs-entryTimeMs) * time.Millisecond
	timeframe := "1m"
	if duration > 48*time.Hour {
		timeframe = "15m"
	} else if duration > 6*time.Hour {
		timeframe = "5m"
	}
	klines, err := market.GetKlinesRange(symbol, timeframe, time.UnixMilli(entryTimeMs), time.UnixMilli(exitTimeMs))
	if err != nil || len(klines) == 0 {
		return 0, 0, false
	}
	maxHigh := klines[0].High
	minLow := klines[0].Low
	for _, k := range klines {
		if k.High > maxHigh {
			maxHigh = k.High
		}
		if k.Low < minLow {
			minLow = k.Low
		}
	}
	sideUpper := strings.ToUpper(strings.TrimSpace(side))
	var mfe, mae float64
	if sideUpper == "SHORT" {
		mfe = (entryPrice - minLow) * qty
		mae = (entryPrice - maxHigh) * qty
	} else {
		mfe = (maxHigh - entryPrice) * qty
		mae = (minLow - entryPrice) * qty
	}
	if mfe < 0 {
		mfe = 0
	}
	if mae > 0 {
		mae = 0
	}
	return mfe, mae, true
}

// quantitiesMatch checks if two quantities are close enough (within tolerance)
func quantitiesMatch(a, b float64) bool {
	const QUANTITY_TOLERANCE = 0.0001
	return math.Abs(a-b) < QUANTITY_TOLERANCE
}
