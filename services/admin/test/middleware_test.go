package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	golangjwt "github.com/golang-jwt/jwt/v5"

	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/admin/internal/handler"
)

const testSecret = "test-secret-key-32bytes-for-hmac!!"

func generateToken(t *testing.T, userID, role string) string {
	t.Helper()
	now := time.Now()
	claims := platformjwt.Claims{
		UserID: userID,
		Email:  "test@tradedrift.io",
		Role:   role,
		RegisteredClaims: golangjwt.RegisteredClaims{
			Issuer:    "tradedrift-auth",
			ExpiresAt: golangjwt.NewNumericDate(now.Add(1 * time.Hour)),
			IssuedAt:  golangjwt.NewNumericDate(now),
		},
	}
	token := golangjwt.NewWithClaims(golangjwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}
	return tokenString
}

func TestRequireAdmin(t *testing.T) {
	jwtValidator := platformjwt.NewHMACValidator([]byte(testSecret))
	middleware := handler.RequireAdmin(jwtValidator)

	called := false
	dummyHandler := middleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		adminID := handler.AdminIDFromContext(r.Context())
		if adminID != "admin-123" {
			t.Errorf("expected adminID 'admin-123', got: %s", adminID)
		}
		w.WriteHeader(http.StatusOK)
	})

	// 1. Missing header
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	rr := httptest.NewRecorder()
	dummyHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing header, got: %d", rr.Code)
	}

	// 2. Non-admin role (user)
	userToken := generateToken(t, "user-456", "user")
	req = httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	rr = httptest.NewRecorder()
	dummyHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for user role, got: %d", rr.Code)
	}

	// 3. Admin role
	adminToken := generateToken(t, "admin-123", "admin")
	req = httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rr = httptest.NewRecorder()
	dummyHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for admin role, got: %d", rr.Code)
	}
	if !called {
		t.Error("expected inner handler to be called")
	}
}

func TestRequireIdempotencyKey(t *testing.T) {
	middleware := handler.RequireIdempotencyKey

	var extractedKey string
	dummyHandler := middleware(func(w http.ResponseWriter, r *http.Request) {
		extractedKey = handler.IdempotencyKeyFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	// 1. Missing header
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	rr := httptest.NewRecorder()
	dummyHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing Idempotency-Key, got: %d", rr.Code)
	}

	// 2. Valid header
	req = httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Header.Set("Idempotency-Key", "test-key-123")
	rr = httptest.NewRecorder()
	dummyHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got: %d", rr.Code)
	}
	if extractedKey != "test-key-123" {
		t.Errorf("expected extracted key 'test-key-123', got: %s", extractedKey)
	}

	// 3. Key too long (> 128 chars)
	longKey := string(make([]byte, 129))
	req = httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Header.Set("Idempotency-Key", longKey)
	rr = httptest.NewRecorder()
	dummyHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for key > 128 characters, got: %d", rr.Code)
	}

	var errBody map[string]string
	_ = json.NewDecoder(rr.Body).Decode(&errBody)
	if errBody["error"] != "Idempotency-Key must be 1-128 characters" {
		t.Errorf("unexpected error message: %s", errBody["error"])
	}
}
