package recovery

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"tradedrift/services/matching-engine/internal/market"
)

// OrderVerifier independently verifies that authoritative Order Service records
// have fully reconciled all active nonterminal orders before trading activation.
type OrderVerifier interface {
	VerifyOrdersReconciled(ctx context.Context, marketID string, manifestChecksum string) error
}

// SettlementVerifier verifies that no unresolved settlement discrepancies or
// fill quantity mismatches exist before approving reconciliation.
type SettlementVerifier interface {
	VerifySettlementInvariants(ctx context.Context, marketID string) error
}

// LeaseChecker confirms that this process currently holds the active partition lease.
type LeaseChecker interface {
	HasLease(partition int) bool
}

// ReconciliationApprovalRequest holds parameters for approving market reconciliation.
type ReconciliationApprovalRequest struct {
	MarketID         string `json:"market_id"`
	Generation       string `json:"generation"`
	ManifestChecksum string `json:"manifest_checksum"`
	ApprovedBy       string `json:"approved_by"`
	AuthToken        string `json:"auth_token,omitempty"`
}

// ReconciliationApprovalResult reports the outcome of the approval operation.
type ReconciliationApprovalResult struct {
	Approved        bool      `json:"approved"`
	MarketID        string    `json:"market_id"`
	Generation      string    `json:"generation"`
	Status          string    `json:"status"`
	AlreadyApproved bool      `json:"already_approved,omitempty"`
	ApprovedAt      time.Time `json:"approved_at"`
}

// ReconciliationService provides authoritative, durable, and auditable reconciliation approval.
type ReconciliationService struct {
	mu                 sync.Mutex
	db                 pgxConn
	manager            *market.MarketManager
	leaseChecker       LeaseChecker
	orderVerifier      OrderVerifier
	settlementVerifier SettlementVerifier
	adminSecret        string
}

// NewReconciliationService creates a new ReconciliationService instance.
func NewReconciliationService(
	db pgxConn,
	manager *market.MarketManager,
	leaseChecker LeaseChecker,
	orderVerifier OrderVerifier,
	settlementVerifier SettlementVerifier,
	adminSecret string,
) *ReconciliationService {
	return &ReconciliationService{
		db:                 db,
		manager:            manager,
		leaseChecker:       leaseChecker,
		orderVerifier:      orderVerifier,
		settlementVerifier: settlementVerifier,
		adminSecret:        adminSecret,
	}
}

// ValidateDependencies checks that all required verifiers, lease checker, DB, manager, and secret are present.
func (s *ReconciliationService) ValidateDependencies() error {
	if s.adminSecret == "" {
		return errors.New("admin secret is missing")
	}
	if s.leaseChecker == nil {
		return errors.New("lease checker is missing")
	}
	if s.orderVerifier == nil {
		return errors.New("order verifier is missing")
	}
	if s.settlementVerifier == nil {
		return errors.New("settlement verifier is missing")
	}
	if s.db == nil {
		return errors.New("database connection is missing")
	}
	if s.manager == nil {
		return errors.New("market manager is missing")
	}
	return nil
}

