package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nofx/mcp"
)

const (
	// MiniMax Token Plan uses Anthropic Messages API
	DefaultMiniMaxBaseURL = "https://api.minimaxi.com/anthropic/v1"
	DefaultMiniMaxModel   = "MiniMax-M2.5"
)

func init() {
	mcp.RegisterProvider(mcp.ProviderMiniMax, func(opts ...mcp.ClientOption) mcp.AIClient {
		return NewMiniMaxClientWithOptions(opts...)
	})
}

type MiniMaxClient struct {
	*mcp.Client
}

func (c *MiniMaxClient) BaseClient() *mcp.Client { return c.Client }

// NewMiniMaxClient creates MiniMax client (backward compatible)
func NewMiniMaxClient() mcp.AIClient {
	return NewMiniMaxClientWithOptions()
}

// NewMiniMaxClientWithOptions creates MiniMax client (supports options pattern)
func NewMiniMaxClientWithOptions(opts ...mcp.ClientOption) mcp.AIClient {
	minimaxOpts := []mcp.ClientOption{
		mcp.WithProvider(mcp.ProviderMiniMax),
		mcp.WithModel(DefaultMiniMaxModel),
		mcp.WithBaseURL(DefaultMiniMaxBaseURL),
	}

	allOpts := append(minimaxOpts, opts...)
	baseClient := mcp.NewClient(allOpts...).(*mcp.Client)

	minimaxClient := &MiniMaxClient{
		Client: baseClient,
	}

	baseClient.Hooks = minimaxClient
	return minimaxClient
}

func (c *MiniMaxClient) SetAPIKey(apiKey string, customURL string, customModel string) {
	c.APIKey = apiKey

	if len(apiKey) > 8 {
		c.Log.Infof("🔧 [MCP] MiniMax API Key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
	}
	if customURL != "" {
		c.BaseURL = customURL
		c.Log.Infof("🔧 [MCP] MiniMax using custom BaseURL: %s", customURL)
	} else {
		c.Log.Infof("🔧 [MCP] MiniMax using default BaseURL: %s", c.BaseURL)
	}
	if customModel != "" {
		c.Model = customModel
		c.Log.Infof("🔧 [MCP] MiniMax using custom Model: %s", customModel)
	} else {
		c.Log.Infof("🔧 [MCP] MiniMax using default Model: %s", c.Model)
	}
}

// SetAuthHeader uses Bearer token (MiniMax Token Plan supports Anthropic-compatible auth)
func (c *MiniMaxClient) SetAuthHeader(reqHeaders http.Header) {
	reqHeaders.Set("Authorization", fmt.Sprintf("Bearer %s", c.APIKey))
	reqHeaders.Set("anthropic-version", "2023-06-01")
}

// BuildUrl targets /messages endpoint (Anthropic Messages API)
func (c *MiniMaxClient) BuildUrl() string {
	return fmt.Sprintf("%s/messages", c.BaseURL)
}

// BuildMCPRequestBody builds Anthropic Messages API format
func (c *MiniMaxClient) BuildMCPRequestBody(systemPrompt, userPrompt string) map[string]any {
	return map[string]any{
		"model":      c.Model,
		"max_tokens": c.MaxTokens,
		"system":     systemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": userPrompt},
		},
	}
}

// ParseMCPResponse parses Anthropic Messages API response - extracts plain text content
func (c *MiniMaxClient) ParseMCPResponse(body []byte) (string, error) {
	r, err := c.ParseMCPResponseFull(body)
	if err != nil {
		return "", err
	}
	return r.Content, nil
}

// ParseMCPResponseFull parses Anthropic Messages API response
func (c *MiniMaxClient) ParseMCPResponseFull(body []byte) (*mcp.LLMResponse, error) {
	var raw struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text,omitempty"`
			Thinking string          `json:"thinking,omitempty"`
			ID       string          `json:"id,omitempty"`
			Name     string          `json:"name,omitempty"`
			Input    json.RawMessage `json:"input,omitempty"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic response: %w — body: %s", err, body)
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("MiniMax API error: %s — %s", raw.Error.Type, raw.Error.Message)
	}

	total := raw.Usage.InputTokens + raw.Usage.OutputTokens
	if mcp.TokenUsageCallback != nil && total > 0 {
		mcp.TokenUsageCallback(mcp.TokenUsage{
			Provider:         c.Provider,
			Model:            c.Model,
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      total,
		})
	}

	result := &mcp.LLMResponse{}
	contentParts := make([]string, 0, len(raw.Content))
	for _, block := range raw.Content {
		switch block.Type {
		case "thinking":
			appendMiniMaxContentBlock(&contentParts, block.Thinking)

		case "text":
			appendMiniMaxContentBlock(&contentParts, block.Text)

		case "tool_use":
			argsJSON, err := json.Marshal(block.Input)
			if err != nil {
				argsJSON = []byte("{}")
			}
			result.ToolCalls = append(result.ToolCalls, mcp.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: mcp.ToolCallFunction{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}
	result.Content = strings.Join(contentParts, "\n\n")
	return result, nil
}

func appendMiniMaxContentBlock(parts *[]string, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	*parts = append(*parts, content)
}
