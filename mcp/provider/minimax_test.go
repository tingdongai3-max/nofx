package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nofx/mcp"
)

func TestMiniMaxClient_AnthropicMessagesAPI(t *testing.T) {
	var seenPath string
	var seenHost string
	var seenHeaders http.Header
	var seenBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seenPath = req.URL.Path
		seenHost = req.Host
		seenHeaders = req.Header.Clone()

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		seenBody = body

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"content":[{"type":"text","text":"MiniMax OK"}],
			"usage":{"input_tokens":12,"output_tokens":34}
		}`))
	}))
	defer server.Close()

	client := NewMiniMaxClientWithOptions(
		mcp.WithHTTPClient(server.Client()),
		mcp.WithLogger(mcp.NewNoopLogger()),
	)

	mmClient := client.(*MiniMaxClient)
	mmClient.SetAPIKey("sk-test-1234567890", server.URL+"/anthropic", "MiniMax-M2.7")

	if mmClient.BaseURL != server.URL+"/anthropic/v1" {
		t.Fatalf("expected normalized BaseURL, got %s", mmClient.BaseURL)
	}

	result, err := mmClient.CallWithMessages("system prompt", "user prompt")
	if err != nil {
		t.Fatalf("CallWithMessages failed: %v", err)
	}
	if result != "MiniMax OK" {
		t.Fatalf("unexpected result: %s", result)
	}

	if seenPath != "/anthropic/v1/messages" {
		t.Fatalf("unexpected request path: %s", seenPath)
	}
	if seenHost != strings.TrimPrefix(server.URL, "http://") {
		t.Fatalf("unexpected request host: %s", seenHost)
	}

	if got := seenHeaders.Get("x-api-key"); got != "sk-test-1234567890" {
		t.Fatalf("unexpected x-api-key header: %s", got)
	}
	if got := seenHeaders.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("unexpected anthropic-version header: %s", got)
	}

	var body map[string]any
	if err := json.Unmarshal(seenBody, &body); err != nil {
		t.Fatalf("failed to parse request body: %v", err)
	}

	if body["model"] != "MiniMax-M2.7" {
		t.Fatalf("unexpected model in body: %#v", body["model"])
	}
	if body["system"] != "system prompt" {
		t.Fatalf("unexpected system prompt in body: %#v", body["system"])
	}

	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("unexpected messages payload: %#v", body["messages"])
	}
	firstMessage, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected first message payload: %#v", messages[0])
	}
	if firstMessage["role"] != "user" || firstMessage["content"] != "user prompt" {
		t.Fatalf("unexpected first message content: %#v", firstMessage)
	}
}
