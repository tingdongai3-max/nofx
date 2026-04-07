package trader

import (
	"strings"

	"nofx/logger"
	"nofx/store"
	"nofx/trader/types"
)

// configureBinanceProtectionStack wires the Binance-only protection truth helpers.
// It does not change execution behavior by itself; it only installs read/write hooks for the truth layers.
func (at *AutoTrader) configureBinanceProtectionStack() {
	if at == nil || at.store == nil || strings.ToLower(strings.TrimSpace(at.exchange)) != "binance" {
		return
	}

	if at.binanceOrderReconcileManager == nil {
		at.binanceOrderReconcileManager = NewBinanceOrderReconcileManager(at.store)
	}
	if at.fixedProtectionManager == nil {
		at.fixedProtectionManager = NewFixedProtectionManager(at.store, at.trader)
	}
	if at.protectionStateReconciler == nil {
		at.protectionStateReconciler = NewProtectionStateReconciler(at.store, at.fixedProtectionManager)
	}

	if at.binanceOrderReconcileManager != nil {
		at.binanceOrderReconcileManager.SetOrderRegistryUpdatedCallback(at.handleBinanceOrderRegistryUpdated)
	}
}

// handleBinanceOrderRegistryUpdated is the Binance user-stream/order-truth callback.
// It reacts to confirmed registry updates by refreshing the protection truth layer.
func (at *AutoTrader) handleBinanceOrderRegistryUpdated(row *store.OrderRegistry) error {
	if at == nil || row == nil || row.Symbol == "" {
		return nil
	}
	if err := at.syncBinanceScaleInState(row.Symbol, at.binanceUserStreamReady()); err != nil {
		return err
	}
	if err := at.syncBinanceProtectionState(row.Symbol, at.binanceUserStreamReady()); err != nil {
		return err
	}
	if err := at.syncBinanceScaleOutState(row.Symbol, at.binanceUserStreamReady()); err != nil {
		return err
	}
	return at.syncBinanceProtectionAdjustmentState(row.Symbol, at.binanceUserStreamReady())
}

// handleBinanceTradeSynced is the Binance trade-history callback.
// It runs after a confirmed fill has already been written to the local truth layers.
func (at *AutoTrader) handleBinanceTradeSynced(trade types.TradeRecord, orderAction string) error {
	if at == nil || strings.TrimSpace(trade.Symbol) == "" {
		return nil
	}
	_ = orderAction
	if err := at.syncBinanceScaleInState(trade.Symbol, at.binanceUserStreamReady()); err != nil {
		return err
	}
	if err := at.syncBinanceProtectionState(trade.Symbol, at.binanceUserStreamReady()); err != nil {
		return err
	}
	if err := at.syncBinanceScaleOutState(trade.Symbol, at.binanceUserStreamReady()); err != nil {
		return err
	}
	return at.syncBinanceProtectionAdjustmentState(trade.Symbol, at.binanceUserStreamReady())
}

// bootstrapBinanceProtectionState rebuilds protection truth once at startup from the latest live positions.
// It is a recovery path, not a per-cycle routine.
func (at *AutoTrader) bootstrapBinanceProtectionState() error {
	if at == nil || at.store == nil || at.protectionStateReconciler == nil {
		return nil
	}

	liveSnapshots, err := CollectLivePositionSnapshots(at.trader)
	if err != nil {
		return err
	}

	aggregates, err := store.NewPositionAggregateBuilder(at.store).BuildForTrader(at.id, liveSnapshots, at.GetPeakPnLCache())
	if err != nil {
		return err
	}

	for _, aggregate := range aggregates {
		if aggregate == nil || aggregate.TotalQty <= 0 {
			continue
		}
		if err := at.syncBinanceProtectionState(aggregate.Symbol, at.binanceUserStreamReady()); err != nil {
			logger.Infof("⚠️ [bootstrap] Binance protection sync failed for %s: %v", aggregate.Symbol, err)
		}
	}

	return nil
}

// syncBinanceProtectionState runs the single protection truth reconciliation path for one symbol.
// The reconciler owns both fixed-protection attachment and sibling-cancel recovery.
func (at *AutoTrader) syncBinanceProtectionState(selectedSymbol string, userStreamReady bool) error {
	if at == nil || at.protectionStateReconciler == nil {
		return nil
	}

	_, err := at.protectionStateReconciler.SyncTraderProtectionState(at.id, selectedSymbol, at.trader, userStreamReady)
	return err
}

func (at *AutoTrader) binanceUserStreamReady() bool {
	if at == nil || at.binanceOrderReconcileManager == nil {
		return false
	}
	return at.binanceOrderReconcileManager.UserStreamReady()
}
