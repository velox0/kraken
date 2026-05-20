package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/velox0/kraken/internal/api"
	"github.com/velox0/kraken/internal/autofix"
	"github.com/velox0/kraken/internal/config"
	"github.com/velox0/kraken/internal/db"
	"github.com/velox0/kraken/internal/incident"
	"github.com/velox0/kraken/internal/logbuf"
	"github.com/velox0/kraken/internal/notifier"
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

	// Shared log buffer — feeds the frontend console.
	lb := logbuf.New()
	logger := lb.Logger(os.Stderr)

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

	autofixCrypto, err := autofix.NewCrypto(cfg.FixEnvSecret)
	if err != nil {
		return fmt.Errorf("fix env crypto init: %w", err)
	}
	if autofixCrypto != nil {
		store.SetFixEnvCrypto(autofixCrypto)
	}

	autofixEngine := autofix.NewEngine(cfg.FixScriptsDir, cfg.AllowedFixCommands, cfg.AllowedFixTools)
	syncResult := autofixEngine.SyncToolsDir()
	logger.Printf("fix tools dir: %s (%d tools linked, %d removed)", syncResult.ToolsDir, len(syncResult.Linked), len(syncResult.Removed))
	incSvc := incident.NewService(store, q, autofixEngine, time.Duration(cfg.AlertCooldownSec)*time.Second, incident.EmailConfig{
		Host: cfg.EmailHost,
		Port: cfg.EmailPort,
		User: cfg.EmailUser,
		Pass: cfg.EmailPass,
		From: cfg.EmailFrom,
	})

	scheduler := &services.Scheduler{
		Store: store,
		Queue: q,
		Tick:  time.Duration(cfg.SchedulerTickSec) * time.Second,
		Log:   logger,
	}
	worker := &services.Worker{
		Store:         store,
		Queue:         q,
		AutofixEngine: autofixEngine,
		Incident:      incSvc,
		Log:           logger,
	}
	notify := &services.Notifier{
		Store:      store,
		Queue:      q,
		SMTPClient: notifier.NewSMTPClient(),
		DefaultSMTP: notifier.SMTPProfile{
			Host:              cfg.EmailHost,
			Port:              cfg.EmailPort,
			Username:          cfg.EmailUser,
			PasswordEncrypted: cfg.EmailPass,
			FromEmail:         cfg.EmailFrom,
		},
		Log: logger,
	}

	for _, validate := range []func() error{scheduler.Validate, worker.Validate, notify.Validate} {
		if err := validate(); err != nil {
			return fmt.Errorf("invalid service config: %w", err)
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := api.NewHandler(store, q, cfg.FixScriptsDir, cfg.UIDir, lb, autofixEngine)
	srv := &http.Server{
		Addr:         cfg.APIAddr,
		Handler:      handler.Router(),
		BaseContext:  func(_ net.Listener) context.Context { return runCtx },
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // disabled: SSE connections are long-lived
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Printf("kraken app listening on %s", cfg.APIAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); scheduler.Run(runCtx) }()
	go func() { defer wg.Done(); worker.Run(runCtx) }()
	go func() { defer wg.Done(); notify.Run(runCtx) }()

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-shutdownSignal:
		logger.Printf("received signal %s, shutting down", sig)
	case err := <-errCh:
		return fmt.Errorf("api server: %w", err)
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Printf("api shutdown error: %v", err)
	}
	wg.Wait()
	logger.Println("kraken app stopped")
	return nil
}
