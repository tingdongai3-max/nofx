package kernel

import (
	"fmt"
	"math"
	"nofx/logger"
	"nofx/market"
	"nofx/provider/nofxos"
	"nofx/store"
	"sort"
	"strings"
	"time"
)

type personalityDimension struct {
	Label  string
	Weight float64
	IC     float64
	Tag    string
}

// ============================================================================
// Prompt Building - System Prompt
// ============================================================================

// BuildSystemPrompt builds System Prompt according to strategy configuration
func (e *StrategyEngine) BuildSystemPrompt(accountEquity float64, variant string) string {
	var sb strings.Builder
	riskControl := e.config.RiskControl
	promptSections := e.config.PromptSections

	// 0. Data Dictionary & Schema (ensure AI understands all fields)
	lang := e.GetLanguage()
	schemaPrompt := GetSchemaPrompt(lang)
	sb.WriteString(schemaPrompt)
	sb.WriteString("\n\n")
	sb.WriteString("---\n\n")

	// 1. Role definition (editable)
	if promptSections.RoleDefinition != "" {
		sb.WriteString(promptSections.RoleDefinition)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("# You are a professional cryptocurrency trading AI\n\n")
		sb.WriteString("Your task is to make trading decisions based on provided market data.\n\n")
	}

	// 2. Trading mode variant
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "aggressive":
		sb.WriteString("## Mode: Aggressive\n- Prioritize capturing trend breakouts, can build positions in batches when confidence ≥ 70\n- Allow higher positions, but must strictly set stop-loss and explain risk-reward ratio\n\n")
	case "conservative":
		sb.WriteString("## Mode: Conservative\n- Only open positions when multiple signals resonate\n- Prioritize cash preservation, must pause for multiple periods after consecutive losses\n\n")
	case "scalping":
		sb.WriteString("## Mode: Scalping\n- Focus on short-term momentum, smaller profit targets but require quick action\n- If price doesn't move as expected within two bars, immediately reduce position or stop-loss\n\n")
	}

	// 3. Hard constraints (risk control)
	btcEthPosValueRatio := riskControl.BTCETHMaxPositionValueRatio
	if btcEthPosValueRatio <= 0 {
		btcEthPosValueRatio = 5.0
	}
	altcoinPosValueRatio := riskControl.AltcoinMaxPositionValueRatio
	if altcoinPosValueRatio <= 0 {
		altcoinPosValueRatio = 1.0
	}

	sb.WriteString("# Hard Constraints (Risk Control)\n\n")
	sb.WriteString("## CODE ENFORCED (Backend validation, cannot be bypassed):\n")
	sb.WriteString(fmt.Sprintf("- Max Positions: %d coins simultaneously\n", riskControl.MaxPositions))
	sb.WriteString(fmt.Sprintf("- Position Value Limit (Altcoins): max %.0f USDT (= equity %.0f × %.1fx)\n",
		accountEquity*altcoinPosValueRatio, accountEquity, altcoinPosValueRatio))
	sb.WriteString(fmt.Sprintf("- Position Value Limit (BTC/ETH): max %.0f USDT (= equity %.0f × %.1fx)\n",
		accountEquity*btcEthPosValueRatio, accountEquity, btcEthPosValueRatio))
	sb.WriteString(fmt.Sprintf("- Max Margin Usage: ≤%.0f%%\n", riskControl.MaxMarginUsage*100))
	sb.WriteString(fmt.Sprintf("- Min Position Size: ≥%.0f USDT\n\n", riskControl.MinPositionSize))

	sb.WriteString("## AI GUIDED (Recommended, you should follow):\n")
	sb.WriteString(fmt.Sprintf("- Trading Leverage: Altcoins max %dx | BTC/ETH max %dx\n",
		riskControl.AltcoinMaxLeverage, riskControl.BTCETHMaxLeverage))
	sb.WriteString(fmt.Sprintf("- Risk-Reward Ratio: ≥1:%.1f (take_profit / stop_loss)\n", riskControl.MinRiskRewardRatio))
	sb.WriteString(fmt.Sprintf("- Min Confidence: ≥%d to open position\n\n", riskControl.MinConfidence))

	// Position sizing guidance
	sb.WriteString("## Position Sizing Guidance\n")
	sb.WriteString("Calculate `position_size_usd` based on your confidence and the Position Value Limits above:\n")
	sb.WriteString("- High confidence (≥85): Use 80-100%% of max position value limit\n")
	sb.WriteString("- Medium confidence (70-84): Use 50-80%% of max position value limit\n")
	sb.WriteString("- Low confidence (60-69): Use 30-50%% of max position value limit\n")
	sb.WriteString(fmt.Sprintf("- Example: With equity %.0f and BTC/ETH ratio %.1fx, max is %.0f USDT\n",
		accountEquity, btcEthPosValueRatio, accountEquity*btcEthPosValueRatio))
	sb.WriteString("- **DO NOT** just use available_balance as position_size_usd. Use the Position Value Limits!\n\n")

	// 4. Trading frequency (editable)
	if promptSections.TradingFrequency != "" {
		sb.WriteString(promptSections.TradingFrequency)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("# ⏱️ Trading Frequency Awareness\n\n")
		sb.WriteString("- Reference frequency: 2-4 trades/day ≈ 0.1-0.2 trades/hour\n")
		sb.WriteString("- >2 trades/hour = Overtrading\n")
		sb.WriteString("- Single position hold time ≥ 30-60 minutes\n")
		sb.WriteString("If you find yourself trading every period → standards too low; if closing positions < 30 minutes → too impatient.\n\n")
	}

	// 5. Entry standards (editable)
	if promptSections.EntryStandards != "" {
		sb.WriteString(promptSections.EntryStandards)
		sb.WriteString("\n\nYou have the following indicator data:\n")
		e.writeAvailableIndicators(&sb)
		sb.WriteString(fmt.Sprintf("\n**Confidence ≥ %d** required to open positions.\n\n", riskControl.MinConfidence))
	} else {
		sb.WriteString("# 🎯 Entry Standards (Strict)\n\n")
		sb.WriteString("Only open positions when multiple signals resonate. You have:\n")
		e.writeAvailableIndicators(&sb)
		sb.WriteString(fmt.Sprintf("\nFeel free to use any effective analysis method, but **confidence ≥ %d** required to open positions; avoid low-quality behaviors such as single indicators, contradictory signals, sideways consolidation, reopening immediately after closing, etc.\n\n", riskControl.MinConfidence))
	}

	// 6. Decision process (editable)
	if promptSections.DecisionProcess != "" {
		sb.WriteString(promptSections.DecisionProcess)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("# 📋 Decision Process\n\n")
		sb.WriteString("1. Check positions → Should we take profit/stop-loss\n")
		sb.WriteString("2. Scan candidate coins + multi-timeframe → Are multiple signals aligned or conflicting\n")
		sb.WriteString("3. Write chain of thought first, then output structured JSON\n\n")
	}

	sb.WriteString("## 核心决策逻辑：权重加总而非硬性共振 (Weighted Sum, Not AND Gate)\n")
	sb.WriteString("1. Recalced LogicScore 只作为【历史表现索引】。它不再强制要求 Global / Sector / Symbol 三维同时为正。\n")
	sb.WriteString("2. 实时归因权重：\n")
	sb.WriteString("   - 每个 Market Tick 都会读取最新的实盘归因权重。\n")
	sb.WriteString("   - Macro = Global, Sector = Meso, Symbol = Micro。\n")
	sb.WriteString("   - 最终入场 EV 按 `EV_macro * W_macro + EV_sector * W_sector + EV_symbol * W_symbol` 计算。\n")
	sb.WriteString("   - 只要加权后的 Final Entry EV 高于阈值，就允许开仓，即使某一维度为负也不应被硬性否决。\n")
	sb.WriteString("   - 权重之和必须接近 1.0；若没有有效样本，使用 33.3% / 33.3% / 33.3% 的平权回退。\n")
	sb.WriteString("3. 分箱阅读规则：\n")
	sb.WriteString("   - 查找当前 Recalced LogicScore 对应的 Bin（例如 32 分对应 Bin 30）。\n")
	sb.WriteString("   - 阅读矩阵时同时检查 EV_L / EV_S 与 PF_L / PF_S，但不再要求三维同时为正。\n")
	sb.WriteString("   - 方向由加权后的最终入场 EV 决定；更高的加权期望值获胜。\n")
	sb.WriteString("   - 若两侧加权期望都低于阈值，则视为噪音区，禁止开仓。\n")
	sb.WriteString("   - 若低分段或高分段存在极端峰值，可直接触发对应方向，无需被邻域低分拉低。\n")
	sb.WriteString("4. 统计置信度：\n")
	sb.WriteString("   - 样本量 N > 30 时，严格遵守该矩阵的 EV / PF 指标。\n")
	sb.WriteString("   - 若当前 Symbol 的 N < 10，请忽略 Symbol 矩阵，优先参考 Sector 矩阵。\n")
	sb.WriteString("   - 若 Sector 的 N < 20，请优先参考 Global 矩阵。\n")
	sb.WriteString("   - 记住：背景环境（大盘/赛道）的稳定性在高波动新币上具有更高的决策权重。\n\n")
	if liveWeightsBlock := e.formatLiveAttributionWeights(e.currentLiveAttributionWeights()); liveWeightsBlock != "" {
		sb.WriteString(liveWeightsBlock)
		sb.WriteString("\n\n")
	}
	sb.WriteString("5. 推理表述要求：\n")
	sb.WriteString("   - 禁止使用“因为胜率高”作为开仓理由。\n")
	sb.WriteString("   - 开仓 reasoning 必须引用 Bin 编号、加权后的 Final Entry EV，以及至少一个当前侧的 EV/PF 数据。\n")
	sb.WriteString("   - 你可以简写指标，但必须保留数字证据，例如 `Bin 55: Final EV +0.12% 已越过阈值，Sector 贡献 90% 承担主导，支持开多。`\n\n")
	sb.WriteString("   - 在描述 EV 表现时，请尽量引用当前中心分数的具体数值。虽然允许描述区间，但精确的数字证据能获得更高的执行置信度。\n\n")
	sb.WriteString("   - 对于 `wait` 决策，你可以引用分箱数据证明 EV/PF 不足，也可以直接引用持仓量、资金流或价格动量，说明当前不适合入场。\n\n")
	sb.WriteString("6. Personality DNA 因子权重识别准则：\n")
	sb.WriteString("   - 先读取每个币种的 `## Personality DNA`，逐项检查 Spearman Rank IC，再决定哪些技术面/情绪面证据可以相信。\n")
	sb.WriteString("   - 若 IC > +0.05：该因子属于【强正向逻辑】，其对应 Matrix 与因子信号具有较高参考价值。\n")
	sb.WriteString("   - 若 IC < -0.05：该因子属于【强反向指标】，你必须进行反向推理，例如情绪越热反而越偏空。\n")
	sb.WriteString("   - 若 |IC| < 0.02：该因子属于【随机噪音】，你必须在 reasoning 中明确声明忽略该项技术面/情绪面分析。\n")
	sb.WriteString("   - 严禁对趋势权重 < 10% 或 IC 归零的币种，写出任何教条化的多头/空头趋势叙事；这类币种的趋势描述必须降权或直接忽略。\n")
	sb.WriteString("   - 开仓时，优先使用 IC 显著且与 EV/PF 同向共振的因子；若 DNA 显示某因子为反向或噪音，不得把它当作直接做多/做空理由。\n\n")

	// 7. Output format
	sb.WriteString("# Output Format (Strictly Follow)\n\n")
	sb.WriteString("**Must use XML tags <reasoning> and <decision> to separate chain of thought and decision JSON, avoiding parsing errors**\n\n")
	sb.WriteString("## Format Requirements\n\n")
	sb.WriteString("<reasoning>\n")
	sb.WriteString("Your chain of thought analysis...\n")
	sb.WriteString("- Briefly analyze your thinking process \n")
	sb.WriteString("</reasoning>\n\n")
	sb.WriteString("<decision>\n")
	sb.WriteString("Step 2: JSON decision array\n\n")
	sb.WriteString("```json\n[\n")
	// Use the actual configured position value ratio for BTC/ETH in the example
	examplePositionSize := accountEquity * btcEthPosValueRatio
	sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300, \"reasoning\": \"Bin 80: EV_S +1.3%% > 0 且 PF_S 2.40 具有优势，高分衰竭支持开空。\"},\n",
		riskControl.BTCETHMaxLeverage, examplePositionSize))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\", \"reasoning\": \"Bin 50: EV_L -0.1% <= 0 且 PF_L 1.02 失去优势，先平多降低噪音暴露。\"},\n")
	sb.WriteString("  {\"symbol\": \"SOLUSDT\", \"action\": \"wait\", \"reasoning\": \"Bin 48: EV_L +0.1% > 0 且 PF_L 1.08 仍低于开仓阈值，继续等待。\"}\n")
	sb.WriteString("]\n```\n")
	sb.WriteString("</decision>\n\n")
	sb.WriteString("## Field Description\n\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | hold | wait\n")
	sb.WriteString(fmt.Sprintf("- `confidence`: 0-100 (opening recommended ≥ %d)\n", riskControl.MinConfidence))
	sb.WriteString("- Required when opening: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd\n")
	sb.WriteString("- `reasoning`: required for every decision. Open actions must cite the referenced Bin number plus the current-side EV/PF evidence. For `wait`, you may either cite bin-based EV/PF insufficiency or risk evidence such as OI, flow, and price momentum. You may abbreviate indicators, and range descriptions are allowed, but precise numeric evidence earns higher execution confidence; for example `Bin 55: EV_L +0.5% > 0 且 PF_L 1.52 具有优势，技术面共振支持开多。`.\n")
	sb.WriteString("- **IMPORTANT**: All numeric values must be calculated numbers, NOT formulas/expressions (e.g., use `27.76` not `3000 * 0.01`)\n\n")

	// ⚡ Forced Output Format - Critical constraint for reasoning models
	sb.WriteString("## ⚡ Forced Output Format (CRITICAL - MUST FOLLOW)\n\n")
	sb.WriteString("**MANDATORY ORDER**: You MUST first wrap your complete logical analysis inside `<reasoning>` tags. Then immediately output the JSON decision block inside `<decision>` tags.\n\n")
	sb.WriteString("**FORBIDDEN**: Do NOT write any content outside the `<reasoning>` and `<decision>` tags. The tags themselves must be on their own lines.\n\n")
	sb.WriteString("**CORRECT EXAMPLE**:\n")
	sb.WriteString("```\n<reasoning>\nYour complete analysis here...\nCheck all indicators, consider risk, then decide.\n</reasoning>\n<decision>\n[{\"symbol\": \"BTCUSDT\", \"action\": \"wait\", ...}]\n</decision>\n```\n\n")
	sb.WriteString("**WRONG - WILL CAUSE PARSING FAILURE**:\n")
	sb.WriteString("- Writing analysis before `<reasoning>` tag\n")
	sb.WriteString("- Skipping `<reasoning>` tag entirely\n")
	sb.WriteString("- Closing `</reasoning>` before analysis is complete\n")
	sb.WriteString("- Writing content after `</decision>` tag\n\n")

	// 8. Custom Prompt
	if e.config.CustomPrompt != "" {
		sb.WriteString("# 📌 Personalized Trading Strategy\n\n")
		sb.WriteString(e.config.CustomPrompt)
		sb.WriteString("\n\n")
		sb.WriteString("Note: The above personalized strategy is a supplement to the basic rules and cannot violate the basic risk control principles.\n")
	}

	return sb.String()
}

