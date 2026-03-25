package trader

import (
	"fmt"
	"hash/fnv"
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
	defaultRealBacktestScanInterval = 3 * time.Minute
	defaultRealBacktestHoldDuration = 15 * time.Minute
	realBacktestLeverage            = 10
)

type realBacktestSelection struct {
	candidate               kernel.CandidateCoin
	entryPrice              float64
	sector                  string
	resonance               kernel.ResonanceResult
	archetypeID             string
	archetypeName           string
	archetypeScope          string
	archetypeMarginScale    float64
	attributionWeights      map[string]float64
	openAction              string
	logicScore              float64
	guardScore              float64
	mahalanobisDistance     float64
	mahalanobisThreshold    float64
	mahalanobisResonant     bool
	featureVectorFocus      string
	featureVectorDeviation  float64
	entryAllowed            bool
	entryBlockReason        string
	rawFinalEV              float64
	riskDiscountFactor      float64
	riskAdjustedEV          float64
	neighborhoodLabel       string
	neighborhoodRadius      float64
	neighborhoodSampleCount int
	neighborhoodWinRate     float64
	neighborhoodLossRate    float64
	shadowMonitorRequired   bool
	neighborhoodFinalMargin float64
	globalEV                float64
	sectorEV                float64
	symbolEV                float64
	avgEV                   float64
	marginUsageRatio        float64
	requiredMargin          float64
	freeBalance             float64
	positionUSD             float64
}

type realBacktestCandidateAssessment struct {
	selection       *realBacktestSelection
	shadowEligible  bool
	reserveBreached bool
}

func (at *AutoTrader) startRealBacktestExecutor() {
	if at.store == nil || at.strategyEngine == nil || at.IsGridStrategy() {
		return
	}

	at.monitorWg.Add(1)
	go func() {
		defer at.monitorWg.Done()

		interval := at.getRealBacktestScanInterval()
		delay, nextTick := calculateNextAlignment(interval)
		logger.Infof("🎯 Started real backtest executor (interval=%s, hold=%s, next=%s)",
			interval,
			at.getRealBacktestHoldDuration(),
			nextTick.Format(time.RFC3339),
		)

		timer := time.NewTimer(delay)
		defer timer.Stop()

		for {
			select {
			case <-timer.C:
				scheduledTick := nextTick.UTC()
				delay, nextTick = calculateNextAlignmentFrom(scheduledTick, interval)
				timer.Reset(delay)
				at.startRealBacktestCycle(scheduledTick)
			case <-at.stopMonitorCh:
				logger.Info("⏹ Stopped real backtest executor")
				return
			}
		}
	}()
}

func (at *AutoTrader) startRealBacktestCycle(tickTime time.Time) {
	at.activeCycleWg.Add(1)
	go func() {
		defer at.activeCycleWg.Done()
		if err := at.runRealBacktestCycle(tickTime); err != nil {
			logger.Warnf("⚠️ Real backtest cycle failed: %v", err)
		}
	}()
}

func (at *AutoTrader) runRealBacktestCycle(tickTime time.Time) error {
	enabled, err := at.isRealBacktestEnabled()
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	ctx, err := at.buildTradingContext(tickTime.UTC(), at.currentCallCount())
	if err != nil {
		return fmt.Errorf("build trading context: %w", err)
	}
	at.saveEquitySnapshot(ctx, tickTime)

	if len(ctx.CandidateCoins) == 0 {
		at.updateCandidateSnapshot(ctx, tickTime)
		return nil
	}

	if err := kernel.PrepareContextWithStrategy(ctx, at.strategyEngine); err != nil {
		return fmt.Errorf("prepare strategy context: %w", err)
	}
	at.updateCandidateSnapshot(ctx, tickTime)

	if err := at.sampleRealTradeResonanceHold(ctx, tickTime); err != nil {
		logger.Warnf("⚠️ Failed to sample real trade resonance hold state: %v", err)
	}

	if err := at.persistShadowSnapshotsForRealBacktest(tickTime, ctx); err != nil {
		return fmt.Errorf("persist shadow snapshots: %w", err)
	}

	riskConfig, err := at.store.GetRealBacktestConfig()
	if err != nil {
		return fmt.Errorf("load real backtest config: %w", err)
	}
	guardConfig, err := at.store.GetResonanceGuardConfig()
	if err != nil {
		return fmt.Errorf("load resonance guard config: %w", err)
	}

	assessments, err := at.evaluateRealBacktestCandidates(ctx, riskConfig, guardConfig)
	if err != nil {
		return err
	}
	selection := chooseRealBacktestSelection(assessments)
	if selection == nil {
		logger.Infof("🎯 Real backtest: no eligible directional resonance signal")
		return nil
	}

	logger.Infof(
		"🎯 Real backtest sizing: %s Raw_EV=%.2f%% Adjusted_EV=%.2f%% Discount=%.0f%% GuardScore=%.2f (Global=%.2f%% Sector=%.2f%% Symbol=%.2f%%) MarginUsage=%.0f%% Calculated Margin=%.2f USDT FreeBalance=%.2f USDT Notional=%.2f USDT",
		selection.candidate.Symbol,
		selection.rawFinalEV*100,
		selection.avgEV*100,
		selection.riskDiscountFactor*100,
		selection.guardScore,
		selection.globalEV*100,
		selection.sectorEV*100,
		selection.symbolEV*100,
		selection.marginUsageRatio*100,
		selection.requiredMargin,
		selection.freeBalance,
		selection.positionUSD,
	)

	actionRecord := store.DecisionAction{
		Action:        selection.openAction,
		Symbol:        selection.candidate.Symbol,
		Leverage:      realBacktestLeverage,
		Price:         selection.entryPrice,
		Timestamp:     time.Now().UTC(),
		Success:       false,
		Reasoning:     buildRealBacktestReasoning(selection),
		ExecutionMode: "real_backtest",
		RealBacktest:  buildRealBacktestDecisionMeta(selection),
	}
	decision := &kernel.Decision{
		Symbol:          selection.candidate.Symbol,
		Action:          selection.openAction,
		Leverage:        realBacktestLeverage,
		PositionSizeUSD: selection.positionUSD,
		Confidence:      100,
		Reasoning:       actionRecord.Reasoning,
	}

	if err := at.executeRealBacktestOpen(decision, &actionRecord, selection.entryPrice); err != nil {
		actionRecord.Error = err.Error()
		at.saveRealBacktestDecisionRecord(tickTime, &actionRecord, false, err)
		return err
	}
	actionRecord.Success = true

	if err := at.markShadowActionsForRealBacktest(tickTime, []string{selection.candidate.Symbol}); err != nil {
		logger.Warnf("⚠️ Failed to mark real backtest shadow action: %v", err)
	}
	at.saveRealBacktestDecisionRecord(tickTime, &actionRecord, true, nil)

	logger.Infof("🎯 Real backtest entry executed: %s %s logic=%.2f bin=%d notional=%.2f",
		selection.openAction,
		selection.candidate.Symbol,
		selection.logicScore,
		selection.resonance.BinStart,
		selection.positionUSD,
	)
	return nil
}

func (at *AutoTrader) selectRealBacktestCandidate(ctx *kernel.Context, riskConfig store.RealBacktestSystemConfig, guardConfig store.ResonanceGuardSystemConfig) (*realBacktestSelection, error) {
	assessments, err := at.evaluateRealBacktestCandidates(ctx, riskConfig, guardConfig)
	if err != nil {
		return nil, err
	}
	return chooseRealBacktestSelection(assessments), nil
}

