package kernel

import (
	"testing"
)

// TestLeverageFallback tests automatic correction when leverage exceeds limit
func TestLeverageFallback(t *testing.T) {
	tests := []struct {
		name            string
		decision        Decision
		accountEquity   float64
		btcEthLeverage  int
		altcoinLeverage int
		wantLeverage    int // Expected leverage after correction
		wantError       bool
	}{
		{
			name: "Altcoin leverage exceeded - auto-correct to limit",
			decision: Decision{
				Symbol:          "SOLUSDT",
				Action:          "open_long",
				Leverage:        20, // Exceeds limit
				PositionSizeUSD: 100,
				StopLoss:        50,
				TakeProfit:      200,
				Reasoning:       "Bin 65: EV_L +1.4% > 0 and PF_L 2.10 > PF_S 0.48, favor long.",
			},
			accountEquity:   100,
			btcEthLeverage:  10,
			altcoinLeverage: 5, // Limit 5x
			wantLeverage:    5, // Should be corrected to 5
			wantError:       false,
		},
		{
			name: "BTC leverage exceeded - auto-correct to limit",
			decision: Decision{
				Symbol:          "BTCUSDT",
				Action:          "open_long",
				Leverage:        20, // Exceeds limit
				PositionSizeUSD: 1000,
				StopLoss:        90000,
				TakeProfit:      110000,
				Reasoning:       "Bin 70: EV_L +1.8% > 0 and PF_L 2.40 > PF_S 0.41, favor long.",
			},
			accountEquity:   100,
			btcEthLeverage:  10, // Limit 10x
			altcoinLeverage: 5,
			wantLeverage:    10, // Should be corrected to 10
			wantError:       false,
		},
		{
			name: "Leverage within limit - no correction",
			decision: Decision{
				Symbol:          "ETHUSDT",
				Action:          "open_short",
				Leverage:        5, // Not exceeded
				PositionSizeUSD: 500,
				StopLoss:        4000,
				TakeProfit:      3000,
				Reasoning:       "Bin 80: EV_S +1.1% > 0 and PF_S 2.30 > PF_L 0.44, favor short.",
			},
			accountEquity:   100,
			btcEthLeverage:  10,
			altcoinLeverage: 5,
			wantLeverage:    5, // Stays unchanged
			wantError:       false,
		},
		{
			name: "Leverage is 0 - should error",
			decision: Decision{
				Symbol:          "SOLUSDT",
				Action:          "open_long",
				Leverage:        0, // Invalid
				PositionSizeUSD: 100,
				StopLoss:        50,
				TakeProfit:      200,
				Reasoning:       "Bin 60: EV_L +0.9% > 0 and PF_L 1.90 > PF_S 0.55, favor long.",
			},
			accountEquity:   100,
			btcEthLeverage:  10,
			altcoinLeverage: 5,
			wantLeverage:    0,
			wantError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use default position value ratios for testing (10x for BTC/ETH, 1.5x for altcoins)
			err := validateDecision(&tt.decision, tt.accountEquity, tt.btcEthLeverage, tt.altcoinLeverage, 10.0, 1.5)

			// Check error status
			if (err != nil) != tt.wantError {
				t.Errorf("validateDecision() error = %v, wantError %v", err, tt.wantError)
				return
			}

			// If shouldn't error, check if leverage was correctly corrected
			if !tt.wantError && tt.decision.Leverage != tt.wantLeverage {
				t.Errorf("Leverage not corrected: got %d, want %d", tt.decision.Leverage, tt.wantLeverage)
			}
		})
	}
}

func TestValidateDecisionRequiresPerformanceMatrixCitation(t *testing.T) {
	decision := Decision{
		Symbol:          "SOLUSDT",
		Action:          "open_long",
		Leverage:        3,
		PositionSizeUSD: 100,
		StopLoss:        50,
		TakeProfit:      200,
		Reasoning:       "Momentum looks good, favor long.",
	}

	err := validateDecision(&decision, 100, 10, 5, 10.0, 1.5)
	if err == nil {
		t.Fatal("expected matrix citation validation error")
	}
	if !contains(err.Error(), "Bin/Ban/区间") {
		t.Fatalf("expected Bin/Ban/区间 requirement error, got %v", err)
	}
}

