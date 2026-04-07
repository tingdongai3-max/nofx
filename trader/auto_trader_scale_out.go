package trader

import (
	"fmt"
	"strings"

	"nofx/logger"
	"nofx/store"
)

// configureBinanceScaleOutStack wires the Binance-only scale-out truth helpers.
// It does not change execution behavior by itself; it only installs read/write hooks for the truth layers.
func (at *AutoTrader) configureBinanceScaleOutStack() {
	if at == nil || at.store == nil || strings.ToLower(strings.TrimSpace(at.exchange)) != "binance" {
		return
	}

	if at.scaleOutManager == nil {
		at.scaleOutManager = NewScaleOutManager(at.store, at.trader)
	}
	if at.protectionRebalanceManager == nil {
		at.protectionRebalanceManager = NewProtectionRebalanceManager(at.store, at.fixedProtectionManager)
	}
	if at.scaleOutStateReconciler == nil {
		at.scaleOutStateReconciler = NewScaleOutStateReconciler(at.store, at.protectionRebalanceManager)
	}
}

// PreviewScaleOutState returns the current read-only scale-out preview for the trader.
func (at *AutoTrader) PreviewScaleOutState(selectedSymbol string) (*ScaleOutReconcilePreview, error) {
	if at == nil || at.store == nil {
		return nil, fmt.Errorf("trader store is not available")
	}

	if at.exchange == "binance" && at.scaleOutStateReconciler != nil {
		return at.scaleOutStateReconciler.PreviewTraderScaleOut(at.id, selectedSymbol, at.trader, at.binanceUserStreamReady())
	}

	return BuildRuntimeScaleOutPreview(at.store, at.id, selectedSymbol, at.binanceUserStreamReady())
}

// bootstrapBinanceScaleOutState rebuilds scale-out truth once at startup from the latest live positions.
// It is a recovery path, not a per-cycle routine.
func (at *AutoTrader) bootstrapBinanceScaleOutState() error {
	if at == nil || at.store == nil || at.scaleOutStateReconciler == nil {
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
		if err := at.syncBinanceScaleOutState(aggregate.Symbol, at.binanceUserStreamReady()); err != nil {
			logger.Infof("⚠️ [bootstrap] Binance scale-out sync failed for %s: %v", aggregate.Symbol, err)
		}
	}

	return nil
}

// syncBinanceScaleOutState runs the single scale-out truth reconciliation path for one symbol.
// The reconciler owns plan progression and downstream protection rebalance coordination.
func (at *AutoTrader) syncBinanceScaleOutState(selectedSymbol string, userStreamReady bool) error {
	if at == nil || at.scaleOutStateReconciler == nil {
		return nil
	}

	_, err := at.scaleOutStateReconciler.SyncTraderScaleOutState(at.id, selectedSymbol, at.trader, userStreamReady)
	return err
}

