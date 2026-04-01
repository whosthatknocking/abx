package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfigRules(t *testing.T) {
	input := `
[messaging]
provider = "signal-cli"

[messaging.signal_cli]
binary_path = "/bin/signal-cli"
account = "+15551234567"
data_dir = "~/.local/share/signal-cli"
rpc_mode = "json-rpc"
rpc_socket = "~/.local/share/signal-cli/rpc.sock"

[agent.primary]
provider = "openai"
api_key = "key"
model = "gpt-4o-mini"
request_timeout_seconds = 180

[agent.fallback]
provider = "openai"
model = "gpt-5-nano"
request_timeout_seconds = 45

[[mcp.servers]]
name = "mcp/playwright"
enabled = true

[[mcp.servers]]
name = "mcp/weather"
enabled = false

[debug]
enabled = true

[security]
trusted_numbers = [
  "+1",
  "+2"
]

[database]
type = "inmemory"
dsn = "ignored"

[command]
timeout_seconds = 60
work_dir = "~/abx/workspace"
policy_mode = "allowlist"

[[command.policy.rules]]
id = "allow-pwd"
enabled = true
action = "allow"
match_type = "exact"
pattern = "pwd"
description = "test"
`

	tree, err := parseTOML(input)
	if err != nil {
		t.Fatalf("parse toml: %v", err)
	}
	cfg, err := decodeConfig(tree)
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if got := len(cfg.Command.Policy.Rules); got != 1 {
		t.Fatalf("expected 1 rule, got %d", got)
	}
	if cfg.Command.Policy.Rules[0].ID != "allow-pwd" {
		t.Fatalf("unexpected rule id %q", cfg.Command.Policy.Rules[0].ID)
	}
	if !cfg.Debug.Enabled {
		t.Fatalf("expected debug.enabled to be true")
	}
	if got := len(cfg.MCP.Servers); got != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", got)
	}
	if got := len(cfg.Agent.Primary.Integrations); got != 1 {
		t.Fatalf("expected 1 enabled integration, got %d", got)
	}
	if cfg.Agent.Primary.Integrations[0] != "mcp/playwright" {
		t.Fatalf("unexpected integration %q", cfg.Agent.Primary.Integrations[0])
	}
	if cfg.Agent.Primary.RequestTimeoutSeconds != 180 {
		t.Fatalf("unexpected primary request timeout %d", cfg.Agent.Primary.RequestTimeoutSeconds)
	}
	if cfg.Agent.Fallback.RequestTimeoutSeconds != 45 {
		t.Fatalf("unexpected fallback request timeout %d", cfg.Agent.Fallback.RequestTimeoutSeconds)
	}
}

func TestLoadCreatesDefaultConfigDirectory(t *testing.T) {
	tmpHome := t.TempDir()
	originalHome, hadHome := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", tmpHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	defer func() {
		if hadHome {
			_ = os.Setenv("HOME", originalHome)
			return
		}
		_ = os.Unsetenv("HOME")
	}()

	configDir := filepath.Join(tmpHome, ".config", "abx")
	configPath := filepath.Join(configDir, "config.toml")
	if _, err := Load(""); err == nil {
		t.Fatal("expected missing config file error")
	}
	if _, err := os.Stat(configDir); err != nil {
		t.Fatalf("expected config dir to be created: %v", err)
	}
	input := `
[agent.primary]
provider = "openai"
model = "gpt-4o-mini"

[security]
trusted_numbers = ["+1"]

[database]
type = "inmemory"
dsn = "ignored"

[command]
work_dir = "~/abx/workspace"

[audit]
file_path = "~/abx/audit.log"

[[command.policy.rules]]
id = "allow-pwd"
enabled = true
action = "allow"
match_type = "exact"
pattern = "pwd"
description = "test"
`

	if err := os.WriteFile(configPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config")
	}
}
