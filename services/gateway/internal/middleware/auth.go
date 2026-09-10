package middleware

import (
	"net/http"
	"strings"

	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/gateway/internal/response"
)

// Auth validates the Bearer token using the shared platform validator.
// Inject a jwt.Validator so the middleware doesn't know HOW validation works —
// only that it either succeeds or fails.
func Auth(validator platformjwt.Validator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// === DEBUG: dump ALL headers ===
			println("[AUTH DEBUG] All headers:")
			for k, v := range r.Header {
				println("  ", k, ":", strings.Join(v, "||"))
			}
			// === END DEBUG ===

			authHeader := r.Header.Get("Authorization")
			println("[AUTH RAW] authHeader len:", len(authHeader), "| val:", authHeader)
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing or invalid authorization header")
				return
			}

			tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			tokenStr = strings.TrimSpace(strings.TrimPrefix(tokenStr, "Bearer "))
			tokenStr = strings.Trim(tokenStr, "\"")
			println("[AUTH TOKEN] len:", len(tokenStr), "| dots:", strings.Count(tokenStr, "."), "| full:", tokenStr)

			claims, err := validator.Validate(r.Context(), tokenStr)
			if err != nil {
				println("[AUTH REJECTED]", err.Error())
				response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "token is invalid or expired")
				return
			}

			// Store full claims in context using the platform's existing helper
			ctx := platformjwt.WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetUserID extracts the userID from the request context.
func GetUserID(r *http.Request) string {
	claims, ok := platformjwt.FromContext(r.Context())
	if !ok {
		return ""
	}
	return claims.UserID
}
