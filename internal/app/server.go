package app

import (
	"context"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/esrid/garageband/internal/platform/db"
)

// Run wires dependencies, starts the HTTP server and blocks until SIGINT or
// SIGTERM, then shuts down gracefully.
func Run(cfg Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(); err != nil {
			slog.Error("close database", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           NewRouter(cfg, database),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", cfg.Addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
