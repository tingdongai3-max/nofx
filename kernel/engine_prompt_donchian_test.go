package kernel

import (
	"strings"
	"testing"

	"nofx/market"
	"nofx/store"
)

func TestFormatIndicatorSummaryIncludesDonchianFields(t *testing.T) {
	indicators := store.IndicatorConfig{
		EnableDonchianBox: true,
		DonchianPeriods:   []int{72, 500},
	}

	result := market.IndicatorResult{
		Donchians: map[int]market.DonchianResult{
			72: {
				Upper: 110,
				Lower: 90,
				Mid:   100,
			},
			500: {
				Upper: 140,
				Lower: 80,
				Mid:   110,
			},
		},
	}

	lines := formatIndicatorSummary(indicators, 111, result)
	text := strings.Join(lines, "\n")

	mustContain := []string{
		"Donchian72_Upper = 110.000",
		"Donchian72_Lower = 90.000",
		"Donchian500_Lower = 80.000",
		"Donchian72_State = Price is currently above the upper bound",
	}

	for _, expected := range mustContain {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected prompt summary to contain %q, got:\n%s", expected, text)
		}
	}
}

func TestDescribeDonchianState(t *testing.T) {
	box := market.DonchianResult{Upper: 110, Lower: 90, Mid: 100}

	cases := []struct {
		name         string
		currentPrice float64
		expected     string
	}{
		{name: "above upper", currentPrice: 111, expected: "Price is currently above the upper bound"},
		{name: "near upper", currentPrice: 109.7, expected: "Price is within 2% range of the upper bound"},
		{name: "near lower", currentPrice: 90.3, expected: "Price is within 2% range of the lower bound"},
		{name: "inside box", currentPrice: 100, expected: "Price is currently between the upper and lower bounds"},
		{name: "below lower", currentPrice: 89, expected: "Price is currently below the lower bound"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeDonchianState(tc.currentPrice, box)
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestFormatMarketDataOmitsTechnicalSummaryWhenIndicatorsDisabled(t *testing.T) {
	config := store.GetDefaultStrategyConfig("en")
	config.Indicators.EnableEMA = false
	config.Indicators.EnableMACD = false
	config.Indicators.EnableRSI = false
	config.Indicators.EnableATR = false
	config.Indicators.EnableBOLL = false
	config.Indicators.EnableDonchianBox = false

	engine := NewStrategyEngine(&config)
	data := &market.Data{
		Symbol:       "TESTUSDT",
		CurrentPrice: 111,
		Indicators: market.IndicatorResult{
			EMAs: map[int]float64{20: 105, 50: 101},
			RSIs: map[int]float64{14: 63.5},
			ATRs: map[int]float64{14: 2.4},
			Donchians: map[int]market.DonchianResult{
				72: {Upper: 110, Lower: 90, Mid: 100},
			},
			MACD: 1.25,
		},
		TimeframeData: map[string]*market.TimeframeSeriesData{
			"5m": {
				Timeframe: "5m",
				Klines: []market.KlineBar{
					{Time: 1, Open: 100, High: 112, Low: 98, Close: 111, Volume: 1000},
				},
				Indicators: market.IndicatorSeries{
					EMAs: map[int][]float64{20: []float64{105}},
					RSIs: map[int][]float64{14: []float64{63.5}},
					ATRs: map[int]float64{14: 2.4},
					Donchians: map[int]market.DonchianSeries{
						72: {Upper: []float64{110}, Lower: []float64{90}, Mid: []float64{100}},
					},
					MACD: []float64{1.25},
				},
			},
		},
		PrimaryTimeframe: "5m",
	}

	text := engine.formatMarketData(data)
	mustNotContain := []string{
		"=== Technical Summary",
		"=== Trend Context",
		"Donchian72_Upper",
		"RSI14:",
		"MACD:",
	}

	for _, forbidden := range mustNotContain {
		if strings.Contains(text, forbidden) {
			t.Fatalf("expected prompt to omit %q when indicators are disabled, got:\n%s", forbidden, text)
		}
	}
}

func TestBuildSystemPromptIncludesScorePerformanceLogicAnchor(t *testing.T) {
	config := store.GetDefaultStrategyConfig("zh")
	engine := NewStrategyEngine(&config)

	text := engine.BuildSystemPrompt(1000, "balanced")
	mustContain := []string{
		"## 核心决策逻辑：数据驱动方向 (Data-Driven Directional Bias)",
		"重算后的 LogicScore 不再硬性代表做多或做空",
		"查找当前 Recalced LogicScore 对应的 Bin",
		"EV 曲线来自滑动窗口平滑趋势",
		"EV_L / EV_S（平均对数回报率）与 PF_L / PF_S（盈利因子）",
		"EV_L / EV_S > 0 表示该方向在统计上是增长的",
		"仅在 EV > 0 且 PF > 1.2 的分段执行对应方向的开仓",
		"若两边 EV 均 <= 0，或两边 PF 均 <= 1.2",
		"低分段（Bin < 40）的 PF_L 显著占优",
		"高分段（Bin > 70）的 PF_S 显著占优",
		"样本量 N > 30 时",
		"若当前 Symbol 的 N < 10",
		"若 Sector 的 N < 20",
		"禁止使用“因为胜率高”作为开仓理由",
		"Reasoning 必须引用 Bin 编号及当前侧的 EV/PF 数据",
		"你可以简写指标，但必须保留数字证据",
		"虽然允许描述区间，但精确的数字证据能获得更高的执行置信度",
		"Bin 55: EV_L +0.5% > 0 且 PF_L 1.52 具有优势，技术面共振支持开多。",
		"`reasoning`: required for every decision",
		"Must cite the referenced Bin number plus the current-side EV/PF evidence",
		"Bin 48: EV_L +0.1% > 0 且 PF_L 1.08 仍低于开仓阈值，继续等待。",
	}

	for _, expected := range mustContain {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected system prompt to contain %q, got:\n%s", expected, text)
		}
	}
}

func TestBuildUserPromptInjectsDensePerformanceMatrices(t *testing.T) {
	config := store.GetDefaultStrategyConfig("en")
	config.Indicators.EnableEMA = false
	config.Indicators.EnableMACD = false
	config.Indicators.EnableRSI = false
	config.Indicators.EnableATR = false
	config.Indicators.EnableBOLL = false
	config.Indicators.EnableDonchianBox = false

	engine := NewStrategyEngine(&config)
	engine.SetPerformanceBinProvider(func(traderID, sector, symbol string) (*PerformanceBinMatrices, error) {
		return &PerformanceBinMatrices{
			Global: []*store.ScoreBinPerformance{
				{BinStart: 30, TradeCount: 27, ExpectedValueLong: -0.008, ProfitFactorLong: 0.24, ExpectedValueShort: 0.017, ProfitFactorShort: 4.20},
				{BinStart: 70, TradeCount: 50, ExpectedValueLong: 0.012, ProfitFactorLong: 2.15, ExpectedValueShort: -0.006, ProfitFactorShort: 0.47},
			},
			Sector: []*store.ScoreBinPerformance{
				{BinStart: 70, TradeCount: 12, ExpectedValueLong: 0.009, ProfitFactorLong: 1.92, ExpectedValueShort: -0.004, ProfitFactorShort: 0.52},
			},
			Symbol: []*store.ScoreBinPerformance{
				{BinStart: 75, TradeCount: 12, ExpectedValueLong: 0.006, ProfitFactorLong: 1.61, ExpectedValueShort: -0.003, ProfitFactorShort: 0.62},
			},
		}, nil
	})

	logicScore := 72.4
	ctx := &Context{
		TraderID:       "prompt-trader",
		CurrentTime:    "2026-03-23 08:20:00 UTC",
		CallCount:      1,
		RuntimeMinutes: 5,
		Account: AccountInfo{
			TotalEquity:      1000,
			AvailableBalance: 1000,
		},
		CandidateCoins: []CandidateCoin{
			{Symbol: "TESTUSDT", Sources: []string{"ai500"}, LogicScore: &logicScore},
		},
		MarketDataMap: map[string]*market.Data{
			"TESTUSDT": {
				Symbol:       "TESTUSDT",
				Sector:       "AI",
				CurrentPrice: 123.45,
				HeatScore: &market.HeatScoreData{
					CompositeScore: 72.4,
				},
			},
		},
	}

	text := engine.BuildUserPrompt(ctx)
	mustContain := []string{
		"[TESTUSDT | 123.4500 | Recalced_Score: 72.4]",
		"=== Smoothed Bin Matrix (Global - 7D) ===",
		"=== Smoothed Bin Matrix (Sector: AI - 7D) ===",
		"=== Smoothed Bin Matrix (Symbol: TESTUSDT - 5000 Samples) ===",
		"Bin  N    EV_L%  PF_L  EV_S%  PF_S",
		"30   27    -0.8 0.24   +1.7 4.20",
		"70   50    +1.2 2.15   -0.6 0.47",
		"75   12    +0.6 1.61   -0.3 0.62",
	}

	for _, expected := range mustContain {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected user prompt to contain %q, got:\n%s", expected, text)
		}
	}

	mustNotContain := []string{
		"bin_start",
		"trade_count",
		"ev_long",
		"profit_factor_long",
		"ev_short",
		"profit_factor_short",
		"EMA",
		"Trend Context",
		"symbol =",
		"current_price =",
		"adaptive_heat_score =",
	}
	for _, forbidden := range mustNotContain {
		if strings.Contains(text, forbidden) {
			t.Fatalf("expected dense matrix prompt without JSON keys %q, got:\n%s", forbidden, text)
		}
	}
}

func TestBuildUserPromptOmitsThinSymbolPerformanceMatrix(t *testing.T) {
	config := store.GetDefaultStrategyConfig("en")
	config.Indicators.EnableEMA = false
	config.Indicators.EnableMACD = false
	config.Indicators.EnableRSI = false
	config.Indicators.EnableATR = false
	config.Indicators.EnableBOLL = false
	config.Indicators.EnableDonchianBox = false

	engine := NewStrategyEngine(&config)
	engine.SetPerformanceBinProvider(func(traderID, sector, symbol string) (*PerformanceBinMatrices, error) {
		return &PerformanceBinMatrices{
			Global: []*store.ScoreBinPerformance{
				{BinStart: 30, TradeCount: 27, ExpectedValueLong: -0.008, ProfitFactorLong: 0.24, ExpectedValueShort: 0.017, ProfitFactorShort: 4.20},
			},
			Sector: []*store.ScoreBinPerformance{
				{BinStart: 70, TradeCount: 12, ExpectedValueLong: 0.009, ProfitFactorLong: 1.92, ExpectedValueShort: -0.004, ProfitFactorShort: 0.52},
			},
			Symbol: []*store.ScoreBinPerformance{
				{BinStart: 75, TradeCount: 7, ExpectedValueLong: 0.006, ProfitFactorLong: 1.61, ExpectedValueShort: -0.003, ProfitFactorShort: 0.62},
			},
		}, nil
	})

	logicScore := 72.4
	ctx := &Context{
		TraderID:       "prompt-trader",
		CurrentTime:    "2026-03-23 08:20:00 UTC",
		CallCount:      1,
		RuntimeMinutes: 5,
		Account: AccountInfo{
			TotalEquity:      1000,
			AvailableBalance: 1000,
		},
		CandidateCoins: []CandidateCoin{
			{Symbol: "TESTUSDT", Sources: []string{"ai500"}, LogicScore: &logicScore},
		},
		MarketDataMap: map[string]*market.Data{
			"TESTUSDT": {
				Symbol:       "TESTUSDT",
				Sector:       "AI",
				CurrentPrice: 123.45,
				HeatScore: &market.HeatScoreData{
					CompositeScore: 72.4,
				},
			},
		},
	}

	text := engine.BuildUserPrompt(ctx)
	if !strings.Contains(text, "=== Smoothed Bin Matrix (Global - 7D) ===") {
		t.Fatalf("expected user prompt to contain global matrix, got:\n%s", text)
	}
	if !strings.Contains(text, "=== Smoothed Bin Matrix (Sector: AI - 7D) ===") {
		t.Fatalf("expected user prompt to contain sector matrix, got:\n%s", text)
	}
	if strings.Contains(text, "=== Smoothed Bin Matrix (Symbol: TESTUSDT - 5000 Samples) ===") {
		t.Fatalf("expected thin symbol matrix to be omitted when N < 10, got:\n%s", text)
	}
}

func TestBuildUserPromptUsesRecalculatedCandidateScoreForHeaderAndMatrixFocus(t *testing.T) {
	config := store.GetDefaultStrategyConfig("en")
	config.Indicators.EnableEMA = false
	config.Indicators.EnableMACD = false
	config.Indicators.EnableRSI = false
	config.Indicators.EnableATR = false
	config.Indicators.EnableBOLL = false
	config.Indicators.EnableDonchianBox = false

	engine := NewStrategyEngine(&config)
	engine.SetPerformanceBinProvider(func(traderID, sector, symbol string) (*PerformanceBinMatrices, error) {
		bins := make([]*store.ScoreBinPerformance, 0, 101)
		for score := 0; score <= 100; score++ {
			bins = append(bins, &store.ScoreBinPerformance{
				BinStart:           score,
				TradeCount:         25,
				ExpectedValueLong:  float64(score) / 10000,
				ProfitFactorLong:   1.5,
				ExpectedValueShort: -float64(score) / 10000,
				ProfitFactorShort:  0.7,
			})
		}
		return &PerformanceBinMatrices{
			Global: bins,
			Sector: bins,
			Symbol: bins,
		}, nil
	})

	logicScore := 72.0
	ctx := &Context{
		TraderID:       "prompt-trader",
		CurrentTime:    "2026-03-23 08:20:00 UTC",
		CallCount:      1,
		RuntimeMinutes: 5,
		Account: AccountInfo{
			TotalEquity:      1000,
			AvailableBalance: 1000,
		},
		CandidateCoins: []CandidateCoin{
			{Symbol: "TESTUSDT", Sources: []string{"ai500"}, LogicScore: &logicScore},
		},
		MarketDataMap: map[string]*market.Data{
			"TESTUSDT": {
				Symbol:       "TESTUSDT",
				Sector:       "AI",
				CurrentPrice: 123.45,
				HeatScore: &market.HeatScoreData{
					CompositeScore: 11.0,
				},
			},
		},
	}

	text := engine.BuildUserPrompt(ctx)
	if !strings.Contains(text, "[TESTUSDT | 123.4500 | Recalced_Score: 72.0]") {
		t.Fatalf("expected prompt header to use recalculated candidate score, got:\n%s", text)
	}
	if !strings.Contains(text, "72   25    +0.7 1.50   -0.7 0.70") {
		t.Fatalf("expected prompt matrix to focus around recalculated score 72, got:\n%s", text)
	}
	if strings.Contains(text, "11   25    +0.1 1.50   -0.1 0.70") {
		t.Fatalf("expected prompt matrix not to focus around stale market heat score, got:\n%s", text)
	}
}
