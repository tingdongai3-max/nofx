package trader

import (
	"fmt"
	"math"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"sort"
	"strings"
	"time"
)

const (
	defaultShadowTargetWindow          = 15 * time.Minute
	defaultShadowExitObservationWindow = 60 * time.Minute
	defaultShadowPollInterval          = 3 * time.Minute
	defaultShadowIncubatorScanInterval = 1 * time.Hour
	defaultShadowFillBatch             = 200
	shadowIncubationSimilarityEpsilon  = 1.25
	shadowIncubationPromotionMinCount  = 5
	shadowIncubationPromotionMinReturn = 0.005
	ShadowRoundTripFeeRate             = store.ShadowRoundTripFeeRate
)

type shadowIncubationSample struct {
	row    *store.ShadowSnapshot
	vector []float64
}

func (at *AutoTrader) startShadowTrackerDaemon() {
	if at == nil || at.store == nil {
		return
	}

	at.shadowTrackerMu.Lock()
	if at.shadowTrackerRunning {
		at.shadowTrackerMu.Unlock()
		return
	}
	at.shadowTrackerRunning = true
	if at.stopMonitorCh == nil {
		at.stopMonitorCh = make(chan struct{})
	}
	at.monitorWg.Add(1)
	at.shadowTrackerMu.Unlock()

	go func() {
		defer at.monitorWg.Done()
		defer func() {
			at.shadowTrackerMu.Lock()
			at.shadowTrackerRunning = false
			at.shadowTrackerMu.Unlock()
		}()

		ticker := time.NewTicker(at.getShadowPollInterval())
		defer ticker.Stop()
		lastIncubatorSweep := time.Time{}

		logger.Infof("🕶 Started shadow tracker daemon (window=%s, poll=%s)", at.getShadowTargetWindow(), at.getShadowPollInterval())

		for {
			select {
			case <-ticker.C:
				now := time.Now().UTC()
				if err := at.processShadowFillCycle(now); err != nil {
					logger.Warnf("⚠️ Shadow tracker cycle failed: %v", err)
				}
				if lastIncubatorSweep.IsZero() || now.Sub(lastIncubatorSweep) >= defaultShadowIncubatorScanInterval {
					if err := at.processShadowIncubatorCycle(now); err != nil {
						logger.Warnf("⚠️ Shadow incubator cycle failed: %v", err)
					}
					lastIncubatorSweep = now
				}
			case <-at.stopMonitorCh:
				logger.Info("⏹ Stopped shadow tracker daemon")
				return
			}
		}
	}()
}

func (at *AutoTrader) persistShadowSnapshots(decisionTime time.Time, ctx *kernel.Context) error {
	return at.persistShadowSnapshotsWithMode(decisionTime, ctx, false)
}

func (at *AutoTrader) persistShadowSnapshotsForRealBacktest(decisionTime time.Time, ctx *kernel.Context) error {
	return at.persistShadowSnapshotsWithMode(decisionTime, ctx, true)
}

