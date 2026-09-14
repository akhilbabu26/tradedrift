package handler

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"

	platformerrors "tradedrift/platform/errors"
)

// ErrorResponse represents a structured JSON error response compatible with API standards.
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// WriteErrorJSON formats and writes an ErrorResponse with the given HTTP status.
func WriteErrorJSON(w http.ResponseWriter, status int, message, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Error: message,
		Code:  code,
	})
}

// HandleServiceError maps service-layer and domain errors to appropriate HTTP status codes and responses.
func HandleServiceError(w http.ResponseWriter, err error, log *zap.Logger) {
	if err == nil {
		return
	}

	code := platformerrors.GetCode(err)
	status := http.StatusInternalServerError

	switch code {
	case platformerrors.CodeInvalidArgument:
		status = http.StatusBadRequest
	case platformerrors.CodeUnauthenticated:
		status = http.StatusUnauthorized
	case platformerrors.CodePermissionDenied:
		status = http.StatusForbidden
	case platformerrors.CodeNotFound:
		status = http.StatusNotFound
	case platformerrors.CodeFailedPrecondition, platformerrors.CodeAlreadyExists:
		status = http.StatusConflict
	case platformerrors.CodeUnavailable:
		status = http.StatusServiceUnavailable
	case platformerrors.CodeInternal:
		status = http.StatusInternalServerError
	default:
		status = http.StatusInternalServerError
	}

	if status == http.StatusInternalServerError && log != nil {
		log.Error("admin handler: internal server error", zap.Error(err), zap.String("code", code))
	}

	WriteErrorJSON(w, status, err.Error(), code)
}
