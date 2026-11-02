package testhelper

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExpectedTestDatabaseName is the only database allowed for wallet integration tests.
const ExpectedTestDatabaseName = "tradedrift_wallet_test"

// WalletTestAdvisoryLockID is a globally unique 64-bit integer used for
// PostgreSQL advisory locking across concurrent Go test package binaries.
const WalletTestAdvisoryLockID int64 = 7823401

// ValidateTestDSN verifies that WALLET_TEST_DSN is explicitly set and points
// strictly to ExpectedTestDatabaseName ("tradedrift_wallet_test").
// It returns a sanitized error (never leaking credentials or complete DSN) if validation fails.
func ValidateTestDSN() (string, error) {
	dsn := os.Getenv("WALLET_TEST_DSN")
	if dsn == "" {
		return "", fmt.Errorf("WALLET_TEST_DSN is not set: wallet integration tests require an explicit WALLET_TEST_DSN pointing to %q", ExpectedTestDatabaseName)
	}

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", fmt.Errorf("WALLET_TEST_DSN is invalid or cannot be parsed")
	}

	if cfg.Database != ExpectedTestDatabaseName {
		return "", fmt.Errorf("WALLET_TEST_DSN targets database %q; integration tests strictly require %q to protect development/production databases", cfg.Database, ExpectedTestDatabaseName)
	}

	return dsn, nil
}

// AcquireTestLock acquires a session-level PostgreSQL advisory lock on the test database.
// It fails closed: if WALLET_TEST_DSN is unset, invalid, points to the wrong database,
// or cannot connect/lock, it returns an explicit error and nil unlock function.
func AcquireTestLock() (func(), error) {
	dsn, err := ValidateTestDSN()
	if err != nil {
		return nil, err
	}

	cfg, _ := pgx.ParseConfig(dsn)

	connCtx, connCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer connCancel()

	conn, err := pgx.Connect(connCtx, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to test database %q on %s:%d: %w", cfg.Database, cfg.Host, cfg.Port, err)
	}

	if err := conn.Ping(connCtx); err != nil {
		_ = conn.Close(connCtx)
		return nil, fmt.Errorf("failed to ping test database %q on %s:%d: %w", cfg.Database, cfg.Host, cfg.Port, err)
	}

	// Acquire session-level advisory lock (blocks until acquired across concurrent test package processes).
	// Allow up to 3 minutes for other test packages to complete before giving up.
	lockCtx, lockCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer lockCancel()
	_, err = conn.Exec(lockCtx, "SELECT pg_advisory_lock($1)", WalletTestAdvisoryLockID)
	if err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("failed to acquire advisory lock on test database %q: %w", cfg.Database, err)
	}

	return func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(cleanupCtx, "SELECT pg_advisory_unlock($1)", WalletTestAdvisoryLockID)
		_ = conn.Close(cleanupCtx)
	}, nil
}

// GetTestPool creates and returns a connection pool for the validated test database.
// It fails closed using t.Fatalf if WALLET_TEST_DSN is invalid or connection fails.
// It registers pool.Close with t.Cleanup so the pool closes cleanly after the test finishes.
func GetTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn, err := ValidateTestDSN()
	if err != nil {
		t.Fatalf("test database isolation error: %v", err)
	}

	cfg, _ := pgxpool.ParseConfig(dsn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create connection pool for %q on %s:%d: %v", cfg.ConnConfig.Database, cfg.ConnConfig.Host, cfg.ConnConfig.Port, err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("failed to ping test database %q on %s:%d: %v", cfg.ConnConfig.Database, cfg.ConnConfig.Host, cfg.ConnConfig.Port, err)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}
