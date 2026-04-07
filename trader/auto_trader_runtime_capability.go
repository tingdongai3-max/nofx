package trader

import (
	"fmt"
	"math"
	"strings"

	"nofx/logger"
	"nofx/store"
)

// PreviewRuntimeCapabilities builds the current read-only capability preview for the trader.
// It never changes execution behavior; it only reads runtime state and returns clipped actions.
func (at *AutoTrader) PreviewRuntimeCapabilities(selectedSymbol string) (*RuntimeCapabilityPreview, error) {
	if at == nil || at.store == nil {
		return nil, fmt.Errorf("trader store is not available")
	}

	executionMode := "readonly"
	allowOrderPlacement := false
	at.isRunningMutex.RLock()
	if at.isRunning {
		executionMode = "live"
		allowOrderPlacement = true
	}
	at.isRunningMutex.RUnlock()

	liveSnapshots := collectLivePositionSnapshots(at)
	peakCache := at.GetPeakPnLCache()
	orderPreview, err := at.PreviewOrderState(selectedSymbol)
	if err != nil {
		return nil, err
	}
	scaleOutPreview, err := at.PreviewScaleOutState(selectedSymbol)
	if err != nil {
		return nil, err
	}
	scaleInPreview, err := at.PreviewScaleInState(selectedSymbol)
	if err != nil {
		return nil, err
	}

	return BuildRuntimeCapabilityPreview(
		at.store,
		at.userID,
		at.id,
		selectedSymbol,
		executionMode,
		liveSnapshots,
		peakCache,
		orderPreview,
		scaleOutPreview,
		scaleInPreview,
		allowOrderPlacement,
		at.runtimeCapabilityResolver,
	)
}

// PreviewOrderState builds the read-only Binance-only order truth preview for the trader.
func (at *AutoTrader) PreviewOrderState(selectedSymbol string) (*OrderReconcilePreview, error) {
	if at == nil || at.store == nil {
		return nil, fmt.Errorf("trader store is not available")
	}

	if at.exchange == "binance" && at.binanceOrderReconcileManager != nil {
		if reader, ok := at.trader.(openOrderReader); ok {
			return at.binanceOrderReconcileManager.PreviewOrderState(at.id, selectedSymbol, reader)
		}
		return at.binanceOrderReconcileManager.PreviewOrderState(at.id, selectedSymbol, nil)
	}

	if at.orderStateReconciler != nil {
		return at.orderStateReconciler.ReconcileTraderOrders(at.id, selectedSymbol, nil, false)
	}

	return BuildRuntimeOrderPreview(at.store, at.id, selectedSymbol, false)
}

// PreviewProtectionState builds the read-only Binance-only protection truth preview for the trader.
func (at *AutoTrader) PreviewProtectionState(selectedSymbol string) (*ProtectionReconcilePreview, error) {
	if at == nil || at.store == nil {
		return nil, fmt.Errorf("trader store is not available")
	}

	if at.exchange == "binance" && at.protectionStateReconciler != nil {
		return at.protectionStateReconciler.PreviewTraderProtection(at.id, selectedSymbol, at.trader, at.binanceUserStreamReady())
	}

	return BuildRuntimeProtectionPreview(at.store, at.id, selectedSymbol, false)
}

func collectLivePositionSnapshots(at *AutoTrader) []store.PositionSnapshot {
	if at == nil {
		return nil
	}

	positions, err := at.GetPositions()
	if err != nil {
		logger.Infof("preview live position snapshot unavailable for trader %s: %v", at.id, err)
		return nil
	}

	snapshots := make([]store.PositionSnapshot, 0, len(positions))
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		entryPrice := toFloat64(pos["entry_price"])
		markPrice := toFloat64(pos["mark_price"])
		quantity := math.Abs(toFloat64(pos["quantity"]))
		unrealizedPnL := toFloat64(pos["unrealized_pnl"])
		leverage := toFloat64(pos["leverage"])
		liquidationPrice := toFloat64(pos["liquidation_price"])

		if symbol == "" || side == "" {
			continue
		}

		snapshots = append(snapshots, store.PositionSnapshot{
			Symbol:           strings.ToUpper(strings.TrimSpace(symbol)),
			Side:             strings.ToUpper(strings.TrimSpace(side)),
			PositionAmt:      quantity,
			EntryPrice:       entryPrice,
			MarkPrice:        markPrice,
			UnrealizedProfit: unrealizedPnL,
			Leverage:         leverage,
			LiquidationPrice: liquidationPrice,
		})
	}

	return snapshots
}

func toFloat64(v interface{}) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case uint64:
		return float64(value)
	case jsonNumber:
		f, _ := value.Float64()
		return f
	default:
		return 0
	}
}

type jsonNumber interface {
	Float64() (float64, error)
}