func TestValidateDecisionAllowsSystemFallbackReasoning(t *testing.T) {
	decision := Decision{
		Symbol:    "ALL",
		Action:    "wait",
		Reasoning: "Model didn't output structured JSON decision, entering safe wait; summary: ...",
	}

	err := validateDecision(&decision, 100, 10, 5, 10.0, 1.5)
	if err != nil {
		t.Fatalf("expected system fallback reasoning to pass, got %v", err)
	}
}

func TestValidateDecisionAcceptsFlexibleBinCitations(t *testing.T) {
	tests := []struct {
		name     string
		decision Decision
	}{
		{
			name: "accepts Ban alias for long",
			decision: Decision{
				Symbol:          "SOLUSDT",
				Action:          "open_long",
				Leverage:        3,
				PositionSizeUSD: 100,
				StopLoss:        50,
				TakeProfit:      200,
				Reasoning:       "Ban 46: EV_L +0.2% > 0 且 PF_L 1.32 具有优势，技术面共振支持开多。",
			},
		},
		{
			name: "accepts bin without whitespace",
			decision: Decision{
				Symbol:          "SOLUSDT",
				Action:          "open_long",
				Leverage:        3,
				PositionSizeUSD: 100,
				StopLoss:        50,
				TakeProfit:      200,
				Reasoning:       "bin46 EV_L +0.2% > 0, PF_L 1.32 有优势，继续看多。",
			},
		},
		{
			name: "accepts Chinese interval for short",
			decision: Decision{
				Symbol:          "SOLUSDT",
				Action:          "open_short",
				Leverage:        3,
				PositionSizeUSD: 100,
				StopLoss:        200,
				TakeProfit:      50,
				Reasoning:       "区间 46: EV_S +0.2% > 0 且 PF_S 1.32 具有优势，技术面共振支持开空。",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDecision(&tt.decision, 100, 10, 5, 10.0, 1.5)
			if err != nil {
				t.Fatalf("expected flexible bin citation to pass, got %v", err)
			}
		})
	}
}

func TestValidateDecisionAllowsPendingZoneWhenBinIsPresent(t *testing.T) {
	decision := Decision{
		Symbol:          "SOLUSDT",
		Action:          "open_long",
		Leverage:        3,
		PositionSizeUSD: 100,
		StopLoss:        50,
		TakeProfit:      200,
		Reasoning:       "Ban 46: 技术面共振支持开多，等待进一步确认。",
	}

	err := validateDecision(&decision, 100, 10, 5, 10.0, 1.5)
	if err != nil {
		t.Fatalf("expected pending-zone reasoning to pass, got %v", err)
	}
}

func TestValidateDecisionRejectsWrongSideOnlyOpenReasoning(t *testing.T) {
	decision := Decision{
		Symbol:          "SOLUSDT",
		Action:          "open_long",
		Leverage:        3,
		PositionSizeUSD: 100,
		StopLoss:        50,
		TakeProfit:      200,
		Reasoning:       "Bin 46: EV_S +0.2% > 0 且 PF_S 1.32 具有优势，因此开多。",
	}

	err := validateDecision(&decision, 100, 10, 5, 10.0, 1.5)
	if err == nil {
		t.Fatal("expected wrong-side-only open reasoning to fail")
	}
	if !contains(err.Error(), "EV_L and PF_L") {
		t.Fatalf("expected current-side metric requirement error, got %v", err)
	}
}

func TestValidateDecisionAllowsCloseLongWithRiskEvidenceWithoutBin(t *testing.T) {
	decision := Decision{
		Symbol:    "BANANAS31USDT",
		Action:    "close_long",
		Reasoning: "价格从0.0163峰值回落至0.0149,OI减少-2.01M,典型多头平仓踩踏。持仓17分钟已亏损-2.17%,Peak PnL从+1.04%大幅回撤。风险控制优先,止损离场释放9 USDT保证金。",
	}

	err := validateDecision(&decision, 25.69, 5, 5, 5.0, 1.0)
	if err != nil {
		t.Fatalf("expected close_long risk-evidence reasoning to pass, got %v", err)
	}
}

