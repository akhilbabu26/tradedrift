package handler

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"tradedrift/services/wallet/internal/repository"
)

// mapToGRPCError converts wallet domain errors to standard gRPC status codes.
// The handler layer owns this conversion — service layer never returns gRPC errors.
func mapToGRPCError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return status.Error(codes.NotFound, "resource not found")
	case errors.Is(err, repository.ErrInsufficientBalance):
		return status.Error(codes.FailedPrecondition, "insufficient balance")
	case errors.Is(err, repository.ErrWalletFrozen):
		return status.Error(codes.FailedPrecondition, "wallet is frozen")
	case errors.Is(err, repository.ErrDuplicate):
		return status.Error(codes.AlreadyExists, "already processed")
	default:
		return status.Errorf(codes.Internal, "internal server error: %v", err)
	}
}
