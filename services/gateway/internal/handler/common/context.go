package common

import (
	"context"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/metadata"
	"tradedrift/services/gateway/internal/middleware"
)

const MetadataRequestID = "x-request-id"

// OutgoingCtx creates a context with timeout, request ID tracing metadata,
// and forwards Bearer authorization token if present in headers or session cookies.
func OutgoingCtx(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	pairs := []string{
		MetadataRequestID, middleware.RequestIDFromContext(r.Context()),
	}

	tokenStr := ""
	if authHeader := r.Header.Get("Authorization"); authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		tokenStr = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		tokenStr = strings.Trim(tokenStr, "\"")
	} else if cookie, err := r.Cookie("access_token"); err == nil && cookie.Value != "" {
		tokenStr = strings.TrimSpace(cookie.Value)
	}

	if tokenStr != "" {
		pairs = append(pairs, "authorization", "Bearer "+tokenStr)
	}

	md := metadata.Pairs(pairs...)
	return metadata.NewOutgoingContext(ctx, md), cancel
}