func TestValidateDecisionAllowsHoldWithRiskEvidenceWithoutBin(t *testing.T) {
	decision := Decision{
		Symbol:          "BANANAS31USDT",
		Action:          "hold",
		Leverage:        3,
		PositionSizeUSD: 25.81,
		Confidence:      82,
		Reasoning:       "持仓已20分钟。OI -2.48M + 价格-4.07% 形成明确空头共振,当前PnL -2.18%但未触及止损。Symbol无EV矩阵但市场数据确认空头主导,持有等待回归0.0144附近止盈。",
	}

	err := validateDecision(&decision, 25.86, 5, 5, 5.0, 1.0)
	if err != nil {
		t.Fatalf("expected hold risk-evidence reasoning to pass, got %v", err)
	}
}

func TestValidateDecisionAllowsWaitWithRiskEvidenceWithoutBin(t *testing.T) {
	decision := Decision{
		Symbol:    "BANANAS31USDT",
		Action:    "wait",
		Reasoning: "OI大幅下降-1.20%,价格下跌-5.41%,属于持仓减少榜第二名。短期资金持续流出,不适合开仓。",
	}

	err := validateDecision(&decision, 25.89, 5, 5, 5.0, 1.0)
	if err != nil {
		t.Fatalf("expected wait risk-evidence reasoning to pass, got %v", err)
	}
}

func TestValidateDecisionAllowsWaitWithStatisticalEvidenceWithoutBin(t *testing.T) {
	decision := Decision{
		Symbol:    "AAVEUSDT",
		Action:    "wait",
		Reasoning: "Symbol矩阵N<30样本不足,Sector Matrix EV≈0且PF<1.2,不满足开仓条件。",
	}

	err := validateDecision(&decision, 25.95, 5, 5, 5.0, 1.0)
	if err != nil {
		t.Fatalf("expected wait statistical-insufficiency reasoning to pass, got %v", err)
	}
}

func TestValidateDecisionStillRejectsManagementReasoningWithoutBinOrRiskEvidence(t *testing.T) {
	decision := Decision{
		Symbol:    "BANANAS31USDT",
		Action:    "close_long",
		Reasoning: "技术面转弱，先离场观望。",
	}

	err := validateDecision(&decision, 25.69, 5, 5, 5.0, 1.0)
	if err == nil {
		t.Fatal("expected management reasoning without bin or risk evidence to fail")
	}
	if !contains(err.Error(), "risk evidence") {
		t.Fatalf("expected management evidence requirement error, got %v", err)
	}
}

func TestParseFullDecisionResponseAllowsHistoricalCloseAndWaitRiskBatch(t *testing.T) {
	rawResponse := `<reasoning>
**候选币种分析：**
1. RIVERUSDT：Symbol Bin 57-63显示EV_L转负,等待企稳。
2. BANANAS31USDT：OI大幅下降-1.20%,价格下跌-5.41%,短期资金持续流出,不适合开仓。
</reasoning>

<decision>
[
  {"symbol": "SIGNUSDT", "action": "close_long", "reasoning": "持仓接近成本价（Entry 0.0546, Current 0.0545, PnL+0.43%）,15M出现-0.60%回调,OI减少-158.31K显示短期多头动能减弱。无明确方向优势,平仓等待更清晰信号。"},
  {"symbol": "RIVERUSDT", "action": "wait", "reasoning": "OI减少-0.43%,价格下跌-2.79%,大单流出-733.22K,短期偏空。Symbol Bin 57-63显示EV_L转负,等待企稳。"},
  {"symbol": "BANANAS31USDT", "action": "wait", "reasoning": "OI大幅下降-1.20%,价格下跌-5.41%,属于持仓减少榜第二名。短期资金持续流出,不适合开仓。"}
]
</decision>`

	decision, err := parseFullDecisionResponse(rawResponse, 25.89, 5, 5, 5.0, 1.0)
	if err != nil {
		t.Fatalf("expected historical close+wait batch to pass, got %v", err)
	}
	if decision == nil || len(decision.Decisions) != 3 {
		t.Fatalf("expected three parsed decisions, got %+v", decision)
	}
	if decision.Decisions[0].Action != "close_long" {
		t.Fatalf("expected first decision to remain executable close_long, got %+v", decision.Decisions[0])
	}
	if decision.Decisions[2].Action != "wait" || decision.Decisions[2].Symbol != "BANANAS31USDT" {
		t.Fatalf("unexpected third decision: %+v", decision.Decisions[2])
	}
}