func (e *StrategyEngine) writeAvailableIndicators(sb *strings.Builder) {
	indicators := e.config.Indicators
	kline := indicators.Klines

	sb.WriteString(fmt.Sprintf("- %s price series", kline.PrimaryTimeframe))
	if kline.EnableMultiTimeframe {
		sb.WriteString(fmt.Sprintf(" + %s K-line series\n", kline.LongerTimeframe))
	} else {
		sb.WriteString("\n")
	}

	if indicators.EnableEMA {
		sb.WriteString("- EMA indicators")
		if len(indicators.EMAPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.EMAPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableMACD {
		sb.WriteString("- MACD indicators\n")
	}

	if indicators.EnableRSI {
		sb.WriteString("- RSI indicators")
		if len(indicators.RSIPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.RSIPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableATR {
		sb.WriteString("- ATR indicators")
		if len(indicators.ATRPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.ATRPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableBOLL {
		sb.WriteString("- Bollinger Bands (BOLL) - Upper/Middle/Lower bands")
		if len(indicators.BOLLPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.BOLLPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableDonchianBox {
		sb.WriteString("- Donchian Box (Trend Filter Factor) - Upper/Lower boundaries and breakout state")
		if len(indicators.DonchianPeriods) > 0 {
			sb.WriteString(fmt.Sprintf(" (periods: %v)", indicators.DonchianPeriods))
		}
		sb.WriteString("\n")
	}

	if indicators.EnableVolume {
		sb.WriteString("- Volume data\n")
	}

	if indicators.EnableOI {
		sb.WriteString("- Open Interest (OI) data\n")
	}

	if indicators.EnableFundingRate {
		sb.WriteString("- Funding rate\n")
	}

	if len(e.config.CoinSource.StaticCoins) > 0 || e.config.CoinSource.UseAI500 || e.config.CoinSource.UseOITop {
		sb.WriteString("- AI500 / OI_Top filter tags (if available)\n")
	}

	if indicators.EnableQuantData {
		sb.WriteString("- Quantitative data (institutional/retail fund flow, position changes, multi-period price changes)\n")
	}
}

// ============================================================================
// Prompt Building - User Prompt
// ============================================================================

// BuildUserPrompt builds User Prompt based on strategy configuration
func (e *StrategyEngine) BuildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// System status
	sb.WriteString(fmt.Sprintf("Time: %s | Period: #%d | Runtime: %d minutes\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC market
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		summary := []string{
			fmt.Sprintf("BTC: %.2f (1h: %+.2f%%, 4h: %+.2f%%)", btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h),
		}
		if e.config.Indicators.EnableMACD {
			summary = append(summary, fmt.Sprintf("MACD: %.4f", btcData.Indicators.MACD))
		}
		if e.config.Indicators.EnableRSI {
			for _, period := range sortedKeysFloatMap(btcData.Indicators.RSIs) {
				summary = append(summary, fmt.Sprintf("RSI%d: %.2f", period, btcData.Indicators.RSIs[period]))
			}
		}
		sb.WriteString(strings.Join(summary, " | "))
		sb.WriteString("\n\n")
	}

	// Account information
	sb.WriteString(fmt.Sprintf("Account: Equity %.2f | Balance %.2f (%.1f%%) | PnL %+.2f%% | Margin %.1f%% | Positions %d\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// Recently completed orders (placed before positions to ensure visibility)
	if len(ctx.RecentOrders) > 0 {
		sb.WriteString("## Recent Completed Trades\n")
		for i, order := range ctx.RecentOrders {
			resultStr := "Profit"
			if order.RealizedPnL < 0 {
				resultStr = "Loss"
			}
			sb.WriteString(fmt.Sprintf("%d. %s %s | Entry %.4f Exit %.4f | %s: %+.2f USDT (%+.2f%%) | %s→%s (%s)\n",
				i+1, order.Symbol, order.Side,
				order.EntryPrice, order.ExitPrice,
				resultStr, order.RealizedPnL, order.PnLPct,
				order.EntryTime, order.ExitTime, order.HoldDuration))
		}
		sb.WriteString("\n")
	}

	// Historical trading statistics (helps AI understand past performance)
	if ctx.TradingStats != nil && ctx.TradingStats.TotalTrades > 0 {
		// Get language from strategy config
		lang := e.GetLanguage()

		// Win/Loss ratio
		var winLossRatio float64
		if ctx.TradingStats.AvgLoss > 0 {
			winLossRatio = ctx.TradingStats.AvgWin / ctx.TradingStats.AvgLoss
		}

		if lang == LangChinese {
			sb.WriteString("## 历史交易统计\n")
			sb.WriteString(fmt.Sprintf("总交易: %d 笔 | 盈利因子: %.2f | 夏普比率: %.2f | 盈亏比: %.2f\n",
				ctx.TradingStats.TotalTrades,
				ctx.TradingStats.ProfitFactor,
				ctx.TradingStats.SharpeRatio,
				winLossRatio))
			sb.WriteString(fmt.Sprintf("总盈亏: %+.2f USDT | 平均盈利: +%.2f | 平均亏损: -%.2f | 最大回撤: %.1f%%\n",
				ctx.TradingStats.TotalPnL,
				ctx.TradingStats.AvgWin,
				ctx.TradingStats.AvgLoss,
				ctx.TradingStats.MaxDrawdownPct))

		} else {
			sb.WriteString("## Historical Trading Statistics\n")
			sb.WriteString(fmt.Sprintf("Total Trades: %d | Profit Factor: %.2f | Sharpe: %.2f | Win/Loss Ratio: %.2f\n",
				ctx.TradingStats.TotalTrades,
				ctx.TradingStats.ProfitFactor,
				ctx.TradingStats.SharpeRatio,
				winLossRatio))
			sb.WriteString(fmt.Sprintf("Total PnL: %+.2f USDT | Avg Win: +%.2f | Avg Loss: -%.2f | Max Drawdown: %.1f%%\n",
				ctx.TradingStats.TotalPnL,
				ctx.TradingStats.AvgWin,
				ctx.TradingStats.AvgLoss,
				ctx.TradingStats.MaxDrawdownPct))
		}
		sb.WriteString("\n")
	}

	// Position information
	if len(ctx.Positions) > 0 {
		sb.WriteString("## Current Positions\n")
		for i, pos := range ctx.Positions {
			sb.WriteString(e.formatPositionInfo(i+1, pos, ctx))
		}
	} else {
		sb.WriteString("Current Positions: None\n\n")
	}

	// Candidate coins (exclude coins already in positions to avoid duplicate data)
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		// Normalize symbol to handle both "ETH" and "ETHUSDT" formats
		normalizedSymbol := market.Normalize(pos.Symbol)
		positionSymbols[normalizedSymbol] = true
	}

	sb.WriteString(fmt.Sprintf("## Candidate Coins (%d coins)\n\n", len(ctx.CandidateCoins)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		// Skip if this coin is already a position (data already shown in positions section)
		normalizedCoinSymbol := market.Normalize(coin.Symbol)
		if positionSymbols[normalizedCoinSymbol] {
			continue
		}

		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sb.WriteString(fmt.Sprintf("%d. %s\n", displayedCount, e.formatCandidateDataIndex(coin, marketData)))
		if personalityBlock := e.formatCandidatePersonalityDNA(ctx.TraderID, marketData); personalityBlock != "" {
			sb.WriteString(personalityBlock)
			sb.WriteString("\n")
		}
		if performanceBlock := e.formatHistoricalPerformanceMatrices(ctx.TraderID, coin, marketData); performanceBlock != "" {
			sb.WriteString(performanceBlock)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Get language for market data formatting
	nofxosLang := nofxos.LangEnglish
	if e.GetLanguage() == LangChinese {
		nofxosLang = nofxos.LangChinese
	}

	// OI Ranking data (market-wide open interest changes)
	if ctx.OIRankingData != nil {
		sb.WriteString(nofxos.FormatOIRankingForAI(ctx.OIRankingData, nofxosLang))
	}

	// NetFlow Ranking data (market-wide fund flow)
	if ctx.NetFlowRankingData != nil {
		sb.WriteString(nofxos.FormatNetFlowRankingForAI(ctx.NetFlowRankingData, nofxosLang))
	}

	// Price Ranking data (market-wide gainers/losers)
	if ctx.PriceRankingData != nil {
		sb.WriteString(nofxos.FormatPriceRankingForAI(ctx.PriceRankingData, nofxosLang))
	}

	sb.WriteString("---\n\n")
	sb.WriteString("Now please analyze and output your decision (Chain of Thought + JSON)\n")

	return sb.String()
}

func (e *StrategyEngine) formatCandidateDataIndex(candidate CandidateCoin, data *market.Data) string {
	if data == nil {
		return ""
	}
	if candidate.LogicScore != nil && !math.IsNaN(*candidate.LogicScore) && !math.IsInf(*candidate.LogicScore, 0) {
		return fmt.Sprintf("[%s | %s | Recalced_Score: %.1f]", candidate.Symbol, compactPromptPrice(data.CurrentPrice), *candidate.LogicScore)
	}
	return fmt.Sprintf("[%s | %s | Recalced_Score: --]", candidate.Symbol, compactPromptPrice(data.CurrentPrice))
}

func compactPromptPrice(price float64) string {
	switch {
	case math.IsNaN(price), math.IsInf(price, 0):
		return "--"
	case math.Abs(price) >= 1000:
		return fmt.Sprintf("%.2f", price)
	case math.Abs(price) >= 1:
		return fmt.Sprintf("%.4f", price)
	default:
		return fmt.Sprintf("%.6f", price)
	}
}

func (e *StrategyEngine) resolveAdaptiveWeightState(traderID string, data *market.Data) market.AdaptiveWeightState {
	if traderID == "" || data == nil || data.Symbol == "" {
		return market.AdaptiveWeightState{}
	}
	if e.adaptiveWeightProvider != nil {
		return e.adaptiveWeightProvider(traderID, data.Sector, data.Symbol)
	}
	return market.GetAdaptiveWeightState(traderID, data.Sector, data.Symbol)
}

func (e *StrategyEngine) formatCandidatePersonalityDNA(traderID string, data *market.Data) string {
	state := e.resolveAdaptiveWeightState(traderID, data)
	dimensions := buildPersonalityDimensions(state)
	if len(dimensions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Personality DNA:\n")
	for _, dimension := range dimensions {
		sb.WriteString(fmt.Sprintf("- %s: Weight %.1f%%, IC %+.2f (%s)\n",
			dimension.Label,
			dimension.Weight*100,
			dimension.IC,
			dimension.Tag,
		))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (e *StrategyEngine) formatLiveAttributionWeights(weights map[string]float64) string {
	normalized := NormalizeLiveAttributionWeights(weights)
	if len(normalized) == 0 {
		return ""
	}

	macro := normalized[RealFireDimensionGlobal]
	sector := normalized[RealFireDimensionSector]
	symbol := normalized[RealFireDimensionSymbol]
	if macro == 0 && sector == 0 && symbol == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Live Attribution Weights\n")
	sb.WriteString(fmt.Sprintf("- Macro / Global: %.1f%%\n", macro*100))
	sb.WriteString(fmt.Sprintf("- Sector / Meso: %.1f%%\n", sector*100))
	sb.WriteString(fmt.Sprintf("- Symbol / Micro: %.1f%%\n", symbol*100))
	sb.WriteString("- Use these real-time contributions as the entry EV weights; they override fixed AND-gate style gating.\n")
	return strings.TrimRight(sb.String(), "\n")
}

func (e *StrategyEngine) currentLiveAttributionWeights() map[string]float64 {
	if e != nil && len(e.liveAttributionWeights) > 0 {
		return e.liveAttributionWeights
	}
	weights, err := GetLiveAttributionWeights()
	if err != nil {
		return nil
	}
	return weights
}

func buildPersonalityDimensions(state market.AdaptiveWeightState) []personalityDimension {
	factors := mapAdaptiveFactorStatesByName(state.Factors)
	momentumWeight, momentumIC, hasMomentum := aggregateAdaptiveFactorSignals(
		[]market.AdaptiveFactorState{
			factors["market"],
			factors["volume_spike"],
		},
	)
	trendWeight, trendIC, hasTrend := aggregateAdaptiveFactorSignals([]market.AdaptiveFactorState{factors["trend"]})
	quantWeight, quantIC, hasQuant := aggregateAdaptiveFactorSignals([]market.AdaptiveFactorState{factors["quant"]})
	sentimentWeight, sentimentIC, hasSentiment := aggregateAdaptiveFactorSignals([]market.AdaptiveFactorState{factors["social"]})
	onChainWeight, onChainIC, hasOnChain := aggregateAdaptiveFactorSignals([]market.AdaptiveFactorState{factors["onchain"]})

	dimensions := make([]personalityDimension, 0, 5)
	if hasMomentum {
		dimensions = append(dimensions, personalityDimension{
			Label:  "Momentum",
			Weight: sanitizeAdaptiveNumber(momentumWeight),
			IC:     sanitizeAdaptiveNumber(momentumIC),
			Tag:    classifyPersonalityDimension("Momentum", momentumWeight, momentumIC),
		})
	}
	if hasTrend {
		dimensions = append(dimensions, personalityDimension{
			Label:  "Trend",
			Weight: sanitizeAdaptiveNumber(trendWeight),
			IC:     sanitizeAdaptiveNumber(trendIC),
			Tag:    classifyPersonalityDimension("Trend", trendWeight, trendIC),
		})
	}
	if hasQuant {
		dimensions = append(dimensions, personalityDimension{
			Label:  "Quant Flow",
			Weight: sanitizeAdaptiveNumber(quantWeight),
			IC:     sanitizeAdaptiveNumber(quantIC),
			Tag:    classifyPersonalityDimension("Quant Flow", quantWeight, quantIC),
		})
	}
	if hasSentiment {
		dimensions = append(dimensions, personalityDimension{
			Label:  "Sentiment",
			Weight: sanitizeAdaptiveNumber(sentimentWeight),
			IC:     sanitizeAdaptiveNumber(sentimentIC),
			Tag:    classifyPersonalityDimension("Sentiment", sentimentWeight, sentimentIC),
		})
	}
	if hasOnChain {
		dimensions = append(dimensions, personalityDimension{
			Label:  "On-chain",
			Weight: sanitizeAdaptiveNumber(onChainWeight),
			IC:     sanitizeAdaptiveNumber(onChainIC),
			Tag:    classifyPersonalityDimension("On-chain", onChainWeight, onChainIC),
		})
	}
	return dimensions
}

func mapAdaptiveFactorStatesByName(factors []market.AdaptiveFactorState) map[string]market.AdaptiveFactorState {
	index := make(map[string]market.AdaptiveFactorState, len(factors))
	for _, factor := range factors {
		index[factor.Name] = factor
	}
	return index
}

func aggregateAdaptiveFactorSignals(factors []market.AdaptiveFactorState) (float64, float64, bool) {
	totalWeight := 0.0
	weightedIC := 0.0
	hasFactor := false
	for _, factor := range factors {
		if factor.Name == "" {
			continue
		}
		weight := sanitizeAdaptiveNumber(factor.FinalWeight)
		ic := sanitizeAdaptiveNumber(factor.FinalIC)
		totalWeight += weight
		weightedIC += weight * ic
		hasFactor = true
	}
	if !hasFactor {
		return 0, 0, false
	}
	if totalWeight <= 0 {
		return 0, 0, true
	}
	return totalWeight, weightedIC / totalWeight, true
}

func sanitizeAdaptiveNumber(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func classifyPersonalityDimension(label string, weight, ic float64) string {
	if label == "Trend" && (weight < 0.10 || math.Abs(ic) < 0.0001) {
		return "Trend Disabled"
	}
	switch {
	case math.Abs(ic) < 0.02:
		return "Noise - IGNORE"
	case ic > 0.05:
		return "Strong Logic"
	case ic < -0.05:
		return "Reverse Signal"
	case ic > 0:
		return "Weak Positive"
	case ic < 0:
		return "Weak Negative"
	default:
		return "Neutral"
	}
}

func (e *StrategyEngine) formatHistoricalPerformanceMatrices(traderID string, candidate CandidateCoin, data *market.Data) string {
	if e.performanceBinProvider == nil || traderID == "" || data == nil || data.Symbol == "" {
		return ""
	}

	matrices, err := e.performanceBinProvider(traderID, data.Sector, data.Symbol)
	if err != nil {
		logger.Warnf("⚠️ Performance bin provider failed: trader=%s sector=%s symbol=%s err=%v",
			traderID, data.Sector, data.Symbol, err)
		return ""
	}
	if matrices == nil {
		return ""
	}

	var sections []string
	if block := formatPerformanceMatrix("Global - 7D", selectRelevantPerformanceBins(matrices.Global, candidate)); block != "" {
		sections = append(sections, block)
	}
	if data.Sector != "" {
		if block := formatPerformanceMatrix(fmt.Sprintf("Sector: %s - 7D", data.Sector), selectRelevantPerformanceBins(matrices.Sector, candidate)); block != "" {
			sections = append(sections, block)
		}
	}
	if totalPerformanceBinSamples(matrices.Symbol) >= 10 {
		if block := formatPerformanceMatrix(fmt.Sprintf("Symbol: %s - %d Samples", data.Symbol, store.PERFORMANCE_SYMBOL_LIMIT), selectRelevantPerformanceBins(matrices.Symbol, candidate)); block != "" {
			sections = append(sections, block)
		}
	}

	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n") + "\n"
}

func formatPerformanceMatrix(title string, bins []*store.ScoreBinPerformance) string {
	if len(bins) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Directional Bin Matrix (%s) ===\n", title))
	sb.WriteString("Bin  N    EV_L%  PF_L  EV_S%  PF_S\n")
	written := 0
	for _, bin := range bins {
		if bin == nil || bin.TradeCount <= 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("%-4d %-4d %+5.1f %-5.2f %+5.1f %.2f\n",
			bin.BinStart,
			bin.TradeCount,
			bin.ExpectedValueLong*100,
			bin.ProfitFactorLong,
			bin.ExpectedValueShort*100,
			bin.ProfitFactorShort,
		))
		written++
	}
	if written == 0 {
		return ""
	}
	return strings.TrimRight(sb.String(), "\n")
}

func totalPerformanceBinSamples(bins []*store.ScoreBinPerformance) int {
	maxTradeCount := 0
	for _, bin := range bins {
		if bin == nil {
			continue
		}
		if bin.TradeCount > maxTradeCount {
			maxTradeCount = bin.TradeCount
		}
	}
	return maxTradeCount
}

func selectRelevantPerformanceBins(
	bins []*store.ScoreBinPerformance,
	candidate CandidateCoin,
) []*store.ScoreBinPerformance {
	if len(bins) == 0 {
		return nil
	}

	if len(bins) <= 15 {
		return bins
	}

	focusScore, ok := resolvePerformanceFocusScore(candidate)
	if !ok {
		return samplePerformanceBinsEvenly(bins, 11)
	}

	lowerBound := int(math.Floor(focusScore)) - 5
	upperBound := int(math.Ceil(focusScore)) + 5
	focused := make([]*store.ScoreBinPerformance, 0, 11)
	for _, bin := range bins {
		if bin == nil {
			continue
		}
		if bin.BinStart >= lowerBound && bin.BinStart <= upperBound {
			focused = append(focused, bin)
		}
	}
	if len(focused) > 0 {
		return focused
	}

	return nearestPerformanceBins(bins, focusScore, 11)
}

func resolvePerformanceFocusScore(candidate CandidateCoin) (float64, bool) {
	if candidate.LogicScore == nil {
		return 0, false
	}
	score := *candidate.LogicScore
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0, false
	}
	return score, true
}

func samplePerformanceBinsEvenly(bins []*store.ScoreBinPerformance, limit int) []*store.ScoreBinPerformance {
	if len(bins) == 0 || limit <= 0 || len(bins) <= limit {
		return bins
	}

	step := float64(len(bins)-1) / float64(limit-1)
	selected := make([]*store.ScoreBinPerformance, 0, limit)
	seen := make(map[int]struct{}, limit)
	for i := 0; i < limit; i++ {
		index := int(math.Round(float64(i) * step))
		if index < 0 {
			index = 0
		}
		if index >= len(bins) {
			index = len(bins) - 1
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		selected = append(selected, bins[index])
	}
	return selected
}

func nearestPerformanceBins(
	bins []*store.ScoreBinPerformance,
	focusScore float64,
	limit int,
) []*store.ScoreBinPerformance {
	if len(bins) == 0 || limit <= 0 || len(bins) <= limit {
		return bins
	}

	type candidate struct {
		bin      *store.ScoreBinPerformance
		distance float64
		index    int
	}

	candidates := make([]candidate, 0, len(bins))
	for index, bin := range bins {
		if bin == nil {
			continue
		}
		candidates = append(candidates, candidate{
			bin:      bin,
			distance: math.Abs(float64(bin.BinStart) - focusScore),
			index:    index,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distance == candidates[j].distance {
			return candidates[i].index < candidates[j].index
		}
		return candidates[i].distance < candidates[j].distance
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].bin.BinStart < candidates[j].bin.BinStart
	})

	selected := make([]*store.ScoreBinPerformance, 0, len(candidates))
	for _, candidate := range candidates {
		selected = append(selected, candidate.bin)
	}
	return selected
}

func (e *StrategyEngine) formatPositionInfo(index int, pos PositionInfo, ctx *Context) string {
	var sb strings.Builder

	holdingDuration := ""
	if pos.UpdateTime > 0 {
		durationMs := time.Now().UnixMilli() - pos.UpdateTime
		durationMin := durationMs / (1000 * 60)
		if durationMin < 60 {
			holdingDuration = fmt.Sprintf(" | Holding Duration %d min", durationMin)
		} else {
			durationHour := durationMin / 60
			durationMinRemainder := durationMin % 60
			holdingDuration = fmt.Sprintf(" | Holding Duration %dh %dm", durationHour, durationMinRemainder)
		}
	}

	positionValue := pos.Quantity * pos.MarkPrice
	if positionValue < 0 {
		positionValue = -positionValue
	}

	sb.WriteString(fmt.Sprintf("%d. %s %s | Entry %.4f Current %.4f | Qty %.4f | Position Value %.2f USDT | PnL%+.2f%% | PnL Amount%+.2f USDT | Peak PnL%.2f%% | Leverage %dx | Margin %.0f | Liq Price %.4f%s\n\n",
		index, pos.Symbol, strings.ToUpper(pos.Side),
		pos.EntryPrice, pos.MarkPrice, pos.Quantity, positionValue, pos.UnrealizedPnLPct, pos.UnrealizedPnL, pos.PeakPnLPct,
		pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

	if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
		sb.WriteString(e.formatMarketData(marketData))

		if ctx.QuantDataMap != nil {
			if quantData, hasQuant := ctx.QuantDataMap[pos.Symbol]; hasQuant {
				sb.WriteString(e.formatQuantData(quantData))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func (e *StrategyEngine) formatCoinSourceTag(sources []string) string {
	if len(sources) > 1 {
		// Multiple signal source combination
		hasAI500 := false
		hasOITop := false
		hasOILow := false
		hasHyperAll := false
		hasHyperMain := false
		for _, s := range sources {
			switch s {
			case "ai500":
				hasAI500 = true
			case "oi_top":
				hasOITop = true
			case "oi_low":
				hasOILow = true
			case "hyper_all":
				hasHyperAll = true
			case "hyper_main":
				hasHyperMain = true
			}
		}
		if hasAI500 && hasOITop {
			return " (AI500+OI_Top dual signal)"
		}
		if hasAI500 && hasOILow {
			return " (AI500+OI_Low dual signal)"
		}
		if hasOITop && hasOILow {
			return " (OI_Top+OI_Low)"
		}
		if hasHyperMain && hasAI500 {
			return " (HyperMain+AI500)"
		}
		if hasHyperAll || hasHyperMain {
			return " (Hyperliquid)"
		}
		return " (Multiple sources)"
	} else if len(sources) == 1 {
		switch sources[0] {
		case "ai500":
			return " (AI500)"
		case "oi_top":
			return " (OI_Top OI increase)"
		case "oi_low":
			return " (OI_Low OI decrease)"
		case "static":
			return " (Manual selection)"
		case "hyper_all":
			return " (Hyperliquid All)"
		case "hyper_main":
			return " (Hyperliquid Top20)"
		}
	}
	return ""
}

// ============================================================================
// Market Data Formatting
// ============================================================================

func (e *StrategyEngine) formatMarketData(data *market.Data) string {
	var sb strings.Builder
	indicators := e.config.Indicators

	if technicalContext := e.formatTechnicalContext(data); technicalContext != "" {
		sb.WriteString(technicalContext)
		sb.WriteString("\n")
	}

	// Clearly label the coin symbol
	sb.WriteString(fmt.Sprintf("=== %s Market Data ===\n\n", data.Symbol))
	sb.WriteString(fmt.Sprintf("current_price = %.4f", data.CurrentPrice))
	for _, line := range formatIndicatorSummary(indicators, data.CurrentPrice, data.Indicators) {
		sb.WriteString(", ")
		sb.WriteString(line)
	}
	sb.WriteString("\n\n")

	if indicators.EnableOI || indicators.EnableFundingRate || data.Orderbook != nil || data.VolatilityUtilization > 0 || data.HeatScore != nil {
		sb.WriteString(fmt.Sprintf("Additional data for %s:\n\n", data.Symbol))

		if indicators.EnableOI && data.OpenInterest != nil {
			sb.WriteString(fmt.Sprintf("Open Interest: Latest: %.2f Average: %.2f\n\n",
				data.OpenInterest.Latest, data.OpenInterest.Average))
		}

		if data.Orderbook != nil && (data.Orderbook.BidTotal > 0 || data.Orderbook.AskTotal > 0) {
			direction := "Buy depth is roughly balanced with sell depth"
			switch {
			case data.Orderbook.Imbalance > 0:
				direction = "Buy depth exceeds sell depth"
			case data.Orderbook.Imbalance < 0:
				direction = "Sell depth exceeds buy depth"
			}
			sb.WriteString(fmt.Sprintf("Orderbook Imbalance (Top 100 levels): %+.1f%% (%s)\n\n",
				data.Orderbook.Imbalance*100, direction))
		}

		if data.DexScreener != nil && data.DexScreener.VolumeH1 > 0 {
			sb.WriteString(fmt.Sprintf("DEX-CEX Heat Ratio (1h): %.2f (DEX volume %.2f vs CEX volume %.2f), DEX buy ratio: %.1f%%\n\n",
				data.DexScreener.OnchainToCEXRatio,
				data.DexScreener.VolumeH1,
				data.DexScreener.CEXVolumeH1,
				data.DexScreener.BuyRatio*100))
		}

		if data.GeckoSentiment != nil && (data.GeckoSentiment.PublicInterestScore > 0 || data.GeckoSentiment.SentimentVotesUpPercentage > 0) {
			sb.WriteString(fmt.Sprintf("CoinGecko Public Sentiment: interest %.2f | up-vote %.1f%%\n\n",
				data.GeckoSentiment.PublicInterestScore,
				data.GeckoSentiment.SentimentVotesUpPercentage))
		}

		if data.VolatilityUtilization > 0 {
			sb.WriteString(fmt.Sprintf("Volatility Utilization (14-period): %.2f (%s)\n\n",
				data.VolatilityUtilization, describeVolatilityUtilization(data.VolatilityUtilization)))
		}

		if data.HeatScore != nil {
			sb.WriteString(fmt.Sprintf("Adaptive Heat Score (24h rolling window): %.1f / 100 (Trading: %.1f, Quant: %.1f)\n\n",
				data.HeatScore.CompositeScore, data.HeatScore.TradingScore, data.HeatScore.QuantScore))
		}

		if indicators.EnableFundingRate {
			sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))
		}
	}

	if len(data.TimeframeData) > 0 {
		timeframeOrder := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w"}
		for _, tf := range timeframeOrder {
			if tfData, ok := data.TimeframeData[tf]; ok {
				sb.WriteString(fmt.Sprintf("=== %s Timeframe (oldest → latest) ===\n\n", strings.ToUpper(tf)))
				e.formatTimeframeSeriesData(&sb, tfData, indicators)
			}
		}
	}

	return sb.String()
}

func (e *StrategyEngine) formatTechnicalContext(data *market.Data) string {
	if data == nil {
		return ""
	}
	if !hasPromptTechnicalIndicators(e.config.Indicators) {
		return ""
	}

	var sb strings.Builder
	primaryTimeframe := data.PrimaryTimeframe
	if primaryTimeframe == "" {
		primaryTimeframe = e.config.Indicators.Klines.PrimaryTimeframe
	}
	if primaryTimeframe == "" {
		primaryTimeframe = "Primary"
	}

	primaryData := data.TimeframeData[primaryTimeframe]
	sb.WriteString(fmt.Sprintf("=== Technical Summary (%s - Primary) ===\n", strings.ToUpper(primaryTimeframe)))
	sb.WriteString(e.formatTechnicalSnapshot(primaryTimeframe, primaryData, data.CurrentPrice, true))
	sb.WriteString("\n")

	for _, tf := range sortedNonPrimaryTimeframes(data.TimeframeData, primaryTimeframe) {
		sb.WriteString(fmt.Sprintf("=== Trend Context (%s) ===\n", strings.ToUpper(tf)))
		sb.WriteString(e.formatTechnicalSnapshot(tf, data.TimeframeData[tf], 0, false))
		sb.WriteString("\n")
	}

	return sb.String()
}

func hasPromptTechnicalIndicators(indicators store.IndicatorConfig) bool {
	return indicators.EnableEMA ||
		indicators.EnableMACD ||
		indicators.EnableRSI ||
		indicators.EnableATR ||
		indicators.EnableBOLL ||
		indicators.EnableDonchianBox
}

func (e *StrategyEngine) formatTechnicalSnapshot(timeframe string, tfData *market.TimeframeSeriesData, currentPrice float64, isPrimary bool) string {
	if tfData == nil {
		return "Data unavailable\n"
	}

	price := currentPrice
	if price <= 0 {
		price = tfData.LatestClose()
	}

	parts := make([]string, 0, 6)
	if price > 0 {
		parts = append(parts, fmt.Sprintf("Price: %.4f", price))
	}

	if e.config.Indicators.EnableRSI && len(e.config.Indicators.RSIPeriods) > 0 {
		period := e.config.Indicators.RSIPeriods[0]
		if rsi := tfData.LatestRSI(period); rsi > 0 {
			parts = append(parts, fmt.Sprintf("RSI%d: %.1f", period, rsi))
		}
	}

	if e.config.Indicators.EnableMACD {
		parts = append(parts, fmt.Sprintf("MACD: %s", describeMACDContext(tfData.LatestMACD())))
	}

	if e.config.Indicators.EnableEMA {
		parts = append(parts, fmt.Sprintf("EMA Stack: %s", describeEMAStackContext(tfData, e.config.Indicators.EMAPeriods)))
	}

	if e.config.Indicators.EnableDonchianBox && len(e.config.Indicators.DonchianPeriods) > 0 {
		period := e.config.Indicators.DonchianPeriods[0]
		box := tfData.LatestDonchian(period)
		if box.Upper > 0 || box.Lower > 0 {
			parts = append(parts, fmt.Sprintf("Donchian P%d: %s", period, describeDonchianState(price, box)))
		}
	}

	if isPrimary && e.config.Indicators.EnableATR && len(e.config.Indicators.ATRPeriods) > 0 {
		atrPeriod := e.config.Indicators.ATRPeriods[0]
		if atr := tfData.LatestATR(atrPeriod); atr > 0 && dataSupportsVolUtil(timeframe, e.config.Indicators.Klines.PrimaryTimeframe) {
			parts = append(parts, fmt.Sprintf("VolUtil: %.0f%%", estimateVolUtilPercent(tfData, atr)))
		}
	}

	if len(parts) == 0 {
		return "No indicators enabled\n"
	}

	return strings.Join(parts, " | ") + "\n"
}

func (e *StrategyEngine) formatTimeframeSeriesData(sb *strings.Builder, data *market.TimeframeSeriesData, indicators store.IndicatorConfig) {
	if len(data.Klines) > 0 {
		sb.WriteString("Time(UTC)      Open      High      Low       Close     Volume\n")
		for i, k := range data.Klines {
			t := time.Unix(k.Time/1000, 0).UTC()
			timeStr := t.Format("01-02 15:04")
			marker := ""
			if i == len(data.Klines)-1 {
				marker = "  <- current"
			}
			sb.WriteString(fmt.Sprintf("%-14s %-9.4f %-9.4f %-9.4f %-9.4f %-12.2f%s\n",
				timeStr, k.Open, k.High, k.Low, k.Close, k.Volume, marker))
		}
		sb.WriteString("\n")
	} else if len(data.MidPrices) > 0 {
		sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.MidPrices)))
		if indicators.EnableVolume && len(data.Volume) > 0 {
			sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.Volume)))
		}
	}

	if indicators.EnableEMA {
		for _, period := range sortedKeysSeriesMap(data.Indicators.EMAs) {
			values := data.Indicators.EMAs[period]
			if len(values) > 0 {
				sb.WriteString(fmt.Sprintf("EMA%d: %s\n", period, formatFloatSlice(values)))
			}
		}
	}

	if indicators.EnableMACD && len(data.Indicators.MACD) > 0 {
		sb.WriteString(fmt.Sprintf("MACD: %s\n", formatFloatSlice(data.Indicators.MACD)))
	}

	if indicators.EnableRSI {
		for _, period := range sortedKeysSeriesMap(data.Indicators.RSIs) {
			values := data.Indicators.RSIs[period]
			if len(values) > 0 {
				sb.WriteString(fmt.Sprintf("RSI%d: %s\n", period, formatFloatSlice(values)))
			}
		}
	}

	if indicators.EnableATR {
		for _, period := range sortedKeysFloatMap(data.Indicators.ATRs) {
			value := data.Indicators.ATRs[period]
			if value > 0 {
				sb.WriteString(fmt.Sprintf("ATR%d: %.4f\n", period, value))
			}
		}
	}

	if indicators.EnableBOLL {
		for _, period := range sortedKeysBollSeriesMap(data.Indicators.Bolls) {
			series := data.Indicators.Bolls[period]
			if len(series.Upper) > 0 {
				sb.WriteString(fmt.Sprintf("BOLL%d Upper: %s\n", period, formatFloatSlice(series.Upper)))
				sb.WriteString(fmt.Sprintf("BOLL%d Middle: %s\n", period, formatFloatSlice(series.Middle)))
				sb.WriteString(fmt.Sprintf("BOLL%d Lower: %s\n", period, formatFloatSlice(series.Lower)))
			}
		}
	}

	if indicators.EnableDonchianBox {
		for _, period := range sortedKeysDonchianSeriesMap(data.Indicators.Donchians) {
			series := data.Indicators.Donchians[period]
			if len(series.Upper) > 0 {
				sb.WriteString(fmt.Sprintf("Donchian%d_Upper: %s\n", period, formatFloatSlice(series.Upper)))
				sb.WriteString(fmt.Sprintf("Donchian%d_Lower: %s\n", period, formatFloatSlice(series.Lower)))
				sb.WriteString(fmt.Sprintf("Donchian%d_Mid: %s\n", period, formatFloatSlice(series.Mid)))
			}
		}
	}

	sb.WriteString("\n")
}

func formatIndicatorSummary(indicators store.IndicatorConfig, currentPrice float64, result market.IndicatorResult) []string {
	lines := make([]string, 0)
	if indicators.EnableEMA {
		for _, period := range sortedKeysFloatMap(result.EMAs) {
			lines = append(lines, fmt.Sprintf("current_ema%d = %.3f", period, result.EMAs[period]))
		}
	}
	if indicators.EnableMACD {
		lines = append(lines, fmt.Sprintf("current_macd = %.3f", result.MACD))
	}
	if indicators.EnableRSI {
		for _, period := range sortedKeysFloatMap(result.RSIs) {
			lines = append(lines, fmt.Sprintf("current_rsi%d = %.3f", period, result.RSIs[period]))
		}
	}
	if indicators.EnableATR {
		for _, period := range sortedKeysFloatMap(result.ATRs) {
			lines = append(lines, fmt.Sprintf("current_atr%d = %.3f", period, result.ATRs[period]))
		}
	}
	if indicators.EnableDonchianBox {
		for _, period := range sortedKeysDonchianResultMap(result.Donchians) {
			box := result.Donchians[period]
			lines = append(lines, fmt.Sprintf("Donchian%d_Upper = %.3f", period, box.Upper))
			lines = append(lines, fmt.Sprintf("Donchian%d_Lower = %.3f", period, box.Lower))
			lines = append(lines, fmt.Sprintf("Donchian%d_Mid = %.3f", period, box.Mid))
			lines = append(lines, fmt.Sprintf("Donchian%d_State = %s", period, describeDonchianState(currentPrice, box)))
		}
	}
	return lines
}

func describeVolatilityUtilization(vu float64) string {
	switch {
	case vu > 0.8:
		return "High efficiency movement / Strong trend"
	case vu >= 0.4:
		return "Normal volatility usage / Structured move"
	default:
		return "Low efficiency movement / Frictional move"
	}
}

func describeMACDContext(macd float64) string {
	switch {
	case macd > 0:
		return "Bullish"
	case macd < 0:
		return "Bearish"
	default:
		return "Neutral"
	}
}

func describeEMAStackContext(tfData *market.TimeframeSeriesData, periods []int) string {
	values := make(map[int]float64)
	for _, period := range periods {
		value := tfData.LatestEMA(period)
		if value > 0 {
			values[period] = value
		}
	}

	switch buildEMASignalState(values) {
	case "bullish_stack":
		return "Bullish"
	case "bearish_stack":
		return "Bearish"
	case "single_ema":
		return "Single EMA"
	default:
		return "Mixed"
	}
}

func buildEMASignalState(values map[int]float64) string {
	periods := sortedKeysFloatMap(values)
	if len(periods) == 0 {
		return "unavailable"
	}
	if len(periods) < 2 {
		return "single_ema"
	}

	bullish := true
	bearish := true
	for i := 1; i < len(periods); i++ {
		prev := values[periods[i-1]]
		curr := values[periods[i]]
		if prev <= curr {
			bullish = false
		}
		if prev >= curr {
			bearish = false
		}
	}

	switch {
	case bullish:
		return "bullish_stack"
	case bearish:
		return "bearish_stack"
	default:
		return "mixed"
	}
}

func estimateVolUtilPercent(tfData *market.TimeframeSeriesData, atr float64) float64 {
	if tfData == nil || len(tfData.Klines) <= market.VolUtilLookback || atr <= 0 {
		return 0
	}

	latest := tfData.Klines[len(tfData.Klines)-1].Close
	base := tfData.Klines[len(tfData.Klines)-1-market.VolUtilLookback].Close
	delta := latest - base
	if delta < 0 {
		delta = -delta
	}

	return (delta / (atr * market.VolUtilMultiplier)) * 100
}

func dataSupportsVolUtil(timeframe, primaryTimeframe string) bool {
	if primaryTimeframe == "" {
		return true
	}
	return timeframe == primaryTimeframe
}

func sortedNonPrimaryTimeframes(timeframeData map[string]*market.TimeframeSeriesData, primary string) []string {
	keys := make([]string, 0, len(timeframeData))
	for key := range timeframeData {
		if key == primary {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return timeframeSortOrder(keys[i]) < timeframeSortOrder(keys[j])
	})
	return keys
}

func timeframeSortOrder(tf string) int {
	order := map[string]int{
		"1m":  1,
		"3m":  2,
		"5m":  3,
		"15m": 4,
		"30m": 5,
		"1h":  6,
		"2h":  7,
		"4h":  8,
		"6h":  9,
		"8h":  10,
		"12h": 11,
		"1d":  12,
		"3d":  13,
		"1w":  14,
	}
	if rank, ok := order[tf]; ok {
		return rank
	}
	return 999
}

func sortedKeysFloatMap[T ~float64](m map[int]T) []int {
	keys := make([]int, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func sortedKeysSeriesMap(m map[int][]float64) []int {
	keys := make([]int, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func sortedKeysBollSeriesMap(m map[int]market.BollSeries) []int {
	keys := make([]int, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func sortedKeysDonchianSeriesMap(m map[int]market.DonchianSeries) []int {
	keys := make([]int, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func sortedKeysDonchianResultMap(m map[int]market.DonchianResult) []int {
	keys := make([]int, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func describeDonchianState(currentPrice float64, box market.DonchianResult) string {
	width := box.Upper - box.Lower
	if width <= 0 {
		return "Price is inside the Donchian Box"
	}

	switch {
	case currentPrice > box.Upper:
		return "Price is currently above the upper bound"
	case currentPrice < box.Lower:
		return "Price is currently below the lower bound"
	case currentPrice >= box.Upper-width*0.02:
		return "Price is within 2% range of the upper bound"
	case currentPrice <= box.Lower+width*0.02:
		return "Price is within 2% range of the lower bound"
	default:
		return "Price is currently between the upper and lower bounds"
	}
}

func (e *StrategyEngine) formatQuantData(data *QuantData) string {
	if data == nil {
		return ""
	}

	indicators := e.config.Indicators
	if !indicators.EnableQuantOI && !indicators.EnableQuantNetflow {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 %s Quantitative Data:\n", data.Symbol))

	if len(data.PriceChange) > 0 {
		sb.WriteString("Price Change: ")
		timeframes := []string{"5m", "15m", "1h", "4h", "12h", "24h"}
		parts := []string{}
		for _, tf := range timeframes {
			if v, ok := data.PriceChange[tf]; ok {
				parts = append(parts, fmt.Sprintf("%s: %+.4f%%", tf, v*100))
			}
		}
		sb.WriteString(strings.Join(parts, " | "))
		sb.WriteString("\n")
	}

	if indicators.EnableQuantNetflow && data.Netflow != nil {
		sb.WriteString("Fund Flow (Netflow):\n")
		timeframes := []string{"5m", "15m", "1h", "4h", "12h", "24h"}

		if data.Netflow.Institution != nil {
			if data.Netflow.Institution.Future != nil && len(data.Netflow.Institution.Future) > 0 {
				sb.WriteString("  Large-Order Flow Futures:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Institution.Future[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
			if data.Netflow.Institution.Spot != nil && len(data.Netflow.Institution.Spot) > 0 {
				sb.WriteString("  Large-Order Flow Spot:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Institution.Spot[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
		}

		if data.Netflow.Personal != nil {
			if data.Netflow.Personal.Future != nil && len(data.Netflow.Personal.Future) > 0 {
				sb.WriteString("  Small-Order Flow Futures:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Personal.Future[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
			if data.Netflow.Personal.Spot != nil && len(data.Netflow.Personal.Spot) > 0 {
				sb.WriteString("  Small-Order Flow Spot:\n")
				for _, tf := range timeframes {
					if v, ok := data.Netflow.Personal.Spot[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %s\n", tf, formatFlowValue(v)))
					}
				}
			}
		}
	}

	if indicators.EnableQuantOI && len(data.OI) > 0 {
		for exchange, oiData := range data.OI {
			if len(oiData.Delta) > 0 {
				sb.WriteString(fmt.Sprintf("Open Interest (%s):\n", exchange))
				for _, tf := range []string{"5m", "15m", "1h", "4h", "12h", "24h"} {
					if d, ok := oiData.Delta[tf]; ok {
						sb.WriteString(fmt.Sprintf("    %s: %+.4f%% (%s)\n", tf, d.OIDeltaPercent, formatFlowValue(d.OIDeltaValue)))
					}
				}
			}
		}
	}

	return sb.String()
}

func formatFlowValue(v float64) string {
	sign := ""
	if v >= 0 {
		sign = "+"
	}
	absV := v
	if absV < 0 {
		absV = -absV
	}
	if absV >= 1e9 {
		return fmt.Sprintf("%s%.2fB", sign, v/1e9)
	} else if absV >= 1e6 {
		return fmt.Sprintf("%s%.2fM", sign, v/1e6)
	} else if absV >= 1e3 {
		return fmt.Sprintf("%s%.2fK", sign, v/1e3)
	}
	return fmt.Sprintf("%s%.2f", sign, v)
}

func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = fmt.Sprintf("%.4f", v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}
