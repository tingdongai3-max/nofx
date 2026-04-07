package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nofx/mcp"
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
		mcp.WithModel(mcp.DefaultMiniMaxModel),
		mcp.WithBaseURL(mcp.DefaultMiniMaxBaseURL),
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
		if strings.HasSuffix(customURL, "#") {
			c.BaseURL = strings.TrimSuffix(customURL, "#")
			c.UseFullURL = true
			c.Log.Infof("🔧 [MCP] MiniMax using custom full URL: %s", c.BaseURL)
		} else {
			c.BaseURL = normalizeMiniMaxAnthropicBaseURL(customURL)
			c.UseFullURL = false
			if c.BaseURL != customURL {
				c.Log.Infof("🔧 [MCP] MiniMax using custom BaseURL: %s (normalized from %s)", c.BaseURL, customURL)
			} else {
				c.Log.Infof("🔧 [MCP] MiniMax using custom BaseURL: %s", c.BaseURL)
			}
		}
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

// MiniMax uses Anthropic-compatible Messages API auth.
func (c *MiniMaxClient) SetAuthHeader(reqHeaders http.Header) {
	reqHeaders.Set("x-api-key", c.APIKey)
	reqHeaders.Set("anthropic-version", "2023-06-01")
}

// BuildUrl targets the Anthropic Messages endpoint.
func (c *MiniMaxClient) BuildUrl() string {
	if c.UseFullURL {
		return c.BaseURL
	}
	return fmt.Sprintf("%s/messages", normalizeMiniMaxAnthropicBaseURL(c.BaseURL))
}

// BuildMCPRequestBody builds Anthropic wire format for the simple CallWithMessages path.
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

// BuildRequestBodyFromRequest converts a *Request into Anthropic Messages wire format.
func (c *MiniMaxClient) BuildRequestBodyFromRequest(req *mcp.Request) map[string]any {
	var systemPrompt string
	var convMsgs []mcp.Message
	for _, m := range req.Messages {
		if m.Role == "system" {
			systemPrompt = m.Content
		} else {
			convMsgs = append(convMsgs, m)
		}
	}

	anthropicMsgs := ConvertMessagesToAnthropic(convMsgs)

	var anthropicTools []map[string]any
	for _, t := range req.Tools {
		anthropicTools = append(anthropicTools, map[string]any{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": t.Function.Parameters,
		})
	}

	body := map[string]any{
		"model":      req.Model,
		"max_tokens": c.MaxTokens,
		"system":     systemPrompt,
		"messages":   anthropicMsgs,
	}

	if len(anthropicTools) > 0 {
		body["tools"] = anthropicTools
	}

	switch req.ToolChoice {
	case "auto":
		body["tool_choice"] = map[string]any{"type": "auto"}
	case "any":
		body["tool_choice"] = map[string]any{"type": "any"}
	case "none", "":
		// omit
	}

	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	return body
}

// ParseMCPResponse extracts the plain-text reply from an Anthropic response.
func (c *MiniMaxClient) ParseMCPResponse(body []byte) (string, error) {
	r, err := c.ParseMCPResponseFull(body)
	if err != nil {
		return "", err
	}
	return r.Content, nil
}

// ParseMCPResponseFull extracts both text and tool calls from an Anthropic response.
func (c *MiniMaxClient) ParseMCPResponseFull(body []byte) (*mcp.LLMResponse, error) {
	var raw struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text,omitempty"`
			ID    string          `json:"id,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
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
		return nil, fmt.Errorf("failed to parse MiniMax Anthropic response: %w — body: %s", err, body)
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("MiniMax Anthropic API error: %s — %s", raw.Error.Type, raw.Error.Message)
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
	for _, block := range raw.Content {
		switch block.Type {
		case "text":
			result.Content = block.Text
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
	return result, nil
}

func normalizeMiniMaxAnthropicBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	base = strings.TrimSuffix(base, "/")
	if base == "" {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	if strings.HasSuffix(base, "/anthropic") || strings.Contains(base, "/anthropic/") {
		return base + "/v1"
	}
	return base
}
