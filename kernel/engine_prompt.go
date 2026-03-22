package kernel

import (
	"fmt"
	"nofx/market"
	"nofx/provider/nofxos"
	"nofx/store"
	"sort"
	"strings"
	"time"
)

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
	sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300},\n",
		riskControl.BTCETHMaxLeverage, examplePositionSize))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\"}\n")
	sb.WriteString("]\n```\n")
	sb.WriteString("</decision>\n\n")
	sb.WriteString("## Field Description\n\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | hold | wait\n")
	sb.WriteString(fmt.Sprintf("- `confidence`: 0-100 (opening recommended ≥ %d)\n", riskControl.MinConfidence))
	sb.WriteString("- Required when opening: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd\n")
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

	sb.WriteString(fmt.Sprintf("## Candidate Coins (%d coins)\n\n", len(ctx.MarketDataMap)))
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

		sourceTags := e.formatCoinSourceTag(coin.Sources)
		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
		sb.WriteString(e.formatMarketData(marketData))

		if ctx.QuantDataMap != nil {
			if quantData, hasQuant := ctx.QuantDataMap[coin.Symbol]; hasQuant {
				sb.WriteString(e.formatQuantData(quantData))
			}
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

	sb.WriteString(e.formatTechnicalContext(data))
	sb.WriteString("\n")

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
