package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/velox0/kraken/internal/config"
	"github.com/velox0/kraken/internal/db"
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

	runner := &services.Scheduler{
		Store: store,
		Queue: q,
		Tick:  time.Duration(cfg.SchedulerTickSec) * time.Second,
		Log:   log.Default(),
	}
	if err := runner.Validate(); err != nil {
		return fmt.Errorf("scheduler config invalid: %w", err)
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
