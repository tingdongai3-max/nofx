package kernel

import (
	"strings"
	"testing"

	"nofx/store"
)

func TestBuildSystemPromptStatic_EnforcesPositivePositionSizeUSD(t *testing.T) {
	config := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&config)
	engine.SetLimitEntryMode(true)

	prompt := engine.BuildSystemPromptStatic("")

	mustContain := []string{
		"**CRITICAL RULE**: The field `position_size_usd` MUST be a positive number",
		"NEVER output `0`, `0.0`, or `null` for `position_size_usd`",
		"`position_size_usd`: 500",
		"`position_size_usd` must be the final calculated notional position value in USDT.",
	}

	for _, want := range mustContain {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing required guidance: %s", want)
		}
	}
}
