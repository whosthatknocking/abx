package executor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/whosthatknocking/abx/internal/config"
)

func TestNewCreatesWorkDirAndBuildsExecutor(t *testing.T) {
	workDir := t.TempDir() + "/nested/work"
	exec, err := New(config.CommandConfig{
		TimeoutSeconds: 1,
		WorkDir:        workDir,
		PolicyMode:     "allowlist",
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
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if exec == nil {
		t.Fatal("expected executor")
	}
}

func TestCheckRejectsCommandsOutsideAllowlist(t *testing.T) {
	exec, err := New(config.CommandConfig{
		TimeoutSeconds: 1,
		WorkDir:        t.TempDir(),
		PolicyMode:     "allowlist",
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
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := exec.Check("ls"); err == nil || !strings.Contains(err.Error(), "no allow rule matched") {
		t.Fatalf("unexpected Check error: %v", err)
	}
}

func TestExecuteRunsAllowedCommand(t *testing.T) {
	workDir := t.TempDir()
	exec, err := New(config.CommandConfig{
		TimeoutSeconds: 1,
		WorkDir:        workDir,
		PolicyMode:     "allowlist",
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
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := exec.Execute(context.Background(), "pwd")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := strings.TrimSpace(out); got != workDir {
		t.Fatalf("unexpected output %q want %q", got, workDir)
	}
}

func TestExecuteReturnsCommandFailureOutput(t *testing.T) {
	exec, err := New(config.CommandConfig{
		TimeoutSeconds: 1,
		WorkDir:        t.TempDir(),
		PolicyMode:     "allowlist",
		Policy: config.CommandPolicyConfig{
			Rules: []config.CommandPolicyRule{{
				ID:          "allow-sh",
				Enabled:     true,
				Action:      "allow",
				MatchType:   "exact",
				Pattern:     "bash -c 'echo fail && exit 7'",
				Description: "allow failing shell",
			}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := exec.Execute(context.Background(), "bash -c 'echo fail && exit 7'")
	if err == nil || !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("unexpected Execute error: %v", err)
	}
	if !strings.Contains(out, "fail") {
		t.Fatalf("expected command output to be returned, got %q", out)
	}
}

func TestExecuteReturnsTimeoutError(t *testing.T) {
	exec, err := New(config.CommandConfig{
		TimeoutSeconds: 1,
		WorkDir:        t.TempDir(),
		PolicyMode:     "allowlist",
		Policy: config.CommandPolicyConfig{
			Rules: []config.CommandPolicyRule{{
				ID:          "allow-sleep",
				Enabled:     true,
				Action:      "allow",
				MatchType:   "exact",
				Pattern:     "sleep 2",
				Description: "allow sleep",
			}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	_, err = exec.Execute(context.Background(), "sleep 2")
	if err == nil || !strings.Contains(err.Error(), "command timed out") {
		t.Fatalf("unexpected Execute timeout error: %v", err)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("expected timeout path to wait roughly for configured timeout")
	}
}
