package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/whosthatknocking/abx/internal/agent"
	"github.com/whosthatknocking/abx/internal/agent/openai"
	"github.com/whosthatknocking/abx/internal/audit"
	"github.com/whosthatknocking/abx/internal/config"
	"github.com/whosthatknocking/abx/internal/executor"
	"github.com/whosthatknocking/abx/internal/handler"
	"github.com/whosthatknocking/abx/internal/messenger/signalcli"
	"github.com/whosthatknocking/abx/internal/repository"
	"github.com/whosthatknocking/abx/internal/repository/inmemory"
	"github.com/whosthatknocking/abx/internal/repository/sqlite"
)

var version = "dev"
var buildCommit = ""
var buildDate = ""

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to config.toml")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logger := log.New(os.Stdout, "abx: ", log.LstdFlags|log.Lmsgprefix)

	auditor, err := audit.New(cfg.Audit)
	if err != nil {
		log.Fatalf("init audit logger: %v", err)
	}
	defer auditor.Close()

	repo, err := newRepository(cfg)
	if err != nil {
		log.Fatalf("init repository: %v", err)
	}

	runtimeAgent, cmdExecutor, err := buildRuntime(cfg)
	if err != nil {
		log.Fatalf("init runtime: %v", err)
	}
	messenger := signalcli.New(cfg.Messaging.SignalCLI, logger)
	svc := handler.NewService(handler.Options{
		Version:   version,
		BuildInfo: buildInfoText(),
		Config:    cfg,
		Logger:    logger,
		Repo:      repo,
		Auditor:   auditor,
		Messenger: messenger,
		Agent:     runtimeAgent,
		Executor:  cmdExecutor,
		Reload: func(ctx context.Context) (*handler.ReloadAgents, error) {
			nextCfg, err := config.Load(configPath)
			if err != nil {
				return nil, err
			}
			nextAgent, _, err := buildRuntime(nextCfg)
			if err != nil {
				return nil, err
			}
			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := nextAgent.Check(checkCtx); err != nil {
				return nil, fmt.Errorf("check reloaded agents: %w", err)
			}
			updatedCfg := *cfg
			updatedCfg.Agent = nextCfg.Agent
			updatedCfg.MCP = nextCfg.MCP
			cfg = &updatedCfg
			return &handler.ReloadAgents{
				Config: cfg,
				Agent:  nextAgent,
			}, nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		sig := <-sigCh
		logger.Printf("shutdown signal received signal=%s", sig)
		cancel()
		sig = <-sigCh
		logger.Printf("second shutdown signal received signal=%s forcing exit", sig)
		os.Exit(1)
	}()

	logger.Printf("starting abx version=%s provider=%s model=%s database=%s", version, cfg.Agent.Primary.Provider, cfg.Agent.Primary.Model, cfg.Database.Type)
	checkCtx, checkCancel := context.WithTimeout(ctx, 15*time.Second)
	defer checkCancel()
	if err := runtimeAgent.Check(checkCtx); err != nil {
		logger.Printf("agent connection check failed provider=%s model=%s err=%v", cfg.Agent.Primary.Provider, cfg.Agent.Primary.Model, err)
	} else {
		logger.Printf("agent connection established provider=%s model=%s", cfg.Agent.Primary.Provider, cfg.Agent.Primary.Model)
	}
	if err := svc.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("run service: %v", err)
	}
	logger.Printf("shutdown complete")
}

func buildInfoText() string {
	parts := make([]string, 0, 2)
	if value := cleanBuildValue(buildCommit); value != "" {
		parts = append(parts, "commit="+value)
	}
	if value := cleanBuildValue(buildDate); value != "" {
		parts = append(parts, "built="+value)
	}
	return strings.Join(parts, " ")
}

func cleanBuildValue(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "", "unknown":
		return ""
	default:
		return value
	}
}

func newRepository(cfg *config.Config) (repository.Repository, error) {
	switch cfg.Database.Type {
	case "", "sqlite":
		return sqlite.New(cfg.Database.DSN)
	case "inmemory":
		return inmemory.New(), nil
	default:
		return nil, fmt.Errorf("unsupported database.type %q", cfg.Database.Type)
	}
}

func buildRuntime(cfg *config.Config) (agent.Provider, *executor.Executor, error) {
	cmdExecutor, err := executor.New(cfg.Command)
	if err != nil {
		return nil, nil, err
	}
	primaryAgent := openai.New(cfg.Agent.Primary)
	var runtimeAgent agent.Provider = primaryAgent
	if cfg.Agent.Fallback.Provider != "" {
		fallbackAgent := openai.New(cfg.Agent.Fallback)
		runtimeAgent = agent.NewFallback(primaryAgent, fallbackAgent)
	}
	return runtimeAgent, cmdExecutor, nil
}
