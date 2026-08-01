// Command test runs the Go suite against an ephemeral PostgreSQL 18 instance.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/esrid/garageband/internal/platform/dbtest"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer stop()

	err := dbtest.RunSuite(
		ctx,
		os.Getenv("TEST_DATABASE_URL"),
		os.Args[1:],
		os.Environ(),
		os.Stdout,
		os.Stderr,
	)
	if err != nil {
		slog.Error("test suite", "err", err)
		os.Exit(1)
	}
}
