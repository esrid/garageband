package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/esrid/garageband/internal/app"
	"github.com/esrid/garageband/internal/platform/db"
)

func main() {
	cfg := app.ConfigFromEnv()
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := database.Close(); err != nil {
			slog.Error("close database", "err", err)
		}
	}()
	if err := db.Migrate(context.Background(), database); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	slog.Info("migrations applied")
}
