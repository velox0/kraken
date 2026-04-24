package main

import (
	"context"
	"errors"
	"log"
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
	"github.com/velox0/kraken/internal/notifier"
	"github.com/velox0/kraken/internal/queue"
	"github.com/velox0/kraken/internal/services"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	store, err := db.New(ctx, cfg.PostgresURL)
	if err != nil {
		log.Fatalf("db init failed: %v", err)
	}
	defer store.Close()

	q := queue.NewRedis(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	defer q.Close()
	if err := q.Ping(ctx); err != nil {
		log.Fatalf("redis ping failed: %v", err)
	}

	autofixCrypto, err := autofix.NewCrypto(cfg.FixEnvSecret)
	if err != nil {
		log.Fatalf("fix env crypto init failed: %v", err)
	}
	if autofixCrypto != nil {
		store.SetFixEnvCrypto(autofixCrypto)
	}

	toolsDir := autofix.EnsureToolsDir()
	log.Printf("fix tools dir: %s", toolsDir)

	autofixEngine := autofix.NewEngine(cfg.FixScriptsDir, cfg.AllowedFixCommands)
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
		Log:   log.Default(),
	}
	worker := &services.Worker{
		Store:         store,
		Queue:         q,
		AutofixEngine: autofixEngine,
		Incident:      incSvc,
		Log:           log.Default(),
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
		Log: log.Default(),
	}

	for _, validate := range []func() error{scheduler.Validate, worker.Validate, notify.Validate} {
		if err := validate(); err != nil {
			log.Fatalf("invalid service config: %v", err)
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := api.NewHandler(store, q, cfg.FixScriptsDir, cfg.UIDir)
	srv := &http.Server{
		Addr:         cfg.APIAddr,
		Handler:      handler.Router(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("kraken app listening on %s", cfg.APIAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		scheduler.Run(runCtx)
	}()
	go func() {
		defer wg.Done()
		worker.Run(runCtx)
	}()
	go func() {
		defer wg.Done()
		notify.Run(runCtx)
	}()

	shutdownSignal := make(chan os.Signal, 1)
	signal.Notify(shutdownSignal, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-shutdownSignal:
		log.Printf("received signal %s, shutting down", sig)
	case err := <-errCh:
		log.Printf("api server failed: %v", err)
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("api shutdown error: %v", err)
	}
	wg.Wait()
	log.Println("kraken app stopped")
}
