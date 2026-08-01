package main

import (
	"log/slog"
	"os"

	"github.com/esrid/template/internal/app"
)

func main() {
	if err := app.Run(app.ConfigFromEnv()); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