func (at *AutoTrader) evaluateRealBacktestCandidates(ctx *kernel.Context, riskConfig store.RealBacktestSystemConfig, guardConfig store.ResonanceGuardSystemConfig) ([]*realBacktestCandidateAssessment, error) {
	if at == nil || ctx == nil || at.performanceCache == nil || len(ctx.CandidateCoins) == 0 {
		return nil, nil
	}
	if ctx.Account.TotalEquity <= 0 {
		return nil, nil
	}
	if riskConfig.MaxMarginPerTrade <= 0 {
		logger.Warnf("⚠️ Real backtest risk config invalid: rb_max_margin_per_trade must be greater than 0")
		return nil, nil
	}
	if riskConfig.ReserveMargin < 0 {
		logger.Warnf("⚠️ Real backtest risk config invalid: rb_reserve_margin must be greater than or equal to 0")
		return nil, nil
	}
	if riskConfig.MinEVThreshold < 0 {
		logger.Warnf("⚠️ Real backtest risk config invalid: rb_min_ev_threshold must be greater than or equal to 0")
		return nil, nil
	}
	if riskConfig.MaxEVThreshold <= riskConfig.MinEVThreshold {
		logger.Warnf("⚠️ Real backtest risk config invalid: rb_max_ev_threshold must be greater than rb_min_ev_threshold")
		return nil, nil
	}
	if guardConfig.AdaptiveEntryFloor < 0 || guardConfig.AdaptiveEntryFloor > 60 {
		logger.Warnf("⚠️ Resonance guard config invalid: adaptive_entry_floor must be between 0 and 60")
		return nil, nil
	}
	if guardConfig.AdaptiveEntryLambda < 0 {
		logger.Warnf("⚠️ Resonance guard config invalid: adaptive_entry_lambda must be greater than or equal to 0")
		return nil, nil
	}
	if guardConfig.AdaptiveEntryLambda > 1 {
		logger.Warnf("⚠️ Resonance guard config invalid: adaptive_entry_lambda must be less than or equal to 1")
		return nil, nil
	}

	sorted := make([]kernel.CandidateCoin, 0, len(ctx.CandidateCoins))
	for _, candidate := range ctx.CandidateCoins {
		if candidate.LogicScore == nil {
			continue
		}
		sorted = append(sorted, candidate)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return *sorted[i].LogicScore > *sorted[j].LogicScore
	})

	assessments := make([]*realBacktestCandidateAssessment, 0, len(sorted))
	for _, candidate := range sorted {
		assessment, err := at.assessRealBacktestCandidate(ctx, candidate, riskConfig, guardConfig)
		if err != nil {
			return nil, err
		}
		if assessment == nil {
			continue
		}
		assessments = append(assessments, assessment)
	}

	return assessments, nil
}

func chooseRealBacktestSelection(assessments []*realBacktestCandidateAssessment) *realBacktestSelection {
	for _, assessment := range assessments {
		if assessment == nil || assessment.selection == nil || assessment.reserveBreached {
			continue
		}
		return assessment.selection
	}
	return nil
}

func (at *AutoTrader) loadNeighborhoodAuditRows(limit int) ([]*store.ShadowSnapshot, error) {
	if at == nil || at.store == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = store.PERFORMANCE_RECENT_SAMPLE_LIMIT
	}

	rows, err := at.store.Shadow().ListFilledForNeighborhoodAudit(at.id, limit)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		return rows, nil
	}
	return at.store.Shadow().ListFilledSharedForNeighborhoodAudit(limit)
}

