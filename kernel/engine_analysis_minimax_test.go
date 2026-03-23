package kernel

import (
	"testing"

	"nofx/mcp"
	"nofx/mcp/provider"
)

func TestEnsureMiniMaxDecisionTokenBudgetRaisesMiniMaxOnly(t *testing.T) {
	minimaxClient := provider.NewMiniMaxClientWithOptions(mcp.WithMaxTokens(2000)).(*provider.MiniMaxClient)
	ensureMiniMaxDecisionTokenBudget(minimaxClient)
	if minimaxClient.MaxTokens != minimaxDecisionMaxTokens {
		t.Fatalf("expected minimax max tokens %d, got %d", minimaxDecisionMaxTokens, minimaxClient.MaxTokens)
	}
	if minimaxClient.Cfg == nil || minimaxClient.Cfg.MaxTokens != minimaxDecisionMaxTokens {
		t.Fatalf("expected minimax config max tokens %d, got %+v", minimaxDecisionMaxTokens, minimaxClient.Cfg)
	}

	deepseekClient := provider.NewDeepSeekClientWithOptions(mcp.WithMaxTokens(2000)).(*provider.DeepSeekClient)
	ensureMiniMaxDecisionTokenBudget(deepseekClient)
	if deepseekClient.MaxTokens != 2000 {
		t.Fatalf("expected non-minimax max tokens unchanged, got %d", deepseekClient.MaxTokens)
	}
}
