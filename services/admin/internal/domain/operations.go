package domain

import (
	"encoding/json"
	"time"
)

// OperationStatus represents the state machine for admin_operations.
//
//	PENDING → PROCESSING → COMPLETED
//	                    └→ FAILED (permanent rejection or non-retryable failure)
type OperationStatus string

const (
	OperationStatusPending    OperationStatus = "PENDING"
	OperationStatusProcessing OperationStatus = "PROCESSING"
	OperationStatusCompleted  OperationStatus = "COMPLETED"
	OperationStatusFailed     OperationStatus = "FAILED"
)

// Operation type constants (matches database CHECK constraint)
const (
	OpSuspendUser    = "SUSPEND_USER"
	OpUnsuspendUser  = "UNSUSPEND_USER"
	OpFreezeWallet   = "FREEZE_WALLET"
	OpUnfreezeWallet = "UNFREEZE_WALLET"
	OpHaltMarket     = "HALT_MARKET"
	OpResumeMarket   = "RESUME_MARKET"
)

// AdminOperation is an idempotent administrative command record.
type AdminOperation struct {
	ID             string          `json:"id"`              // UUIDv7 operation_id
	AdminID        string          `json:"admin_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	RequestID      string          `json:"request_id"`
	OperationType  string          `json:"operation_type"`
	TargetID       string          `json:"target_id"`
	Reason         string          `json:"reason"`
	Status         OperationStatus `json:"status"`
	ResponseBody   json.RawMessage `json:"response_body,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// IsTerminal returns true if the operation is in a terminal state.
func (o *AdminOperation) IsTerminal() bool {
	return o.Status == OperationStatusCompleted || o.Status == OperationStatusFailed
}