func (at *AutoTrader) assessRealBacktestCandidate(ctx *kernel.Context, candidate kernel.CandidateCoin, riskConfig store.RealBacktestSystemConfig, guardConfig store.ResonanceGuardSystemConfig) (*realBacktestCandidateAssessment, error) {
	if at == nil || ctx == nil || at.performanceCache == nil || len(ctx.CandidateCoins) == 0 {
		return nil, nil
	}
	if candidate.LogicScore == nil {
		return nil, nil
	}

	data := ctx.MarketDataMap[candidate.Symbol]
	if data == nil || data.CurrentPrice <= 0 {
		return nil, nil
	}
	if hasLivePositionForSymbol(ctx.Positions, candidate.Symbol) {
		return nil, nil
	}

	logicScore := *candidate.LogicScore
	matrices, err := at.performanceCache.GetBackcastMatricesWithWindow(
		at.id,
		data.Sector,
		candidate.Symbol,
		store.PERFORMANCE_WINDOW_SIZE_DEFAULT,
	)
	if err != nil {
		return nil, fmt.Errorf("load backcast matrices for %s: %w", candidate.Symbol, err)
	}

	resonance := kernel.CheckDirectionalResonance(logicScore, matrices)
	if !resonance.HasSignal() {
		return nil, nil
	}
	liveWeights := kernel.NormalizeLiveAttributionWeights(resonance.LiveAttributionWeights)

	globalEV := realBacktestBinExpectedValue(resonance.GlobalBin, resonance.Signal)
	sectorEV := realBacktestBinExpectedValue(resonance.SectorBin, resonance.Signal)
	symbolEV := realBacktestBinExpectedValue(resonance.SymbolBin, resonance.Signal)
	guardFactors := realBacktestGuardFactors(globalEV, sectorEV, symbolEV)
	if ok, reason, minFactor := kernel.ValidateResonanceFactors(guardFactors, guardConfig.AdaptiveEntryFloor); !ok {
		logger.Infof(
			"🎯 Real backtest skipped: %s for %s (MinFactor=%.2f Floor=%.2f Factors=%s)",
			reason,
			candidate.Symbol,
			minFactor,
			guardConfig.AdaptiveEntryFloor,
			formatRealBacktestGuardFactors(guardFactors),
		)
		return nil, nil
	}

	guardScore := kernel.CalculateResonanceScore(guardFactors, nil, guardConfig.AdaptiveEntryLambda)
	baseGuardScore := guardScore
	mahalanobisDistance := 0.0
	mahalanobisThreshold := kernel.DefaultMahalanobisThreshold
	mahalanobisResonant := false
	featureVectorFocus := ""
	featureVectorDeviation := 0.0
	entryAllowed := true
	entryBlockReason := ""
	featureVector := []float64(nil)
	archetypeID := ""
	archetypeName := ""
	archetypeScope := ""
	archetypeMarginScale := 1.0
	var matchedArchetype *kernel.FeatureArchetype
	var matchedLibrary *kernel.FeatureArchetypeLibrary
	if builtFeatureVector, ok := kernel.BuildResonanceFeatureVectorFromMarketData(data); ok {
		featureVector = builtFeatureVector
		library, libraryScope, err := at.loadFeatureArchetypeLibrary(candidate.Symbol, data.Sector)
		if err != nil {
			return nil, fmt.Errorf("load resonance feature archetype library for %s: %w", candidate.Symbol, err)
		}
		if library != nil {
			matchedLibrary = library
			match := kernel.MatchFeatureArchetype(featureVector, library, mahalanobisThreshold)
			mahalanobisDistance = match.Distance
			mahalanobisResonant = match.Known && match.Distance <= mahalanobisThreshold
			archetypeScope = libraryScope
			if match.Archetype != nil {
				matchedArchetype = match.Archetype
				archetypeID = match.Archetype.ID
				archetypeName = match.Archetype.Name
				archetypeMarginScale = match.Archetype.MaxMarginFraction
				featureVectorFocus, featureVectorDeviation = realBacktestFeatureVectorHealth(featureVector, match.Archetype.Cluster())
			}
			entryAllowed = match.Known
			entryBlockReason = match.Reason
			if !match.Known {
				guardScore = 0
				if entryBlockReason == "" && mahalanobisDistance > mahalanobisThreshold {
					entryBlockReason = fmt.Sprintf("D > %.1f", mahalanobisThreshold)
				}
				if err := at.preserveOutlierForIncubation(candidate.Symbol, mahalanobisDistance, mahalanobisThreshold); err != nil {
					return nil, fmt.Errorf("preserve outlier for incubation %s: %w", candidate.Symbol, err)
				}
				logger.Infof(
					"🎯 Real backtest skipped: Structural gate blocked for %s (Scope=%s Archetype=%s Distance=%.2f Threshold=%.2f Reason=%s Factors=%s)",
					candidate.Symbol,
					archetypeScope,
					archetypeID,
					mahalanobisDistance,
					mahalanobisThreshold,
					entryBlockReason,
					formatRealBacktestGuardFactors(guardFactors),
				)
				return nil, nil
			}
			guardScore = kernel.CalculateResonanceScore(guardFactors, nil, guardConfig.AdaptiveEntryLambda, mahalanobisDistance)
			if matchedArchetype != nil {
				logger.Infof(
					"🎯 Real backtest archetype adopted: %s scope=%s archetype=%s/%s distance=%.2f margin_scale=%.0f%% base=%.2f final=%.2f",
					candidate.Symbol,
					archetypeScope,
					archetypeID,
					archetypeName,
					mahalanobisDistance,
					archetypeMarginScale*100,
					baseGuardScore,
					guardScore,
				)
			}
		}
	}
	if guardScore < guardConfig.AdaptiveEntryFloor {
		logger.Infof(
			"🎯 Real backtest skipped: Logic Incoherence for %s (GuardScore=%.2f Floor=%.2f Factors=%s)",
			candidate.Symbol,
			guardScore,
			guardConfig.AdaptiveEntryFloor,
			formatRealBacktestGuardFactors(guardFactors),
		)
		return nil, nil
	}

	rawFinalEV := realBacktestAverageEV(globalEV, sectorEV, symbolEV, liveWeights)
	adaptiveEntryEVFloor := kernel.AdaptiveEntryEVFloor(guardConfig.AdaptiveEntryFloor)
	neighborhoodRows, err := at.loadNeighborhoodAuditRows(store.PERFORMANCE_RECENT_SAMPLE_LIMIT)
	if err != nil {
		return nil, fmt.Errorf("load neighborhood audit rows for %s: %w", candidate.Symbol, err)
	}
	neighborhoodAudit := kernel.EvaluateNeighborhoodAttributionInArchetype(
		featureVector,
		neighborhoodRows,
		matchedLibrary,
		matchedArchetype,
		kernel.DefaultNeighborhoodRadius,
	)
	neighborhoodRisk := kernel.EvaluateNeighborhoodRiskAdjustment(rawFinalEV, neighborhoodAudit)
	if neighborhoodRisk.Blocked {
		logger.Infof(
			"🎯 Real backtest skipped: [Rejected] %s 原始EV: %.2f%%, 邻域=%s (Radius=%.2f N=%d Win=%.0f%% Loss=%.0f%%), 折价 %.0f%%, 折价后EV: %.2f%%, 理由=%s, 拒绝入场。",
			candidate.Symbol,
			rawFinalEV*100,
			neighborhoodRisk.Label,
			neighborhoodAudit.Radius,
			neighborhoodAudit.SampleCount,
			neighborhoodAudit.WinRate*100,
			neighborhoodAudit.LossRate*100,
			neighborhoodRisk.RiskDiscountFactor*100,
			neighborhoodRisk.RiskAdjustedEV*100,
			neighborhoodRisk.BlockReason,
		)
		return nil, nil
	}
	if !kernel.ClearsRiskAdjustedEntryFloor(neighborhoodRisk.RiskAdjustedEV, guardConfig.AdaptiveEntryFloor) {
		shadowMonitorHint := ""
		if neighborhoodRisk.ShadowMonitorRequired {
			shadowMonitorHint = ", 已转交影子监控"
		}
		logger.Infof(
			"🎯 Real backtest skipped: [Rejected] %s 原始EV: %.2f%%, 邻域=%s (Radius=%.2f N=%d Win=%.0f%% Loss=%.0f%%), 折价 %.0f%%, 折价后EV: %.2f%% < %.2f%% 底线, 拒绝入场%s。",
			candidate.Symbol,
			rawFinalEV*100,
			neighborhoodRisk.Label,
			neighborhoodAudit.Radius,
			neighborhoodAudit.SampleCount,
			neighborhoodAudit.WinRate*100,
			neighborhoodAudit.LossRate*100,
			neighborhoodRisk.RiskDiscountFactor*100,
			neighborhoodRisk.RiskAdjustedEV*100,
			adaptiveEntryEVFloor*100,
			shadowMonitorHint,
		)
		return nil, nil
	}

	avgEV := neighborhoodRisk.RiskAdjustedEV
	freeBalance := realBacktestFreeBalance(ctx.Account)
	marginUsageRatio, requiredMargin, meetsThreshold, reserveBreached := evaluateRealBacktestSizing(
		avgEV,
		riskConfig,
		freeBalance,
	)
	if !meetsThreshold {
		logger.Infof(
			"🎯 Real backtest skipped: Average EV too low for %s (Raw_EV=%.2f%% Adjusted_EV=%.2f%% threshold=%.2f%%, G=%.2f%% S=%.2f%% C=%.2f%%, GuardScore=%.2f)",
			candidate.Symbol,
			rawFinalEV*100,
			avgEV*100,
			riskConfig.MinEVThreshold*100,
			globalEV*100,
			sectorEV*100,
			symbolEV*100,
			guardScore,
		)
		return nil, nil
	}
	if archetypeMarginScale <= 0 {
		archetypeMarginScale = 1
	}
	marginUsageRatio *= archetypeMarginScale
	requiredMargin *= archetypeMarginScale
	reserveBreached = freeBalance-requiredMargin < riskConfig.ReserveMargin
	positionUSD := realBacktestRequiredPositionUSD(requiredMargin)
	reserveBreached = freeBalance-requiredMargin < riskConfig.ReserveMargin

	if reserveBreached {
		logger.Infof(
			"🎯 Real backtest skipped: Reserve margin breached for %s (Free_Balance=%.2f USDT, Required_Margin=%.2f USDT, Reserve=%.2f USDT, GuardScore=%.2f NWGV=%s Discount=%.0f%%)",
			candidate.Symbol,
			freeBalance,
			requiredMargin,
			riskConfig.ReserveMargin,
			guardScore,
			neighborhoodRisk.Label,
			neighborhoodRisk.RiskDiscountFactor*100,
		)
	}

	openAction := "open_long"
	if resonance.Signal == kernel.DirectionalResonanceShort {
		openAction = "open_short"
	}

	selection := &realBacktestSelection{
		candidate:               candidate,
		entryPrice:              data.CurrentPrice,
		sector:                  data.Sector,
		resonance:               resonance,
		archetypeID:             archetypeID,
		archetypeName:           archetypeName,
		archetypeScope:          archetypeScope,
		archetypeMarginScale:    archetypeMarginScale,
		attributionWeights:      liveWeights,
		openAction:              openAction,
		logicScore:              logicScore,
		guardScore:              guardScore,
		mahalanobisDistance:     mahalanobisDistance,
		mahalanobisThreshold:    mahalanobisThreshold,
		mahalanobisResonant:     mahalanobisResonant,
		featureVectorFocus:      featureVectorFocus,
		featureVectorDeviation:  featureVectorDeviation,
		entryAllowed:            entryAllowed,
		entryBlockReason:        entryBlockReason,
		rawFinalEV:              rawFinalEV,
		riskDiscountFactor:      neighborhoodRisk.RiskDiscountFactor,
		riskAdjustedEV:          neighborhoodRisk.RiskAdjustedEV,
		neighborhoodLabel:       neighborhoodRisk.Label,
		neighborhoodRadius:      neighborhoodAudit.Radius,
		neighborhoodSampleCount: neighborhoodAudit.SampleCount,
		neighborhoodWinRate:     neighborhoodAudit.WinRate,
		neighborhoodLossRate:    neighborhoodAudit.LossRate,
		shadowMonitorRequired:   neighborhoodRisk.ShadowMonitorRequired,
		neighborhoodFinalMargin: requiredMargin,
		globalEV:                globalEV,
		sectorEV:                sectorEV,
		symbolEV:                symbolEV,
		avgEV:                   avgEV,
		marginUsageRatio:        marginUsageRatio,
		requiredMargin:          requiredMargin,
		freeBalance:             freeBalance,
		positionUSD:             positionUSD,
	}

	return &realBacktestCandidateAssessment{
		selection:       selection,
		shadowEligible:  true,
		reserveBreached: reserveBreached,
	}, nil
}

