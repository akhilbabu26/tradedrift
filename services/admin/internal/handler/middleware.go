package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/admin/internal/domain"
)

type contextKey string

const (
	ctxKeyAdminID     contextKey = "adminID"
	ctxKeyRequestID   contextKey = "requestID"
	ctxKeyIdempotency contextKey = "idempotencyKey"
)

// AdminIDFromContext retrieves the validated admin's user ID from the request context.
func AdminIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyAdminID).(string); ok {
		return v
	}
	return ""
}

// RequestIDFromContext retrieves the X-Request-ID from the request context.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

// IdempotencyKeyFromContext retrieves the Idempotency-Key from the request context.
func IdempotencyKeyFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyIdempotency).(string); ok {
		return v
	}
	return ""
}

// RequireAdmin validates the Bearer JWT and asserts Role == "admin".
func RequireAdmin(jwtValidator *platformjwt.HMACValidator) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				writeJSON(w, http.StatusUnauthorized, errorResponse("missing or malformed Authorization header"))
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

			claims, err := jwtValidator.Validate(r.Context(), tokenStr)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, errorResponse("invalid or expired token"))
				return
			}
			if claims.Role != "admin" {
				writeJSON(w, http.StatusForbidden, errorResponse("admin role required"))
				return
			}

			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = "req-" + domain.MustNewV7()
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, ctxKeyAdminID, claims.UserID)
			ctx = context.WithValue(ctx, ctxKeyRequestID, requestID)
			next(w, r.WithContext(ctx))
		}
	}
}

// RequireIdempotencyKey enforces the presence of the Idempotency-Key header.
func RequireIdempotencyKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse("Idempotency-Key header is required"))
			return
		}
		if len(key) > 128 {
			writeJSON(w, http.StatusBadRequest, errorResponse("Idempotency-Key must be 1-128 characters"))
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyIdempotency, key)
		next(w, r.WithContext(ctx))
	}
}

// StructuredLoggingMiddleware emits structured JSON logs for every HTTP request.
func StructuredLoggingMiddleware(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			duration := time.Since(start)
			adminID := AdminIDFromContext(r.Context())
			requestID := RequestIDFromContext(r.Context())
			if requestID == "" {
				requestID = r.Header.Get("X-Request-ID")
			}

			log.Info("http_request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rw.status),
				zap.Int64("duration_ms", duration.Milliseconds()),
				zap.String("admin_id", adminID),
				zap.String("request_id", requestID),
				zap.String("remote_ip", r.RemoteAddr),
				zap.String("user_agent", r.UserAgent()),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func errorResponse(msg string) map[string]string {
	return map[string]string{"error": msg}
}
