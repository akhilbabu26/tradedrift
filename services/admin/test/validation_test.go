package test

import (
	"strings"
	"testing"

	"tradedrift/services/admin/internal/handler"
)

func TestValidateReason(t *testing.T) {
	val, err := handler.ValidateReason("Fraudulent trading activity detected")
	if err != nil {
		t.Fatalf("expected valid reason, got error: %v", err)
	}
	if val != "Fraudulent trading activity detected" {
		t.Fatalf("unexpected trimmed value: %s", val)
	}

	_, err = handler.ValidateReason("     ")
	if err != handler.ErrInvalidReason {
		t.Fatalf("expected ErrInvalidReason for whitespace, got: %v", err)
	}

	_, err = handler.ValidateReason("bad")
	if err != handler.ErrInvalidReason {
		t.Fatalf("expected ErrInvalidReason for short string, got: %v", err)
	}

	longReason := strings.Repeat("a", 501)
	_, err = handler.ValidateReason(longReason)
	if err != handler.ErrInvalidReason {
		t.Fatalf("expected ErrInvalidReason for long string, got: %v", err)
	}
}

func TestValidateUserID(t *testing.T) {
	if err := handler.ValidateUserID("1f85afe9-e866-4629-bf51-c8dc8a72d7aa"); err != nil {
		t.Fatalf("expected valid UUID, got: %v", err)
	}

	if err := handler.ValidateUserID("invalid-id"); err != handler.ErrInvalidUserID {
		t.Fatalf("expected ErrInvalidUserID for invalid string, got: %v", err)
	}
	if err := handler.ValidateUserID(""); err != handler.ErrInvalidUserID {
		t.Fatalf("expected ErrInvalidUserID for empty string, got: %v", err)
	}
}

func TestValidateAsset(t *testing.T) {
	validAssets := []string{"BTC", "ETH", "USDT", "INR"}
	for _, a := range validAssets {
		if err := handler.ValidateAsset(a); err != nil {
			t.Fatalf("expected valid asset for %s, got: %v", a, err)
		}
	}

	invalidAssets := []string{"", "b", "btc", "TOOLONGASSETNAME123", "BTC$"}
	for _, a := range invalidAssets {
		if err := handler.ValidateAsset(a); err != handler.ErrInvalidAsset {
			t.Fatalf("expected ErrInvalidAsset for %s, got: %v", a, err)
		}
	}
}

func TestValidateMarketID(t *testing.T) {
	validMarkets := []string{"BTC-INR", "ETH-USDT", "SOL-INR"}
	for _, m := range validMarkets {
		if err := handler.ValidateMarketID(m); err != nil {
			t.Fatalf("expected valid market ID for %s, got: %v", m, err)
		}
	}

	invalidMarkets := []string{"", "BTC", "btc-inr", "BTC_INR", "BTC--INR"}
	for _, m := range invalidMarkets {
		if err := handler.ValidateMarketID(m); err != handler.ErrInvalidMarketID {
			t.Fatalf("expected ErrInvalidMarketID for %s, got: %v", m, err)
		}
	}
}
