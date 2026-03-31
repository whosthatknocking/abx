package config

import "testing"

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
}
