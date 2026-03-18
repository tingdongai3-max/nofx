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
			err := validateDecision(&tt.decision, tt.accountEquity, tt.btcEthLeverage, tt.altcoinLeverage, 10.0, 1.5, true)

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

func TestValidateDecision_StagedTakeProfitMustBeTwoByFifty(t *testing.T) {
	tests := []struct {
		name      string
		decision   Decision
		wantError bool
	}{
		{
			name: "valid two stage 50 50",
			decision: Decision{
				Symbol:          "SOLUSDT",
				Action:          "open_long",
				Leverage:        5,
				PositionSizeUSD: 100,
				StopLoss:        90,
				TakeProfitStages: []TakeProfitStage{
					{Price: 110, ClosePct: 50},
					{Price: 120, ClosePct: 50},
				},
			},
			wantError: false,
		},
		{
			name: "single take profit forbidden when staged enabled",
			decision: Decision{
				Symbol:          "SOLUSDT",
				Action:          "open_long",
				Leverage:        5,
				PositionSizeUSD: 100,
				StopLoss:        90,
				TakeProfit:      120,
			},
			wantError: true,
		},
		{
			name: "must be exactly two stages",
			decision: Decision{
				Symbol:          "SOLUSDT",
				Action:          "open_long",
				Leverage:        5,
				PositionSizeUSD: 100,
				StopLoss:        90,
				TakeProfitStages: []TakeProfitStage{
					{Price: 110, ClosePct: 30},
					{Price: 120, ClosePct: 30},
					{Price: 130, ClosePct: 40},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDecision(&tt.decision, 1000, 10, 5, 10.0, 1.5, true)
			if (err != nil) != tt.wantError {
				t.Fatalf("validateDecision() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestValidateDecision_TrailingRetraceGuardrail(t *testing.T) {
	tests := []struct {
		name      string
		decision   Decision
		wantError bool
	}{
		{
			name: "activation 5 retrace 2 allowed at 40 percent boundary",
			decision: Decision{
				Symbol:                "ETHUSDT",
				Action:                "open_long",
				Leverage:              5,
				PositionSizeUSD:       100,
				StopLoss:              2200,
				TakeProfit:            2500,
				TrailingActivationPct: 5,
				TrailingRetracePct:    2,
			},
			wantError: false,
		},
		{
			name: "activation 5 retrace 1 point 9 allowed",
			decision: Decision{
				Symbol:                "ETHUSDT",
				Action:                "open_long",
				Leverage:              5,
				PositionSizeUSD:       100,
				StopLoss:              2200,
				TakeProfit:            2500,
				TrailingActivationPct: 5,
				TrailingRetracePct:    1.9,
			},
			wantError: false,
		},
		{
			name: "activation 5 retrace 2 point 1 rejected",
			decision: Decision{
				Symbol:                "ETHUSDT",
				Action:                "open_long",
				Leverage:              5,
				PositionSizeUSD:       100,
				StopLoss:              2200,
				TakeProfit:            2500,
				TrailingActivationPct: 5,
				TrailingRetracePct:    2.1,
			},
			wantError: true,
		},
		{
			name: "trailing fields must be provided together",
			decision: Decision{
				Symbol:                "ETHUSDT",
				Action:                "open_long",
				Leverage:              5,
				PositionSizeUSD:       100,
				StopLoss:              2200,
				TakeProfit:            2500,
				TrailingActivationPct: 5,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDecision(&tt.decision, 1000, 10, 5, 10.0, 1.5, false)
			if (err != nil) != tt.wantError {
				t.Fatalf("validateDecision() error = %v, wantError %v", err, tt.wantError)
			}
		})
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
