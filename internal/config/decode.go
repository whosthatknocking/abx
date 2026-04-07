package config

import (
	"fmt"
)

func decodeConfig(tree map[string]any) (*Config, error) {
	cfg := &Config{}

	messaging := childMap(tree, "messaging")
	cfg.Messaging.Provider = stringValue(messaging, "provider")
	signal := childMap(messaging, "signal_cli")
	cfg.Messaging.SignalCLI = SignalCLIConfig{
		BinaryPath: stringValue(signal, "binary_path"),
		Account:    stringValue(signal, "account"),
		DataDir:    stringValue(signal, "data_dir"),
		RPCMode:    stringValue(signal, "rpc_mode"),
		RPCSocket:  stringValue(signal, "rpc_socket"),
		RPCHost:    stringValue(signal, "rpc_host"),
		RPCPort:    intValue(signal, "rpc_port"),
	}

	agent := childMap(tree, "agent")
	cfg.Agent.Primary = decodeProvider(childMap(agent, "primary"))
	cfg.Agent.Fallback = decodeProvider(childMap(agent, "fallback"))

	mcp := childMap(tree, "mcp")
	for _, item := range childArrayMap(mcp, "servers") {
		cfg.MCP.Servers = append(cfg.MCP.Servers, MCPServerConfig{
			Name:    stringValue(item, "name"),
			Enabled: boolValueDefault(item, "enabled", true),
		})
	}

	debug := childMap(tree, "debug")
	cfg.Debug = DebugConfig{
		Enabled: boolValueDefault(debug, "enabled", false),
	}

	security := childMap(tree, "security")
	cfg.Security.TrustedNumbers = stringSliceValue(security, "trusted_numbers")

	audit := childMap(tree, "audit")
	cfg.Audit = AuditConfig{
		FilePath:       stringValue(audit, "file_path"),
		RetentionDays:  intValue(audit, "retention_days"),
		MaxOutputBytes: intValue(audit, "max_output_bytes"),
	}

	database := childMap(tree, "database")
	cfg.Database = DatabaseConfig{
		Type: stringValue(database, "type"),
		DSN:  stringValue(database, "dsn"),
	}

	command := childMap(tree, "command")
	cfg.Command = CommandConfig{
		TimeoutSeconds: intValue(command, "timeout_seconds"),
		WorkDir:        stringValue(command, "work_dir"),
		PolicyMode:     stringValue(command, "policy_mode"),
	}

	rulesAny := childArrayMap(command, "policy", "rules")
	for _, item := range rulesAny {
		cfg.Command.Policy.Rules = append(cfg.Command.Policy.Rules, CommandPolicyRule{
			ID:          stringValue(item, "id"),
			Enabled:     boolValueDefault(item, "enabled", true),
			Action:      stringValue(item, "action"),
			MatchType:   stringValue(item, "match_type"),
			Pattern:     stringValue(item, "pattern"),
			Description: stringValue(item, "description"),
		})
	}

	return cfg, nil
}

func decodeProvider(m map[string]any) ProviderConfig {
	thinking := childMap(m, "thinking")
	defaultMode := stringValue(m, "thinking_default")
	if defaultMode == "" {
		defaultMode = stringValue(thinking, "default")
	}
	parameterPath := stringValue(m, "thinking_parameter_path")
	if parameterPath == "" {
		parameterPath = stringValue(thinking, "parameter_path")
	}
	enableSuffix := stringValue(m, "thinking_enable_suffix")
	if enableSuffix == "" {
		enableSuffix = stringValue(thinking, "enable_suffix")
	}
	disableSuffix := stringValue(m, "thinking_disable_suffix")
	if disableSuffix == "" {
		disableSuffix = stringValue(thinking, "disable_suffix")
	}
	enableSystemPrompt := stringValue(m, "thinking_enable_system_prompt")
	if enableSystemPrompt == "" {
		enableSystemPrompt = stringValue(thinking, "enable_system_prompt")
	}
	disableSystemPrompt := stringValue(m, "thinking_disable_system_prompt")
	if disableSystemPrompt == "" {
		disableSystemPrompt = stringValue(thinking, "disable_system_prompt")
	}
	return ProviderConfig{
		Provider:              stringValue(m, "provider"),
		APIKey:                stringValue(m, "api_key"),
		Model:                 stringValue(m, "model"),
		BaseURL:               stringValue(m, "base_url"),
		RequestTimeoutSeconds: intValue(m, "request_timeout_seconds"),
		Thinking: ThinkingConfig{
			DefaultMode:         defaultMode,
			ParameterPath:       parameterPath,
			EnableSuffix:        enableSuffix,
			DisableSuffix:       disableSuffix,
			EnableSystemPrompt:  enableSystemPrompt,
			DisableSystemPrompt: disableSystemPrompt,
		},
	}
}

func childMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	v, ok := m[key]
	if !ok {
		return map[string]any{}
	}
	mv, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return mv
}

func childArrayMap(m map[string]any, keys ...string) []map[string]any {
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			v, ok := current[key]
			if !ok {
				return nil
			}
			items, ok := v.([]any)
			if !ok {
				return nil
			}
			out := make([]map[string]any, 0, len(items))
			for _, item := range items {
				if mv, ok := item.(map[string]any); ok {
					out = append(out, mv)
				}
			}
			return out
		}
		current = childMap(current, key)
	}
	return nil
}

func stringValue(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprint(v)
}

func intValue(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func boolValueDefault(m map[string]any, key string, defaultValue bool) bool {
	v, ok := m[key]
	if !ok {
		return defaultValue
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return defaultValue
}

func stringSliceValue(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
