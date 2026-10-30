package topup_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/gateway/internal/handler/topup"
)

func TestTopUpProxy_InjectsAndSanitizesXUserID(t *testing.T) {
	var receivedXUserID string
	var receivedIdempotencyKey string

	// Upstream mock server representing wallet-topup
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedXUserID = r.Header.Get("X-User-ID")
		receivedIdempotencyKey = r.Header.Get("X-Idempotency-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	targetURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("failed to parse upstream URL: %v", err)
	}

	logger := zap.NewNop()
	proxyHandler := topup.NewHandler(targetURL, logger)

	t.Run("authenticated request overwrites any spoofed X-User-ID", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/topups", nil)
		// Spoofed client header that must be stripped/overwritten
		req.Header.Set("X-User-ID", "attacker-victim-uuid")
		req.Header.Set("X-Idempotency-Key", "idem-123")

		// Authenticated claims in context
		claims := &platformjwt.Claims{
			UserID: "legitimate-auth-uuid",
		}
		ctx := platformjwt.WithClaims(req.Context(), claims)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		proxyHandler.Proxy(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}
		if receivedXUserID != "legitimate-auth-uuid" {
			t.Fatalf("expected X-User-ID 'legitimate-auth-uuid', got '%s'", receivedXUserID)
		}
		if receivedIdempotencyKey != "idem-123" {
			t.Fatalf("expected X-Idempotency-Key 'idem-123', got '%s'", receivedIdempotencyKey)
		}
	})

	t.Run("unauthenticated request has X-User-ID stripped", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/webhooks/payment/razorpay", nil)
		req.Header.Set("X-User-ID", "untrusted-user-uuid")

		rr := httptest.NewRecorder()
		proxyHandler.Proxy(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}
		if receivedXUserID != "" {
			t.Fatalf("expected empty X-User-ID, got '%s'", receivedXUserID)
		}
	})
}

func TestTopUpProxy_UpstreamUnavailable(t *testing.T) {
	// Point to an unavailable port
	targetURL, _ := url.Parse("http://127.0.0.1:59999")
	logger := zap.NewNop()
	proxyHandler := topup.NewHandler(targetURL, logger)

	req := httptest.NewRequest("GET", "/api/v1/topups", nil)
	rr := httptest.NewRecorder()
	proxyHandler.Proxy(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", rr.Code)
	}

	body, _ := io.ReadAll(rr.Body)
	if len(body) == 0 {
		t.Fatal("expected error body, got empty")
	}
}

func TestTopUpProxy_StripsUpstreamCORSHeaders(t *testing.T) {
	// Upstream server that emits Access-Control-Allow-Origin
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"usage":1}`))
	}))
	defer upstream.Close()

	targetURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("failed to parse upstream URL: %v", err)
	}

	logger := zap.NewNop()
	proxyHandler := topup.NewHandler(targetURL, logger)

	// Direct proxy call: verify proxy.ModifyResponse stripped the upstream CORS header
	req := httptest.NewRequest("GET", "/api/v1/topups/daily-usage", nil)
	rr := httptest.NewRecorder()
	proxyHandler.Proxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if origin := rr.Header().Get("Access-Control-Allow-Origin"); origin != "" {
		t.Fatalf("expected upstream Access-Control-Allow-Origin to be stripped by proxy, got '%s'", origin)
	}
}