func TestParseFullDecisionResponseAllowsHistoricalCloseAndWaitStatsBatch(t *testing.T) {
	rawResponse := `<reasoning>
## 决策分析
1. SIGNUSDT：持仓浮盈有限，资金流转弱，先平仓。
2. AAVEUSDT：Symbol矩阵N<30样本不足,Sector Matrix EV≈0且PF<1.2,不满足开仓条件。
</reasoning>

<decision>
[
  {"symbol": "SIGNUSDT", "action": "close_long", "reasoning": "Bin 50: 方向模糊,无EV支撑。OI收缩(-54.58K 4h) + 大单净流出(-281.81K 4h) + 5M动能衰减三重看跌共振。当前PnL +0.80%,执行止盈离场规避噪音暴露。"},
  {"symbol": "AAVEUSDT", "action": "wait", "reasoning": "Symbol矩阵N<30样本不足,Sector Matrix EV≈0且PF<1.2,不满足开仓条件。"}
]
</decision>`

	decision, err := parseFullDecisionResponse(rawResponse, 25.95, 5, 5, 5.0, 1.0)
	if err != nil {
		t.Fatalf("expected historical close+wait stats batch to pass, got %v", err)
	}
	if decision == nil || len(decision.Decisions) != 2 {
		t.Fatalf("expected two parsed decisions, got %+v", decision)
	}
	if decision.Decisions[0].Action != "close_long" || decision.Decisions[0].Symbol != "SIGNUSDT" {
		t.Fatalf("unexpected first decision: %+v", decision.Decisions[0])
	}
	if decision.Decisions[1].Action != "wait" || decision.Decisions[1].Symbol != "AAVEUSDT" {
		t.Fatalf("unexpected second decision: %+v", decision.Decisions[1])
	}
}

func TestParseFullDecisionResponseAcceptsHistoricalBanCitation(t *testing.T) {
	rawResponse := `<reasoning>
## 核心决策
**BANANAS31USDT** 是当前最优选择：
1. 强势价格表现（4h +7.59%）
2. OI增加+1.93M（多头趋势确认）
3. 大单资金净流入+318K
4. Symbol Bin 46: EV_L > 0 且 PF_L 1.32 > 1.2，Long方向明确
</reasoning>

<decision>
[
  {"symbol": "BANANAS31USDT", "action": "open_long", "leverage": 5, "position_size_usd": 20, "stop_loss": 0.01442, "take_profit": 0.01668, "confidence": 78, "risk_usd": 4, "reasoning": "Ban 46: EV_L +0.2% > 0 且 PF_L 1.32 > 1.2, 4h涨幅+7.59%, OI增加+1.93M, 大单资金+318K流入, 多指标共振支持Long。"}
]
</decision>`

	decision, err := parseFullDecisionResponse(rawResponse, 26.09, 5, 5, 5.0, 1.0)
	if err != nil {
		t.Fatalf("expected historical Ban citation response to pass, got %v", err)
	}
	if decision == nil || len(decision.Decisions) != 1 {
		t.Fatalf("expected one parsed decision, got %+v", decision)
	}
	if decision.Decisions[0].Action != "open_long" || decision.Decisions[0].Symbol != "BANANAS31USDT" {
		t.Fatalf("unexpected parsed decision: %+v", decision.Decisions[0])
	}
}

// contains checks if string contains substring (helper function)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
