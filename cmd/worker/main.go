package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/velox0/kraken/internal/autofix"
	"github.com/velox0/kraken/internal/config"
	"github.com/velox0/kraken/internal/db"
	"github.com/velox0/kraken/internal/incident"
	"github.com/velox0/kraken/internal/queue"
	"github.com/velox0/kraken/internal/services"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	ctx := context.Background()

	store, err := db.New(ctx, cfg.PostgresURL)
	if err != nil {
		return fmt.Errorf("postgres unavailable: %w", err)
	}
	defer store.Close()

	q := queue.NewRedis(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	defer q.Close()
	if err := q.Ping(ctx); err != nil {
		return fmt.Errorf("redis unavailable: %w", err)
	}

	autofixEngine := autofix.NewEngine(cfg.FixScriptsDir, cfg.AllowedFixCommands, cfg.AllowedFixTools)
	autofixEngine.SyncToolsDir()
	incSvc := incident.NewService(store, q, autofixEngine, time.Duration(cfg.AlertCooldownSec)*time.Second, incident.EmailConfig{
		Host: cfg.EmailHost,
		Port: cfg.EmailPort,
		User: cfg.EmailUser,
		Pass: cfg.EmailPass,
		From: cfg.EmailFrom,
	})

	runner := &services.Worker{
		Store:         store,
		Queue:         q,
		AutofixEngine: autofixEngine,
		Incident:      incSvc,
		Log:           log.Default(),
	}
	if err := runner.Validate(); err != nil {
		return fmt.Errorf("worker config invalid: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
	}()

	runner.Run(runCtx)
	return nil
}
