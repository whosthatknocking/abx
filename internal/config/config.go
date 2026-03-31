package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Messaging MessagingConfig
	Agent     AgentConfig
	Security  SecurityConfig
	Audit     AuditConfig
	Database  DatabaseConfig
	Command   CommandConfig
}

type MessagingConfig struct {
	Provider  string
	SignalCLI SignalCLIConfig
}

type SignalCLIConfig struct {
	BinaryPath string
	Account    string
	DataDir    string
	RPCMode    string
	RPCSocket  string
	RPCHost    string
	RPCPort    int
}

type AgentConfig struct {
	Primary  ProviderConfig
	Fallback ProviderConfig
}

type ProviderConfig struct {
	Provider string
	APIKey   string
	Model    string
	BaseURL  string
}

type SecurityConfig struct {
	TrustedNumbers []string
}

type AuditConfig struct {
	FilePath       string
	RetentionDays  int
	MaxOutputBytes int
}

type DatabaseConfig struct {
	Type string
	DSN  string
}

type CommandConfig struct {
	TimeoutSeconds int
	WorkDir        string
	PolicyMode     string
	Policy         CommandPolicyConfig
}

type CommandPolicyConfig struct {
	Rules []CommandPolicyRule
}

type CommandPolicyRule struct {
	ID          string
	Enabled     bool
	Action      string
	MatchType   string
	Pattern     string
	Description string
}

func Load(path string) (*Config, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		path = filepath.Join(home, ".config", "abx", "config.toml")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	tree, err := parseTOML(string(raw))
	if err != nil {
		return nil, err
	}

	cfg, err := decodeConfig(tree)
	if err != nil {
		return nil, err
	}

	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) normalize() error {
	c.Messaging.SignalCLI.BinaryPath = expandHome(c.Messaging.SignalCLI.BinaryPath)
	c.Messaging.SignalCLI.DataDir = expandHome(c.Messaging.SignalCLI.DataDir)
	c.Messaging.SignalCLI.RPCSocket = expandHome(c.Messaging.SignalCLI.RPCSocket)
	c.Audit.FilePath = expandHome(c.Audit.FilePath)
	c.Database.DSN = expandHome(c.Database.DSN)
	c.Command.WorkDir = expandHome(c.Command.WorkDir)

	if c.Messaging.Provider == "" {
		c.Messaging.Provider = "signal-cli"
	}
	if c.Messaging.SignalCLI.RPCMode == "" {
		c.Messaging.SignalCLI.RPCMode = "json-rpc"
	}
	if c.Database.Type == "" {
		c.Database.Type = "sqlite"
	}
	if c.Command.TimeoutSeconds <= 0 {
		c.Command.TimeoutSeconds = 60
	}
	if c.Command.PolicyMode == "" {
		c.Command.PolicyMode = "allowlist"
	}
	if c.Audit.RetentionDays <= 0 {
		c.Audit.RetentionDays = 30
	}
	if c.Audit.MaxOutputBytes <= 0 {
		c.Audit.MaxOutputBytes = 8192
	}

	if c.Agent.Primary.Provider == "openai" && c.Agent.Primary.Model == "" {
		return errors.New("agent.primary.model is required for openai")
	}
	if len(c.Security.TrustedNumbers) == 0 {
		return errors.New("security.trusted_numbers must not be empty")
	}
	return nil
}

func expandHome(v string) string {
	if v == "" || !strings.HasPrefix(v, "~/") {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return v
	}
	return filepath.Join(home, strings.TrimPrefix(v, "~/"))
}