func (at *AutoTrader) createShadowBacktestPosition(candidate kernel.CandidateCoin, selection *realBacktestSelection, tickTime time.Time) *store.TraderPosition {
	if at == nil || selection == nil {
		return nil
	}

	entryPrice := selection.entryPrice
	if entryPrice <= 0 {
		return nil
	}

	positionUSD := selection.positionUSD
	if positionUSD <= 0 {
		positionUSD = realBacktestRequiredPositionUSD(selection.requiredMargin)
	}
	if positionUSD <= 0 {
		return nil
	}

	side := "LONG"
	if selection.resonance.Signal == kernel.DirectionalResonanceShort {
		side = "SHORT"
	}

	positionID := shadowBacktestPositionID(candidate.Symbol, side, tickTime)
	nowMs := tickTime.UTC().UnixMilli()
	return &store.TraderPosition{
		ID:                 positionID,
		TraderID:           GlobalRealSniperShadowTraderID,
		ExchangeID:         at.exchangeID,
		ExchangeType:       at.exchange,
		ExchangePositionID: fmt.Sprintf("shadow_%s_%s_%d", market.Normalize(candidate.Symbol), strings.ToLower(side), nowMs),
		Symbol:             market.Normalize(candidate.Symbol),
		Side:               side,
		EntryQuantity:      positionUSD / entryPrice,
		Quantity:           positionUSD / entryPrice,
		EntryPrice:         entryPrice,
		EntryOrderID:       fmt.Sprintf("shadow_order_%d", positionID),
		EntryTime:          nowMs,
		Leverage:           realBacktestLeverage,
		EntryLogicScore:    selection.logicScore,
		EntryExpectedEV:    selection.avgEV,
		Status:             "OPEN",
		Source:             "shadow_backtest",
		AutoCloseAt:        nowMs + defaultShadowExitObservationWindow.Milliseconds(),
		CreatedAt:          nowMs,
		UpdatedAt:          nowMs,
	}
}

func shadowBacktestPositionID(symbol, side string, tickTime time.Time) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(strings.ToUpper(strings.TrimSpace(symbol))))
	_, _ = hasher.Write([]byte("|"))
	_, _ = hasher.Write([]byte(strings.ToUpper(strings.TrimSpace(side))))
	_, _ = hasher.Write([]byte("|"))
	_, _ = hasher.Write([]byte(fmt.Sprintf("%d", tickTime.UTC().UnixMilli())))
	value := int64(hasher.Sum64() & 0x7fffffffffffffff)
	if value == 0 {
		return tickTime.UTC().UnixMilli()
	}
	return value
}

func buildRealBacktestReasoning(selection *realBacktestSelection) string {
	if selection == nil {
		return "实战回测: 动态贝叶斯入场"
	}
	triggerLabel := realBacktestTriggerLabel(selection.attributionWeights)
	gateLabel := "Gate=许可"
	if !selection.entryAllowed {
		gateLabel = fmt.Sprintf("Gate=结构性禁入(%s)", selection.entryBlockReason)
	}
	neighborhoodLabel := ""
	if selection.neighborhoodLabel != "" {
		neighborhoodLabel = fmt.Sprintf(", RNN=%s, Radius=%.2f, N=%d, 本地胜率=%.0f%%, 折价=%.0f%%",
			selection.neighborhoodLabel,
			selection.neighborhoodRadius,
			selection.neighborhoodSampleCount,
			selection.neighborhoodWinRate*100,
			selection.riskDiscountFactor*100,
		)
		if selection.shadowMonitorRequired {
			neighborhoodLabel += ", ShadowMonitor=ON"
		}
	}
	archetypeLabel := ""
	if strings.TrimSpace(selection.archetypeID) != "" {
		archetypeLabel = fmt.Sprintf(", 基因=%s/%s@%s", selection.archetypeID, selection.archetypeName, selection.archetypeScope)
	}
	return fmt.Sprintf(
		"实战回测: 加权入场 %s, Final_EV=%.2f%%, 原始EV=%.2f%%, 触发=%s, %s, 逻辑分=%.2f, 分箱=%d, 赛道=%s%s%s",
		selection.resonance.Signal,
		selection.avgEV*100,
		selection.rawFinalEV*100,
		triggerLabel,
		gateLabel,
		selection.logicScore,
		selection.resonance.BinStart,
		selection.sector,
		archetypeLabel,
		neighborhoodLabel,
	)
}

func buildRealBacktestDecisionMeta(selection *realBacktestSelection) *store.RealBacktestDecisionMeta {
	if selection == nil {
		return nil
	}

	weights := kernel.NormalizeLiveAttributionWeights(selection.attributionWeights)
	entryAllowed := selection.entryAllowed
	return &store.RealBacktestDecisionMeta{
		Signal:                  string(selection.resonance.Signal),
		LogicScore:              selection.logicScore,
		Sector:                  selection.sector,
		PositionSizeUSD:         selection.positionUSD,
		BinCenter:               selection.resonance.BinStart,
		SmoothingHalfWidth:      0,
		HoldDurationSeconds:     int(defaultRealBacktestHoldDuration / time.Second),
		RawEntryEV:              selection.rawFinalEV,
		FinalEntryEV:            selection.avgEV,
		RiskDiscountFactor:      selection.riskDiscountFactor,
		NeighborhoodLabel:       selection.neighborhoodLabel,
		NeighborhoodSampleCount: selection.neighborhoodSampleCount,
		NeighborhoodRadius:      selection.neighborhoodRadius,
		ShadowMonitorRequired:   selection.shadowMonitorRequired,
		AttributionWeights:      weights,
		MahalanobisDistance:     selection.mahalanobisDistance,
		MahalanobisThreshold:    selection.mahalanobisThreshold,
		MahalanobisResonant:     selection.mahalanobisResonant,
		FeatureVectorFocus:      selection.featureVectorFocus,
		FeatureVectorDeviation:  selection.featureVectorDeviation,
		EntryAllowed:            &entryAllowed,
		EntryBlockReason:        selection.entryBlockReason,
		Global:                  buildRealBacktestDimension(selection.resonance.GlobalBin, selection.resonance.Signal),
		SectorBin:               buildRealBacktestDimension(selection.resonance.SectorBin, selection.resonance.Signal),
		Symbol:                  buildRealBacktestDimension(selection.resonance.SymbolBin, selection.resonance.Signal),
	}
}

