package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binaryPath string

func TestMain(m *testing.M) {
	// Build the CLI binary once for all tests
	tmpDir, err := os.MkdirTemp("", "migrate-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	binaryPath = filepath.Join(tmpDir, "migrate.exe")
	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		panic(string(out))
	}

	code := m.Run()
	os.Exit(code)
}

func TestCLI_MissingServiceNameFails(t *testing.T) {
	cmd := exec.Command(binaryPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected command without -service to fail, but it succeeded: %s", string(out))
	}
	if !strings.Contains(string(out), "Usage: migrate") {
		t.Fatalf("expected usage message, got: %s", string(out))
	}
}

func TestCLI_InvalidActionFails(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=auth", "-action=invalid_action", "-no-env")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected invalid action to fail, but it succeeded: %s", string(out))
	}
	if !strings.Contains(string(out), "Unsupported action") {
		t.Fatalf("expected 'Unsupported action' in output, got: %s", string(out))
	}
}

func TestCLI_DownBlockedInProductionWithoutFlag(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=auth", "-action=down", "-no-env")
	cmd.Env = append(os.Environ(), "ENV=production")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected down in production to fail, but it succeeded: %s", string(out))
	}
	if !strings.Contains(string(out), "blocked in production/strict mode") {
		t.Fatalf("expected down blocked message, got: %s", string(out))
	}
}

func TestCLI_StrictFailsOnMissingDSN(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=auth", "-action=verify", "-strict", "-no-env")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ENV=development",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing DSN in strict mode to exit non-zero, got success: %s", string(out))
	}
	if !strings.Contains(string(out), "[FAIL] Service auth: No DSN provided") {
		t.Fatalf("expected failure output for missing DSN, got: %s", string(out))
	}
}

func TestCLI_ProductionRejectsLocalhostDSN(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=auth", "-action=verify", "-strict", "-no-env")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ENV=production",
		"AUTH_POSTGRES_DSN=postgres://user:SuperSecretPassword123@localhost:5432/tradedrift_auth",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected production localhost DSN to fail, got success: %s", string(out))
	}
	outStr := string(out)
	if !strings.Contains(outStr, "cannot target local loopback host") {
		t.Fatalf("expected loopback error, got: %s", outStr)
	}
	if strings.Contains(outStr, "SuperSecretPassword123") {
		t.Fatalf("FATAL: password leaked in output: %s", outStr)
	}
}

func TestCLI_ProductionRejectsHostDockerInternal(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=wallet", "-action=verify", "-strict", "-no-env")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ENV=production",
		"WALLET_POSTGRES_DSN=postgres://user:pass123@host.docker.internal:5432/tradedrift_wallet",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected host.docker.internal to fail in production, got success: %s", string(out))
	}
	if !strings.Contains(string(out), "cannot target local loopback host") {
		t.Fatalf("expected loopback host error, got: %s", string(out))
	}
}

func TestCLI_ProductionRejectsDatabaseMismatch(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=auth", "-action=verify", "-strict", "-no-env")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ENV=production",
		"AUTH_POSTGRES_DSN=postgres://user:pass123@aws-rds.internal:5432/tradedrift_wallet",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected database mismatch to fail, got success: %s", string(out))
	}
	if !strings.Contains(string(out), "DSN database mismatch") {
		t.Fatalf("expected database mismatch error, got: %s", string(out))
	}
}

func TestCLI_ProductionRejectsUnresolvedPlaceholder(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=trade", "-action=verify", "-strict", "-no-env")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ENV=production",
		"TRADE_POSTGRES_DSN=postgres://admin:REPLACE_WITH_STRONG_PASSWORD@aws-rds.internal:5432/tradedrift_trade",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected placeholder DSN to fail, got success: %s", string(out))
	}
	if !strings.Contains(string(out), "unresolved placeholder values") {
		t.Fatalf("expected placeholder error, got: %s", string(out))
	}
}

func TestCLI_MasksPasswordOnConnectionFailure(t *testing.T) {
	// Point to an unreachable host with a secret password to verify error message sanitization
	cmd := exec.Command(binaryPath, "-service=auth", "-action=verify", "-strict", "-no-env")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ENV=development",
		"AUTH_POSTGRES_DSN=postgres://testuser:MyHighlyConfidentialPassword99@192.0.2.1:5432/tradedrift_auth?connect_timeout=1",
	}
	out, _ := cmd.CombinedOutput()
	outStr := string(out)

	if strings.Contains(outStr, "MyHighlyConfidentialPassword99") {
		t.Fatalf("FATAL: Secret password leaked in CLI output: %s", outStr)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "services", "auth", "migrations")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repository root containing services/auth/migrations")
	return ""
}

