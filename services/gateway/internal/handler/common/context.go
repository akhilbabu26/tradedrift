package common

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/grpc/metadata"
	"tradedrift/services/gateway/internal/middleware"
)

const MetadataRequestID = "x-request-id"

// OutgoingCtx creates a context with timeout and request ID tracing metadata.
func OutgoingCtx(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	md := metadata.Pairs(
		MetadataRequestID, middleware.RequestIDFromContext(r.Context()),
	)
	return metadata.NewOutgoingContext(ctx, md), cancel
}
