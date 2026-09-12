package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/metrics"
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

var (
	routeSuspendRegex   = regexp.MustCompile(`^/api/v1/admin/users/[^/]+/suspend$`)
	routeUnsuspendRegex = regexp.MustCompile(`^/api/v1/admin/users/[^/]+/unsuspend$`)
	routeFreezeRegex    = regexp.MustCompile(`^/api/v1/admin/users/[^/]+/wallets/[^/]+/freeze$`)
	routeUnfreezeRegex  = regexp.MustCompile(`^/api/v1/admin/users/[^/]+/wallets/[^/]+/unfreeze$`)
	routeHaltRegex      = regexp.MustCompile(`^/api/v1/admin/markets/[^/]+/halt$`)
	routeResumeRegex    = regexp.MustCompile(`^/api/v1/admin/markets/[^/]+/resume$`)
)

// NormalizeRoute maps arbitrary requested paths to strictly bounded, parameterized route patterns
// preventing Prometheus metric label explosion.
func NormalizeRoute(pattern, path string) string {
	if pattern != "" {
		parts := strings.SplitN(pattern, " ", 2)
		if len(parts) == 2 {
			return parts[1]
		}
		return pattern
	}

	switch path {
	case "/health":
		return "/health"
	case "/ready":
		return "/ready"
	case "/metrics":
		return "/metrics"
	case "/api/v1/admin/system/health":
		return "/api/v1/admin/system/health"
	}

	if routeSuspendRegex.MatchString(path) {
		return "/api/v1/admin/users/{user_id}/suspend"
	}
	if routeUnsuspendRegex.MatchString(path) {
		return "/api/v1/admin/users/{user_id}/unsuspend"
	}
	if routeFreezeRegex.MatchString(path) {
		return "/api/v1/admin/users/{user_id}/wallets/{asset}/freeze"
	}
	if routeUnfreezeRegex.MatchString(path) {
		return "/api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze"
	}
	if routeHaltRegex.MatchString(path) {
		return "/api/v1/admin/markets/{market_id}/halt"
	}
	if routeResumeRegex.MatchString(path) {
		return "/api/v1/admin/markets/{market_id}/resume"
	}

	return "unmatched"
}

// MetricsMiddleware tracks HTTP request count and latency histograms with normalized route labels.
func MetricsMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			duration := time.Since(start)

			route := NormalizeRoute(r.Pattern, r.URL.Path)
			metrics.RecordHTTPRequest(r.Method, route, rw.status, duration)
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

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func errorResponse(msg string) map[string]string {
	return map[string]string{"error": msg}
}
