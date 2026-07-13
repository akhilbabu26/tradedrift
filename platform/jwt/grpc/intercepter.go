package grpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"tradedrift/platform/jwt"
)

// UnaryAuthInterceptor returns a gRPC unary interceptor that validates JWT access tokens.
func UnaryAuthInterceptor(v jwt.Validator, publicMethods map[string]bool) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Bypass validation for public methods (e.g. Login, Register)
		if publicMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeader := md.Get("authorization")
		if len(authHeader) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization token")
		}

		tokenStr := authHeader[0]
		// Strict prefix check
		if !strings.HasPrefix(tokenStr, "Bearer ") {
			return nil, status.Error(codes.Unauthenticated, "invalid or expired token")
		}

		tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")
		tokenStr = strings.TrimSpace(tokenStr)

		claims, err := v.Validate(ctx, tokenStr)
		if err != nil {
			// Do not leak internal validation details (revoked, database connection error, etc.)
			return nil, status.Error(codes.Unauthenticated, "invalid or expired token")
		}

		// Inject claims into context
		newCtx := jwt.WithClaims(ctx, claims)
		return handler(newCtx, req)
	}
}
