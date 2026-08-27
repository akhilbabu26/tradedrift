package middleware

import (
	"net/http"
	"strings"
)

func CORS(origins []string) func(http.Handler) http.Handler {
	// Build lookup map once during initialization.
	allowedOrigins := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		for _, o := range strings.Split(origin, ",") {
			trimmed := strings.TrimSpace(o)
			if trimmed != "" {
				allowedOrigins[trimmed] = struct{}{}
			}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			isAllowed := false
			if _, ok := allowedOrigins[origin]; ok {
				isAllowed = true
			} else if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
				// Allow local development ports (e.g. 5173, 5174, etc.)
				isAllowed = true
			}

			if isAllowed && origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")

			// Handle preflight requests.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}