package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tradedrift/services/gateway/internal/middleware"
)

func TestCORS_PreflightOptions(t *testing.T) {
	corsHandler := middleware.CORS([]string{"http://localhost:5173"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("preflight OPTIONS request must not reach downstream handler")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/topups", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, X-Idempotency-Key")

	rr := httptest.NewRecorder()
	corsHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected status 204 No Content, got %d", rr.Code)
	}

	origins := rr.Header().Values("Access-Control-Allow-Origin")
	if len(origins) != 1 {
		t.Fatalf("expected exactly 1 Access-Control-Allow-Origin header, got %d: %v", len(origins), origins)
	}
	if origins[0] != "http://localhost:5173" {
		t.Fatalf("expected origin 'http://localhost:5173', got '%s'", origins[0])
	}

	allowHeaders := rr.Header().Get("Access-Control-Allow-Headers")
	if allowHeaders == "" {
		t.Fatal("expected non-empty Access-Control-Allow-Headers")
	}
	expectedHeaders := []string{"Authorization", "Content-Type", "Idempotency-Key", "X-Idempotency-Key", "X-Request-ID"}
	for _, h := range expectedHeaders {
		if !contains(allowHeaders, h) {
			t.Errorf("expected Access-Control-Allow-Headers to contain '%s', got '%s'", h, allowHeaders)
		}
	}

	if creds := rr.Header().Get("Access-Control-Allow-Credentials"); creds != "true" {
		t.Errorf("expected Access-Control-Allow-Credentials 'true', got '%s'", creds)
	}
}

func TestCORS_AllowedOrigin_SuccessAndError(t *testing.T) {
	origins := []string{"http://localhost:5173"}

	t.Run("successful response has exactly one origin header", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true}`))
		})
		handler := middleware.CORS(origins)(next)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/topups/daily-usage", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		vals := rr.Header().Values("Access-Control-Allow-Origin")
		if len(vals) != 1 || vals[0] != "http://localhost:5173" {
			t.Fatalf("expected exactly one 'http://localhost:5173', got %v", vals)
		}
	})

	t.Run("error response preserves single origin header", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		})
		handler := middleware.CORS(origins)(next)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/topups", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rr.Code)
		}
		vals := rr.Header().Values("Access-Control-Allow-Origin")
		if len(vals) != 1 || vals[0] != "http://localhost:5173" {
			t.Fatalf("expected exactly one 'http://localhost:5173', got %v", vals)
		}
	})
}

func TestCORS_LocalhostWildcardPorts(t *testing.T) {
	handler := middleware.CORS([]string{"http://localhost:5173"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	testOrigins := []string{
		"http://localhost:5174",
		"http://localhost:3000",
		"http://127.0.0.1:5173",
	}

	for _, o := range testOrigins {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/markets", nil)
		req.Header.Set("Origin", o)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		vals := rr.Header().Values("Access-Control-Allow-Origin")
		if len(vals) != 1 || vals[0] != o {
			t.Errorf("for origin '%s', expected exactly one allow header, got %v", o, vals)
		}
	}
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	handler := middleware.CORS([]string{"http://localhost:5173"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/topups", nil)
	req.Header.Set("Origin", "https://unauthorized-attacker.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	vals := rr.Header().Values("Access-Control-Allow-Origin")
	if len(vals) != 0 {
		t.Fatalf("expected no Access-Control-Allow-Origin for unauthorized origin, got %v", vals)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || findSubstr(s, substr)))
}

func findSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
