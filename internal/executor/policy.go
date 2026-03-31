package executor

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/whosthatknocking/abx/internal/config"
)

type compiledRule struct {
	config.CommandPolicyRule
	regex *regexp.Regexp
}

type policy struct {
	mode  string
	rules []compiledRule
}

type evaluation struct {
	Allowed      bool
	AllowMatches []string
	DenyMatches  []string
}

func newPolicy(mode string, rules []config.CommandPolicyRule) (*policy, error) {
	seen := map[string]struct{}{}
	compiled := make([]compiledRule, 0, len(rules))
	enabledAllows := 0
	for _, rule := range rules {
		if rule.ID == "" || rule.Action == "" || rule.MatchType == "" || rule.Pattern == "" || rule.Description == "" {
			return nil, fmt.Errorf("policy rule missing required fields: %+v", rule)
		}
		if _, ok := seen[rule.ID]; ok {
			return nil, fmt.Errorf("duplicate rule id %q", rule.ID)
		}
		seen[rule.ID] = struct{}{}
		if !rule.Enabled {
			continue
		}
		cr := compiledRule{CommandPolicyRule: rule}
		switch rule.MatchType {
		case "exact", "prefix", "contains":
		case "regex":
			rx, err := regexp.Compile(rule.Pattern)
			if err != nil {
				return nil, fmt.Errorf("compile regex for rule %q: %w", rule.ID, err)
			}
			cr.regex = rx
		default:
			return nil, fmt.Errorf("unknown match_type %q for rule %q", rule.MatchType, rule.ID)
		}
		if rule.Action != "allow" && rule.Action != "deny" {
			return nil, fmt.Errorf("unknown action %q for rule %q", rule.Action, rule.ID)
		}
		if rule.Action == "allow" {
			enabledAllows++
		}
		compiled = append(compiled, cr)
	}

	if mode == "" {
		mode = "allowlist"
	}
	if mode == "allowlist" && enabledAllows == 0 {
		return nil, fmt.Errorf("allowlist mode requires at least one enabled allow rule")
	}
	return &policy{mode: mode, rules: compiled}, nil
}

func (p *policy) Evaluate(command string) evaluation {
	result := evaluation{}
	for _, rule := range p.rules {
		if !rule.matches(command) {
			continue
		}
		if rule.Action == "deny" {
			result.DenyMatches = append(result.DenyMatches, rule.ID)
		}
		if rule.Action == "allow" {
			result.AllowMatches = append(result.AllowMatches, rule.ID)
		}
	}
	if len(result.DenyMatches) > 0 {
		return result
	}
	if p.mode == "allowlist" {
		result.Allowed = len(result.AllowMatches) > 0
		return result
	}
	result.Allowed = len(result.AllowMatches) > 0
	return result
}

func (r compiledRule) matches(command string) bool {
	switch r.MatchType {
	case "exact":
		return command == r.Pattern
	case "prefix":
		return strings.HasPrefix(command, r.Pattern)
	case "contains":
		return strings.Contains(command, r.Pattern)
	case "regex":
		return r.regex.MatchString(command)
	default:
		return false
	}
}