// isHex returns true only if the string contains valid hexadecimal digits [0-9a-fA-F].
func isHex(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// ApproveReconciliation verifies all invariants and durably approves market reconciliation.
func (s *ReconciliationService) ApproveReconciliation(ctx context.Context, req ReconciliationApprovalRequest) (*ReconciliationApprovalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Mandatory service-level authentication guard (fail closed if secret missing)
	if s.adminSecret == "" {
		return nil, fmt.Errorf("reconciliation approval disabled: admin secret not configured")
	}
	if req.AuthToken == "" || subtle.ConstantTimeCompare([]byte(req.AuthToken), []byte(s.adminSecret)) != 1 {
		return nil, fmt.Errorf("unauthorized: invalid or missing reconciliation auth token")
	}

	// 2. Request parameter validation
	if req.MarketID == "" {
		return nil, fmt.Errorf("market_id is required")
	}
	if req.Generation == "" {
		return nil, fmt.Errorf("generation is required")
	}
	if len(req.ManifestChecksum) != 64 || !isHex(req.ManifestChecksum) {
		return nil, fmt.Errorf("invalid manifest_checksum: must be a 64-character hexadecimal SHA-256 hash")
	}

	// 3. Verifier and lease dependency enforcement (fail closed if any verifier or lease checker is nil)
	if s.orderVerifier == nil {
		return nil, fmt.Errorf("cannot approve reconciliation: order verifier is nil")
	}
	if s.settlementVerifier == nil {
		return nil, fmt.Errorf("cannot approve reconciliation: settlement verifier is nil")
	}
	if s.leaseChecker == nil {
		return nil, fmt.Errorf("cannot approve reconciliation: lease checker is nil")
	}

	// 4. Engine state check
	engine := s.manager.Get(req.MarketID)
	if engine == nil {
		return nil, fmt.Errorf("market %s not found in matching engine manager", req.MarketID)
	}
	if engine.IsFatalHalt() {
		return nil, fmt.Errorf("cannot approve reconciliation: engine for %s is halted under fatal error", req.MarketID)
	}
	if engine.IsBlocked() {
		return nil, fmt.Errorf("cannot approve reconciliation: engine for %s is blocked", req.MarketID)
	}

	// 5. Lease check: ensure engine holds the active partition lease (fail closed)
	if !s.leaseChecker.HasLease(engine.Partition()) {
		return nil, fmt.Errorf("lease loss: cannot approve reconciliation without partition lease for partition %d", engine.Partition())
	}

	// 6. Database Transaction for Durable State Verification & Commit
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin reconciliation approval transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		generation       string
		partition        int
		logEndOffset     int64
		manifestChecksum string
		status           string
	)

	const queryAuditSQL = `
		SELECT generation, partition, log_end_offset, manifest_checksum, status
		FROM market_reconciliation_audit
		WHERE market_id = $1 AND generation = $2
		FOR UPDATE`
	row := tx.QueryRow(ctx, queryAuditSQL, req.MarketID, req.Generation)
	if err := row.Scan(&generation, &partition, &logEndOffset, &manifestChecksum, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("no reconciliation audit record found for market %s generation %s", req.MarketID, req.Generation)
		}
		return nil, fmt.Errorf("query reconciliation audit: %w", err)
	}

	// 7. Active reset generation binding: ensure this approval corresponds to the active reset
	var latestResetGen string
	err = tx.QueryRow(ctx, "SELECT generation FROM market_reset_audit WHERE market_id = $1 ORDER BY authorized_at DESC LIMIT 1", req.MarketID).Scan(&latestResetGen)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("query active reset generation: %w", err)
	}
	if latestResetGen != "" && latestResetGen != req.Generation {
		return nil, fmt.Errorf("stale approval: requested generation %s does not match active reset generation %s",
			req.Generation, latestResetGen)
	}

	// 8. Strict Manifest checksum validation (both stored and requested must be 64-char hex and match exactly)
	if manifestChecksum == "" {
		return nil, fmt.Errorf("database reconciliation record has empty manifest checksum for generation %s", req.Generation)
	}
	if len(manifestChecksum) != 64 || !isHex(manifestChecksum) {
		return nil, fmt.Errorf("stored manifest checksum is invalid: %s", manifestChecksum)
	}
	if !strings.EqualFold(manifestChecksum, req.ManifestChecksum) {
		return nil, fmt.Errorf("stale approval: manifest checksum mismatch (recorded %s != requested %s)",
			manifestChecksum, req.ManifestChecksum)
	}

	// 9. Independent Authoritative Order Service Verification (fail closed on any error)
	if err := s.orderVerifier.VerifyOrdersReconciled(ctx, req.MarketID, manifestChecksum); err != nil {
		return nil, fmt.Errorf("authoritative order verification failed: %w", err)
	}

	// 10. Settlement Invariant Check (fail closed on any error)
	if err := s.settlementVerifier.VerifySettlementInvariants(ctx, req.MarketID); err != nil {
		return nil, fmt.Errorf("settlement invariant check failed: %w", err)
	}

	// 11. Re-check lease immediately before state transition (fail closed)
	if s.leaseChecker == nil || !s.leaseChecker.HasLease(engine.Partition()) {
		return nil, fmt.Errorf("lease loss during approval: partition lease lost before state transition (partition=%d)", engine.Partition())
	}

	// 12. Idempotency handling: If already approved, all checks passed; complete activation if engine is reconciling
	if status == "APPROVED" {
		_ = tx.Rollback(ctx)
		if engine.IsReconciling() {
			if s.leaseChecker == nil || !s.leaseChecker.HasLease(engine.Partition()) {
				return nil, fmt.Errorf("lease loss during idempotent approval: partition lease lost (partition=%d)", engine.Partition())
			}
			if err := engine.ApproveReconciliation(); err != nil {
				engine.TriggerFatalHalt()
				return nil, fmt.Errorf("fatal: reconciliation approved but in-memory activation failed: %w", err)
			}
			log.Printf("[reconciliation] ✅ ACTIVATED (re-approval): market=%s generation=%s transitioned to LIVE", req.MarketID, req.Generation)
		}
		return &ReconciliationApprovalResult{
			Approved:        true,
			MarketID:        req.MarketID,
			Generation:      req.Generation,
			Status:          "APPROVED",
			AlreadyApproved: true,
			ApprovedAt:      time.Now().UTC(),
		}, nil
	}

	// 13. Durably persist approval
	now := time.Now().UTC()
	approvedBy := req.ApprovedBy
	if approvedBy == "" {
		approvedBy = "operator"
	}

	const updateAuditSQL = `
		UPDATE market_reconciliation_audit
		SET status = 'APPROVED', approved_at = $1, approved_by = $2
		WHERE market_id = $3 AND generation = $4`
	tag, err := tx.Exec(ctx, updateAuditSQL, now, approvedBy, req.MarketID, req.Generation)
	if err != nil {
		return nil, fmt.Errorf("persist reconciliation approval: %w", err)
	}
	if tag.String() != "" && tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("failed to update reconciliation audit record for market %s generation %s", req.MarketID, req.Generation)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit reconciliation approval transaction: %w", err)
	}

	// 14. Atomic Engine CAS transition to LIVE (verify lease immediately before CAS)
	if s.leaseChecker == nil || !s.leaseChecker.HasLease(engine.Partition()) {
		engine.TriggerFatalHalt()
		return nil, fmt.Errorf("lease lost immediately after approval commit: partition lease lost (partition=%d)", engine.Partition())
	}
	if err := engine.ApproveReconciliation(); err != nil {
		// DB commit succeeded, but in-memory transition failed! Fail-stop immediately to prevent split-brain.
		engine.TriggerFatalHalt()
		return nil, fmt.Errorf("fatal: reconciliation approved in database but in-memory transition failed: %w", err)
	}

	log.Printf("[reconciliation] ✅ APPROVED: market=%s generation=%s partition=%d logEndOffset=%d approvedBy=%s — transitioned to LIVE",
		req.MarketID, req.Generation, partition, logEndOffset, approvedBy)

	return &ReconciliationApprovalResult{
		Approved:   true,
		MarketID:   req.MarketID,
		Generation: req.Generation,
		Status:     "APPROVED",
		ApprovedAt: now,
	}, nil
}

