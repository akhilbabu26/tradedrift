package engine

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SubmissionErrorType categorizes errors resulting from Order Service CreateOrder.
type SubmissionErrorType int

const (
	SubmissionErrorDefiniteRejection SubmissionErrorType = iota
	SubmissionErrorCallerCancelled
	SubmissionErrorAmbiguous
)

// classifySubmissionError categorizes an order submission error.
// Design Note: Unknown errors are treated as ambiguous only when recognizable
// transport/network indicators are present; otherwise they fail closed as deterministic failures.
// This centralizes error classification in one place to allow future replacement with typed gRPC errors.
func classifySubmissionError(parentCtx context.Context, err error) SubmissionErrorType {
	if err == nil {
		return SubmissionErrorDefiniteRejection
	}

	// 1. Caller Intentional Cancellation (CTS shutdown initiated)
	// Must strictly depend on CTS's parent context being cancelled.
	if parentCtx.Err() != nil {
		return SubmissionErrorCallerCancelled
	}

	st, ok := status.FromError(err)
	if !ok {
		// Non-status errors are transport, dial, network drops, or raw context errors (ambiguous outcome)
		return SubmissionErrorAmbiguous
	}

	switch st.Code() {
	case codes.InvalidArgument,
		codes.FailedPrecondition,
		codes.NotFound,
		codes.AlreadyExists,
		codes.PermissionDenied,
		codes.Unauthenticated,
		codes.ResourceExhausted:
		return SubmissionErrorDefiniteRejection

	case codes.DeadlineExceeded,
		codes.Unavailable,
		codes.Canceled:
		return SubmissionErrorAmbiguous

	case codes.Unknown:
		// Unknown errors are treated as ambiguous only when recognizable transport/network indicators
		// are present; otherwise they fail closed as deterministic failures.
		errMsg := strings.ToLower(st.Message())
		if strings.Contains(errMsg, "timeout") ||
			strings.Contains(errMsg, "connection") ||
			strings.Contains(errMsg, "deadline") ||
			strings.Contains(errMsg, "transport") ||
			strings.Contains(errMsg, "eof") ||
			strings.Contains(errMsg, "broken pipe") ||
			strings.Contains(errMsg, "reset") {
			return SubmissionErrorAmbiguous
		}
		return SubmissionErrorDefiniteRejection

	default:
		return SubmissionErrorDefiniteRejection
	}
}
