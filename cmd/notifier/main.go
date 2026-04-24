package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/velox0/kraken/internal/config"
	"github.com/velox0/kraken/internal/db"
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

	runner := &services.Notifier{
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
	if err := runner.Validate(); err != nil {
		log.Fatalf("notifier config invalid: %v", err)
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
}
