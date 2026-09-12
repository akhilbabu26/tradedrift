package domain

import "time"

// SagaStatus represents the state machine for admin_saga_tasks.
//
//	PENDING → RETRYING → COMPLETED
//	                  └→ EXHAUSTED (all 10 attempts consumed)
type SagaStatus string

const (
	SagaStatusPending   SagaStatus = "PENDING"
	SagaStatusRetrying  SagaStatus = "RETRYING"
	SagaStatusCompleted SagaStatus = "COMPLETED"
	SagaStatusExhausted SagaStatus = "EXHAUSTED"
)

// SagaTaskType is the category of background compensating task.
const (
	SagaTaskAuthInvalidateSessions = "AUTH_INVALIDATE_SESSIONS"
)

// RetrySchedule is the explicit per-attempt base delay.
// Index = attempt_count (0-based, so attempt 1 uses index 0).
var RetrySchedule = []time.Duration{
	30 * time.Second, // attempt 1
	1 * time.Minute,  // attempt 2
	2 * time.Minute,  // attempt 3
	5 * time.Minute,  // attempt 4
	15 * time.Minute, // attempt 5
	30 * time.Minute, // attempt 6
	1 * time.Hour,    // attempt 7
	2 * time.Hour,    // attempt 8
	4 * time.Hour,    // attempt 9
	8 * time.Hour,    // attempt 10
}

// SagaTask is a persistent retry record for the Auth session invalidation saga.
type SagaTask struct {
	ID            string     `json:"id"`             // UUIDv7 task_id
	OperationID   string     `json:"operation_id"`
	TaskType      string     `json:"task_type"`
	Payload       []byte     `json:"payload"`        // raw JSON, e.g. {"user_id":"...", "reason":"..."}
	Status        SagaStatus `json:"status"`
	AttemptCount  int        `json:"attempt_count"`
	MaxAttempts   int        `json:"max_attempts"`   // always 10
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastError     *string    `json:"last_error,omitempty"`
	LockedAt      *time.Time `json:"locked_at,omitempty"`
	LockedBy      *string    `json:"locked_by,omitempty"` // worker lease token
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// NextDelay returns the base delay for the next attempt, or 8h if beyond the schedule.
func NextDelay(attemptCount int) time.Duration {
	if attemptCount < len(RetrySchedule) {
		return RetrySchedule[attemptCount]
	}
	return RetrySchedule[len(RetrySchedule)-1]
}

// SagaAuthPayload is the typed payload for AUTH_INVALIDATE_SESSIONS tasks.
type SagaAuthPayload struct {
	UserID    string `json:"user_id"`
	Reason    string `json:"reason"`
	RequestID string `json:"request_id"`
}
