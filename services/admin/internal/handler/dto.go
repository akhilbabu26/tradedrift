package handler

import (
	"encoding/json"
	"time"

	"tradedrift/services/admin/internal/domain"
)

// ─── Request DTOs ─────────────────────────────────────────────────────────────

// SuspendUserRequest is the body for POST /admin/users/:id/suspend
type SuspendUserRequest struct {
	Reason string `json:"reason"`
}

// UnsuspendUserRequest is the body for POST /admin/users/:id/unsuspend
type UnsuspendUserRequest struct {
	Reason string `json:"reason"`
}

// FreezeWalletRequest is the body for POST /admin/users/:id/wallets/:asset/freeze
type FreezeWalletRequest struct {
	Reason string `json:"reason"`
}

// UnfreezeWalletRequest is the body for POST /admin/users/:id/wallets/:asset/unfreeze
type UnfreezeWalletRequest struct {
	Reason string `json:"reason"`
}

// HaltMarketRequest is the body for POST /admin/markets/:id/halt
type HaltMarketRequest struct {
	Reason string `json:"reason"`
}

// ResumeMarketRequest is the body for POST /admin/markets/:id/resume
type ResumeMarketRequest struct {
	Reason string `json:"reason"`
}

// ─── Response DTOs ────────────────────────────────────────────────────────────

// OperationResponseDTO is the standard response for all admin mutations.
type OperationResponseDTO struct {
	OperationID   string          `json:"operation_id"`
	AdminID       string          `json:"admin_id"`
	OperationType string          `json:"operation_type"`
	TargetID      string          `json:"target_id"`
	Status        string          `json:"status"`
	ResponseBody  json.RawMessage `json:"response,omitempty"`
	CreatedAt     string          `json:"created_at"`
}

// ToOperationDTO converts a domain.AdminOperation to a REST response.
func ToOperationDTO(op *domain.AdminOperation) *OperationResponseDTO {
	return &OperationResponseDTO{
		OperationID:   op.ID,
		AdminID:       op.AdminID,
		OperationType: op.OperationType,
		TargetID:      op.TargetID,
		Status:        string(op.Status),
		ResponseBody:  op.ResponseBody,
		CreatedAt:     op.CreatedAt.Format(time.RFC3339),
	}
}
