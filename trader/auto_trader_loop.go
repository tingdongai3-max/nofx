package trader

import (
	"encoding/json"
	"fmt"
	"math"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/provider/nofxos"
	"nofx/store"
	"nofx/wallet"
	"strings"
	"time"
)

// calculateNextAlignment returns the delay until the next natural wall-clock boundary.
// Example: with a 3m interval, ticks align to :00, :03, :06 ...
func calculateNextAlignment(interval time.Duration) (time.Duration, time.Time) {
	return calculateNextAlignmentFrom(time.Now(), interval)
}

func calculateNextAlignmentFrom(now time.Time, interval time.Duration) (time.Duration, time.Time) {
	if interval <= 0 {
		nextTick := now.Add(time.Second)
		if !nextTick.After(now) {
			nextTick = now.Add(time.Second)
		}
		return nextTick.Sub(now), nextTick
	}

	nextTick := now.Truncate(interval).Add(interval)
	return nextTick.Sub(now), nextTick
}

// runCycle runs one trading cycle (using AI full decision-making).
// tickTime is the logical wall-clock boundary this cycle belongs to.
func (at *AutoTrader) runCycle(tickTime time.Time) error {
	tickTime = tickTime.UTC()
	cycleNumber, callCount := at.reserveAICycle()
	startedAt := time.Now().UTC()
	drift := startedAt.Sub(tickTime)

	logger.Info("\n" + strings.Repeat("=", 70) + "\n")
	logger.Infof("⏰ %s - AI decision cycle #%d (tick=%s, drift=%dms)",
		startedAt.Format(time.RFC3339Nano),
		callCount,
		tickTime.Format(time.RFC3339),
		drift.Milliseconds(),
	)
	logger.Info(strings.Repeat("=", 70))

	// 0. Check if trader is stopped (early exit to prevent trades after Stop() is called)
	at.isRunningMutex.RLock()
	running := at.isRunning
	at.isRunningMutex.RUnlock()
	if !running {
		logger.Infof("⏹ Trader is stopped, aborting cycle #%d", callCount)
		return nil
	}

	// Check USDC balance periodically for claw402 users (every 10 cycles)
	if callCount%10 == 0 && store.IsClaw402Config(at.config.AIModel) {
		at.checkClaw402Balance()
	}

	// Create decision record
	record := &store.DecisionRecord{
		CycleNumber:     cycleNumber,
		Timestamp:       tickTime,
		PriceSnapshotAt: market.NormalizePriceSnapshotTime(tickTime),
		ExecutionLog: []string{
			fmt.Sprintf("Scheduled tick: %s", tickTime.Format(time.RFC3339)),
			fmt.Sprintf("Dispatch start: %s", startedAt.Format(time.RFC3339Nano)),
			fmt.Sprintf("Clock drift: %d ms", drift.Milliseconds()),
		},
		Success: true,
	}

	// 1. Check if trading needs to be stopped
	stopUntil := at.currentStopUntil()
	if startedAt.Before(stopUntil) {
		remaining := stopUntil.Sub(startedAt)
		logger.Infof("⏸ Risk control: Trading paused, remaining %.0f minutes", remaining.Minutes())
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("Risk control paused, remaining %.0f minutes", remaining.Minutes())
		at.saveDecision(record)
		return nil
	}

	// 2. Reset daily P&L (reset every day)
	if at.resetDailyPnLIfNeeded(startedAt) {
		logger.Info("📅 Daily P&L reset")
	}

	// 4. Collect trading context
	ctx, err := at.buildTradingContext(tickTime, callCount)
	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("Failed to build trading context: %v", err)
		at.saveDecision(record)
		return fmt.Errorf("failed to build trading context: %w", err)
	}
	record.PriceSnapshotAt = ctx.PriceSnapshotAt

	// Save equity snapshot independently (decoupled from AI decision, used for drawing profit curve)
	// NOTE: Must be called BEFORE candidate coins check to ensure equity is always recorded
	at.saveEquitySnapshot(ctx, tickTime)

	// If no candidate coins available, log but do not error
	if len(ctx.CandidateCoins) == 0 {
		at.updateCandidateSnapshot(ctx, tickTime)
		logger.Infof("ℹ️  No candidate coins available, skipping this cycle")
		record.Success = true // Not an error, just no candidate coins
		record.ExecutionLog = append(record.ExecutionLog, "No candidate coins available, cycle skipped")
		record.AccountState = store.AccountSnapshot{
			TotalBalance:          ctx.Account.TotalEquity,
			AvailableBalance:      ctx.Account.AvailableBalance,
			TotalUnrealizedProfit: ctx.Account.UnrealizedPnL,
			PositionCount:         ctx.Account.PositionCount,
			InitialBalance:        at.initialBalance,
		}
		at.saveDecision(record)
		return nil
	}

	logger.Info(strings.Repeat("=", 70))
	for _, coin := range ctx.CandidateCoins {
		record.CandidateCoins = append(record.CandidateCoins, coin.Symbol)
	}

	logger.Infof("📊 Account equity: %.2f USDT | Available: %.2f USDT | Positions: %d",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)

	decisionTime := tickTime.UTC()

	// 5. Use strategy engine to call AI for decision
	logger.Infof("🤖 Requesting AI analysis and decision... [Strategy Engine]")
	aiDecision, err := kernel.GetFullDecisionWithStrategy(ctx, at.mcpClient, at.strategyEngine, "balanced")
	record.PriceSnapshots = buildDecisionPriceSnapshots(ctx)

	if aiDecision != nil && aiDecision.AIRequestDurationMs > 0 {
		record.AIRequestDurationMs = aiDecision.AIRequestDurationMs
		logger.Infof("⏱️ AI call duration: %.2f seconds", float64(record.AIRequestDurationMs)/1000)
		record.ExecutionLog = append(record.ExecutionLog,
			fmt.Sprintf("AI call duration: %d ms", record.AIRequestDurationMs))
	}

	at.updateCandidateSnapshot(ctx, tickTime)
	if shadowErr := at.persistShadowSnapshots(decisionTime, ctx); shadowErr != nil {
		logger.Warnf("⚠️ Failed to persist shadow snapshots: %v", shadowErr)
	}

	// Save chain of thought, decisions, and input prompt even if there's an error (for debugging)
	if aiDecision != nil {
		if shadowErr := at.markShadowActions(decisionTime, aiDecision.Decisions); shadowErr != nil {
			logger.Warnf("⚠️ Failed to mark shadow actions: %v", shadowErr)
		}
		record.SystemPrompt = aiDecision.SystemPrompt // Save system prompt
		record.InputPrompt = aiDecision.UserPrompt
		record.CoTTrace = aiDecision.CoTTrace
		record.RawResponse = aiDecision.RawResponse // Save raw AI response for debugging
		if len(aiDecision.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(aiDecision.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		}
	}

	// Record AI charge (track cost regardless of decision outcome)
	if aiDecision != nil && at.store != nil {
		if chargeErr := at.store.AICharge().Record(at.id, at.aiModel, at.config.AIModel); chargeErr != nil {
			logger.Warnf("⚠️ Failed to record AI charge: %v", chargeErr)
		}
	}

	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("Failed to get AI decision: %v", err)
		if humanized, intercepted := humanizeDecisionInterception(err, aiDecision); intercepted {
			record.ErrorMessage = humanized
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("Decision intercepted by validator: %v", err))
		}

		// Print system prompt and AI chain of thought (output even with errors for debugging)
		if aiDecision != nil {
			logger.Info("\n" + strings.Repeat("=", 70) + "\n")
			logger.Infof("📋 System prompt (error case)")
			logger.Info(strings.Repeat("=", 70))
			logger.Info(aiDecision.SystemPrompt)
			logger.Info(strings.Repeat("=", 70))

			if aiDecision.CoTTrace != "" {
				logger.Info("\n" + strings.Repeat("-", 70) + "\n")
				logger.Info("💭 AI chain of thought analysis (error case):")
				logger.Info(strings.Repeat("-", 70))
				logger.Info(aiDecision.CoTTrace)
				logger.Info(strings.Repeat("-", 70))
			}
		}

		at.saveDecision(record)
		return fmt.Errorf("failed to get AI decision: %w", err)
	}

	// // 5. Print system prompt
	// logger.Infof("\n" + strings.Repeat("=", 70))
	// logger.Infof("📋 System prompt [template: %s]", at.systemPromptTemplate)
	// logger.Info(strings.Repeat("=", 70))
	// logger.Info(decision.SystemPrompt)
	// logger.Infof(strings.Repeat("=", 70) + "\n")

	// 6. Print AI chain of thought
	// logger.Infof("\n" + strings.Repeat("-", 70))
	// logger.Info("💭 AI chain of thought analysis:")
	// logger.Info(strings.Repeat("-", 70))
	// logger.Info(decision.CoTTrace)
	// logger.Infof(strings.Repeat("-", 70) + "\n")

	// 7. Print AI decisions
	// logger.Infof("📋 AI decision list (%d items):\n", len(kernel.Decisions))
	// for i, d := range kernel.Decisions {
	//     logger.Infof("  [%d] %s: %s - %s", i+1, d.Symbol, d.Action, d.Reasoning)
	//     if d.Action == "open_long" || d.Action == "open_short" {
	//        logger.Infof("      Leverage: %dx | Position: %.2f USDT | Stop loss: %.4f | Take profit: %.4f",
	//           d.Leverage, d.PositionSizeUSD, d.StopLoss, d.TakeProfit)
	//     }
	// }
	logger.Info()
	logger.Info(strings.Repeat("-", 70))
	// 8. Sort decisions: ensure close positions first, then open positions (prevent position stacking overflow)
	logger.Info(strings.Repeat("-", 70))

	// 8. Sort decisions: ensure close positions first, then open positions (prevent position stacking overflow)
	sortedDecisions := sortDecisionsByPriority(aiDecision.Decisions)

	logger.Info("🔄 Execution order (optimized): Close positions first → Open positions later")
	for i, d := range sortedDecisions {
		logger.Infof("  [%d] %s %s", i+1, d.Symbol, d.Action)
	}
	logger.Info()

	// Check if trader is stopped before executing any decisions (prevent trades after Stop())
	at.isRunningMutex.RLock()
	running = at.isRunning
	at.isRunningMutex.RUnlock()
	if !running {
		logger.Infof("⏹ Trader stopped before decision execution, aborting cycle #%d", callCount)
		return nil
	}

	// Execute decisions and record results
	for _, d := range sortedDecisions {
		// Check if trader is stopped before each decision (allow immediate stop during execution)
		at.isRunningMutex.RLock()
		running = at.isRunning
		at.isRunningMutex.RUnlock()
		if !running {
			logger.Infof("⏹ Trader stopped during decision execution, aborting remaining decisions")
			break
		}

		actionRecord := store.DecisionAction{
			Action:     d.Action,
			Symbol:     d.Symbol,
			Quantity:   0,
			Leverage:   d.Leverage,
			Price:      0,
			StopLoss:   d.StopLoss,
			TakeProfit: d.TakeProfit,
			Confidence: d.Confidence,
			Reasoning:  d.Reasoning,
			Timestamp:  time.Now().UTC(),
			Success:    false,
		}

		if err := at.executeDecisionWithRecord(&d, &actionRecord); err != nil {
			logger.Infof("❌ Failed to execute decision (%s %s): %v", d.Symbol, d.Action, err)
			actionRecord.Error = err.Error()
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("❌ %s %s failed: %v", d.Symbol, d.Action, err))
		} else {
			actionRecord.Success = true
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("✓ %s %s succeeded", d.Symbol, d.Action))
			// Brief delay after successful execution
			time.Sleep(1 * time.Second)
		}

		record.Decisions = append(record.Decisions, actionRecord)
	}

	// 9. Save decision record
	if err := at.saveDecision(record); err != nil {
		logger.Infof("⚠ Failed to save decision record: %v", err)
	}

	return nil
}

