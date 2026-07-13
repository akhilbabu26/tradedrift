package postgres

import (
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // Registers standard SQL pgx driver
	"github.com/pressly/goose/v3"      // A database migration tool for Go
)

// RunMigrations connects to the database via standard database/sql and applies all migrations from migrationsDir.
// It opens and closes the connection internally, shielding the caller from standard library DB management.
func RunMigrations(dsn string, migrationsDir string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("failed to open sql connection for migrations: %w", err)
	}
	defer db.Close()

	// Establish a connection to verify the database is reachable
	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping postgres database for migrations: %w", err)
	}

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose failed to set dialect: %w", err)
	}

	if err := goose.Up(db, migrationsDir); err != nil {
		return fmt.Errorf("goose migration up failed: %w", err)
	}

	return nil
}
