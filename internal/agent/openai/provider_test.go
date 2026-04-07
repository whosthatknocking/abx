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
	}}, types.AgentOptions{})

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
	}}, types.AgentOptions{})

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
	}}, types.AgentOptions{})

	if provider.chatURL() != "http://127.0.0.1:1234/v1/chat/completions" {
		t.Fatalf("unexpected local non-MCP chat URL %q", provider.chatURL())
	}
	if _, exists := body["integrations"]; exists {
		t.Fatalf("expected integrations to be omitted when no MCP servers are enabled, got %#v", body["integrations"])
	}
}

func TestChatRequestBodyAppliesThinkingParameterPath(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "http://127.0.0.1:1234/v1",
		Model:   "Qwen/Qwen3-8B",
		Thinking: config.ThinkingConfig{
			ParameterPath: "extra_body.chat_template_kwargs.enable_thinking",
		},
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}}, types.AgentOptions{Thinking: boolPtr(false)})

	extraBody, ok := body["extra_body"].(map[string]any)
	if !ok {
		t.Fatalf("expected extra_body map, got %#v", body["extra_body"])
	}
	templateKwargs, ok := extraBody["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("expected chat_template_kwargs map, got %#v", extraBody["chat_template_kwargs"])
	}
	if value, ok := templateKwargs["enable_thinking"].(bool); !ok || value {
		t.Fatalf("expected enable_thinking=false, got %#v", templateKwargs["enable_thinking"])
	}
}

func TestChatRequestBodyAppliesThinkingParameterPathCustomValues(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "http://127.0.0.1:1234/v1",
		Model:   "google/gemma-4-e4b",
		Thinking: config.ThinkingConfig{
			ParameterPath:         "reasoning",
			EnableParameterValue:  "on",
			DisableParameterValue: "off",
		},
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}}, types.AgentOptions{Thinking: boolPtr(false)})

	if value, ok := body["reasoning"].(string); !ok || value != "off" {
		t.Fatalf("expected reasoning=off, got %#v", body["reasoning"])
	}
}

func TestChatRequestBodyAppliesThinkingSuffix(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "http://127.0.0.1:1234/v1",
		Model:   "Qwen/Qwen3-8B",
		Thinking: config.ThinkingConfig{
			DisableSuffix: "/nothink",
		},
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}}, types.AgentOptions{Thinking: boolPtr(false)})

	messages, ok := body["messages"].([]map[string]string)
	if !ok || len(messages) != 1 {
		t.Fatalf("expected chat messages, got %#v", body["messages"])
	}
	if got := messages[0]["content"]; !strings.Contains(got, "/nothink") {
		t.Fatalf("expected /nothink suffix in %q", got)
	}
}

func TestChatRequestBodyAppliesThinkingSystemPrompt(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "http://127.0.0.1:1234/v1",
		Model:   "google/gemma-4-e4b",
		Thinking: config.ThinkingConfig{
			EnableSystemPrompt: "<|think|>",
		},
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}}, types.AgentOptions{Thinking: boolPtr(true)})

	messages, ok := body["messages"].([]map[string]string)
	if !ok || len(messages) != 2 {
		t.Fatalf("expected system and user chat messages, got %#v", body["messages"])
	}
	if messages[0]["role"] != "system" || messages[0]["content"] != "<|think|>" {
		t.Fatalf("expected thinking system prompt, got %#v", messages[0])
	}
}

func TestChatRequestBodyPrependsThinkingSystemPromptToExistingSystemMessage(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "http://127.0.0.1:1234/v1",
		Model:   "google/gemma-4-e4b",
		Thinking: config.ThinkingConfig{
			EnableSystemPrompt: "<|think|>",
		},
	})

	body := provider.chatRequestBody([]types.Message{
		{Role: types.RoleSystem, Text: "Keep replies concise."},
		{Role: types.RoleUser, Text: "hello"},
	}, types.AgentOptions{Thinking: boolPtr(true)})

	messages, ok := body["messages"].([]map[string]string)
	if !ok || len(messages) != 2 {
		t.Fatalf("expected chat messages, got %#v", body["messages"])
	}
	if got := messages[0]["content"]; got != "<|think|>\n\nKeep replies concise." {
		t.Fatalf("expected thinking prompt to prefix system message, got %q", got)
	}
}

func TestNativeLMStudioChatOmitsThinkingParameterPath(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "Qwen/Qwen3-8B",
		Integrations: []string{"mcp/playwright"},
		Thinking: config.ThinkingConfig{
			ParameterPath: "extra_body.chat_template_kwargs.enable_thinking",
			DisableSuffix: "/nothink",
		},
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}}, types.AgentOptions{Thinking: boolPtr(false)})

	if _, exists := body["extra_body"]; exists {
		t.Fatalf("expected native LM Studio body to omit extra_body, got %#v", body["extra_body"])
	}
	input, ok := body["input"].(string)
	if !ok || !strings.Contains(input, "/nothink") {
		t.Fatalf("expected /nothink in native LM Studio input, got %#v", body["input"])
	}
}

