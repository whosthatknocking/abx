package executor

import (
	"testing"

	"github.com/whosthatknocking/abx/internal/config"
)

func TestPolicyAllowlist(t *testing.T) {
	p, err := newPolicy("allowlist", []config.CommandPolicyRule{
		{
			ID:          "allow-pwd",
			Enabled:     true,
			Action:      "allow",
			MatchType:   "exact",
			Pattern:     "pwd",
			Description: "allow pwd",
		},
		{
			ID:          "deny-sudo",
			Enabled:     true,
			Action:      "deny",
			MatchType:   "prefix",
			Pattern:     "sudo ",
			Description: "deny sudo",
		},
	})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	if ok, _ := p.Evaluate("pwd"); !ok {
		t.Fatalf("expected pwd to be allowed")
	}
	if ok, _ := p.Evaluate("ls"); ok {
		t.Fatalf("expected ls to be denied by default")
	}
	if ok, _ := p.Evaluate("sudo pwd"); ok {
		t.Fatalf("expected sudo pwd to be denied")
	}
}