func buildDecisionPriceSnapshots(ctx *kernel.Context) map[string]float64 {
	if ctx == nil || len(ctx.MarketDataMap) == 0 {
		return nil
	}

	snapshots := make(map[string]float64, len(ctx.MarketDataMap))
	for symbol, data := range ctx.MarketDataMap {
		if data == nil || data.CurrentPrice <= 0 {
			continue
		}
		snapshots[market.Normalize(symbol)] = data.CurrentPrice
	}
	if len(snapshots) == 0 {
		return nil
	}
	return snapshots
}

func (at *AutoTrader) updateCandidateSnapshot(ctx *kernel.Context, snapshotTime time.Time) {
	if ctx == nil {
		return
	}
	if snapshotTime.IsZero() {
		snapshotTime = time.Now().UTC()
	}

	snapshot := CandidateSnapshot{
		TraderID:        at.id,
		TraderName:      at.name,
		UpdatedAt:       snapshotTime.UTC(),
		PriceSnapshotAt: ctx.PriceSnapshotAt,
		ScoreEngine:     "recalculated_backcast",
		Candidates:      make([]CandidateMarketSnapshot, 0, len(ctx.CandidateCoins)),
	}
	telemetry := make(map[string]candidateTelemetrySnapshot, len(ctx.CandidateCoins))

	for _, coin := range ctx.CandidateCoins {
		data, ok := ctx.MarketDataMap[coin.Symbol]
		if !ok || data == nil {
			continue
		}

		recalculatedHeat := at.recalculateCandidateHeatScore(coin.Symbol, data, ctx.QuantDataMap)
		logicScore := 0.0
		hasLogicScore := false
		if coin.LogicScore != nil && !math.IsNaN(*coin.LogicScore) && !math.IsInf(*coin.LogicScore, 0) {
			logicScore = *coin.LogicScore
			hasLogicScore = true
		} else if recalculatedHeat != nil && !math.IsNaN(recalculatedHeat.CompositeScore) && !math.IsInf(recalculatedHeat.CompositeScore, 0) {
			logicScore = recalculatedHeat.CompositeScore
			hasLogicScore = true
		}

		bias := store.PerformanceBiasWait
		expectedEV := 0.0
		var currentBin *store.ScoreBinPerformance
		if hasLogicScore {
			bias, expectedEV, currentBin = at.resolveCandidatePerformanceSummary(
				coin.Symbol,
				data,
				logicScore,
			)
		}

		if recalculatedHeat != nil {
			telemetry[strings.ToUpper(coin.Symbol)] = candidateTelemetrySnapshot{
				Heat:       recalculatedHeat.CompositeScore,
				TradingSub: recalculatedHeat.TradingScore,
				QuantSub:   recalculatedHeat.QuantScore,
			}
		}

		snapshot.Candidates = append(snapshot.Candidates, CandidateMarketSnapshot{
			Symbol:        coin.Symbol,
			Sector:        data.Sector,
			CurrentPrice:  data.CurrentPrice,
			LogicScore:    logicScore,
			Bias:          bias,
			ExpectedEV:    expectedEV,
			DebugBinStats: compactCandidateBin(currentBin),
		})
	}

	at.candidateSnapshotMu.Lock()
	if !at.candidateSnapshot.UpdatedAt.IsZero() && at.candidateSnapshot.UpdatedAt.After(snapshot.UpdatedAt) {
		at.candidateSnapshotMu.Unlock()
		return
	}
	at.candidateSnapshot = snapshot
	at.candidateTelemetry = telemetry
	at.candidateSnapshotMu.Unlock()

	if len(ctx.CandidateCoins) > 0 && len(snapshot.Candidates) == 0 {
		logger.Warnf("⚠️ Candidate snapshot is empty despite %d candidate coins; market data map size=%d", len(ctx.CandidateCoins), len(ctx.MarketDataMap))
	}
}

