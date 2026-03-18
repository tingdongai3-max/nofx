package trader

import (
	"nofx/kernel"
	"testing"
)

func TestApplyLeverageDiscipline_AutoReduceByStopDistance(t *testing.T) {
	decision := &kernel.Decision{
		Symbol:   "SIRENUSDT",
		Action:   "open_long",
		Leverage: 20,
		StopLoss: 92,
	}

	err := applyLeverageDiscipline(decision.Symbol, "long", 100, decision)
	if err != nil {
		t.Fatalf("applyLeverageDiscipline() error = %v", err)
	}

	if decision.Leverage != 10 {
		t.Fatalf("expected leverage to be reduced to 10, got %d", decision.Leverage)
	}
}

func TestApplyLeverageDiscipline_RejectTooWideStop(t *testing.T) {
	decision := &kernel.Decision{
		Symbol:   "SIRENUSDT",
		Action:   "open_long",
		Leverage: 1,
		StopLoss: 10,
	}

	err := applyLeverageDiscipline(decision.Symbol, "long", 100, decision)
	if err == nil {
		t.Fatal("expected error for stop distance exceeding margin-loss budget at 1x")
	}
}

func TestResolveTakeProfitTargetPrice(t *testing.T) {
	tests := []struct {
		name     string
		side     string
		decision *kernel.Decision
		want     float64
	}{
		{
			name: "single take profit",
			side: "LONG",
			decision: &kernel.Decision{
				TakeProfit: 110,
			},
			want: 110,
		},
		{
			name: "long staged uses furthest target",
			side: "LONG",
			decision: &kernel.Decision{
				TakeProfitStages: []kernel.TakeProfitStage{
					{Price: 106, ClosePct: 50},
					{Price: 110, ClosePct: 50},
				},
			},
			want: 110,
		},
		{
			name: "short staged uses furthest target",
			side: "SHORT",
			decision: &kernel.Decision{
				TakeProfitStages: []kernel.TakeProfitStage{
					{Price: 94, ClosePct: 50},
					{Price: 90, ClosePct: 50},
				},
			},
			want: 90,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveTakeProfitTargetPrice(tt.side, tt.decision); got != tt.want {
				t.Fatalf("resolveTakeProfitTargetPrice() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalculateWatchdogActivationMovePctFromTargetProgress(t *testing.T) {
	pos := watchdogPosition{
		Side:                  "long",
		EntryPrice:            100,
		TakeProfitPrice:       110,
		TrailingActivationPct: 80,
	}
	got := calculateWatchdogActivationMovePctFromTargetProgress(pos)
	if got != 8 {
		t.Fatalf("calculateWatchdogActivationMovePctFromTargetProgress() = %v, want 8", got)
	}
}
