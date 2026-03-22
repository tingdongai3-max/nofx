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
	defaultShadowTargetWindow = 15 * time.Minute
	defaultShadowPollInterval = 1 * time.Minute
	defaultShadowFillBatch    = 200
)

func (at *AutoTrader) startShadowTrackerDaemon() {
	if at.store == nil {
		return
	}

	at.monitorWg.Add(1)
	go func() {
		defer at.monitorWg.Done()

		ticker := time.NewTicker(at.getShadowPollInterval())
		defer ticker.Stop()

		logger.Infof("🕶 Started shadow tracker daemon (window=%s, poll=%s)", at.getShadowTargetWindow(), at.getShadowPollInterval())

		for {
			select {
			case <-ticker.C:
				if err := at.processShadowFillCycle(time.Now().UTC()); err != nil {
					logger.Warnf("⚠️ Shadow tracker cycle failed: %v", err)
				}
			case <-at.stopMonitorCh:
				logger.Info("⏹ Stopped shadow tracker daemon")
				return
			}
		}
	}()
}

func (at *AutoTrader) persistShadowSnapshots(decisionTime time.Time) error {
	if at.store == nil {
		return nil
	}

	snapshot := at.GetCandidateSnapshot()
	if len(snapshot.Candidates) == 0 {
		return nil
	}

	decisionTime = decisionTime.UTC()
	decisionTimeMs := decisionTime.UnixMilli()
	rows := make([]*store.ShadowSnapshot, 0, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		if candidate.Symbol == "" || candidate.CurrentPrice <= 0 {
			continue
		}

		heatSlice := extractShadowHeatScores(candidate.HeatScore)
		row := &store.ShadowSnapshot{
			TraderID:           at.id,
			DecisionTime:       decisionTimeMs,
			Symbol:             market.Normalize(candidate.Symbol),
			Sector:             candidate.Sector,
			ActionTaken:        0,
			PriceT0:            candidate.CurrentPrice,
			HeatScore:          heatSlice.Heat,
			TradingSub:         heatSlice.TradingSub,
			QuantSub:           heatSlice.QuantSub,
			MarketFactor:       heatSlice.MarketFactor,
			TrendFactor:        heatSlice.TrendFactor,
			DonchianFactor:     heatSlice.DonchianFactor,
			VolumeSpikeFactor:  heatSlice.VolumeSpikeFactor,
			MTFResonanceFactor: heatSlice.MTFResonanceFactor,
			QuantFactor:        heatSlice.QuantFactor,
			SocialFactor:       heatSlice.SocialFactor,
			OnChainFactor:      heatSlice.OnChainFactor,
			VolUtilization:     candidate.VolUtilization,
			FundingRate:        candidate.FundingRate,
			SourceSummary:      strings.Join(candidate.Sources, ","),
			Filled:             false,
			CreatedAt:          decisionTimeMs,
			UpdatedAt:          decisionTimeMs,
		}
		rows = append(rows, row)
	}

	if err := at.store.Shadow().CreateBatch(rows); err != nil {
		return err
	}

	for _, row := range rows {
		logger.Debugf("V3_AUDIT_SHADOW_SNAP: Trader=%s, DecisionTime=%d, Symbol=%s, Sector=%s, ActionTaken=%d, PriceT0=%.4f, Heat=%.1f",
			row.TraderID, row.DecisionTime, row.Symbol, row.Sector, row.ActionTaken, row.PriceT0, row.HeatScore)
		logger.Infof("V3_AUDIT_SHADOW_SNAP: Trader=%s, DecisionTime=%d, Symbol=%s, Sector=%s, ActionTaken=%d, PriceT0=%.4f, Heat=%.1f",
			row.TraderID, row.DecisionTime, row.Symbol, row.Sector, row.ActionTaken, row.PriceT0, row.HeatScore)
	}

	return nil
}

func (at *AutoTrader) markShadowActions(decisionTime time.Time, decisions []kernel.Decision) error {
	if at.store == nil || len(decisions) == 0 {
		return nil
	}

	openSymbols := make([]string, 0, len(decisions))
	seen := make(map[string]struct{}, len(decisions))
	for _, decision := range decisions {
		if decision.Action != "open_long" && decision.Action != "open_short" {
			continue
		}
		symbol := market.Normalize(decision.Symbol)
		if symbol == "" {
			continue
		}
		if _, ok := seen[symbol]; ok {
			continue
		}
		seen[symbol] = struct{}{}
		openSymbols = append(openSymbols, symbol)
	}

	return at.store.Shadow().MarkActionTaken(at.id, decisionTime.UTC().UnixMilli(), openSymbols)
}

