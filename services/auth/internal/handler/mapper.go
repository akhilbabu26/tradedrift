package handler

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	platformerrors "tradedrift/platform/errors"
)

// mapToGRPCError converts a structured platform error to standard gRPC statuses.
func mapToGRPCError(err error) error {
	if err == nil {
		return nil
	}

	var pErr *platformerrors.PlatformError
	if errors.As(err, &pErr) {
		switch pErr.Code {
		case platformerrors.CodeInvalidArgument:
			return status.Error(codes.InvalidArgument, pErr.Message)
		case platformerrors.CodeInvalidCredentials:
			return status.Error(codes.Unauthenticated, pErr.Message)
		case platformerrors.CodeTokenExpired:
			return status.Error(codes.Unauthenticated, pErr.Message)
		case platformerrors.CodeTokenRevoked:
			return status.Error(codes.Unauthenticated, pErr.Message)
		case platformerrors.CodeAccountNotActive:
			return status.Error(codes.FailedPrecondition, pErr.Message)
		case platformerrors.CodeAlreadyExists:
			return status.Error(codes.AlreadyExists, pErr.Message)
		case platformerrors.CodeNotFound:
			return status.Error(codes.NotFound, pErr.Message)
		case platformerrors.CodePermissionDenied:
			return status.Error(codes.PermissionDenied, pErr.Message)
		case platformerrors.CodeFailedPrecondition:
			return status.Error(codes.FailedPrecondition, pErr.Message)
		default:
			return status.Error(codes.Internal, pErr.Message)
		}
	}

	// Fallback for default errors
	return status.Errorf(codes.Internal, "internal server error: %v", err)
}
