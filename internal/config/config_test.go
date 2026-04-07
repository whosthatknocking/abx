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

func TestDecodeProviderThinkingFlatKeys(t *testing.T) {
	input := `
[agent.primary]
provider = "openai"
model = "qwen/qwen3-8b"
thinking_default = "enabled"
thinking_parameter_path = "extra_body.chat_template_kwargs.enable_thinking"
thinking_enable_parameter_value = "on"
thinking_disable_parameter_value = "off"
thinking_enable_suffix = "/think"
thinking_disable_suffix = "/no_think"
thinking_enable_system_prompt = "<|think|>"
thinking_disable_system_prompt = "<|no_think|>"

[security]
trusted_numbers = ["+1"]

[database]
type = "inmemory"
dsn = "ignored"

[command]
work_dir = "~/abx/workspace"

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
	if cfg.Agent.Primary.Thinking.DefaultMode != "enabled" {
		t.Fatalf("unexpected thinking default %q", cfg.Agent.Primary.Thinking.DefaultMode)
	}
	if cfg.Agent.Primary.Thinking.ParameterPath != "extra_body.chat_template_kwargs.enable_thinking" {
		t.Fatalf("unexpected thinking parameter path %q", cfg.Agent.Primary.Thinking.ParameterPath)
	}
	if cfg.Agent.Primary.Thinking.EnableParameterValue != "on" {
		t.Fatalf("unexpected thinking enable parameter value %q", cfg.Agent.Primary.Thinking.EnableParameterValue)
	}
	if cfg.Agent.Primary.Thinking.DisableParameterValue != "off" {
		t.Fatalf("unexpected thinking disable parameter value %q", cfg.Agent.Primary.Thinking.DisableParameterValue)
	}
	if cfg.Agent.Primary.Thinking.EnableSuffix != "/think" {
		t.Fatalf("unexpected thinking enable suffix %q", cfg.Agent.Primary.Thinking.EnableSuffix)
	}
	if cfg.Agent.Primary.Thinking.DisableSuffix != "/no_think" {
		t.Fatalf("unexpected thinking disable suffix %q", cfg.Agent.Primary.Thinking.DisableSuffix)
	}
	if cfg.Agent.Primary.Thinking.EnableSystemPrompt != "<|think|>" {
		t.Fatalf("unexpected thinking enable system prompt %q", cfg.Agent.Primary.Thinking.EnableSystemPrompt)
	}
	if cfg.Agent.Primary.Thinking.DisableSystemPrompt != "<|no_think|>" {
		t.Fatalf("unexpected thinking disable system prompt %q", cfg.Agent.Primary.Thinking.DisableSystemPrompt)
	}
}

func TestDecodeProviderThinkingNestedKeysRemainSupported(t *testing.T) {
	input := `
[agent.primary]
provider = "openai"
model = "qwen/qwen3-8b"

[agent.primary.thinking]
default = "disabled"
parameter_path = "extra_body.enable_thinking"
enable_parameter_value = "on"
disable_parameter_value = "off"
enable_suffix = "/think"
disable_suffix = "/no_think"
enable_system_prompt = "<|think|>"
disable_system_prompt = "<|no_think|>"

[security]
trusted_numbers = ["+1"]

[database]
type = "inmemory"
dsn = "ignored"

[command]
work_dir = "~/abx/workspace"

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
	if cfg.Agent.Primary.Thinking.DefaultMode != "disabled" {
		t.Fatalf("unexpected thinking default %q", cfg.Agent.Primary.Thinking.DefaultMode)
	}
	if cfg.Agent.Primary.Thinking.ParameterPath != "extra_body.enable_thinking" {
		t.Fatalf("unexpected thinking parameter path %q", cfg.Agent.Primary.Thinking.ParameterPath)
	}
	if cfg.Agent.Primary.Thinking.EnableParameterValue != "on" {
		t.Fatalf("unexpected thinking enable parameter value %q", cfg.Agent.Primary.Thinking.EnableParameterValue)
	}
	if cfg.Agent.Primary.Thinking.DisableParameterValue != "off" {
		t.Fatalf("unexpected thinking disable parameter value %q", cfg.Agent.Primary.Thinking.DisableParameterValue)
	}
	if cfg.Agent.Primary.Thinking.EnableSystemPrompt != "<|think|>" {
		t.Fatalf("unexpected thinking enable system prompt %q", cfg.Agent.Primary.Thinking.EnableSystemPrompt)
	}
	if cfg.Agent.Primary.Thinking.DisableSystemPrompt != "<|no_think|>" {
		t.Fatalf("unexpected thinking disable system prompt %q", cfg.Agent.Primary.Thinking.DisableSystemPrompt)
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

func TestNormalizeRejectsUnsupportedPolicyMode(t *testing.T) {
	cfg := &Config{
		Agent: AgentConfig{
			Primary: ProviderConfig{
				Provider: "openai",
				Model:    "gpt-4o-mini",
			},
		},
		Security: SecurityConfig{TrustedNumbers: []string{"+1"}},
		Command: CommandConfig{
			WorkDir:    "~/abx/workspace",
			PolicyMode: "denylist",
		},
	}

	err := cfg.normalize()
	if err == nil {
		t.Fatal("expected unsupported policy mode error")
	}
	if got := err.Error(); got != `command.policy_mode must be "allowlist"` {
		t.Fatalf("unexpected error: %q", got)
	}
}