func (at *AutoTrader) processShadowFillCycle(now time.Time) error {
	if at.store == nil {
		return nil
	}

	window := at.getShadowTargetWindow()
	cutoff := now.UTC().Add(-window).UnixMilli()
	pending, err := at.store.Shadow().GetPendingFill(cutoff, defaultShadowFillBatch)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	for _, snapshot := range pending {
		targetTime := time.UnixMilli(snapshot.DecisionTime).UTC().Add(window)
		priceT1, err := at.fetchShadowPriceAt(snapshot.Symbol, targetTime)
		if err != nil {
			logger.Warnf("⚠️ Shadow fill lookup failed: symbol=%s decision_time=%d target=%s err=%v",
				snapshot.Symbol, snapshot.DecisionTime, targetTime.Format(time.RFC3339), err)
			continue
		}

		returnPct := 0.0
		if snapshot.PriceT0 > 0 {
			returnPct = (priceT1 - snapshot.PriceT0) / snapshot.PriceT0
		}

		filledAt := now.UTC().UnixMilli()
		if err := at.store.Shadow().MarkFilled(snapshot.ID, priceT1, returnPct, filledAt); err != nil {
			logger.Warnf("⚠️ Failed to persist shadow fill: id=%d symbol=%s err=%v", snapshot.ID, snapshot.Symbol, err)
			continue
		}
		market.InvalidateAdaptiveWeightScope(snapshot.TraderID)

		logger.Debugf("V3_AUDIT_SHADOW_FILL: Symbol=%s, T0_Price=%.4f, T1_Price=%.4f, Return=%+.1f%%",
			snapshot.Symbol, snapshot.PriceT0, priceT1, returnPct*100)
		logger.Infof("V3_AUDIT_SHADOW_FILL: Symbol=%s, T0_Price=%.4f, T1_Price=%.4f, Return=%+.1f%%",
			snapshot.Symbol, snapshot.PriceT0, priceT1, returnPct*100)
	}

	return nil
}

func (at *AutoTrader) fetchShadowPriceAt(symbol string, target time.Time) (float64, error) {
	if at.shadowPriceFetcher != nil {
		return at.shadowPriceFetcher(symbol, target)
	}
	return lookupShadowClosePrice(symbol, target)
}

func (at *AutoTrader) getShadowTargetWindow() time.Duration {
	if at.shadowTargetWindow > 0 {
		return at.shadowTargetWindow
	}
	return defaultShadowTargetWindow
}

func (at *AutoTrader) getShadowPollInterval() time.Duration {
	if at.shadowPollInterval > 0 {
		return at.shadowPollInterval
	}
	return defaultShadowPollInterval
}

type shadowHeatSlice struct {
	Heat               float64
	TradingSub         float64
	QuantSub           float64
	MarketFactor       float64
	TrendFactor        float64
	DonchianFactor     float64
	VolumeSpikeFactor  float64
	MTFResonanceFactor float64
	QuantFactor        float64
	SocialFactor       float64
	OnChainFactor      float64
}

func extractShadowHeatScores(heat *market.HeatScoreData) shadowHeatSlice {
	if heat == nil {
		return shadowHeatSlice{}
	}
	return shadowHeatSlice{
		Heat:               sanitizeTelemetryScore(heat.CompositeScore),
		TradingSub:         sanitizeTelemetryScore(heat.TradingScore),
		QuantSub:           sanitizeTelemetryScore(heat.QuantScore),
		MarketFactor:       sanitizeTelemetryScore(heat.MarketScore),
		TrendFactor:        sanitizeTelemetryScore(heat.TrendScore),
		DonchianFactor:     sanitizeTelemetryScore(heat.DonchianFactorScore),
		VolumeSpikeFactor:  sanitizeTelemetryScore(heat.VolumeSpikeScore),
		MTFResonanceFactor: sanitizeTelemetryScore(heat.MTFResonanceFactorScore),
		QuantFactor:        sanitizeTelemetryScore(heat.QuantFactorScore),
		SocialFactor:       sanitizeTelemetryScore(heat.SocialScore),
		OnChainFactor:      sanitizeTelemetryScore(heat.OnChainScore),
	}
}

func lookupShadowClosePrice(symbol string, target time.Time) (float64, error) {
	start := target.Add(-1 * time.Minute)
	end := target.Add(2 * time.Minute)
	klines, err := market.GetKlinesRange(symbol, "1m", start, end)
	if err != nil {
		return 0, err
	}

	targetMs := target.UTC().UnixMilli()
	for _, kline := range klines {
		if kline.CloseTime >= targetMs && kline.Close > 0 {
			return kline.Close, nil
		}
	}
	if len(klines) > 0 && klines[len(klines)-1].Close > 0 {
		return klines[len(klines)-1].Close, nil
	}

	return 0, fmt.Errorf("no close price found for %s near %s", symbol, target.Format(time.RFC3339))
}