func realBacktestTriggerLabel(weights map[string]float64) string {
	normalized := kernel.NormalizeLiveAttributionWeights(weights)
	sector := normalized["sector"]
	symbol := normalized["symbol"]
	global := normalized["global"]

	if sector >= symbol && sector >= global {
		return "赛道强势 / Sector Strength"
	}
	if symbol >= sector && symbol >= global {
		return "微观突破 / Micro Breakout"
	}
	return "宏观护航 / Macro Tailwind"
}

func realBacktestFeatureVectorHealth(vector []float64, cluster *kernel.ResonanceFeatureCluster) (string, float64) {
	if cluster == nil || len(vector) == 0 || len(cluster.Mean) == 0 {
		return "", 0
	}

	limit := len(vector)
	if len(cluster.Mean) < limit {
		limit = len(cluster.Mean)
	}
	bestIndex := -1
	bestAbsDelta := 0.0
	bestDelta := 0.0
	for i := 0; i < limit; i++ {
		delta := vector[i] - cluster.Mean[i]
		if absDelta := math.Abs(delta); absDelta > bestAbsDelta {
			bestAbsDelta = absDelta
			bestDelta = delta
			bestIndex = i
		}
	}
	if bestIndex < 0 {
		return "", 0
	}
	if bestIndex < len(cluster.FeatureNames) {
		return cluster.FeatureNames[bestIndex], bestDelta
	}
	return fmt.Sprintf("feature_%d", bestIndex+1), bestDelta
}

func realBacktestDimensionBucket(selection *realBacktestSelection) string {
	if selection == nil {
		return "GLOBAL"
	}

	weights := kernel.NormalizeLiveAttributionWeights(selection.attributionWeights)
	scores := map[string]float64{
		"GLOBAL": math.Abs(selection.globalEV) * weights["global"],
		"SECTOR": math.Abs(selection.sectorEV) * weights["sector"],
		"SYMBOL": math.Abs(selection.symbolEV) * weights["symbol"],
	}
	bestBucket := "GLOBAL"
	bestScore := scores[bestBucket]
	for bucket, score := range scores {
		if score > bestScore {
			bestBucket = bucket
			bestScore = score
		}
	}
	if bestScore <= 0 {
		return "GLOBAL"
	}
	return bestBucket
}

func realBacktestAverageEV(globalEV, sectorEV, symbolEV float64, weights map[string]float64) float64 {
	normalized := kernel.NormalizeLiveAttributionWeights(weights)
	return safeRealBacktestValue(globalEV)*normalized["global"] +
		safeRealBacktestValue(sectorEV)*normalized["sector"] +
		safeRealBacktestValue(symbolEV)*normalized["symbol"]
}

func realBacktestGuardFactors(globalEV, sectorEV, symbolEV float64) []float64 {
	return []float64{
		realBacktestGuardFactorScore(globalEV),
		realBacktestGuardFactorScore(sectorEV),
		realBacktestGuardFactorScore(symbolEV),
	}
}

func realBacktestGuardFactorScore(expectedValue float64) float64 {
	if math.IsNaN(expectedValue) || math.IsInf(expectedValue, 0) {
		return 0
	}
	return expectedValue * 10000.0
}

func formatRealBacktestGuardFactors(factors []float64) string {
	if len(factors) < 3 {
		return "[]"
	}
	return fmt.Sprintf("[%.1f, %.1f, %.1f]", factors[0], factors[1], factors[2])
}

func realBacktestMarginUsageRatio(avgEV, minEV, maxEV float64) float64 {
	factor := 1.0
	if maxEV > minEV {
		factor = (avgEV - minEV) / (maxEV - minEV)
	}
	if factor < 0 {
		factor = 0
	}
	if factor > 1 {
		factor = 1
	}
	return 0.40 + (0.60 * factor)
}

func realBacktestRequiredMargin(maxMarginPerTrade, usageRatio float64) float64 {
	return maxMarginPerTrade * usageRatio
}

func realBacktestRequiredPositionUSD(requiredMargin float64) float64 {
	return requiredMargin * float64(realBacktestLeverage)
}

func evaluateRealBacktestSizing(avgEV float64, riskConfig store.RealBacktestSystemConfig, freeBalance float64) (marginUsageRatio, requiredMargin float64, meetsThreshold bool, reserveBreached bool) {
	if avgEV < riskConfig.MinEVThreshold {
		return 0, 0, false, false
	}
	if riskConfig.MaxEVThreshold <= riskConfig.MinEVThreshold {
		return 0, 0, false, false
	}

	marginUsageRatio = realBacktestMarginUsageRatio(avgEV, riskConfig.MinEVThreshold, riskConfig.MaxEVThreshold)
	requiredMargin = realBacktestRequiredMargin(riskConfig.MaxMarginPerTrade, marginUsageRatio)
	if freeBalance-requiredMargin < riskConfig.ReserveMargin {
		return marginUsageRatio, requiredMargin, true, true
	}
	return marginUsageRatio, requiredMargin, true, false
}

func realBacktestFreeBalance(account kernel.AccountInfo) float64 {
	if account.AvailableBalance > 0 {
		return account.AvailableBalance
	}
	fallback := account.TotalEquity - account.MarginUsed
	if fallback < 0 {
		return 0
	}
	return fallback
}

func buildRealBacktestDimension(
	bin *store.ScoreBinPerformance,
	signal kernel.DirectionalResonance,
) *store.RealBacktestDimension {
	if bin == nil {
		return nil
	}

	dimension := &store.RealBacktestDimension{
		BinStart:                 bin.BinStart,
		BinLabel:                 bin.BinLabel,
		TradeCount:               bin.TradeCount,
		ExpectedValueLong:        bin.ExpectedValueLong,
		ExpectedValueShort:       bin.ExpectedValueShort,
		MedianExpectedValueLong:  bin.MedianExpectedValueLong,
		MedianExpectedValueShort: bin.MedianExpectedValueShort,
		ProfitFactorLong:         bin.ProfitFactorLong,
		ProfitFactorShort:        bin.ProfitFactorShort,
		Smoothed:                 bin.Smoothed,
		SmoothedBy:               bin.SmoothedBy,
	}

	switch signal {
	case kernel.DirectionalResonanceShort:
		dimension.ExpectedValue = bin.ExpectedValueShort
		dimension.MedianExpectedValue = bin.MedianExpectedValueShort
		dimension.ProfitFactor = bin.ProfitFactorShort
	default:
		dimension.ExpectedValue = bin.ExpectedValueLong
		dimension.MedianExpectedValue = bin.MedianExpectedValueLong
		dimension.ProfitFactor = bin.ProfitFactorLong
	}

	return dimension
}

