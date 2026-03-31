package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whosthatknocking/abx/internal/config"
	"github.com/whosthatknocking/abx/pkg/types"
)

func TestChatRequestBodyIncludesIntegrationsForLocalEndpoint(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "mistralai/magistral-small-2509",
		Integrations: []string{"mcp/playwright"},
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}})

	if provider.chatURL() != "http://127.0.0.1:1234/api/v1/chat" {
		t.Fatalf("unexpected local LM Studio chat URL %q", provider.chatURL())
	}
	integrations, ok := body["integrations"].([]string)
	if !ok || len(integrations) != 1 || integrations[0] != "mcp/playwright" {
		t.Fatalf("expected integrations to include mcp/playwright, got %#v", body["integrations"])
	}
	if _, exists := body["messages"]; exists {
		t.Fatalf("expected LM Studio native body to omit messages, got %#v", body["messages"])
	}
	input, ok := body["input"].(string)
	if !ok || !strings.Contains(input, "User: hello") {
		t.Fatalf("expected LM Studio native input messages, got %#v", body["input"])
	}
	if !strings.Contains(input, "System: You have access to these MCP integrations: mcp/playwright.") {
		t.Fatalf("expected MCP system guidance in LM Studio input, got %#v", body["input"])
	}
	if store, ok := body["store"].(bool); !ok || store {
		t.Fatalf("expected LM Studio native request to disable store, got %#v", body["store"])
	}
}

func TestChatRequestBodyOmitsIntegrationsForRemoteEndpoint(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "https://api.example.test/v1",
		Model:   "gpt-5-nano",
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}})

	if provider.chatURL() != "https://api.example.test/v1/chat/completions" {
		t.Fatalf("unexpected remote chat URL %q", provider.chatURL())
	}
	if _, exists := body["integrations"]; exists {
		t.Fatalf("expected integrations to be omitted for remote endpoint, got %#v", body["integrations"])
	}
}

func TestChatRequestBodyOmitsIntegrationsWhenNoServersAreEnabled(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "http://127.0.0.1:1234/v1",
		Model:   "mistralai/magistral-small-2509",
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}})

	if provider.chatURL() != "http://127.0.0.1:1234/v1/chat/completions" {
		t.Fatalf("unexpected local non-MCP chat URL %q", provider.chatURL())
	}
	if _, exists := body["integrations"]; exists {
		t.Fatalf("expected integrations to be omitted when no MCP servers are enabled, got %#v", body["integrations"])
	}
}

func TestDecodeLMStudioChatResponse(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "mistralai/magistral-small-2509",
		Integrations: []string{"mcp/playwright"},
	})

	resp, err := provider.decodeLMStudioChatResponse(strings.NewReader(`{
		"output": [
			{"type": "tool_call"},
			{"type": "message", "content": "Here is what I found."}
		]
	}`))
	if err != nil {
		t.Fatalf("decode LM Studio chat response: %v", err)
	}
	if resp.Text != "Here is what I found." {
		t.Fatalf("unexpected LM Studio chat text %q", resp.Text)
	}
}

func TestDecodeLMStudioInvalidToolCall(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "mistralai/magistral-small-2509",
		Integrations: []string{"mcp/playwright"},
	})

	_, err := provider.decodeLMStudioChatResponse(strings.NewReader(`{
		"output": [
			{"type": "invalid_tool_call", "reason": "integration not allowed"}
		]
	}`))
	if err == nil || !strings.Contains(err.Error(), "integration not allowed") {
		t.Fatalf("expected invalid tool call error, got %v", err)
	}
}

func TestToLMStudioInputBuildsTranscript(t *testing.T) {
	input := toLMStudioInput([]types.Message{
		{Role: types.RoleSystem, Text: "You are helpful."},
		{Role: types.RoleUser, Text: "Open the page."},
		{Role: types.RoleAssistant, Text: "I will open it."},
	})

	if !strings.Contains(input, "System: You are helpful.") {
		t.Fatalf("expected system text in transcript, got %q", input)
	}
	if !strings.Contains(input, "User: Open the page.") {
		t.Fatalf("expected user text in transcript, got %q", input)
	}
	if !strings.Contains(input, "Assistant: I will open it.") {
		t.Fatalf("expected assistant text in transcript, got %q", input)
	}
}

func TestWithMCPSystemPromptPrependsSystemInstruction(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "mistralai/magistral-small-2509",
		Integrations: []string{"mcp/playwright"},
	})

	messages := provider.withMCPSystemPrompt([]types.Message{{
		Role: types.RoleUser,
		Text: "Open Hacker News.",
	}})

	if len(messages) != 2 {
		t.Fatalf("expected prompt plus original message, got %d messages", len(messages))
	}
	if messages[0].Role != types.RoleSystem {
		t.Fatalf("expected first message to be system, got %s", messages[0].Role)
	}
	if !strings.Contains(messages[0].Text, "use them instead of saying you cannot browse") {
		t.Fatalf("expected MCP guidance in system prompt, got %q", messages[0].Text)
	}
}

func TestFormatAPIErrorLMStudioPermissionDenied(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "mistralai/magistral-small-2509",
		Integrations: []string{"mcp/playwright"},
	})

	err := provider.formatAPIError(403, "403 Forbidden", `{
		"error": {
			"message": "Permission denied to use plugin 'mcp/playwright'. Ensure that the server configuration allows plugin usage and, if using an API token, it has the necessary permissions.",
			"type": "invalid_request",
			"param": "integrations",
			"code": null
		}
	}`)
	if err == nil {
		t.Fatal("expected LM Studio permission error")
	}
	if !strings.Contains(err.Error(), "LM Studio denied MCP server mcp/playwright") {
		t.Fatalf("unexpected permission error %q", err)
	}
}

func TestFormatAPIErrorLMStudioIntegrationsRejected(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "mistralai/magistral-small-2509",
		Integrations: []string{"mcp/playwright"},
	})

	err := provider.formatAPIError(400, "400 Bad Request", `{
		"error": {
			"message": "Integration payload invalid.",
			"type": "invalid_request",
			"param": "integrations",
			"code": null
		}
	}`)
	if err == nil {
		t.Fatal("expected LM Studio integrations error")
	}
	if !strings.Contains(err.Error(), "LM Studio rejected the configured MCP integrations") {
		t.Fatalf("unexpected integrations error %q", err)
	}
}

func TestCheckFallsBackToModelsList(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		switch r.URL.Path {
		case "/v1/models/gpt-5-nano":
			http.Error(w, "not found", http.StatusNotFound)
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "gpt-5-nano"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := New(config.ProviderConfig{
		BaseURL: server.URL + "/v1",
		Model:   "gpt-5-nano",
	})

	if err := provider.Check(context.Background()); err != nil {
		t.Fatalf("expected fallback connectivity check to succeed, got %v", err)
	}
	if len(requested) != 2 || requested[0] != "/v1/models/gpt-5-nano" || requested[1] != "/v1/models" {
		t.Fatalf("unexpected connectivity check sequence: %#v", requested)
	}
}
