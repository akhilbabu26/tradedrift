package domain

import "time"

// AuditLog actions
const (
	ActionSuspendUser   = "SUSPEND_USER"
	ActionUnsuspendUser = "UNSUSPEND_USER"
	ActionFreezeWallet  = "FREEZE_WALLET"
	ActionUnfreezeWallet = "UNFREEZE_WALLET"
	ActionHaltMarket    = "HALT_MARKET"
	ActionResumeMarket  = "RESUME_MARKET"
)

// Target types
const (
	TargetTypeUser   = "USER"
	TargetTypeWallet = "WALLET"
	TargetTypeMarket = "MARKET"
)

// AuditLog represents an immutable record of an administrative action.
// Enforced as append-only by PostgreSQL trigger trg_enforce_audit_immutability.
type AuditLog struct {
	ID          string            `json:"id"`
	AdminID     string            `json:"admin_id"`
	OperationID *string           `json:"operation_id,omitempty"`
	RequestID   string            `json:"request_id"`
	Action      string            `json:"action"`
	TargetType  string            `json:"target_type"`
	TargetID    string            `json:"target_id"`
	Reason      string            `json:"reason"`
	Metadata    map[string]string `json:"metadata"`
	IPAddress   string            `json:"ip_address,omitempty"`
	UserAgent   string            `json:"user_agent,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}
