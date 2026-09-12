package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

type auditRepo struct {
	db *pgxpool.Pool
}

var _ repository.AuditRepository = (*auditRepo)(nil)

// NewAuditRepo constructs an AuditRepository backed by PostgreSQL.
func NewAuditRepo(db *pgxpool.Pool) repository.AuditRepository {
	return &auditRepo{db: db}
}

// Insert writes a standalone audit log row.
func (r *auditRepo) Insert(ctx context.Context, log *domain.AuditLog) error {
	meta, err := json.Marshal(log.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	query := `
		INSERT INTO admin_audit_log
			(id, admin_id, operation_id, request_id, action, target_type, target_id,
			 reason, metadata, ip_address, user_agent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	_, err = r.db.Exec(ctx, query,
		log.ID, log.AdminID, log.OperationID, log.RequestID,
		log.Action, log.TargetType, log.TargetID, log.Reason,
		meta, log.IPAddress, log.UserAgent, log.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("audit_repo: insert: %w", err)
	}
	return nil
}
