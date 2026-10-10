package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

type contextKey string

const (
	UserIDKey         contextKey = "userID"
	IdempotencyKeyKey contextKey = "idempotencyKey"
)

func UserIDFromContext(ctx context.Context) string {
	if val, ok := ctx.Value(UserIDKey).(string); ok {
		return val
	}
	return ""
}

func IdempotencyKeyFromContext(ctx context.Context) string {
	if val, ok := ctx.Value(IdempotencyKeyKey).(string); ok {
		return val
	}
	return ""
}

// CORSMiddleware handles cross-origin requests and preflight OPTIONS checks.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User-ID, X-Idempotency-Key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RequireAuth extracts X-User-ID header (or falls back to Bearer JWT token) and ensures it is present.
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-ID")
		if userID == "" {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
				userID = extractUserIDFromJWT(tokenStr)
			}
		}

		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "missing X-User-ID header or valid authorization token",
			})
			return
		}
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next(w, r.WithContext(ctx))
	}
}

func extractUserIDFromJWT(tokenStr string) string {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return ""
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payloadBytes, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims struct {
		UserID string `json:"user_id"`
		Sub    string `json:"sub"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return ""
	}
	if claims.UserID != "" {
		return claims.UserID
	}
	return claims.Sub
}

// LoggingMiddleware logs request duration and status.
func LoggingMiddleware(log *zap.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Debug("HTTP request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Duration("duration", time.Since(start)),
		)
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
