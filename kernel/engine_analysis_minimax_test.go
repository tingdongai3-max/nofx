package kernel

import (
	"testing"

	"nofx/mcp"
	"nofx/mcp/provider"
)

func TestEnsureMiniMaxDecisionTokenBudgetRaisesMiniMaxOnly(t *testing.T) {
	minimaxClient := provider.NewMiniMaxClientWithOptions(mcp.WithMaxTokens(2000)).(*provider.MiniMaxClient)
	ensureDecisionTokenBudget(minimaxClient)
	if minimaxClient.MaxTokens != minimaxDecisionMaxTokens {
		t.Fatalf("expected minimax max tokens %d, got %d", minimaxDecisionMaxTokens, minimaxClient.MaxTokens)
	}
	if minimaxClient.Cfg == nil || minimaxClient.Cfg.MaxTokens != minimaxDecisionMaxTokens {
		t.Fatalf("expected minimax config max tokens %d, got %+v", minimaxDecisionMaxTokens, minimaxClient.Cfg)
	}

	geminiClient := provider.NewGeminiClientWithOptions(mcp.WithMaxTokens(2000)).(*provider.GeminiClient)
	ensureDecisionTokenBudget(geminiClient)
	if geminiClient.MaxTokens != geminiDecisionMaxTokens {
		t.Fatalf("expected gemini max tokens %d, got %d", geminiDecisionMaxTokens, geminiClient.MaxTokens)
	}
	if geminiClient.Cfg == nil || geminiClient.Cfg.MaxTokens != geminiDecisionMaxTokens {
		t.Fatalf("expected gemini config max tokens %d, got %+v", geminiDecisionMaxTokens, geminiClient.Cfg)
	}

	deepseekClient := provider.NewDeepSeekClientWithOptions(mcp.WithMaxTokens(2000)).(*provider.DeepSeekClient)
	ensureDecisionTokenBudget(deepseekClient)
	if deepseekClient.MaxTokens != 2000 {
		t.Fatalf("expected unrelated provider max tokens unchanged, got %d", deepseekClient.MaxTokens)
	}
}
