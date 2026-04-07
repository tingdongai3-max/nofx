package trader

import (
	"fmt"
	"strings"

	"nofx/logger"
	"nofx/store"
)

// configureBinanceProtectionAdjustmentStack wires the Binance-only dynamic-protection truth helpers.
// It does not change execution behavior by itself; it only installs read/write hooks for the truth layers.
func (at *AutoTrader) configureBinanceProtectionAdjustmentStack() {
	if at == nil || at.store == nil || strings.ToLower(strings.TrimSpace(at.exchange)) != "binance" {
		return
	}

	if at.protectionAdjustmentManager == nil {
		at.protectionAdjustmentManager = NewProtectionAdjustmentManager(at.store, at.trader)
	}
}

// PreviewProtectionAdjustmentState returns the current read-only dynamic protection preview for the trader.
func (at *AutoTrader) PreviewProtectionAdjustmentState(selectedSymbol string) (*ProtectionAdjustmentPreview, error) {
	if at == nil || at.store == nil {
		return nil, fmt.Errorf("trader store is not available")
	}

	liveSnapshots := collectLivePositionSnapshots(at)
	aggregates, err := store.NewPositionAggregateBuilder(at.store).BuildForTrader(at.id, liveSnapshots, at.GetPeakPnLCache())
	if err != nil {
		return nil, err
	}
	selectedAggregate := selectPreviewAggregate(aggregates, selectedSymbol)
	marketPrice := selectProtectionAdjustmentMarketPrice(liveSnapshots, selectedAggregate, selectedSymbol)

	return BuildRuntimeProtectionAdjustmentPreview(at.store, at.id, selectedSymbol, at.binanceUserStreamReady(), marketPrice)
}

// bootstrapBinanceProtectionAdjustmentState rebuilds dynamic protection truth once at startup from the latest live positions.
// It is a recovery path, not a per-cycle routine.
func (at *AutoTrader) bootstrapBinanceProtectionAdjustmentState() error {
	if at == nil || at.store == nil || at.protectionAdjustmentManager == nil {
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
		if err := at.syncBinanceProtectionAdjustmentState(aggregate.Symbol, at.binanceUserStreamReady()); err != nil {
			logger.Infof("⚠️ [bootstrap] Binance protection adjustment sync failed for %s: %v", aggregate.Symbol, err)
		}
	}

	return nil
}

// syncBinanceProtectionAdjustmentState runs the single dynamic-protection truth reconciliation path for one symbol.
// The manager owns the trigger evaluation, cancel/replace flow, and downstream aggregate refresh.
func (at *AutoTrader) syncBinanceProtectionAdjustmentState(selectedSymbol string, userStreamReady bool) error {
	if at == nil || at.store == nil || at.protectionAdjustmentManager == nil {
		return nil
	}

	liveSnapshots, err := CollectLivePositionSnapshots(at.trader)
	if err != nil {
		liveSnapshots = nil
	}

	aggregates, err := store.NewPositionAggregateBuilder(at.store).BuildForTrader(at.id, liveSnapshots, at.GetPeakPnLCache())
	if err != nil {
		return err
	}
	selectedAggregate := selectPreviewAggregate(aggregates, selectedSymbol)
	marketPrice := selectProtectionAdjustmentMarketPrice(liveSnapshots, selectedAggregate, selectedSymbol)

	preview, err := BuildRuntimeProtectionAdjustmentPreview(at.store, at.id, selectedSymbol, userStreamReady, marketPrice)
	if err != nil {
		return err
	}
	if preview == nil || preview.GuardAssessment == nil || !preview.GuardAssessment.Allowed {
		return nil
	}

	_, err = at.protectionAdjustmentManager.ApplyProtectionAdjustmentPreview(preview)
	return err
}