func (at *AutoTrader) recalculateCandidateHeatScore(
	symbol string,
	data *market.Data,
	quantMap map[string]*kernel.QuantData,
) *market.HeatScoreData {
	if data == nil || symbol == "" {
		return nil
	}

	var quantData *nofxos.QuantData
	if quantMap != nil {
		quantData = toNofxosQuantData(quantMap[symbol])
	}
	return market.RecalculateHeatScore(at.id, symbol, data, quantData)
}

func (at *AutoTrader) resolveCandidatePerformanceSummary(
	symbol string,
	data *market.Data,
	logicScore float64,
) (string, float64, *store.ScoreBinPerformance) {
	if at == nil || at.performanceCache == nil || data == nil || symbol == "" {
		return store.PerformanceBiasWait, 0, nil
	}

	matrices, err := at.performanceCache.GetMatricesWithWindow(
		at.id,
		data.Sector,
		symbol,
		store.PERFORMANCE_WINDOW_SIZE_DEFAULT,
	)
	if err != nil {
		logger.Warnf("candidate performance summary failed: trader=%s symbol=%s err=%v", at.id, symbol, err)
		return store.PerformanceBiasWait, 0, nil
	}

	var currentBin *store.ScoreBinPerformance
	if matrices != nil {
		currentBin = store.FindPerformanceBinForScore(matrices.Symbol, logicScore)
		if currentBin == nil {
			currentBin = store.FindPerformanceBinForScore(matrices.Sector, logicScore)
		}
		if currentBin == nil {
			currentBin = store.FindPerformanceBinForScore(matrices.Global, logicScore)
		}
	}

	bias, expectedEV := store.ResolvePerformanceBias(currentBin)
	if currentBin == nil {
		return bias, expectedEV, nil
	}

	clonedBin := *currentBin
	return bias, expectedEV, &clonedBin
}

