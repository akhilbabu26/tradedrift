package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/gateway/internal/response"
)

// Auth validates the Bearer token and verifies the user is not suspended in real time.
func Auth(validator platformjwt.Validator, rdb redis.Cmdable, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing or invalid authorization header")
				return
			}

			tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			tokenStr = strings.Trim(tokenStr, "\"")

			claims, err := validator.Validate(r.Context(), tokenStr)
			if err != nil {
				response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "token is invalid or expired")
				return
			}

			// Real-time user suspension enforcement check (fail-closed)
			if rdb != nil {
				val, err := rdb.Get(r.Context(), "user:suspended:"+claims.UserID).Result()
				if err == nil && val == "1" {
					if log != nil {
						log.Warn("Request rejected: user is suspended", zap.String("user_id", claims.UserID))
					}
					response.WriteError(w, http.StatusForbidden, "AUTH_USER_SUSPENDED", "account has been suspended")
					return
				}
				if err != nil && !errors.Is(err, redis.Nil) {
					if log != nil {
						log.Error("Redis suspension lookup failed, failing closed", zap.String("user_id", claims.UserID), zap.Error(err))
					}
					response.WriteError(w, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE", "authentication verification unavailable")
					return
				}
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
