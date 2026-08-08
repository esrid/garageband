package dbtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const postgresImage = "postgres:18-alpine"

// RunSuite starts one PostgreSQL 18 container and runs every requested Go test
// package against it. Individual tests still create isolated schemas through
// Open, which is substantially faster than starting one container per test.
// An explicit TEST_DATABASE_URL keeps a fast path for an already-running test
// database without changing the test code.
func RunSuite(
	ctx context.Context,
	existingDatabaseURL string,
	patterns []string,
	environment []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	databaseURL := strings.TrimSpace(existingDatabaseURL)
	if databaseURL == "" {
		container, err := postgres.Run(
			ctx,
			postgresImage,
			postgres.WithDatabase("garage_test"),
			postgres.WithUsername("garage"),
			postgres.WithPassword("garage_test"),
			// Every test builds a whole schema and drops it with CASCADE, which
			// takes one lock per object inside a single transaction. With the
			// packages running in parallel the default lock table (64 x
			// max_connections) runs out and tests fail with "out of shared
			// memory" (SQLSTATE 53200) - intermittently, which is worse than
			// always. The option is appended to the module's own postgres
			// command; verified against testcontainers-go v0.43.0 (postgres
			// module Run applies user options after its defaults, and
			// WithCmdArgs appends) 2026-08-08.
			// ponytail: raise it again if the schema grows another few hundred
			// objects; the lock table costs about 170 bytes a slot.
			testcontainers.WithCmdArgs("-c", "max_locks_per_transaction=512"),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			return fmt.Errorf("start %s: %w", postgresImage, err)
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			returnErr = errors.Join(
				returnErr,
				testcontainers.TerminateContainer(container, testcontainers.StopContext(cleanupCtx)),
			)
		}()

		databaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			return fmt.Errorf("PostgreSQL test connection string: %w", err)
		}
	}

	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	arguments := append([]string{"test"}, patterns...)
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Env = replaceEnvironment(
		environment, "TEST_DATABASE_URL", databaseURL,
	)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("go test: %w", err)
	}
	return nil
}

func replaceEnvironment(environment []string, key string, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, variable := range environment {
		if !strings.HasPrefix(variable, prefix) {
			result = append(result, variable)
		}
	}
	return append(result, prefix+value)
}
