package config

import (
	"os"
	"strings"
	"testing"
)

func TestIsProduction(t *testing.T) {
	tests := []struct {
		name       string
		envVal     string
		appEnvVal  string
		setEnv     bool
		setAppEnv  bool
		wantIsProd bool
	}{
		{
			name:       "ENV=production",
			envVal:     "production",
			setEnv:     true,
			wantIsProd: true,
		},
		{
			name:       "APP_ENV=production",
			appEnvVal:  "production",
			setAppEnv:  true,
			wantIsProd: true,
		},
		{
			name:       "ENV=prod",
			envVal:     "prod",
			setEnv:     true,
			wantIsProd: true,
		},
		{
			name:       "APP_ENV=prod",
			appEnvVal:  "prod",
			setAppEnv:  true,
			wantIsProd: true,
		},
		{
			name:       "case normalization uppercase PRODUCTION",
			envVal:     "PRODUCTION",
			setEnv:     true,
			wantIsProd: true,
		},
		{
			name:       "case normalization mixed PrOd",
			appEnvVal:  "PrOd",
			setAppEnv:  true,
			wantIsProd: true,
		},
		{
			name:       "whitespace normalization",
			envVal:     "  production \t ",
			setEnv:     true,
			wantIsProd: true,
		},
		{
			name:       "conflicting: ENV=development and APP_ENV=production",
			envVal:     "development",
			appEnvVal:  "production",
			setEnv:     true,
			setAppEnv:  true,
			wantIsProd: true, // Fails safe: cannot bypass production restrictions
		},
		{
			name:       "conflicting: ENV=production and APP_ENV=development",
			envVal:     "production",
			appEnvVal:  "development",
			setEnv:     true,
			setAppEnv:  true,
			wantIsProd: true,
		},
		{
			name:       "both development",
			envVal:     "development",
			appEnvVal:  "development",
			setEnv:     true,
			setAppEnv:  true,
			wantIsProd: false,
		},
		{
			name:       "staging not production",
			envVal:     "staging",
			setEnv:     true,
			wantIsProd: false,
		},
		{
			name:       "empty strings",
			envVal:     "",
			appEnvVal:  "",
			setEnv:     true,
			setAppEnv:  true,
			wantIsProd: false,
		},
		{
			name:       "neither set",
			wantIsProd: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origEnv, hasEnv := os.LookupEnv("ENV")
			origAppEnv, hasAppEnv := os.LookupEnv("APP_ENV")
			defer func() {
				if hasEnv {
					os.Setenv("ENV", origEnv)
				} else {
					os.Unsetenv("ENV")
				}
				if hasAppEnv {
					os.Setenv("APP_ENV", origAppEnv)
				} else {
					os.Unsetenv("APP_ENV")
				}
			}()

			os.Unsetenv("ENV")
			os.Unsetenv("APP_ENV")

			if tt.setEnv {
				os.Setenv("ENV", tt.envVal)
			}
			if tt.setAppEnv {
				os.Setenv("APP_ENV", tt.appEnvVal)
			}

			got := IsProduction()
			if got != tt.wantIsProd {
				t.Errorf("IsProduction() = %v, want %v (ENV=%q, APP_ENV=%q)", got, tt.wantIsProd, tt.envVal, tt.appEnvVal)
			}
		})
	}
}

