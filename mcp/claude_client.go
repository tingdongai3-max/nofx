package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	ProviderClaude       = "claude"
	DefaultClaudeBaseURL = "https://api.anthropic.com/v1"
	DefaultClaudeModel   = "claude-opus-4-6"
)

type ClaudeClient struct {
	*Client
}

// NewClaudeClient creates Claude client (backward compatible)
func NewClaudeClient() AIClient {
	return NewClaudeClientWithOptions()
}

// NewClaudeClientWithOptions creates Claude client (supports options pattern)
func NewClaudeClientWithOptions(opts ...ClientOption) AIClient {
	// 1. Create Claude preset options
	claudeOpts := []ClientOption{
		WithProvider(ProviderClaude),
		WithModel(DefaultClaudeModel),
		WithBaseURL(DefaultClaudeBaseURL),
	}

	// 2. Merge user options (user options have higher priority)
	allOpts := append(claudeOpts, opts...)

	// 3. Create base client
	baseClient := NewClient(allOpts...).(*Client)

	// 4. Create Claude client
	claudeClient := &ClaudeClient{
		Client: baseClient,
	}

	// 5. Set hooks to point to ClaudeClient (implement dynamic dispatch)
	baseClient.hooks = claudeClient

	return claudeClient
}

func (c *ClaudeClient) SetAPIKey(apiKey string, customURL string, customModel string) {
	c.APIKey = apiKey

	if len(apiKey) > 8 {
		c.logger.Infof("🔧 [MCP] Claude API Key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
	}
	if customURL != "" {
		c.BaseURL = customURL
		c.logger.Infof("🔧 [MCP] Claude using custom BaseURL: %s", customURL)
	} else {
		c.logger.Infof("🔧 [MCP] Claude using default BaseURL: %s", c.BaseURL)
	}
	if customModel != "" {
		c.Model = customModel
		c.logger.Infof("🔧 [MCP] Claude using custom Model: %s", customModel)
	} else {
		c.logger.Infof("🔧 [MCP] Claude using default Model: %s", c.Model)
	}
}

// setAuthHeader Claude uses x-api-key header instead of Authorization Bearer
func (c *ClaudeClient) setAuthHeader(reqHeaders http.Header) {
	reqHeaders.Set("x-api-key", c.APIKey)
	reqHeaders.Set("anthropic-version", "2023-06-01")
}

// buildUrl Claude uses /messages endpoint
func (c *ClaudeClient) buildUrl() string {
	return fmt.Sprintf("%s/messages", c.BaseURL)
}

// buildMCPRequestBody Claude has different request format
func (c *ClaudeClient) buildMCPRequestBody(systemPrompt, userPrompt string) map[string]any {
	requestBody := map[string]any{
		"model":      c.Model,
		"max_tokens": c.MaxTokens,
		"system":     systemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": userPrompt},
		},
	}

	return requestBody
}

// buildBodyWithCache builds request body with prompt caching: system = [cacheable static block, dynamic block].
// Only the static block is marked cache_control so dynamic (equity) and user prompt are never cached.
func (c *ClaudeClient) buildBodyWithCache(systemStatic, systemDynamic, userPrompt string) map[string]any {
	systemBlocks := []map[string]any{}
	if systemStatic != "" {
		systemBlocks = append(systemBlocks, map[string]any{
			"type":          "text",
			"text":          systemStatic,
			"cache_control": map[string]string{"type": "ephemeral"},
		})
	}
	if systemDynamic != "" {
		systemBlocks = append(systemBlocks, map[string]any{
			"type": "text",
			"text": systemDynamic,
		})
	}
	var systemVal any = systemStatic + systemDynamic
	if len(systemBlocks) > 0 {
		systemVal = systemBlocks
	}
	return map[string]any{
		"model":      c.Model,
		"max_tokens": c.MaxTokens,
		"system":     systemVal,
		"messages": []map[string]string{
			{"role": "user", "content": userPrompt},
		},
	}
}

// CallWithCacheableSystem uses prompt caching: systemStatic is cached, systemDynamic and userPrompt are sent fresh.
func (c *ClaudeClient) CallWithCacheableSystem(systemStatic, systemDynamic, userPrompt string) (string, error) {
	if c.APIKey == "" {
		return "", fmt.Errorf("AI API key not set, please call SetAPIKey first")
	}
	c.logger.Infof("📡 [Claude] Request with prompt caching (static block cached, dynamic + user fresh)")
	body := c.buildBodyWithCache(systemStatic, systemDynamic, userPrompt)
	return c.Client.executeRequestBody(body)
}

// parseMCPResponse Claude has different response format
func (c *ClaudeClient) parseMCPResponse(body []byte) (string, error) {
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens               int `json:"input_tokens"`
			OutputTokens              int `json:"output_tokens"`
			CacheReadInputTokens      int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens  int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("failed to parse Claude response: %w, body: %s", err, string(body))
	}

	if response.Error != nil {
		return "", fmt.Errorf("Claude API error: %s - %s", response.Error.Type, response.Error.Message)
	}

	if len(response.Content) == 0 {
		return "", fmt.Errorf("Claude returned empty content, body: %s", string(body))
	}

	// Log prompt cache usage when present
	if response.Usage.CacheReadInputTokens > 0 || response.Usage.CacheCreationInputTokens > 0 {
		c.logger.Infof("📦 [Claude] Prompt cache: read=%d created=%d input_after_breakpoint=%d",
			response.Usage.CacheReadInputTokens, response.Usage.CacheCreationInputTokens, response.Usage.InputTokens)
	}

	// Report token usage if callback is set (input_tokens here = tokens after cache breakpoint; total input = cache_read + cache_creation + input_tokens)
	totalInput := response.Usage.InputTokens + response.Usage.CacheReadInputTokens + response.Usage.CacheCreationInputTokens
	totalTokens := totalInput + response.Usage.OutputTokens
	if TokenUsageCallback != nil && totalTokens > 0 {
		TokenUsageCallback(TokenUsage{
			Provider:         c.Provider,
			Model:            c.Model,
			PromptTokens:     totalInput,
			CompletionTokens: response.Usage.OutputTokens,
			TotalTokens:      totalTokens,
		})
	}

	// Find text content
	for _, content := range response.Content {
		if content.Type == "text" {
			return content.Text, nil
		}
	}

	return "", fmt.Errorf("no text content in Claude response")
}
