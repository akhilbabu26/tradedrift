package config

import (
	"os"
	"strings"
	"testing"
)

func TestIsProduction(t *testing.T) {
	defer os.Unsetenv("ENV")
	defer os.Unsetenv("APP_ENV")

	os.Setenv("ENV", "development")
	if IsProduction() {
		t.Fatal("expected IsProduction() to be false in development")
	}

	os.Setenv("ENV", "production")
	if !IsProduction() {
		t.Fatal("expected IsProduction() to be true when ENV=production")
	}

	os.Setenv("ENV", "")
	os.Setenv("APP_ENV", "prod")
	if !IsProduction() {
		t.Fatal("expected IsProduction() to be true when APP_ENV=prod")
	}
}

func TestGetAddress_DevFallback(t *testing.T) {
	defer os.Unsetenv("ENV")
	defer os.Unsetenv("TEST_SERVICE_ADDR")

	os.Setenv("ENV", "development")
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
	defer os.Unsetenv("TEST_SERVICE_ADDR")

	os.Setenv("ENV", "production")

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

	devSecret := "super-secret-jwt-key-tradedrift-dev-32bytes"

	// Dev mode: allows dev secret
	os.Setenv("ENV", "development")
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

