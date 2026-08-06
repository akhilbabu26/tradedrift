package middleware

import (
	"net/http"
	"runtime/debug"

	"go.uber.org/zap"
	"tradedrift/services/gateway/internal/response"
)

// Recovery catches any panic in downstream handlers and returns a 500
// instead of crashing the entire server process.
func Recovery(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					// Log the panic with full stack trace
					log.Error("panic recovered",
						zap.Any("error", err),
						zap.String("stack", string(debug.Stack())),
						zap.String("method", r.Method),
						zap.String("path", r.URL.Path),
						zap.String("request_id", RequestIDFromContext(r.Context())),
					)

					response.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
