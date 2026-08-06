package handler

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/grpc/metadata"
	"tradedrift/services/gateway/internal/middleware"
)

// MetadataRequestID is the gRPC metadata key for request tracing.
// All gateway handlers and downstream services must use the same key.
const MetadataRequestID = "x-request-id"

// outgoingCtx creates a context with:
//   - a timeout for the gRPC call
//   - outgoing gRPC metadata carrying the request ID for cross-service tracing
//
// All handlers use this instead of plain context.WithTimeout so tracing
// is never accidentally omitted.
func outgoingCtx(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	md := metadata.Pairs(
		MetadataRequestID, middleware.RequestIDFromContext(r.Context()),
	)
	return metadata.NewOutgoingContext(ctx, md), cancel
}
