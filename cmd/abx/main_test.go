package main

import (
	"strings"
	"testing"

	"github.com/whosthatknocking/abx/internal/agent"
	"github.com/whosthatknocking/abx/internal/config"
)

func TestBuildInfoTextIncludesCommitAndBuildDate(t *testing.T) {
	originalCommit := buildCommit
	originalDate := buildDate
	buildCommit = "abc123"
	buildDate = "2026-04-02T20:00:00Z"
	defer func() {
		buildCommit = originalCommit
		buildDate = originalDate
	}()

	got := buildInfoText()
	if got != "commit=abc123 built=2026-04-02T20:00:00Z" {
		t.Fatalf("unexpected build info text %q", got)
	}
}

func TestCleanBuildValueOmitsUnknownAndWhitespace(t *testing.T) {
	for _, value := range []string{"", "unknown", "  unknown  "} {
		if got := cleanBuildValue(value); got != "" {
			t.Fatalf("expected %q to normalize to empty string, got %q", value, got)
		}
	}
	if got := cleanBuildValue(" abc123 "); got != "abc123" {
		t.Fatalf("unexpected cleaned build value %q", got)
	}
}

func TestNewRepositorySupportsInMemoryAndRejectsUnsupportedType(t *testing.T) {
	repo, err := newRepository(&config.Config{
		Database: config.DatabaseConfig{Type: "inmemory"},
	})
	if err != nil {
		t.Fatalf("newRepository inmemory: %v", err)
	}
	if repo == nil {
		t.Fatal("expected repository")
	}

	_, err = newRepository(&config.Config{
		Database: config.DatabaseConfig{Type: "nope"},
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported database.type "nope"`) {
		t.Fatalf("unexpected unsupported database error: %v", err)
	}
}

func TestBuildRuntimeReturnsPrimaryOnlyWhenFallbackNotConfigured(t *testing.T) {
	runtimeAgent, exec, err := buildRuntime(&config.Config{
		Agent: config.AgentConfig{
			Primary: config.ProviderConfig{Provider: "openai", Model: "gpt-4o-mini"},
		},
		Command: config.CommandConfig{
			WorkDir:    t.TempDir(),
			PolicyMode: "allowlist",
			Policy: config.CommandPolicyConfig{
				Rules: []config.CommandPolicyRule{{
					ID:          "allow-pwd",
					Enabled:     true,
					Action:      "allow",
					MatchType:   "exact",
					Pattern:     "pwd",
					Description: "allow pwd",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildRuntime primary only: %v", err)
	}
	if exec == nil {
		t.Fatal("expected executor")
	}
	if _, ok := runtimeAgent.(agent.Switcher); ok {
		t.Fatal("did not expect switchable fallback agent when fallback is not configured")
	}
}

func TestBuildRuntimeReturnsSwitchableAgentWhenFallbackConfigured(t *testing.T) {
	runtimeAgent, _, err := buildRuntime(&config.Config{
		Agent: config.AgentConfig{
			Primary:  config.ProviderConfig{Provider: "openai", Model: "primary"},
			Fallback: config.ProviderConfig{Provider: "openai", Model: "fallback"},
		},
		Command: config.CommandConfig{
			WorkDir:    t.TempDir(),
			PolicyMode: "allowlist",
			Policy: config.CommandPolicyConfig{
				Rules: []config.CommandPolicyRule{{
					ID:          "allow-pwd",
					Enabled:     true,
					Action:      "allow",
					MatchType:   "exact",
					Pattern:     "pwd",
					Description: "allow pwd",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildRuntime with fallback: %v", err)
	}
	if _, ok := runtimeAgent.(agent.Switcher); !ok {
		t.Fatal("expected switchable fallback agent when fallback is configured")
	}
}