func TestNativeLMStudioChatAppliesReasoningToggle(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "google/gemma-4-e4b",
		Integrations: []string{"mcp/playwright"},
		Thinking: config.ThinkingConfig{
			ParameterPath:         "reasoning",
			EnableParameterValue:  "on",
			DisableParameterValue: "off",
		},
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}}, types.AgentOptions{Thinking: boolPtr(false)})

	if value, ok := body["reasoning"].(string); !ok || value != "off" {
		t.Fatalf("expected native LM Studio reasoning=off, got %#v", body["reasoning"])
	}
}

func TestNativeLMStudioChatAppliesThinkingSystemPrompt(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "google/gemma-4-e4b",
		Integrations: []string{"mcp/playwright"},
		Thinking: config.ThinkingConfig{
			EnableSystemPrompt: "<|think|>",
		},
	})

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}}, types.AgentOptions{Thinking: boolPtr(true)})

	if _, exists := body["extra_body"]; exists {
		t.Fatalf("expected native LM Studio body to omit extra_body, got %#v", body["extra_body"])
	}
	input, ok := body["input"].(string)
	if !ok {
		t.Fatalf("expected LM Studio input string, got %#v", body["input"])
	}
	if !strings.Contains(input, "System: <|think|>") {
		t.Fatalf("expected thinking system prompt in native LM Studio input, got %#v", body["input"])
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
			{"type": "tool_call", "provider_info": {"type": "plugin", "plugin_id": "mcp/playwright"}},
			{"type": "message", "content": "Here is what I found."}
		],
		"stats": {
			"input_tokens": 123,
			"total_output_tokens": 45,
			"time_to_first_token_seconds": 1.25
		}
	}`))
	if err != nil {
		t.Fatalf("decode LM Studio chat response: %v", err)
	}
	if resp.Text != "Here is what I found." {
		t.Fatalf("unexpected LM Studio chat text %q", resp.Text)
	}
	if len(resp.Integrations) != 1 || resp.Integrations[0] != "mcp/playwright" {
		t.Fatalf("unexpected LM Studio integrations %#v", resp.Integrations)
	}
	if resp.InputTokens != 123 || resp.OutputTokens != 45 || resp.TotalTokens != 168 {
		t.Fatalf("unexpected LM Studio token stats %#v", resp)
	}
	if resp.TimeToFirst != 1.25 {
		t.Fatalf("unexpected LM Studio ttft %v", resp.TimeToFirst)
	}
}

func TestDecodeLMStudioChatResponseAppendsToolSummaryWhenReplyReferencesAbove(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "qwen/qwen3-4b-2507",
		Integrations: []string{"mcp/yfinance"},
	})

	resp, err := provider.decodeLMStudioChatResponse(strings.NewReader(`{
		"output": [
			{
				"type": "tool_call",
				"tool": "get_option_expirations",
				"provider_info": {"type": "plugin", "plugin_id": "mcp/yfinance"},
				"output": "[{\"type\":\"text\",\"text\":\"2026-04-01\"},{\"type\":\"text\",\"text\":\"2026-04-02\"},{\"type\":\"text\",\"text\":\"2026-04-03\"}]"
			},
			{
				"type": "tool_call",
				"tool": "get_option_chain",
				"provider_info": {"type": "plugin", "plugin_id": "mcp/yfinance"},
				"output": "[{\"type\":\"text\",\"text\":\"{\\n  \\\"symbol\\\": \\\"SPY\\\",\\n  \\\"date\\\": \\\"2026-04-01\\\",\\n  \\\"calls\\\": {\\n    \\\"data\\\": [[1],[2],[3]]\\n  },\\n  \\\"puts\\\": {\\n    \\\"data\\\": [[1],[2]]\\n  }\\n}\"}]"
			},
			{
				"type": "message",
				"content": "The option chain is provided above."
			}
		]
	}`))
	if err != nil {
		t.Fatalf("decode LM Studio chat response: %v", err)
	}
	if !strings.Contains(resp.Text, "Tool results:") {
		t.Fatalf("expected tool results to be appended, got %q", resp.Text)
	}
	if !strings.Contains(resp.Text, "Option expirations: 2026-04-01, 2026-04-02, 2026-04-03") {
		t.Fatalf("expected expiration summary in %q", resp.Text)
	}
	if !strings.Contains(resp.Text, "Option chain for SPY on 2026-04-01: 3 calls, 2 puts.") {
		t.Fatalf("expected option chain summary in %q", resp.Text)
	}
	if len(resp.ToolSummaries) != 2 {
		t.Fatalf("expected tool summaries metadata, got %#v", resp.ToolSummaries)
	}
}

func TestDecodeOpenAIChatResponseIncludesUsage(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "https://api.example.test/v1",
		Model:   "gpt-5-nano",
	})

	resp, err := provider.decodeOpenAIChatResponse(strings.NewReader(`{
		"choices": [
			{"message": {"content": "Hello"}}
		],
		"usage": {
			"prompt_tokens": 11,
			"completion_tokens": 7,
			"total_tokens": 18
		}
	}`))
	if err != nil {
		t.Fatalf("decode openai chat response: %v", err)
	}
	if resp.InputTokens != 11 || resp.OutputTokens != 7 || resp.TotalTokens != 18 {
		t.Fatalf("unexpected OpenAI usage stats %#v", resp)
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

func boolPtr(value bool) *bool {
	return &value
}
