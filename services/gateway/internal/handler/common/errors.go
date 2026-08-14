package common

import (
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"tradedrift/services/gateway/internal/response"
)

// WriteGRPCError maps gRPC status codes to standard HTTP JSON error responses.
func WriteGRPCError(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
		return
	}

	switch st.Code() {
	case codes.NotFound:
		response.WriteError(w, http.StatusNotFound, "NOT_FOUND", st.Message())
	case codes.InvalidArgument:
		response.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", st.Message())
	case codes.AlreadyExists:
		response.WriteError(w, http.StatusConflict, "ALREADY_EXISTS", st.Message())
	case codes.Unauthenticated:
		response.WriteError(w, http.StatusUnauthorized, "UNAUTHENTICATED", st.Message())
	case codes.PermissionDenied:
		response.WriteError(w, http.StatusForbidden, "PERMISSION_DENIED", st.Message())
	case codes.FailedPrecondition:
		response.WriteError(w, http.StatusUnprocessableEntity, "FAILED_PRECONDITION", st.Message())
	default:
		response.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", st.Message())
	}
}
