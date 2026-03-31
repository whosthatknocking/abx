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

func (p *policy) Evaluate(command string) (allowed bool, matches []string) {
	matchedAllow := false
	matchedDeny := false
	for _, rule := range p.rules {
		if !rule.matches(command) {
			continue
		}
		matches = append(matches, rule.ID)
		if rule.Action == "deny" {
			matchedDeny = true
		}
		if rule.Action == "allow" {
			matchedAllow = true
		}
	}
	if matchedDeny {
		return false, matches
	}
	if p.mode == "allowlist" {
		return matchedAllow, matches
	}
	return matchedAllow || !matchedDeny, matches
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
