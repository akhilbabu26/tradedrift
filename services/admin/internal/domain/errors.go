package domain

import (
	"errors"

	platformerrors "tradedrift/platform/errors"
)

var (
	// Authentication / Authorization
	ErrUnauthorized = platformerrors.WithCode(errors.New("unauthorized"), platformerrors.CodeUnauthenticated)
	ErrForbidden    = platformerrors.WithCode(errors.New("forbidden: admin role required"), platformerrors.CodePermissionDenied)
	ErrMissingToken = platformerrors.WithCode(errors.New("missing Authorization header"), platformerrors.CodeUnauthenticated)

	// Idempotency
	ErrOperationInProgress   = platformerrors.WithCode(errors.New("operation in progress: same idempotency key is currently being processed"), platformerrors.CodeFailedPrecondition)
	ErrMissingIdempotencyKey = platformerrors.WithCode(errors.New("Idempotency-Key header is required"), platformerrors.CodeInvalidArgument)
	ErrIdempotencyKeyTooLong = platformerrors.WithCode(errors.New("Idempotency-Key must be 1-128 characters"), platformerrors.CodeInvalidArgument)

	// Business validation
	ErrInvalidTarget     = platformerrors.WithCode(errors.New("target_id is required"), platformerrors.CodeInvalidArgument)
	ErrInvalidReason     = platformerrors.WithCode(errors.New("reason is required and must be 5-500 characters"), platformerrors.CodeInvalidArgument)
	ErrOperationNotFound = platformerrors.WithCode(errors.New("operation not found"), platformerrors.CodeNotFound)

	// Saga
	ErrSagaExhausted = platformerrors.WithCode(errors.New("saga task exhausted all retry attempts"), platformerrors.CodeInternal)

	// Downstream
	ErrAuthUnavailable   = platformerrors.WithCode(errors.New("auth service temporarily unavailable"), platformerrors.CodeUnavailable)
	ErrWalletUnavailable = platformerrors.WithCode(errors.New("wallet service temporarily unavailable"), platformerrors.CodeUnavailable)

	// Worker Leasing
	ErrWorkerLeaseLost = platformerrors.WithCode(errors.New("worker lease lost or expired; operation aborted"), platformerrors.CodeFailedPrecondition)

	// Incidents
	ErrIncidentNotFound        = platformerrors.WithCode(errors.New("incident not found"), platformerrors.CodeNotFound)
	ErrIncidentAlreadyResolved = platformerrors.WithCode(errors.New("incident is already resolved"), platformerrors.CodeFailedPrecondition)
	ErrActiveIncidentExists    = platformerrors.WithCode(errors.New("active incident already exists for service"), platformerrors.CodeFailedPrecondition)
)