func compactCandidateBin(bin *store.ScoreBinPerformance) *CandidateDebugBinStats {
	if bin == nil {
		return nil
	}
	return &CandidateDebugBinStats{
		BinStart:           bin.BinStart,
		TradeCount:         bin.TradeCount,
		ExpectedValueLong:  bin.ExpectedValueLong,
		ProfitFactorLong:   bin.ProfitFactorLong,
		ExpectedValueShort: bin.ExpectedValueShort,
		ProfitFactorShort:  bin.ProfitFactorShort,
	}
}

func toNofxosQuantData(source *kernel.QuantData) *nofxos.QuantData {
	if source == nil {
		return nil
	}

	result := &nofxos.QuantData{
		Symbol:      source.Symbol,
		Price:       source.Price,
		PriceChange: make(map[string]float64, len(source.PriceChange)),
	}
	for key, value := range source.PriceChange {
		result.PriceChange[key] = value
	}

	if source.Netflow != nil {
		result.Netflow = &nofxos.NetflowData{}
		if source.Netflow.Institution != nil {
			result.Netflow.Institution = &nofxos.FlowTypeData{
				Future: cloneFloatMap(source.Netflow.Institution.Future),
				Spot:   cloneFloatMap(source.Netflow.Institution.Spot),
			}
		}
		if source.Netflow.Personal != nil {
			result.Netflow.Personal = &nofxos.FlowTypeData{
				Future: cloneFloatMap(source.Netflow.Personal.Future),
				Spot:   cloneFloatMap(source.Netflow.Personal.Spot),
			}
		}
	}

	if len(source.OI) > 0 {
		result.OI = make(map[string]*nofxos.OIData, len(source.OI))
		for exchange, oi := range source.OI {
			if oi == nil {
				continue
			}
			target := &nofxos.OIData{
				CurrentOI: oi.CurrentOI,
				Delta:     make(map[string]*nofxos.OIDeltaData, len(oi.Delta)),
			}
			for duration, delta := range oi.Delta {
				if delta == nil {
					continue
				}
				target.Delta[duration] = &nofxos.OIDeltaData{
					OIDelta:        delta.OIDelta,
					OIDeltaValue:   delta.OIDeltaValue,
					OIDeltaPercent: delta.OIDeltaPercent,
				}
			}
			result.OI[exchange] = target
		}
	}

	return result
}

