package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestMigrationValidation_DisposableDB(t *testing.T) {
	adminDSN := "postgres://postgres:123@localhost:5432/postgres?sslmode=disable"
	adminDB, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Skipf("cannot connect to postgres admin db: %v", err)
		return
	}
	defer adminDB.Close()

	if err := adminDB.Ping(); err != nil {
		t.Skipf("postgres ping failed: %v", err)
		return
	}

	tempDBName := "tradedrift_test_mig_disposable"
	_, _ = adminDB.Exec("DROP DATABASE IF EXISTS " + tempDBName + ";")
	_, err = adminDB.Exec("CREATE DATABASE " + tempDBName + ";")
	if err != nil {
		t.Skipf("failed to create disposable db: %v", err)
		return
	}
	defer func() {
		_, _ = adminDB.Exec("DROP DATABASE IF EXISTS " + tempDBName + ";")
	}()

	dsn := "postgres://postgres:123@localhost:5432/" + tempDBName + "?sslmode=disable"
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("cannot connect to temp db: %v", err)
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("failed to set goose dialect: %v", err)
	}

	// 1. Run migrations UP to Version 3
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("failed to run goose Up: %v", err)
	}

	// 2. Validate index existence after UP
	var count int
	err = db.QueryRowContext(context.Background(), `
		SELECT count(*) 
		FROM pg_indexes 
		WHERE tablename = 'users' AND indexname = 'idx_users_email_lower';
	`).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query pg_indexes: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 index for idx_users_email_lower after UP, got %d", count)
	}

	// 3. Test uniqueness enforcement
	_, err = db.Exec(`
		INSERT INTO users (id, email, password_hash, username, role, status)
		VALUES (gen_random_uuid(), 'TestUser@example.com', 'hash', 'user1', 'user', 'VERIFIED');
	`)
	if err != nil {
		t.Fatalf("failed to insert initial user: %v", err)
	}

	// Duplicate with different case and leading/trailing whitespace
	_, err = db.Exec(`
		INSERT INTO users (id, email, password_hash, username, role, status)
		VALUES (gen_random_uuid(), '   testuser@EXAMPLE.COM   ', 'hash', 'user2', 'user', 'VERIFIED');
	`)
	if err == nil {
		t.Fatalf("expected duplicate insert to fail due to idx_users_email_lower, but it succeeded")
	}
	t.Logf("Migration 00003 UP successfully enforced unique case-insensitive email constraint: %v", err)

	// 4. Test DOWN direction (rollback migration 00003)
	if err := goose.Down(db, "."); err != nil {
		t.Fatalf("failed to run goose Down for migration 00003: %v", err)
	}

	// Verify index was dropped by Down migration
	err = db.QueryRowContext(context.Background(), `
		SELECT count(*) 
		FROM pg_indexes 
		WHERE tablename = 'users' AND indexname = 'idx_users_email_lower';
	`).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query pg_indexes after Down: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected index idx_users_email_lower to be dropped after Down, got count %d", count)
	}
	t.Log("Migration 00003 DOWN successfully dropped idx_users_email_lower index")

	// 5. Test UP again (reapply migration 00003)
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("failed to re-apply goose Up for migration 00003: %v", err)
	}
	err = db.QueryRowContext(context.Background(), `
		SELECT count(*) 
		FROM pg_indexes 
		WHERE tablename = 'users' AND indexname = 'idx_users_email_lower';
	`).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("expected index idx_users_email_lower to exist after re-applying UP, got count %d (err: %v)", count, err)
	}
	t.Log("Migration 00003 re-applied UP successfully")
}