func (at *AutoTrader) executeRealBacktestOpen(decision *kernel.Decision, actionRecord *store.DecisionAction, entryPrice float64) error {
	if decision == nil || actionRecord == nil {
		return fmt.Errorf("real backtest decision is nil")
	}

	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		if market.Normalize(symbol) != market.Normalize(decision.Symbol) {
			continue
		}
		if rawPositionQuantity(pos) == 0 {
			continue
		}
		return fmt.Errorf("❌ [REAL BACKTEST] %s already has an open position", decision.Symbol)
	}

	if err := at.trader.SetMarginMode(decision.Symbol, false); err != nil {
		return fmt.Errorf("failed to force isolated margin mode for %s: %w", decision.Symbol, err)
	}

	quantity := decision.PositionSizeUSD / entryPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = entryPrice

	var order map[string]interface{}
	switch decision.Action {
	case "open_long":
		order, err = at.trader.OpenLong(decision.Symbol, quantity, decision.Leverage)
		if err == nil {
			at.notePositionOpened(decision.Symbol, "long", time.Now().UTC())
		}
	case "open_short":
		order, err = at.trader.OpenShort(decision.Symbol, quantity, decision.Leverage)
		if err == nil {
			at.notePositionOpened(decision.Symbol, "short", time.Now().UTC())
		}
	default:
		return fmt.Errorf("unsupported real backtest open action: %s", decision.Action)
	}
	if err != nil {
		return err
	}

	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}
	autoCloseAt := time.Now().UTC().Add(at.getRealBacktestHoldDuration()).UnixMilli()
	at.recordRealBacktestExecution(order, decision.Symbol, decision.Action, quantity, entryPrice, decision.Leverage, 0, nil, autoCloseAt, "", actionRecord.RealBacktest)
	return nil
}

func (at *AutoTrader) executeRealBacktestClose(symbol, action string, actionRecord *store.DecisionAction) error {
	if actionRecord == nil {
		return fmt.Errorf("real backtest close action record is nil")
	}

	side := "LONG"
	if action == "close_short" {
		side = "SHORT"
	}

	entryPrice, quantity := at.lookupOpenPositionForTrader(GlobalRealSniperTraderID, symbol, side)
	openPos, err := at.lookupOpenPositionRecordForTrader(GlobalRealSniperTraderID, symbol, side)
	if err != nil {
		return err
	}
	if openPos != nil {
		entryPrice = openPos.EntryPrice
		quantity = openPos.Quantity
	}
	marketPrice, err := at.trader.GetMarketPrice(symbol)
	if err == nil && marketPrice > 0 {
		actionRecord.Price = marketPrice
	}
	actionRecord.Quantity = quantity
	if actionRecord.RealBacktest == nil {
		actionRecord.RealBacktest = at.loadLatestRealBacktestMeta(symbol, action)
	}

	var order map[string]interface{}
	switch action {
	case "close_long":
		order, err = at.trader.CloseLong(symbol, 0)
	case "close_short":
		order, err = at.trader.CloseShort(symbol, 0)
	default:
		return fmt.Errorf("unsupported real backtest close action: %s", action)
	}
	if err != nil {
		return err
	}

	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}
	at.recordRealBacktestExecution(order, symbol, action, quantity, actionRecord.Price, 0, entryPrice, openPos, 0, actionRecord.Reasoning, actionRecord.RealBacktest)
	return nil
}

func (at *AutoTrader) lookupOpenPositionForTrader(traderID, symbol, side string) (float64, float64) {
	normalizedSymbol := market.Normalize(symbol)
	if at.store != nil {
		if openPos, err := at.store.Position().GetOpenPositionBySymbol(traderID, normalizedSymbol, side); err == nil && openPos != nil {
			return openPos.EntryPrice, openPos.Quantity
		}
	}

	positions, err := at.trader.GetPositions()
	if err != nil {
		return 0, 0
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
		return entryPrice, absPositionQuantity(pos)
	}
	return 0, 0
}

func (at *AutoTrader) saveRealBacktestDecisionRecord(tickTime time.Time, action *store.DecisionAction, success bool, execErr error) {
	if action == nil {
		return
	}

	record := &store.DecisionRecord{
		Timestamp:    tickTime.UTC(),
		Success:      success,
		ErrorMessage: action.Error,
		ExecutionLog: []string{"Execution mode: real_backtest"},
		Decisions:    []store.DecisionAction{*action},
	}
	if execErr != nil && record.ErrorMessage == "" {
		record.ErrorMessage = execErr.Error()
	}
	if err := at.saveDecisionForTrader(record, GlobalRealSniperTraderID); err != nil {
		logger.Warnf("⚠️ Failed to save real backtest decision record: %v", err)
	}
}

func (at *AutoTrader) recordRealBacktestExecution(
	orderResult map[string]interface{},
	symbol string,
	action string,
	quantity float64,
	price float64,
	leverage int,
	entryPrice float64,
	openPos *store.TraderPosition,
	autoCloseAt int64,
	closeReason string,
	meta *store.RealBacktestDecisionMeta,
) {
	if at == nil || at.store == nil {
		return
	}

	orderID := extractOrderID(orderResult)
	if orderID == "" || orderID == "0" {
		logger.Infof("  ⚠️ Real backtest order ID is empty, skipping persistent record")
		return
	}

	positionSide := positionSideFromAction(action)
	orderRecord := at.createOrderRecordForTrader(GlobalRealSniperTraderID, orderID, symbol, action, positionSide, quantity, price, leverage)
	nowMs := time.Now().UTC().UnixMilli()
	orderRecord.Status = "FILLED"
	orderRecord.FilledQuantity = quantity
	orderRecord.AvgFillPrice = price
	orderRecord.CreatedAt = nowMs
	orderRecord.UpdatedAt = nowMs
	orderRecord.FilledAt = nowMs
	if err := at.store.Order().CreateOrder(orderRecord); err != nil {
		logger.Infof("  ⚠️ Failed to record real backtest order: %v", err)
	} else if err := at.store.Order().UpdateOrderStatus(orderRecord.ID, "FILLED", quantity, price, 0); err != nil {
		logger.Infof("  ⚠️ Failed to mark real backtest order as filled: %v", err)
	}
	at.recordOrderFillForTrader(GlobalRealSniperTraderID, orderRecord.ID, orderID, symbol, action, price, quantity, 0)

	switch {
	case isOpenAction(action):
		at.persistRealBacktestOpenPosition(symbol, positionSide, orderID, quantity, price, leverage, autoCloseAt, meta)
	case isCloseAction(action):
		if openPos != nil && openPos.ID > 0 {
			at.persistRealBacktestClosePositionByPosition(openPos, orderID, price, quantity, closeReason, entryPrice)
		} else {
			at.persistRealBacktestClosePosition(symbol, positionSide, orderID, price, quantity, closeReason, entryPrice)
		}
	}
}

