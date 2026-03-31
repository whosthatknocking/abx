package openai

import (
	"io"
	"log"
	"testing"

	"github.com/whosthatknocking/abx/internal/config"
	"github.com/whosthatknocking/abx/pkg/types"
)

func TestChatRequestBodyIncludesIntegrationsForLocalEndpoint(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL:      "http://127.0.0.1:1234/v1",
		Model:        "mistralai/magistral-small-2509",
		Integrations: []string{"mcp/playwright"},
	}, log.New(io.Discard, "", 0))

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}})

	integrations, ok := body["integrations"].([]string)
	if !ok || len(integrations) != 1 || integrations[0] != "mcp/playwright" {
		t.Fatalf("expected integrations to include mcp/playwright, got %#v", body["integrations"])
	}
}

func TestChatRequestBodyOmitsIntegrationsForRemoteEndpoint(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "https://api.example.test/v1",
		Model:   "gpt-5-nano",
	}, log.New(io.Discard, "", 0))

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}})

	if _, exists := body["integrations"]; exists {
		t.Fatalf("expected integrations to be omitted for remote endpoint, got %#v", body["integrations"])
	}
}

func TestChatRequestBodyOmitsIntegrationsWhenNoServersAreEnabled(t *testing.T) {
	provider := New(config.ProviderConfig{
		BaseURL: "http://127.0.0.1:1234/v1",
		Model:   "mistralai/magistral-small-2509",
	}, log.New(io.Discard, "", 0))

	body := provider.chatRequestBody([]types.Message{{
		Role: types.RoleUser,
		Text: "hello",
	}})

	if _, exists := body["integrations"]; exists {
		t.Fatalf("expected integrations to be omitted when no MCP servers are enabled, got %#v", body["integrations"])
	}
}
