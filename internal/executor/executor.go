package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/whosthatknocking/abx/internal/config"
)

type Executor struct {
	timeout time.Duration
	workDir string
	policy  *policy
}

func New(cfg config.CommandConfig) (*Executor, error) {
	if cfg.WorkDir == "" {
		return nil, fmt.Errorf("command.work_dir is required")
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}
	policy, err := newPolicy(cfg.PolicyMode, cfg.Policy.Rules)
	if err != nil {
		return nil, err
	}
	return &Executor{
		timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		workDir: filepath.Clean(cfg.WorkDir),
		policy:  policy,
	}, nil
}

func (e *Executor) Execute(ctx context.Context, command string) (string, error) {
	command = strings.TrimSpace(command)
	allowed, matches := e.policy.Evaluate(command)
	if !allowed {
		return "", fmt.Errorf("command blocked by policy (matched rules: %s)", strings.Join(matches, ", "))
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "/bin/bash", "-c", command)
	cmd.Dir = e.workDir
	out, err := cmd.CombinedOutput()
	if timeoutCtx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("command timed out after %s", e.timeout)
	}
	if err != nil {
		return string(out), fmt.Errorf("command failed: %w", err)
	}
	return string(out), nil
}