func (at *AutoTrader) persistShadowSnapshotsWithMode(decisionTime time.Time, ctx *kernel.Context, realBacktestMode bool) error {
	if at.store == nil || ctx == nil {
		return nil
	}
	if len(ctx.CandidateCoins) == 0 {
		return nil
	}
	enabled, err := at.isRealBacktestEnabled()
	if err != nil {
		return err
	}
	if realBacktestMode != enabled {
		return nil
	}

	decisionTime = decisionTime.UTC()
	decisionTimeMs := decisionTime.UnixMilli()
	rows := make([]*store.ShadowSnapshot, 0, len(ctx.CandidateCoins))
	for _, candidate := range ctx.CandidateCoins {
		data := ctx.MarketDataMap[candidate.Symbol]
		if data == nil || candidate.Symbol == "" || data.CurrentPrice <= 0 {
			continue
		}

		heatSlice := extractShadowHeatScores(data.HeatScore)
		rawFactors := extractShadowRawFactors(data)
		socialRankRaw, socialUpvoteRaw := market.SocialSubfactorRawValues(data.GeckoSentiment)
		row := &store.ShadowSnapshot{
			TraderID:           at.id,
			DecisionTime:       decisionTimeMs,
			Symbol:             market.Normalize(candidate.Symbol),
			Sector:             data.Sector,
			ActionTaken:        0,
			PriceT0:            data.CurrentPrice,
			HeatScore:          heatSlice.Heat,
			TradingSub:         heatSlice.TradingSub,
			QuantSub:           heatSlice.QuantSub,
			MarketFactor:       heatSlice.MarketFactor,
			TrendFactor:        heatSlice.TrendFactor,
			DonchianFactor:     heatSlice.DonchianFactor,
			VolumeSpikeFactor:  heatSlice.VolumeSpikeFactor,
			MTFResonanceFactor: heatSlice.MTFResonanceFactor,
			QuantFactor:        heatSlice.QuantFactor,
			QuantOIRaw:         heatSlice.QuantOIRaw,
			QuantImbalanceRaw:  heatSlice.QuantImbalanceRaw,
			QuantNetflowRaw:    heatSlice.QuantNetflowRaw,
			SocialFactor:       heatSlice.SocialFactor,
			SocialRankRaw:      socialRankRaw,
			SocialUpvoteRaw:    socialUpvoteRaw,
			OnChainFactor:      heatSlice.OnChainFactor,
			OnChainRatioRaw:    heatSlice.OnChainRatioRaw,
			OnChainBuyRaw:      heatSlice.OnChainBuyRaw,
			RawFactors:         rawFactors.MarshalText(),
			VolUtilization:     data.VolatilityUtilization,
			FundingRate:        data.FundingRate,
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
	return at.markShadowActionsWithMode(decisionTime, decisions, false)
}

func (at *AutoTrader) markShadowActionsForRealBacktest(decisionTime time.Time, symbols []string) error {
	decisions := make([]kernel.Decision, 0, len(symbols))
	for _, symbol := range symbols {
		decisions = append(decisions, kernel.Decision{
			Symbol: symbol,
			Action: "open_long",
		})
	}
	return at.markShadowActionsWithMode(decisionTime, decisions, true)
}

func (at *AutoTrader) markShadowActionsWithMode(decisionTime time.Time, decisions []kernel.Decision, realBacktestMode bool) error {
	if at.store == nil || len(decisions) == 0 {
		return nil
	}
	enabled, err := at.isRealBacktestEnabled()
	if err != nil {
		return err
	}
	if realBacktestMode != enabled {
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
	return at.processShadowFillCycleWithMode(now, false)
}

func (at *AutoTrader) processShadowFillCycleForRealBacktest(now time.Time) error {
	return at.processShadowFillCycleWithMode(now, true)
}

func (at *AutoTrader) processShadowFillCycleWithMode(now time.Time, realBacktestMode bool) error {
	if at.store == nil {
		return nil
	}
	enabled, err := at.isRealBacktestEnabled()
	if err != nil {
		return err
	}
	if realBacktestMode != enabled {
		return nil
	}
	return processShadowFillCycleWithStore(at.store, now, at.getShadowTargetWindow(), at.fetchShadowPriceAt)
}

func (at *AutoTrader) getRecentTrajectoryPositions(traderIDs []string, cutoffEntryTime int64) ([]*store.TraderPosition, error) {
	if at == nil || at.store == nil || len(traderIDs) == 0 {
		return nil, nil
	}

	positionsByID := make(map[int64]*store.TraderPosition)
	for _, traderID := range traderIDs {
		traderID = strings.TrimSpace(traderID)
		if traderID == "" {
			continue
		}
		rows, err := at.store.Position().GetRecentTrajectoryPositions(traderID, cutoffEntryTime)
		if err != nil {
			return nil, err
		}
		for _, pos := range rows {
			if pos == nil || pos.ID <= 0 {
				continue
			}
			positionsByID[pos.ID] = pos
		}
	}

	positions := make([]*store.TraderPosition, 0, len(positionsByID))
	for _, pos := range positionsByID {
		positions = append(positions, pos)
	}
	sort.SliceStable(positions, func(i, j int) bool {
		if positions[i].EntryTime == positions[j].EntryTime {
			return positions[i].ID > positions[j].ID
		}
		return positions[i].EntryTime > positions[j].EntryTime
	})
	return positions, nil
}

func shadowNetReturnPct(side string, entryPrice, exitPrice float64) float64 {
	if entryPrice <= 0 || exitPrice <= 0 {
		return 0
	}

	gross := (exitPrice - entryPrice) / entryPrice
	if strings.ToUpper(strings.TrimSpace(side)) == "SHORT" {
		gross = (entryPrice - exitPrice) / entryPrice
	}
	return gross - ShadowRoundTripFeeRate
}

func processShadowFillCycleWithStore(
	st *store.Store,
	now time.Time,
	window time.Duration,
	fetchPrice func(symbol string, target time.Time) (float64, error),
) error {
	if st == nil {
		return nil
	}
	if window <= 0 {
		window = defaultShadowTargetWindow
	}
	if fetchPrice == nil {
		fetchPrice = lookupShadowClosePrice
	}

	cutoff := now.UTC().Add(-window).UnixMilli()
	pending, err := st.Shadow().GetPendingFill(cutoff, defaultShadowFillBatch)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	filledCount := 0
	for _, snapshot := range pending {
		targetTime := time.UnixMilli(snapshot.DecisionTime).UTC().Add(window)
		priceT1, err := fetchPrice(snapshot.Symbol, targetTime)
		if err != nil {
			logger.Warnf("⚠️ Shadow fill lookup failed: symbol=%s decision_time=%d target=%s err=%v",
				snapshot.Symbol, snapshot.DecisionTime, targetTime.Format(time.RFC3339), err)
			continue
		}

		grossReturnPct := 0.0
		if snapshot.PriceT0 > 0 {
			grossReturnPct = (priceT1 - snapshot.PriceT0) / snapshot.PriceT0
		}
		returnPct := grossReturnPct
		if snapshot.PriceT0 > 0 {
			returnPct = grossReturnPct - ShadowRoundTripFeeRate
		}

		filledAt := now.UTC().UnixMilli()
		if err := st.Shadow().MarkFilled(snapshot.ID, priceT1, returnPct, filledAt); err != nil {
			logger.Warnf("⚠️ Failed to persist shadow fill: id=%d symbol=%s err=%v", snapshot.ID, snapshot.Symbol, err)
			continue
		}
		market.InvalidateAdaptiveWeightScope(snapshot.TraderID)
		filledCount++

		logger.Debugf("V3_AUDIT_SHADOW_FILL: Symbol=%s, T0_Price=%.4f, T1_Price=%.4f, GrossReturn=%+.1f%%, NetReturn=%+.1f%%",
			snapshot.Symbol, snapshot.PriceT0, priceT1, grossReturnPct*100, returnPct*100)
		logger.Infof("V3_AUDIT_SHADOW_FILL: Symbol=%s, T0_Price=%.4f, T1_Price=%.4f, GrossReturn=%+.1f%%, NetReturn=%+.1f%%",
			snapshot.Symbol, snapshot.PriceT0, priceT1, grossReturnPct*100, returnPct*100)
	}

	if filledCount > 0 {
		if err := store.RefreshCache(); err != nil {
			return err
		}
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
	QuantOIRaw         float64
	QuantImbalanceRaw  float64
	QuantNetflowRaw    float64
	SocialFactor       float64
	OnChainFactor      float64
	OnChainRatioRaw    float64
	OnChainBuyRaw      float64
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
		QuantOIRaw:         sanitizeShadowRawFactor(heat.QuantOIRaw),
		QuantImbalanceRaw:  sanitizeShadowRawFactor(heat.QuantImbalanceRaw),
		QuantNetflowRaw:    sanitizeShadowRawFactor(heat.QuantNetflowRaw),
		SocialFactor:       sanitizeTelemetryScore(heat.SocialScore),
		OnChainFactor:      sanitizeTelemetryScore(heat.OnChainScore),
		OnChainRatioRaw:    sanitizeShadowRawFactor(heat.OnChainRatioRaw),
		OnChainBuyRaw:      sanitizeShadowRawFactor(heat.OnChainBuyRaw),
	}
}

func extractShadowRawFactors(data *market.Data) store.ShadowRawFactors {
	scores := make(map[string]float64)
	available := make(map[string]bool)

	if data != nil && data.HeatScore != nil {
		scores = make(map[string]float64, len(data.HeatScore.RawFactorScores)+3)
		for key, value := range data.HeatScore.RawFactorScores {
			scores[key] = sanitizeShadowRawFactor(value)
		}

		available = make(map[string]bool, len(data.HeatScore.RawFactorAvailable)+3)
		for key, value := range data.HeatScore.RawFactorAvailable {
			available[key] = value
		}
	}

	if data != nil && !math.IsNaN(data.VolatilityUtilization) && !math.IsInf(data.VolatilityUtilization, 0) && data.VolatilityUtilization > 0 {
		scores["vol_utilization"] = sanitizeShadowRawFactor(data.VolatilityUtilization * 100)
		available["vol_utilization"] = true
	}
	if data != nil && !math.IsNaN(data.FundingRate) && !math.IsInf(data.FundingRate, 0) && data.FundingRate != 0 {
		scores["funding_rate"] = sanitizeShadowRawFactor(data.FundingRate * 10000)
		available["funding_rate"] = true
	}
	if data != nil && data.Orderbook != nil && !math.IsNaN(data.Orderbook.Imbalance) && !math.IsInf(data.Orderbook.Imbalance, 0) {
		scores["orderbook_imbalance"] = sanitizeShadowRawFactor(data.Orderbook.Imbalance * 100)
		available["orderbook_imbalance"] = true
	}

	return store.ShadowRawFactors{
		Scores:    scores,
		Available: available,
	}
}

func sanitizeShadowRawFactor(v float64) float64 {
	switch {
	case math.IsNaN(v), math.IsInf(v, 0):
		return 0
	default:
		return v
	}
}

func (at *AutoTrader) preserveOutlierForIncubation(symbol string, distance, threshold float64) error {
	if at == nil || at.store == nil {
		return nil
	}

	normalizedSymbol := market.Normalize(symbol)
	if normalizedSymbol == "" {
		return nil
	}
	nowMs := time.Now().UTC().UnixMilli()
	if err := at.store.Shadow().MarkLatestIncubating(at.id, normalizedSymbol, "OUTLIER", distance, threshold, nowMs); err != nil {
		return err
	}
	logger.Infof("🧬 Outlier preserved for incubation: trader=%s symbol=%s distance=%.2f threshold=%.2f", at.id, normalizedSymbol, distance, threshold)
	return nil
}

func (at *AutoTrader) processShadowIncubatorCycle(now time.Time) error {
	if at == nil || at.store == nil {
		return nil
	}

	rows, err := at.store.Shadow().ListIncubatingFilledForPromotion(at.id, 2000)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	samples := make([]shadowIncubationSample, 0, len(rows))
	for _, row := range rows {
		vector, ok := kernel.BuildResonanceFeatureVectorFromSnapshot(row)
		if !ok {
			continue
		}
		samples = append(samples, shadowIncubationSample{
			row:    row,
			vector: vector,
		})
	}
	if len(samples) < shadowIncubationPromotionMinCount {
		return nil
	}

	sort.SliceStable(samples, func(i, j int) bool {
		if samples[i].row == nil || samples[j].row == nil {
			return false
		}
		if samples[i].row.DecisionTime != samples[j].row.DecisionTime {
			return samples[i].row.DecisionTime > samples[j].row.DecisionTime
		}
		return samples[i].row.ID > samples[j].row.ID
	})

	used := make(map[uint]struct{}, len(samples))
	promotionIndex := 0
	for i := range samples {
		if samples[i].row == nil {
			continue
		}
		if _, exists := used[samples[i].row.ID]; exists {
			continue
		}

		memberIndexes := []int{i}
		centroid := append([]float64(nil), samples[i].vector...)
		for j := i + 1; j < len(samples); j++ {
			if samples[j].row == nil {
				continue
			}
			if _, exists := used[samples[j].row.ID]; exists {
				continue
			}
			distance, ok := shadowIncubationDistance(samples[j].vector, centroid)
			if !ok || distance >= shadowIncubationSimilarityEpsilon {
				continue
			}
			memberIndexes = append(memberIndexes, j)
			centroid = shadowIncubationCentroid(samples, memberIndexes)
		}

		if len(memberIndexes) < shadowIncubationPromotionMinCount {
			continue
		}

		totalReturn := 0.0
		ids := make([]uint, 0, len(memberIndexes))
		for _, memberIndex := range memberIndexes {
			member := samples[memberIndex]
			if member.row == nil {
				continue
			}
			totalReturn += member.row.ReturnPct
			ids = append(ids, member.row.ID)
		}
		if len(ids) < shadowIncubationPromotionMinCount {
			continue
		}

		avgReturn := totalReturn / float64(len(ids))
		if avgReturn <= shadowIncubationPromotionMinReturn {
			continue
		}

		promotionIndex++
		archetypeLabel := fmt.Sprintf("Type_N_%s_%02d", now.UTC().Format("2006010215"), promotionIndex)
		if err := at.store.Shadow().PromoteIncubationCohort(archetypeLabel, ids, now.UTC().UnixMilli()); err != nil {
			return err
		}
		for _, id := range ids {
			used[id] = struct{}{}
		}
		logger.Infof(
			"🧬 Shadow incubator auto-promoted: trader=%s archetype=%s samples=%d avg_return=%.2f%% epsilon=%.2f",
			at.id,
			archetypeLabel,
			len(ids),
			avgReturn*100,
			shadowIncubationSimilarityEpsilon,
		)
	}

	return nil
}

func shadowIncubationCentroid(samples []shadowIncubationSample, indexes []int) []float64 {
	if len(indexes) == 0 {
		return nil
	}
	dim := len(samples[indexes[0]].vector)
	centroid := make([]float64, dim)
	for _, index := range indexes {
		vector := samples[index].vector
		for dimIndex, value := range vector {
			centroid[dimIndex] += value
		}
	}
	for dimIndex := range centroid {
		centroid[dimIndex] /= float64(len(indexes))
	}
	return centroid
}

func shadowIncubationDistance(a, b []float64) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}

	sum := 0.0
	for i := range a {
		if math.IsNaN(a[i]) || math.IsInf(a[i], 0) || math.IsNaN(b[i]) || math.IsInf(b[i], 0) {
			return 0, false
		}
		diff := a[i] - b[i]
		sum += diff * diff
	}
	if sum < 0 {
		sum = 0
	}
	return math.Sqrt(sum), true
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
