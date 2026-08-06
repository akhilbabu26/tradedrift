package middleware

import (
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
	platformjwt "tradedrift/platform/jwt"
)

// responseWriter wraps http.ResponseWriter to capture status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += n
	return n, err
}

func Logger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			// Extract just the IP, not the port
			clientIP, _, _ := net.SplitHostPort(r.RemoteAddr)

			// Build base log fields
			fields := []zap.Field{
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("query", r.URL.RawQuery),
				zap.Int("status", rw.status),
				zap.Int("bytes", rw.bytes),
				zap.Duration("duration", time.Since(start)),
				zap.String("request_id", RequestIDFromContext(r.Context())),
				zap.String("client_ip", clientIP),
				zap.String("user_agent", r.UserAgent()),
				zap.Int64("content_length", r.ContentLength),
			}

			// Add userID if the route was authenticated
			if claims, ok := platformjwt.FromContext(r.Context()); ok {
				fields = append(fields, zap.String("user_id", claims.UserID))
			}

			log.Info("request", fields...)
		})
	}
}
