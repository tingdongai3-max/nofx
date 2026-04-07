package trader

import (
	"fmt"
	"strings"

	"nofx/logger"
	"nofx/store"
)

// configureBinanceScaleInStack wires the Binance-only scale-in truth helpers.
// It does not change execution behavior by itself; it only installs read/write hooks for the truth layers.
func (at *AutoTrader) configureBinanceScaleInStack() {
	if at == nil || at.store == nil || strings.ToLower(strings.TrimSpace(at.exchange)) != "binance" {
		return
	}

	if at.scaleInRiskGuard == nil {
		at.scaleInRiskGuard = NewScaleInRiskGuard()
	}
	if at.fixedProtectionManager == nil {
		at.fixedProtectionManager = NewFixedProtectionManager(at.store, at.trader)
	}
	if at.protectionRebalanceManager == nil {
		at.protectionRebalanceManager = NewProtectionRebalanceManager(at.store, at.fixedProtectionManager)
	}
	if at.scaleInManager == nil {
		at.scaleInManager = NewScaleInManager(at.store, at.trader, at.scaleInRiskGuard)
	}
	if at.scaleInStateReconciler == nil {
		at.scaleInStateReconciler = NewScaleInStateReconciler(at.store, at.protectionRebalanceManager)
	}
}

// PreviewScaleInState returns the current read-only scale-in preview for the trader.
func (at *AutoTrader) PreviewScaleInState(selectedSymbol string) (*ScaleInReconcilePreview, error) {
	if at == nil || at.store == nil {
		return nil, fmt.Errorf("trader store is not available")
	}

	if at.exchange == "binance" && at.scaleInStateReconciler != nil {
		return at.scaleInStateReconciler.PreviewTraderScaleIn(at.id, selectedSymbol, at.trader, at.binanceUserStreamReady())
	}

	return BuildRuntimeScaleInPreview(at.store, at.id, selectedSymbol, at.binanceUserStreamReady(), 0)
}

// bootstrapBinanceScaleInState rebuilds scale-in truth once at startup from the latest live positions.
// It is a recovery path, not a per-cycle routine.
func (at *AutoTrader) bootstrapBinanceScaleInState() error {
	if at == nil || at.store == nil || at.scaleInStateReconciler == nil {
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
		if err := at.syncBinanceScaleInState(aggregate.Symbol, at.binanceUserStreamReady()); err != nil {
			logger.Infof("⚠️ [bootstrap] Binance scale-in sync failed for %s: %v", aggregate.Symbol, err)
		}
	}

	return nil
}

// syncBinanceScaleInState runs the single scale-in truth reconciliation path for one symbol.
// The reconciler owns plan progression and downstream protection rebalance coordination.
func (at *AutoTrader) syncBinanceScaleInState(selectedSymbol string, userStreamReady bool) error {
	if at == nil || at.scaleInStateReconciler == nil {
		return nil
	}

	_, err := at.scaleInStateReconciler.SyncTraderScaleInState(at.id, selectedSymbol, at.trader, userStreamReady)
	return err
}
