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

	if got := p.Evaluate("pwd"); !got.Allowed {
		t.Fatalf("expected pwd to be allowed")
	}
	if got := p.Evaluate("ls"); got.Allowed {
		t.Fatalf("expected ls to be denied by default")
	}
	if got := p.Evaluate("sudo pwd"); got.Allowed {
		t.Fatalf("expected sudo pwd to be denied")
	}
}

func TestPolicyEvaluationCapturesAllowAndDenyMatches(t *testing.T) {
	p, err := newPolicy("allowlist", []config.CommandPolicyRule{
		{
			ID:          "allow-git",
			Enabled:     true,
			Action:      "allow",
			MatchType:   "prefix",
			Pattern:     "git ",
			Description: "allow git commands",
		},
		{
			ID:          "deny-git-push",
			Enabled:     true,
			Action:      "deny",
			MatchType:   "exact",
			Pattern:     "git push",
			Description: "deny git push",
		},
	})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	got := p.Evaluate("git push")
	if got.Allowed {
		t.Fatal("expected git push to be denied")
	}
	if len(got.AllowMatches) != 1 || got.AllowMatches[0] != "allow-git" {
		t.Fatalf("unexpected allow matches: %#v", got.AllowMatches)
	}
	if len(got.DenyMatches) != 1 || got.DenyMatches[0] != "deny-git-push" {
		t.Fatalf("unexpected deny matches: %#v", got.DenyMatches)
	}
}