func TestCLI_AllServicesPreflight_FailsBeforeAnyMigrationWhenLaterServiceInvalid(t *testing.T) {
	// Provide valid DSNs for services, but an invalid DB for the last service (wallet-topup).
	// Verify that the preflight fails closed and NO migration for service 1 (auth) or any other service starts.
	repoRoot := findRepoRoot(t)
	cmd := exec.Command(binaryPath, "-service=all", "-action=up", "-strict", "-no-env")
	cmd.Dir = repoRoot // Run from repository root so default migration directories exist
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ENV=development",
		"AUTH_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_auth",
		"WALLET_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_wallet",
		"MARKET_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_market",
		"ORDER_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_order",
		"MATCHING_ENGINE_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_matching",
		"SETTLEMENT_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_settlement",
		"TRADE_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_trade",
		"PORTFOLIO_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_portfolio",
		"NOTIFICATION_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_notification",
		"ADMIN_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/tradedrift_admin",
		"WALLET_TOPUP_POSTGRES_DSN=postgres://user:pass@127.0.0.1:5432/wrong_topup_db", // INVALID
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected all-services preflight to fail due to invalid wallet-topup DSN, got success: %s", string(out))
	}
	outStr := string(out)
	if !strings.Contains(outStr, "[PREFLIGHT FAIL]") {
		t.Fatalf("expected '[PREFLIGHT FAIL]' in output, got: %s", outStr)
	}
	if !strings.Contains(outStr, "DSN database mismatch for wallet-topup") {
		t.Fatalf("expected mismatch for wallet-topup, got: %s", outStr)
	}
	if strings.Contains(outStr, ">>> [UP]") {
		t.Fatalf("FATAL: migrations began executing despite preflight failure! Output: %s", outStr)
	}
	if !strings.Contains(outStr, "No migrations or operations were executed") {
		t.Fatalf("expected confirmation that no migrations were executed, got: %s", outStr)
	}
}

func TestCLI_MissingDatabaseNameInDSNFailsPreflight(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=auth", "-action=verify", "-strict", "-no-env")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ENV=development",
		"AUTH_POSTGRES_DSN=postgres://user:pass@aws-db.internal:5432",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected DSN with missing database name to fail, got success: %s", string(out))
	}
	outStr := string(out)
	if !strings.Contains(outStr, "missing database name") {
		t.Fatalf("expected 'missing database name' error, got: %s", outStr)
	}
}

func TestCLI_MissingMigrationDirectoryFailsPreflight(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=auth", "-dir=nonexistent/migration/dir", "-action=verify", "-strict", "-no-env")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ENV=development",
		"AUTH_POSTGRES_DSN=postgres://user:pass@aws-db.internal:5432/tradedrift_auth",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing migration directory to fail preflight, got success: %s", string(out))
	}
	outStr := string(out)
	if !strings.Contains(outStr, "not found") {
		t.Fatalf("expected 'not found' directory error, got: %s", outStr)
	}
	if strings.Contains(outStr, ">>> [VERIFY]") {
		t.Fatalf("FATAL: operation began executing despite missing migration directory! Output: %s", outStr)
	}
}

func TestCLI_PowerShellSplitEnvFileResolvedProperly(t *testing.T) {
	// Simulate PowerShell passing "-env-file=" and ".env.test" as two separate tokens
	tmpEnv, err := os.CreateTemp("", "prod-test-*.env")
	if err != nil {
		t.Fatalf("failed to create temp env: %v", err)
	}
	defer os.Remove(tmpEnv.Name())

	content := "ENV=production\nAPP_ENV=production\nAUTH_POSTGRES_DSN=postgres://user:pass@aws-db.internal:5432/tradedrift_auth\n"
	if _, err := tmpEnv.WriteString(content); err != nil {
		t.Fatalf("failed to write temp env: %v", err)
	}
	tmpEnv.Close()

	cmd := exec.Command(binaryPath, "-service=auth", "-action=verify", "-strict", "-env-file=", tmpEnv.Name())
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
	}
	out, _ := cmd.CombinedOutput()
	outStr := string(out)

	if !strings.Contains(outStr, "Prod: true") {
		t.Fatalf("expected 'Prod: true' when loading env file, got: %s", outStr)
	}
}

func TestCLI_EmptyEnvFileFailsClosed(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=auth", "-action=verify", "-strict", "-env-file=")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected empty -env-file flag without filename to fail, got success: %s", string(out))
	}
	outStr := string(out)
	if !strings.Contains(outStr, "-env-file flag was provided without a valid file path") {
		t.Fatalf("expected error about missing file path, got: %s", outStr)
	}
}

func TestCLI_UnexpectedPositionalArgFailsClosed(t *testing.T) {
	cmd := exec.Command(binaryPath, "-service=auth", "-action=verify", "-strict", "dangling_argument")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected unexpected positional argument to fail, got success: %s", string(out))
	}
	outStr := string(out)
	if !strings.Contains(outStr, "Unexpected argument") {
		t.Fatalf("expected 'Unexpected argument' in output, got: %s", outStr)
	}
}