func TestLoadEnv(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-*.env")
	if err != nil {
		t.Fatalf("failed to create temp env file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := `
# Comment line
ENV=production
APP_ENV=production
TEST_NEW_KEY=hello_world
TEST_PRESERVED_KEY=from_file
TEST_EMPTY_OVERWRITTEN=from_file_value
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("failed to write temp env file: %v", err)
	}
	tmpFile.Close()

	origPreserved, hasPreserved := os.LookupEnv("TEST_PRESERVED_KEY")
	origEmpty, hasEmpty := os.LookupEnv("TEST_EMPTY_OVERWRITTEN")
	origNew, hasNew := os.LookupEnv("TEST_NEW_KEY")
	origEnv, hasEnv := os.LookupEnv("ENV")
	origAppEnv, hasAppEnv := os.LookupEnv("APP_ENV")

	defer func() {
		if hasPreserved {
			os.Setenv("TEST_PRESERVED_KEY", origPreserved)
		} else {
			os.Unsetenv("TEST_PRESERVED_KEY")
		}
		if hasEmpty {
			os.Setenv("TEST_EMPTY_OVERWRITTEN", origEmpty)
		} else {
			os.Unsetenv("TEST_EMPTY_OVERWRITTEN")
		}
		if hasNew {
			os.Setenv("TEST_NEW_KEY", origNew)
		} else {
			os.Unsetenv("TEST_NEW_KEY")
		}
		if hasEnv {
			os.Setenv("ENV", origEnv)
		} else {
			os.Unsetenv("ENV")
		}
		if hasAppEnv {
			os.Setenv("APP_ENV", origAppEnv)
		} else {
			os.Unsetenv("APP_ENV")
		}
	}()

	os.Setenv("TEST_PRESERVED_KEY", "from_process")
	os.Setenv("TEST_EMPTY_OVERWRITTEN", "") // empty string should be populated from file
	os.Unsetenv("TEST_NEW_KEY")
	os.Unsetenv("ENV")
	os.Unsetenv("APP_ENV")

	LoadEnv(tmpFile.Name())

	if os.Getenv("TEST_NEW_KEY") != "hello_world" {
		t.Errorf("expected TEST_NEW_KEY=hello_world, got %q", os.Getenv("TEST_NEW_KEY"))
	}
	if os.Getenv("TEST_PRESERVED_KEY") != "from_process" {
		t.Errorf("expected TEST_PRESERVED_KEY to be preserved as 'from_process', got %q", os.Getenv("TEST_PRESERVED_KEY"))
	}
	if os.Getenv("TEST_EMPTY_OVERWRITTEN") != "from_file_value" {
		t.Errorf("expected TEST_EMPTY_OVERWRITTEN to be populated from file, got %q", os.Getenv("TEST_EMPTY_OVERWRITTEN"))
	}
	if !IsProduction() {
		t.Errorf("expected IsProduction() to be true after loading file with ENV=production")
	}
}

func TestGetAddress_DevFallback(t *testing.T) {
	defer os.Unsetenv("ENV")
	defer os.Unsetenv("APP_ENV")
	defer os.Unsetenv("TEST_SERVICE_ADDR")

	os.Setenv("ENV", "development")
	os.Unsetenv("APP_ENV")
	os.Unsetenv("TEST_SERVICE_ADDR")

	addr, err := GetAddress("TEST_SERVICE_ADDR", "localhost:50051")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "localhost:50051" {
		t.Fatalf("expected fallback addr 'localhost:50051', got %s", addr)
	}
}

func TestGetAddress_ProductionRequiresNonLocalhost(t *testing.T) {
	defer os.Unsetenv("ENV")
	defer os.Unsetenv("APP_ENV")
	defer os.Unsetenv("TEST_SERVICE_ADDR")

	os.Setenv("ENV", "production")
	os.Unsetenv("APP_ENV")

	// Missing in production
	os.Unsetenv("TEST_SERVICE_ADDR")
	_, err := GetAddress("TEST_SERVICE_ADDR", "localhost:50051")
	if err == nil {
		t.Fatal("expected error for missing address in production")
	}

	// Localhost forbidden in production
	os.Setenv("TEST_SERVICE_ADDR", "localhost:50051")
	_, err = GetAddress("TEST_SERVICE_ADDR", "fallback:50051")
	if err == nil || !strings.Contains(err.Error(), "loopback/local address") {
		t.Fatalf("expected localhost rejection in production, got: %v", err)
	}

	// 127.0.0.1 forbidden in production
	os.Setenv("TEST_SERVICE_ADDR", "127.0.0.1:50051")
	_, err = GetAddress("TEST_SERVICE_ADDR", "fallback:50051")
	if err == nil || !strings.Contains(err.Error(), "loopback/local address") {
		t.Fatalf("expected 127.0.0.1 rejection in production, got: %v", err)
	}

	// Valid production service DNS
	os.Setenv("TEST_SERVICE_ADDR", "auth.internal.tradedrift.local:50051")
	addr, err := GetAddress("TEST_SERVICE_ADDR", "fallback:50051")
	if err != nil {
		t.Fatalf("unexpected error for valid production DNS: %v", err)
	}
	if addr != "auth.internal.tradedrift.local:50051" {
		t.Fatalf("expected resolved addr, got %s", addr)
	}
}

func TestValidateProductionSecret(t *testing.T) {
	defer os.Unsetenv("ENV")
	defer os.Unsetenv("APP_ENV")

	devSecret := "super-secret-jwt-key-tradedrift-dev-32bytes"

	// Dev mode: allows dev secret
	os.Setenv("ENV", "development")
	os.Unsetenv("APP_ENV")
	if err := ValidateProductionSecret("JWT_SECRET", devSecret, devSecret); err != nil {
		t.Fatalf("expected dev mode to allow dev secret, got: %v", err)
	}

	// Prod mode: rejects dev secret
	os.Setenv("ENV", "production")
	if err := ValidateProductionSecret("JWT_SECRET", devSecret, devSecret); err == nil {
		t.Fatal("expected production mode to reject dev secret")
	}

	// Prod mode: rejects short secret (< 32 chars)
	if err := ValidateProductionSecret("JWT_SECRET", "short-secret-key-too-brief", devSecret); err == nil {
		t.Fatal("expected production mode to reject short secret")
	}

	// Prod mode: accepts strong secret
	strongSecret := "c7a8e1b2d3f4e5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0"
	if err := ValidateProductionSecret("JWT_SECRET", strongSecret, devSecret); err != nil {
		t.Fatalf("expected production mode to accept strong secret, got: %v", err)
	}
}

func TestAutoMigrateEnabled(t *testing.T) {
	defer os.Unsetenv("AUTO_MIGRATE")

	os.Unsetenv("AUTO_MIGRATE")
	if !AutoMigrateEnabled() {
		t.Fatal("expected AutoMigrateEnabled to be true by default")
	}

	os.Setenv("AUTO_MIGRATE", "false")
	if AutoMigrateEnabled() {
		t.Fatal("expected AutoMigrateEnabled to be false when AUTO_MIGRATE=false")
	}

	os.Setenv("AUTO_MIGRATE", "0")
	if AutoMigrateEnabled() {
		t.Fatal("expected AutoMigrateEnabled to be false when AUTO_MIGRATE=0")
	}

	os.Setenv("AUTO_MIGRATE", "true")
	if !AutoMigrateEnabled() {
		t.Fatal("expected AutoMigrateEnabled to be true when AUTO_MIGRATE=true")
	}
}
