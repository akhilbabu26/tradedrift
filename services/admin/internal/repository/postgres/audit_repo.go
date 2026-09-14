package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

// GetAuditLogsInWindow fetches audit events that occurred within a specific timestamp window [start, end].
func (r *auditRepo) GetAuditLogsInWindow(ctx context.Context, start, end time.Time, limit int) ([]domain.AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	query := `
		SELECT id, admin_id, operation_id, request_id, action, target_type, target_id,
		       reason, metadata, ip_address, user_agent, created_at
		FROM admin_audit_log
		WHERE created_at >= $1 AND created_at <= $2
		ORDER BY created_at ASC
		LIMIT $3
	`
	rows, err := r.db.Query(ctx, query, start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("audit_repo: get in window: %w", err)
	}
	defer rows.Close()

	var logs []domain.AuditLog
	for rows.Next() {
		var l domain.AuditLog
		var metaBytes []byte
		err := rows.Scan(
			&l.ID,
			&l.AdminID,
			&l.OperationID,
			&l.RequestID,
			&l.Action,
			&l.TargetType,
			&l.TargetID,
			&l.Reason,
			&metaBytes,
			&l.IPAddress,
			&l.UserAgent,
			&l.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("audit_repo: scan window row: %w", err)
		}
		if len(metaBytes) > 0 {
			_ = json.Unmarshal(metaBytes, &l.Metadata)
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// GetAuditStats aggregates audit counts by action, target type, and active admins since a given timestamp.
func (r *auditRepo) GetAuditStats(ctx context.Context, since time.Time) (*repository.AuditAnalyticsStats, error) {
	stats := &repository.AuditAnalyticsStats{
		ByAction:     make(map[string]int),
		ByTargetType: make(map[string]int),
		TopAdmins:    make(map[string]int),
	}

	// 1. Actions Breakdown
	actionQuery := `
		SELECT action, COUNT(*)
		FROM admin_audit_log
		WHERE created_at >= $1
		GROUP BY action
	`
	actionRows, err := r.db.Query(ctx, actionQuery, since)
	if err != nil {
		return nil, fmt.Errorf("audit_repo: stats by action: %w", err)
	}
	defer actionRows.Close()

	total := 0
	for actionRows.Next() {
		var action string
		var count int
		if err := actionRows.Scan(&action, &count); err != nil {
			return nil, err
		}
		stats.ByAction[action] = count
		total += count
	}
	stats.TotalAuditEvents = total

	// 2. Target Types Breakdown
	targetQuery := `
		SELECT target_type, COUNT(*)
		FROM admin_audit_log
		WHERE created_at >= $1
		GROUP BY target_type
	`
	targetRows, err := r.db.Query(ctx, targetQuery, since)
	if err != nil {
		return nil, fmt.Errorf("audit_repo: stats by target: %w", err)
	}
	defer targetRows.Close()

	for targetRows.Next() {
		var targetType string
		var count int
		if err := targetRows.Scan(&targetType, &count); err != nil {
			return nil, err
		}
		stats.ByTargetType[targetType] = count
	}

	// 3. Top Admins Activity
	adminQuery := `
		SELECT admin_id, COUNT(*)
		FROM admin_audit_log
		WHERE created_at >= $1
		GROUP BY admin_id
		ORDER BY COUNT(*) DESC
		LIMIT 10
	`
	adminRows, err := r.db.Query(ctx, adminQuery, since)
	if err != nil {
		return nil, fmt.Errorf("audit_repo: stats by admin: %w", err)
	}
	defer adminRows.Close()

	for adminRows.Next() {
		var adminID string
		var count int
		if err := adminRows.Scan(&adminID, &count); err != nil {
			return nil, err
		}
		stats.TopAdmins[adminID] = count
	}

	return stats, nil
}

