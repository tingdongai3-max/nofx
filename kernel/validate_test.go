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
