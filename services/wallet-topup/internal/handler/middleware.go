package handler

import (
	"context"
	"encoding/json"
	"net/http"
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

// RequireAuth extracts X-User-ID header and ensures it is present.
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-ID")
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "missing X-User-ID header",
			})
			return
		}
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next(w, r.WithContext(ctx))
	}
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
