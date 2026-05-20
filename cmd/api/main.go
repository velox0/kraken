package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/velox0/kraken/internal/api"
	"github.com/velox0/kraken/internal/autofix"
	"github.com/velox0/kraken/internal/config"
	"github.com/velox0/kraken/internal/db"
	"github.com/velox0/kraken/internal/queue"
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

	handler := api.NewHandler(store, q, cfg.FixScriptsDir, cfg.UIDir, nil, autofixEngine)
	srv := &http.Server{
		Addr:         cfg.APIAddr,
		Handler:      handler.Router(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("api listening on %s", cfg.APIAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-shutdown:
		log.Printf("received signal %s, shutting down", sig)
	case err := <-errCh:
		return fmt.Errorf("api server: %w", err)
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctxTimeout); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	return nil
}
