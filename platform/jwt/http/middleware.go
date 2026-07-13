package http

import (
	"net/http"
	"strings"

	"tradedrift/platform/jwt"
)

// AuthMiddleware returns an HTTP middleware that extracts and validates a JWT token.
func AuthMiddleware(v jwt.Validator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error": "Authorization header required"}`, http.StatusUnauthorized)
				return
			}

			// Strict prefix check to avoid accepting raw invalid credentials
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"error": "invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			tokenStr = strings.TrimSpace(tokenStr)

			claims, err := v.Validate(r.Context(), tokenStr)
			if err != nil {
				// Client-safe obfuscated error response
				http.Error(w, `{"error": "invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			// Inject claims into request context
			ctx := jwt.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