func cloneFloatMap(source map[string]float64) map[string]float64 {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]float64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

// buildTradingContext builds trading context
func (at *AutoTrader) buildTradingContext(tickTime time.Time, callCount int) (*kernel.Context, error) {
	tickTime = tickTime.UTC()
	priceSnapshotAt := market.NormalizePriceSnapshotTime(tickTime)

	// 1. Get account information
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("failed to get account balance: %w", err)
	}

	// Get account fields
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0
	totalEquity := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Use totalEquity directly if provided by trader (more accurate)
	if eq, ok := balance["totalEquity"].(float64); ok && eq > 0 {
		totalEquity = eq
	} else {
		// Fallback: Total Equity = Wallet balance + Unrealized profit
		totalEquity = totalWalletBalance + totalUnrealizedProfit
	}

	// 2. Get position information
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	var positionInfos []kernel.PositionInfo
	totalMarginUsed := 0.0

	// Current position key set (for cleaning up closed position records)
	currentPositionKeys := make(map[string]bool)

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // Short position quantity is negative, convert to positive
		}

		// Skip closed positions (quantity = 0), prevent "ghost positions" from being passed to AI
		if quantity == 0 {
			continue
		}

		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		// Calculate margin used (estimated)
		leverage := 10 // Default value, should actually be fetched from position info
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed

		// Calculate P&L percentage (based on margin, considering leverage)
		pnlPct := calculatePnLPercentage(unrealizedPnl, marginUsed)

		// Get position open time from exchange (preferred) or fallback to local tracking
		posKey := symbol + "_" + side
		currentPositionKeys[posKey] = true

		var updateTime int64
		// Priority 1: Get from database (trader_positions table) - most accurate
		if at.store != nil {
			if dbPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, symbol, side); err == nil && dbPos != nil {
				if dbPos.EntryTime > 0 {
					updateTime = dbPos.EntryTime
				}
			}
		}
		// Priority 2: Get from exchange API (Bybit: createdTime, OKX: createdTime)
		if updateTime == 0 {
			if createdTime, ok := pos["createdTime"].(int64); ok && createdTime > 0 {
				updateTime = createdTime
			}
		}
		// Priority 3: Fallback to local tracking
		if updateTime == 0 {
			updateTime = at.positionFirstSeenAt(posKey, tickTime)
		}

		// Get peak profit rate for this position
		at.peakPnLCacheMutex.RLock()
		peakPnlPct := at.peakPnLCache[posKey]
		at.peakPnLCacheMutex.RUnlock()

		positionInfos = append(positionInfos, kernel.PositionInfo{
			Symbol:           symbol,
			Side:             side,
			EntryPrice:       entryPrice,
			MarkPrice:        markPrice,
			Quantity:         quantity,
			Leverage:         leverage,
			UnrealizedPnL:    unrealizedPnl,
			UnrealizedPnLPct: pnlPct,
			PeakPnLPct:       peakPnlPct,
			LiquidationPrice: liquidationPrice,
			MarginUsed:       marginUsed,
			UpdateTime:       updateTime,
		})
	}

	// Clean up closed position records
	at.pruneClosedPositionTracking(currentPositionKeys)

	// 3. Use strategy engine to get candidate coins (must have strategy engine)
	var candidateCoins []kernel.CandidateCoin
	if at.strategyEngine == nil {
		logger.Infof("⚠️ [%s] No strategy engine configured, skipping candidate coins", at.name)
	} else {
		coins, err := at.strategyEngine.GetCandidateCoins()
		if err != nil {
			// Log warning but don't fail - equity snapshot should still be saved
			logger.Infof("⚠️ [%s] Failed to get candidate coins: %v (will use empty list)", at.name, err)
		} else {
			candidateCoins = coins
			logger.Infof("📋 [%s] Strategy engine fetched candidate coins: %d", at.name, len(candidateCoins))
		}
	}

	// 4. Calculate total P&L
	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 5. Get leverage from strategy config
	strategyConfig := at.strategyEngine.GetConfig()
	btcEthLeverage := strategyConfig.RiskControl.BTCETHMaxLeverage
	altcoinLeverage := strategyConfig.RiskControl.AltcoinMaxLeverage
	logger.Infof("📋 [%s] Strategy leverage config: BTC/ETH=%dx, Altcoin=%dx", at.name, btcEthLeverage, altcoinLeverage)

	// 6. Build context
	ctx := &kernel.Context{
		TraderID:        at.id,
		CurrentTime:     tickTime.Format("2006-01-02 15:04:05 UTC"),
		RuntimeMinutes:  int(tickTime.Sub(at.startTime).Minutes()),
		CallCount:       callCount,
		DecisionTime:    tickTime,
		PriceSnapshotAt: priceSnapshotAt,
		BTCETHLeverage:  btcEthLeverage,
		AltcoinLeverage: altcoinLeverage,
		Account: kernel.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			UnrealizedPnL:    totalUnrealizedProfit,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:      positionInfos,
		CandidateCoins: candidateCoins,
	}

	// 7. Add recent closed trades (if store is available)
	if at.store != nil {
		// Get recent 10 closed trades for AI context
		recentTrades, err := at.store.Position().GetRecentTrades(at.id, 10)
		if err != nil {
			logger.Infof("⚠️ [%s] Failed to get recent trades: %v", at.name, err)
		} else {
			logger.Infof("📊 [%s] Found %d recent closed trades for AI context", at.name, len(recentTrades))
			for _, trade := range recentTrades {
				// Convert Unix timestamps to formatted strings for AI readability
				entryTimeStr := ""
				if trade.EntryTime > 0 {
					entryTimeStr = time.Unix(trade.EntryTime, 0).UTC().Format("01-02 15:04 UTC")
				}
				exitTimeStr := ""
				if trade.ExitTime > 0 {
					exitTimeStr = time.Unix(trade.ExitTime, 0).UTC().Format("01-02 15:04 UTC")
				}

				ctx.RecentOrders = append(ctx.RecentOrders, kernel.RecentOrder{
					Symbol:       trade.Symbol,
					Side:         trade.Side,
					EntryPrice:   trade.EntryPrice,
					ExitPrice:    trade.ExitPrice,
					RealizedPnL:  trade.RealizedPnL,
					PnLPct:       trade.PnLPct,
					EntryTime:    entryTimeStr,
					ExitTime:     exitTimeStr,
					HoldDuration: trade.HoldDuration,
				})
			}
		}
		// Get trading statistics for AI context
		stats, err := at.store.Position().GetFullStats(at.id)
		if err != nil {
			logger.Infof("⚠️ [%s] Failed to get trading stats: %v", at.name, err)
		} else if stats == nil {
			logger.Infof("⚠️ [%s] GetFullStats returned nil", at.name)
		} else if stats.TotalTrades == 0 {
			logger.Infof("⚠️ [%s] GetFullStats returned 0 trades (traderID=%s)", at.name, at.id)
		} else {
			ctx.TradingStats = &kernel.TradingStats{
				TotalTrades:    stats.TotalTrades,
				WinRate:        stats.WinRate,
				ProfitFactor:   stats.ProfitFactor,
				SharpeRatio:    stats.SharpeRatio,
				TotalPnL:       stats.TotalPnL,
				AvgWin:         stats.AvgWin,
				AvgLoss:        stats.AvgLoss,
				MaxDrawdownPct: stats.MaxDrawdownPct,
			}
			logger.Infof("📈 [%s] Trading stats: %d trades, %.1f%% win rate, PF=%.2f, Sharpe=%.2f, DD=%.1f%%",
				at.name, stats.TotalTrades, stats.WinRate, stats.ProfitFactor, stats.SharpeRatio, stats.MaxDrawdownPct)
		}
	} else {
		logger.Infof("⚠️ [%s] Store is nil, cannot get recent trades", at.name)
	}

	// 8. Get quantitative data (if enabled in strategy config)
	if strategyConfig.Indicators.EnableQuantData {
		// Collect symbols to query (candidate coins + position coins)
		symbolsToQuery := make(map[string]bool)
		for _, coin := range candidateCoins {
			symbolsToQuery[coin.Symbol] = true
		}
		for _, pos := range positionInfos {
			symbolsToQuery[pos.Symbol] = true
		}

		symbols := make([]string, 0, len(symbolsToQuery))
		for sym := range symbolsToQuery {
			symbols = append(symbols, sym)
		}

		logger.Infof("📊 [%s] Fetching quantitative data for %d symbols...", at.name, len(symbols))
		ctx.QuantDataMap = at.strategyEngine.FetchQuantDataBatch(symbols)
		logger.Infof("📊 [%s] Successfully fetched quantitative data for %d symbols", at.name, len(ctx.QuantDataMap))
	}

	// 9. Get OI ranking data (market-wide position changes)
	if strategyConfig.Indicators.EnableOIRanking {
		logger.Infof("📊 [%s] Fetching OI ranking data...", at.name)
		ctx.OIRankingData = at.strategyEngine.FetchOIRankingData()
		if ctx.OIRankingData != nil {
			logger.Infof("📊 [%s] OI ranking data ready: %d top, %d low positions",
				at.name, len(ctx.OIRankingData.TopPositions), len(ctx.OIRankingData.LowPositions))
		}
	}

	// 10. Get NetFlow ranking data (market-wide fund flow)
	if strategyConfig.Indicators.EnableNetFlowRanking {
		logger.Infof("💰 [%s] Fetching NetFlow ranking data...", at.name)
		ctx.NetFlowRankingData = at.strategyEngine.FetchNetFlowRankingData()
		if ctx.NetFlowRankingData != nil {
			logger.Infof("💰 [%s] NetFlow ranking data ready: inst_in=%d, inst_out=%d",
				at.name, len(ctx.NetFlowRankingData.InstitutionFutureTop), len(ctx.NetFlowRankingData.InstitutionFutureLow))
		}
	}

	// 11. Get Price ranking data (market-wide gainers/losers)
	if strategyConfig.Indicators.EnablePriceRanking {
		logger.Infof("📈 [%s] Fetching Price ranking data...", at.name)
		ctx.PriceRankingData = at.strategyEngine.FetchPriceRankingData()
		if ctx.PriceRankingData != nil {
			logger.Infof("📈 [%s] Price ranking data ready for %d durations",
				at.name, len(ctx.PriceRankingData.Durations))
		}
	}

	return ctx, nil
}