func (at *AutoTrader) persistRealBacktestOpenPosition(symbol, side, orderID string, quantity, price float64, leverage int, autoCloseAt int64, meta *store.RealBacktestDecisionMeta) {
	if at == nil || at.store == nil {
		return
	}

	normalizedSymbol := market.Normalize(symbol)
	nowMs := time.Now().UTC().UnixMilli()
	pos := &store.TraderPosition{
		TraderID:        GlobalRealSniperTraderID,
		ExchangeID:      at.exchangeID,
		ExchangeType:    at.exchange,
		Symbol:          normalizedSymbol,
		Side:            side,
		Quantity:        quantity,
		EntryPrice:      price,
		EntryOrderID:    orderID,
		EntryTime:       nowMs,
		Leverage:        leverage,
		EntryLogicScore: metaLogicScore(meta),
		EntryExpectedEV: realBacktestCompositeExpectedValue(meta),
		Status:          "OPEN",
		Source:          "real_backtest",
		AutoCloseAt:     autoCloseAt,
		CreatedAt:       nowMs,
		UpdatedAt:       nowMs,
	}
	if err := at.store.Position().CreateOpenPosition(pos); err != nil {
		logger.Infof("  ⚠️ Failed to persist real backtest position: %v", err)
		return
	}
	if err := at.store.RealTradeStats().CreateOpenRecord(&store.RealTradeResonanceRecord{
		TraderID:      GlobalRealSniperTraderID,
		ExchangeID:    at.exchangeID,
		OrderID:       orderID,
		Symbol:        normalizedSymbol,
		Side:          strings.ToLower(side),
		EntryTime:     nowMs,
		EntryGlobalEV: realBacktestDimensionExpectedValue(metaDimension(meta, "global")),
		EntrySectorEV: realBacktestDimensionExpectedValue(metaDimension(meta, "sector")),
		EntrySymbolEV: realBacktestDimensionExpectedValue(metaDimension(meta, "symbol")),
		CreatedAt:     nowMs,
		UpdatedAt:     nowMs,
	}); err != nil {
		logger.Infof("  ⚠️ Failed to persist real trade resonance open record: %v", err)
	}
	logger.Infof("  📊 Real backtest position recorded [%s] %s %s @ %.6f auto_close_at=%d",
		GlobalRealSniperTraderID,
		normalizedSymbol,
		side,
		price,
		autoCloseAt,
	)
	at.armRealBacktestAutoCloseTimer(pos, autoCloseAt)
}

func (at *AutoTrader) persistRealBacktestClosePosition(symbol, side, orderID string, exitPrice, quantity float64, closeReason string, entryPriceHint float64) {
	if at == nil || at.store == nil {
		return
	}

	normalizedSymbol := market.Normalize(symbol)
	openPos, err := at.store.Position().GetOpenPositionBySymbol(GlobalRealSniperTraderID, normalizedSymbol, side)
	if err != nil {
		logger.Infof("  ⚠️ Failed to load open real backtest position: %v", err)
		return
	}
	if openPos == nil {
		logger.Infof("  ⚠️ No open real backtest position found for %s %s", normalizedSymbol, side)
		return
	}
	at.persistRealBacktestClosePositionByPosition(openPos, orderID, exitPrice, quantity, closeReason, entryPriceHint)
}

func (at *AutoTrader) loadLatestRealBacktestMeta(symbol, closeAction string) *store.RealBacktestDecisionMeta {
	if at == nil || at.store == nil {
		return nil
	}

	targetAction := "open_long"
	if closeAction == "close_short" {
		targetAction = "open_short"
	}
	normalizedSymbol := market.Normalize(symbol)

	records, err := at.store.Decision().GetLatestRecords(GlobalRealSniperTraderID, 240)
	if err != nil {
		return nil
	}
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		for j := len(record.Decisions) - 1; j >= 0; j-- {
			action := record.Decisions[j]
			if !isRealBacktestOpenAction(&action, targetAction) {
				continue
			}
			if market.Normalize(action.Symbol) != normalizedSymbol {
				continue
			}
			return cloneRealBacktestDecisionMeta(action.RealBacktest)
		}
	}
	return nil
}

func cloneRealBacktestDecisionMeta(meta *store.RealBacktestDecisionMeta) *store.RealBacktestDecisionMeta {
	if meta == nil {
		return nil
	}
	cloned := *meta
	if len(meta.AttributionWeights) > 0 {
		cloned.AttributionWeights = make(map[string]float64, len(meta.AttributionWeights))
		for key, value := range meta.AttributionWeights {
			cloned.AttributionWeights[key] = value
		}
	}
	cloned.Global = cloneRealBacktestDimension(meta.Global)
	cloned.SectorBin = cloneRealBacktestDimension(meta.SectorBin)
	cloned.Symbol = cloneRealBacktestDimension(meta.Symbol)
	return &cloned
}

func cloneRealBacktestDimension(dimension *store.RealBacktestDimension) *store.RealBacktestDimension {
	if dimension == nil {
		return nil
	}
	cloned := *dimension
	return &cloned
}

func (at *AutoTrader) sampleRealTradeResonanceHold(ctx *kernel.Context, tickTime time.Time) error {
	if at == nil || at.store == nil || at.performanceCache == nil || ctx == nil {
		return nil
	}

	openPositions, err := at.store.Position().GetOpenPositions(GlobalRealSniperTraderID)
	if err != nil {
		return err
	}

	sampleTimeMs := tickTime.UTC().UnixMilli()
	for _, pos := range openPositions {
		if pos == nil || pos.ExchangeID != at.exchangeID || pos.EntryOrderID == "" {
			continue
		}
		sample, ok := at.buildRealTradeResonanceSample(ctx, pos)
		if !ok {
			continue
		}
		if err := at.store.RealTradeStats().ApplyHoldSampleByOrderID(
			GlobalRealSniperTraderID,
			at.exchangeID,
			pos.EntryOrderID,
			sample,
			sampleTimeMs,
		); err != nil {
			logger.Warnf("⚠️ Failed to append real trade resonance sample for %s %s: %v", pos.Symbol, pos.Side, err)
		}
	}
	return nil
}

func (at *AutoTrader) buildRealTradeResonanceSample(ctx *kernel.Context, pos *store.TraderPosition) (store.RealTradeResonanceSample, bool) {
	if at == nil || ctx == nil || pos == nil {
		return store.RealTradeResonanceSample{}, false
	}

	symbol := market.Normalize(pos.Symbol)
	data := marketDataBySymbol(ctx.MarketDataMap, symbol)
	if data == nil {
		return store.RealTradeResonanceSample{}, false
	}

	recalculated := at.recalculateCandidateHeatScore(symbol, data, ctx.QuantDataMap)
	if recalculated == nil || !isFiniteRealBacktestScore(recalculated.CompositeScore) {
		return store.RealTradeResonanceSample{}, false
	}

	sector := strings.TrimSpace(data.Sector)
	if sector == "" {
		meta := at.loadLatestRealBacktestMeta(symbol, closeActionFromPositionSide(pos.Side))
		if meta != nil {
			sector = strings.TrimSpace(meta.Sector)
		}
	}

	matrices, err := at.performanceCache.GetBackcastMatricesWithWindow(
		at.id,
		sector,
		symbol,
		store.PERFORMANCE_WINDOW_SIZE_DEFAULT,
	)
	if err != nil || matrices == nil {
		return store.RealTradeResonanceSample{}, false
	}

	signal := signalFromPositionSide(pos.Side)
	return store.RealTradeResonanceSample{
		GlobalEV: realBacktestBinExpectedValue(store.FindPerformanceBinForScore(matrices.Global, recalculated.CompositeScore), signal),
		SectorEV: realBacktestBinExpectedValue(store.FindPerformanceBinForScore(matrices.Sector, recalculated.CompositeScore), signal),
		SymbolEV: realBacktestBinExpectedValue(store.FindPerformanceBinForScore(matrices.Symbol, recalculated.CompositeScore), signal),
	}, true
}

