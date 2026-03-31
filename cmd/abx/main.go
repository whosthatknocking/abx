package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
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

	cmdExecutor, err := executor.New(cfg.Command)
	if err != nil {
		log.Fatalf("init command executor: %v", err)
	}

	primaryAgent := openai.New(cfg.Agent.Primary, logger)
	var runtimeAgent agent.Provider = primaryAgent
	if cfg.Agent.Fallback.Provider != "" {
		fallbackAgent := openai.New(cfg.Agent.Fallback, logger)
		runtimeAgent = agent.NewFallback(primaryAgent, fallbackAgent)
	}
	messenger := signalcli.New(cfg.Messaging.SignalCLI, logger)
	svc := handler.NewService(handler.Options{
		Version:   version,
		Config:    cfg,
		Logger:    logger,
		Repo:      repo,
		Auditor:   auditor,
		Messenger: messenger,
		Agent:     runtimeAgent,
		Executor:  cmdExecutor,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Printf("starting abx version=%s provider=%s model=%s database=%s", version, cfg.Agent.Primary.Provider, cfg.Agent.Primary.Model, cfg.Database.Type)
	checkCtx, checkCancel := context.WithTimeout(ctx, 15*time.Second)
	defer checkCancel()
	if err := runtimeAgent.Check(checkCtx); err != nil {
		logger.Printf("agent connection check failed provider=%s model=%s err=%v", cfg.Agent.Primary.Provider, cfg.Agent.Primary.Model, err)
	} else {
		logger.Printf("agent connection established provider=%s model=%s", cfg.Agent.Primary.Provider, cfg.Agent.Primary.Model)
	}
	if err := svc.Start(ctx); err != nil && err != context.Canceled {
		log.Fatalf("run service: %v", err)
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