// sortDecisionsByPriority sorts decisions: close positions first, then open positions, finally hold/wait
// This avoids position stacking overflow when changing positions
func sortDecisionsByPriority(decisions []kernel.Decision) []kernel.Decision {
	if len(decisions) <= 1 {
		return decisions
	}

	// Define priority
	getActionPriority := func(action string) int {
		switch action {
		case "close_long", "close_short":
			return 1 // Highest priority: close positions first
		case "open_long", "open_short":
			return 2 // Second priority: open positions later
		case "hold", "wait":
			return 3 // Lowest priority: wait
		default:
			return 999 // Unknown actions at the end
		}
	}

	// Copy decision list
	sorted := make([]kernel.Decision, len(decisions))
	copy(sorted, decisions)

	// Sort by priority
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if getActionPriority(sorted[i].Action) > getActionPriority(sorted[j].Action) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

// checkClaw402Balance checks USDC balance and logs warnings if low
func (at *AutoTrader) checkClaw402Balance() {
	scanMinutes := int(at.config.ScanInterval.Minutes())
	if scanMinutes <= 0 {
		scanMinutes = 3
	}
	dailyCost, _ := store.EstimateRunway(1.0, at.config.CustomModelName, scanMinutes)
	logger.Infof("💰 [%s] Estimated daily AI cost: ~$%.2f (model: %s, interval: %dm)",
		at.name, dailyCost, at.config.CustomModelName, scanMinutes)

	if at.claw402WalletAddr != "" {
		balance, err := wallet.QueryUSDCBalance(at.claw402WalletAddr)
		if err != nil {
			logger.Warnf("⚠️ [%s] Failed to query USDC balance: %v", at.name, err)
			return
		}

		if balance < 1.0 {
			logger.Warnf("⚠️ [%s] Low USDC balance: $%.2f — AI may stop soon!", at.name, balance)
		}
		if balance <= 0 {
			logger.Errorf("🚨 [%s] USDC balance is ZERO — AI calls will fail!", at.name)
		}

		runway := float64(0)
		if dailyCost > 0 {
			runway = balance / dailyCost
		}
		logger.Infof("💰 [%s] USDC Balance: $%.2f | Daily AI cost: ~$%.2f | Runway: ~%.1f days",
			at.name, balance, dailyCost, runway)
	}
}