func marketDataBySymbol(items map[string]*market.Data, symbol string) *market.Data {
	if len(items) == 0 || symbol == "" {
		return nil
	}
	if direct := items[symbol]; direct != nil {
		return direct
	}
	normalized := market.Normalize(symbol)
	for key, item := range items {
		if market.Normalize(key) == normalized {
			return item
		}
	}
	return nil
}

func signalFromPositionSide(side string) kernel.DirectionalResonance {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "SHORT":
		return kernel.DirectionalResonanceShort
	default:
		return kernel.DirectionalResonanceLong
	}
}

func closeActionFromPositionSide(side string) string {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "SHORT":
		return "close_short"
	default:
		return "close_long"
	}
}

func realBacktestBinExpectedValue(bin *store.ScoreBinPerformance, signal kernel.DirectionalResonance) float64 {
	switch signal {
	case kernel.DirectionalResonanceShort:
		return safeRealBacktestValue(binValue(bin, "short_ev"))
	default:
		return safeRealBacktestValue(binValue(bin, "long_ev"))
	}
}

func binValue(bin *store.ScoreBinPerformance, field string) float64 {
	if bin == nil {
		return 0
	}
	switch field {
	case "short_ev":
		return bin.ExpectedValueShort
	case "short_median":
		return bin.MedianExpectedValueShort
	case "short_pf":
		return bin.ProfitFactorShort
	case "long_median":
		return bin.MedianExpectedValueLong
	case "long_pf":
		return bin.ProfitFactorLong
	default:
		return bin.ExpectedValueLong
	}
}

func metaDimension(meta *store.RealBacktestDecisionMeta, scope string) *store.RealBacktestDimension {
	if meta == nil {
		return nil
	}
	switch scope {
	case "sector":
		return meta.SectorBin
	case "symbol":
		return meta.Symbol
	default:
		return meta.Global
	}
}

func realBacktestDimensionExpectedValue(dimension *store.RealBacktestDimension) float64 {
	if dimension == nil {
		return 0
	}
	return safeRealBacktestValue(dimension.ExpectedValue)
}

func metaLogicScore(meta *store.RealBacktestDecisionMeta) float64 {
	if meta == nil {
		return 0
	}
	return safeRealBacktestValue(meta.LogicScore)
}

func realBacktestCompositeExpectedValue(meta *store.RealBacktestDecisionMeta) float64 {
	if meta == nil {
		return 0
	}

	globalEV := realBacktestDimensionExpectedValue(metaDimension(meta, "global"))
	sectorEV := realBacktestDimensionExpectedValue(metaDimension(meta, "sector"))
	symbolEV := realBacktestDimensionExpectedValue(metaDimension(meta, "symbol"))
	return realBacktestAverageEV(globalEV, sectorEV, symbolEV, meta.AttributionWeights)
}

func safeRealBacktestValue(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func isFiniteRealBacktestScore(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func extractOrderID(orderResult map[string]interface{}) string {
	if orderResult == nil {
		return ""
	}
	switch v := orderResult["orderId"].(type) {
	case int64:
		return fmt.Sprintf("%d", v)
	case int:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%.0f", v)
	case string:
		return v
	default:
		if v != nil {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

func positionSideFromAction(action string) string {
	switch action {
	case "open_long", "close_long":
		return "LONG"
	case "open_short", "close_short":
		return "SHORT"
	default:
		return ""
	}
}

func isOpenAction(action string) bool {
	return action == "open_long" || action == "open_short"
}

func isCloseAction(action string) bool {
	return action == "close_long" || action == "close_short"
}

func isRealBacktestOpenAction(action *store.DecisionAction, targetAction string) bool {
	if action == nil || !action.Success {
		return false
	}
	if action.Action != targetAction {
		return false
	}
	return action.ExecutionMode == "real_backtest"
}

func buildRealBacktestTimedExitReason(holdDuration time.Duration) string {
	return fmt.Sprintf("实战回测: %d分钟强制到点离场", int(holdDuration/time.Minute))
}

func (at *AutoTrader) forceCloseExpiredRealBacktestPosition(pos *store.TraderPosition) error {
	if pos == nil {
		return nil
	}
	return at.forceCloseExpiredRealBacktestPositionByID(pos.ID)
}

func (at *AutoTrader) hasPhysicalPosition(symbol, side string) (bool, float64, error) {
	if at == nil || at.trader == nil {
		return false, 0, nil
	}

	positions, err := at.trader.GetPositions()
	if err != nil {
		return false, 0, err
	}

	normalizedSymbol := market.Normalize(symbol)
	targetSide := strings.ToLower(strings.TrimSpace(side))
	for _, pos := range positions {
		posSymbol, _ := pos["symbol"].(string)
		if market.Normalize(posSymbol) != normalizedSymbol {
			continue
		}
		if normalizePositionSideForRaw(pos["side"]) != targetSide {
			continue
		}
		if absPositionQuantity(pos) == 0 {
			continue
		}
		return true, floatFromRaw(pos["markPrice"]), nil
	}

	price, priceErr := at.trader.GetMarketPrice(symbol)
	if priceErr != nil {
		return false, 0, nil
	}
	return false, price, nil
}

func normalizePositionSideForRaw(value interface{}) string {
	text, _ := value.(string)
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "long":
		return "long"
	case "short":
		return "short"
	default:
		return ""
	}
}

func floatFromRaw(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func (at *AutoTrader) isRealBacktestEnabled() (bool, error) {
	if at == nil || at.store == nil {
		return false, nil
	}
	return at.store.GetRealBacktestEnabled()
}

func (at *AutoTrader) getRealBacktestScanInterval() time.Duration {
	if at != nil && at.realBacktestScanInterval > 0 {
		return at.realBacktestScanInterval
	}
	return defaultRealBacktestScanInterval
}

func (at *AutoTrader) getRealBacktestHoldDuration() time.Duration {
	if at != nil && at.realBacktestHoldDuration > 0 {
		return at.realBacktestHoldDuration
	}
	return defaultRealBacktestHoldDuration
}

func countLivePositions(positions []kernel.PositionInfo) int {
	count := 0
	for _, pos := range positions {
		if pos.Quantity > 0 {
			count++
		}
	}
	return count
}

func hasLivePositionForSymbol(positions []kernel.PositionInfo, symbol string) bool {
	normalized := market.Normalize(symbol)
	for _, pos := range positions {
		if pos.Quantity <= 0 {
			continue
		}
		if market.Normalize(pos.Symbol) == normalized {
			return true
		}
	}
	return false
}

func countLiveRawPositions(positions []map[string]interface{}) int {
	count := 0
	for _, pos := range positions {
		if rawPositionQuantity(pos) != 0 {
			count++
		}
	}
	return count
}

func rawPositionQuantity(pos map[string]interface{}) float64 {
	quantity, _ := pos["positionAmt"].(float64)
	return quantity
}

func absPositionQuantity(pos map[string]interface{}) float64 {
	quantity := rawPositionQuantity(pos)
	if quantity < 0 {
		return -quantity
	}
	return quantity
}
