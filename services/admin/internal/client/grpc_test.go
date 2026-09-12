package client_test

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"tradedrift/services/admin/internal/client"
)

func TestIsRetryableGRPCError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"unavailable", status.Error(codes.Unavailable, "server unavailable"), true},
		{"deadline exceeded", status.Error(codes.DeadlineExceeded, "deadline exceeded"), true},
		{"resource exhausted", status.Error(codes.ResourceExhausted, "rate limited"), true},
		{"internal", status.Error(codes.Internal, "internal server error"), true},
		{"invalid argument", status.Error(codes.InvalidArgument, "bad user_id"), false},
		{"not found", status.Error(codes.NotFound, "user not found"), false},
		{"permission denied", status.Error(codes.PermissionDenied, "forbidden"), false},
		{"unauthenticated", status.Error(codes.Unauthenticated, "unauthenticated"), false},
		{"generic network error", errors.New("connection reset by peer"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.IsRetryableGRPCError(tt.err)
			if got != tt.expected {
				t.Errorf("IsRetryableGRPCError() for %s = %v, expected %v", tt.name, got, tt.expected)
			}
		})
	}
}
