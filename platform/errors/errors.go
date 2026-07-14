package errors

import (
	"errors"
	"fmt"
)

// Platform error codes matching the API Standards.
const (
	CodeTokenExpired       = "AUTH_TOKEN_EXPIRED"
	CodeInvalidCredentials = "AUTH_INVALID_CREDENTIALS"
	CodeTokenRevoked       = "AUTH_TOKEN_REVOKED"
	CodeAccountNotActive   = "AUTH_ACCOUNT_NOT_ACTIVE"

	CodeInvalidArgument    = "INVALID_ARGUMENT"
	CodeAlreadyExists      = "ALREADY_EXISTS"
	CodeNotFound           = "NOT_FOUND"
	CodeInternal           = "INTERNAL"
	CodePermissionDenied   = "PERMISSION_DENIED"
	CodeFailedPrecondition = "FAILED_PRECONDITION"
)

// PlatformError represents a structured, code-carrying error.
// It carries a machine-readable string code to be consumed by clients.
type PlatformError struct {
	Code    string // Machine-readable string code
	Message string // Human-readable message
	Err     error  // The underlying error (optional)
}

// Error implements the standard error interface.
func (e *PlatformError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap allows standard library functions like errors.Unwrap or errors.As to traverse the error chain.
func (e *PlatformError) Unwrap() error {
	return e.Err
}

// New creates a new PlatformError.
func New(code, message string) error {
	return &PlatformError{
		Code:    code,
		Message: message,
	}
}

// Wrap wraps an existing error into a PlatformError with a code and context message.
func Wrap(err error, code, message string) error {
	return &PlatformError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// GetCode extracts the machine-readable error code if the error chain contains a PlatformError.
// It returns an empty string if no PlatformError is found.
func GetCode(err error) string {
	var pErr *PlatformError
	if errors.As(err, &pErr) {
		return pErr.Code
	}
	return ""
}

// IsCode checks if the error chain contains a PlatformError with the given machine-readable code.
func IsCode(err error, code string) bool {
	return GetCode(err) == code
}

// As checks if the error is or wraps a PlatformError and returns it.
// This is a helper to avoid writing standard errors.As boilerplates.
func As(err error) (*PlatformError, bool) {
	var pErr *PlatformError
	if errors.As(err, &pErr) {
		return pErr, true
	}
	return nil, false
}
