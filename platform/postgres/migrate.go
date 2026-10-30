package postgres

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib" // Registers standard SQL pgx driver
	"github.com/pressly/goose/v3"      // A database migration tool for Go

	"tradedrift/platform/config"
)

// RunMigrations connects to the database and applies all migrations from migrationsDir.
// If AUTO_MIGRATE=false (recommended for multi-replica production), migrations are skipped
// so they can be run in a controlled pre-deployment migration step.
func RunMigrations(dsn string, migrationsDir string) error {
	if !config.AutoMigrateEnabled() {
		log.Printf("[postgres] AUTO_MIGRATE is false; skipping startup migrations for %s", migrationsDir)
		return nil
	}
	return ApplyMigrations(dsn, migrationsDir)
}

// ApplyMigrations connects to the database via standard database/sql and applies all migrations unconditionally.
// This is used by dedicated migration runner jobs or CLI tools.
func ApplyMigrations(dsn string, migrationsDir string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("failed to open sql connection for migrations: %w", err)
	}
	defer db.Close()

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

// RollbackMigration rolls back the last applied migration.
func RollbackMigration(dsn string, migrationsDir string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("failed to open sql connection for rollback: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping postgres database for rollback: %w", err)
	}

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose failed to set dialect: %w", err)
	}

	if err := goose.Down(db, migrationsDir); err != nil {
		return fmt.Errorf("goose migration down failed: %w", err)
	}

	return nil
}

// MigrationStatus prints the status of all migrations.
func MigrationStatus(dsn string, migrationsDir string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("failed to open sql connection for status: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping postgres database for status: %w", err)
	}

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose failed to set dialect: %w", err)
	}

	if err := goose.Status(db, migrationsDir); err != nil {
		return fmt.Errorf("goose migration status failed: %w", err)
	}

	return nil
}
