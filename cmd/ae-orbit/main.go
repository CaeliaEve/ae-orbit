package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/config"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}
func run(parent context.Context) error {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("AE Orbit protocol 1")
		return nil
	}
	if len(os.Args) > 1 && os.Args[1] != "serve" && os.Args[1] != "migrate" {
		return fmt.Errorf("usage: ae-orbit [serve|migrate|version]")
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL, cfg.Store)
	if err != nil {
		return err
	}
	defer func() { cancel(); db.Close() }()
	if err = db.Migrate(ctx); err != nil {
		return err
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		slog.Info("database migrations applied")
		return nil
	}
	if _, err = db.Bootstrap(ctx, cfg.AdminToken); err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Listen, Handler: api.New(db, cfg.HTTP), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				work, stop := context.WithTimeout(ctx, 10*time.Second)
				err := db.Maintain(work)
				stop()
				if err != nil && ctx.Err() == nil {
					slog.Error("maintenance failed", "error", err)
				}
			}
		}
	}()
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	slog.Info("AE Orbit listening", "address", cfg.Listen, "public_origin", cfg.PublicURL)
	select {
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		return server.Shutdown(shutdown)
	}
}
