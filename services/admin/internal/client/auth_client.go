package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	authv1 "tradedrift/platform/api/gen/auth/v1"
)

// AuthClient wraps the Auth gRPC client with correlation metadata injection and error classification.
type AuthClient struct {
	client authv1.AuthServiceClient
	conn   *grpc.ClientConn
}

// NewAuthClient creates a gRPC connection to the Auth service.
func NewAuthClient(grpcAddr string) (*AuthClient, error) {
	conn, err := grpc.Dial(grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("auth_client: dial %s: %w", grpcAddr, err)
	}
	return &AuthClient{client: authv1.NewAuthServiceClient(conn), conn: conn}, nil
}

// Close releases the underlying gRPC connection.
func (c *AuthClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Ping checks if the underlying connection to Auth service is healthy.
func (c *AuthClient) Ping(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("auth_client: connection is nil")
	}
	state := c.conn.GetState()
	if state == connectivity.TransientFailure || state == connectivity.Shutdown {
		return fmt.Errorf("auth_client: connection state is %s", state.String())
	}
	return nil
}

// InvalidateUserSessions calls the Auth service's internal InvalidateUserSessions RPC
// to revoke all active sessions for the given user. Injects request_id and operation_id
// into gRPC metadata for end-to-end tracing.
func (c *AuthClient) InvalidateUserSessions(ctx context.Context, userID, reason, requestID, operationID string) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("auth_client: client is nil")
	}

	// Use incoming context or apply 5-second deadline
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Propagate distributed tracing headers into gRPC metadata.
	md := metadata.Pairs(
		"x-request-id", requestID,
		"x-operation-id", operationID,
	)
	callCtx = metadata.NewOutgoingContext(callCtx, md)

	resp, err := c.client.InvalidateUserSessions(callCtx, &authv1.InvalidateUserSessionsRequest{
		UserId: userID,
		Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("auth_client: InvalidateUserSessions: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("auth_client: InvalidateUserSessions: auth service returned success=false")
	}
	return nil
}

// IsRetryableGRPCError evaluates whether a gRPC error is transient and safe to retry.
// Non-retryable client errors (InvalidArgument, NotFound, PermissionDenied, etc.) return false.
func IsRetryableGRPCError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		// Non-gRPC error (network drop, timeout, etc.) -> retry
		return true
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	case codes.Internal:
		return true
	case codes.InvalidArgument, codes.NotFound, codes.PermissionDenied, codes.Unauthenticated, codes.AlreadyExists:
		return false
	default:
		return false
	}
}
