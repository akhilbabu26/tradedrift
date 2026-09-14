package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

type incidentRepo struct {
	db *pgxpool.Pool
}

var _ repository.IncidentRepository = (*incidentRepo)(nil)

// NewIncidentRepo constructs an IncidentRepository backed by PostgreSQL.
func NewIncidentRepo(db *pgxpool.Pool) repository.IncidentRepository {
	return &incidentRepo{db: db}
}

// Create inserts a new platform incident.
func (r *incidentRepo) Create(ctx context.Context, incident *domain.Incident) error {
	meta, err := json.Marshal(incident.Metadata)
	if err != nil {
		return fmt.Errorf("incident_repo: marshal metadata: %w", err)
	}

	query := `
		INSERT INTO admin_incidents
			(id, service_name, severity, status, title, root_cause,
			 triggered_at, detected_at, resolved_at, last_seen_at,
			 probe_failure_count, mttd_seconds, mttr_seconds, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`
	_, err = r.db.Exec(ctx, query,
		incident.ID,
		incident.ServiceName,
		string(incident.Severity),
		string(incident.Status),
		incident.Title,
		incident.RootCause,
		incident.TriggeredAt,
		incident.DetectedAt,
		incident.ResolvedAt,
		incident.LastSeenAt,
		incident.ProbeFailureCount,
		incident.MTTDSeconds,
		incident.MTTRSeconds,
		meta,
		incident.CreatedAt,
		incident.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrActiveIncidentExists
		}
		return fmt.Errorf("incident_repo: create: %w", err)
	}
	return nil
}

// GetActiveByService returns the currently open or investigating incident for a service, or nil if none exists.
func (r *incidentRepo) GetActiveByService(ctx context.Context, serviceName string) (*domain.Incident, error) {
	query := `
		SELECT id, service_name, severity, status, title, root_cause,
		       triggered_at, detected_at, resolved_at, last_seen_at,
		       probe_failure_count, mttd_seconds, mttr_seconds, metadata, created_at, updated_at
		FROM admin_incidents
		WHERE service_name = $1 AND status IN ('OPEN', 'INVESTIGATING')
		LIMIT 1
	`
	row := r.db.QueryRow(ctx, query, serviceName)
	inc, err := scanIncident(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("incident_repo: get active by service: %w", err)
	}
	return inc, nil
}

// GetByID returns the incident by primary key.
func (r *incidentRepo) GetByID(ctx context.Context, id string) (*domain.Incident, error) {
	query := `
		SELECT id, service_name, severity, status, title, root_cause,
		       triggered_at, detected_at, resolved_at, last_seen_at,
		       probe_failure_count, mttd_seconds, mttr_seconds, metadata, created_at, updated_at
		FROM admin_incidents
		WHERE id = $1
	`
	row := r.db.QueryRow(ctx, query, id)
	inc, err := scanIncident(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("incident_repo: get by id: %w", err)
	}
	return inc, nil
}

// UpdateHeartbeat updates the last_seen_at timestamp and failure counter during ongoing outage probes.
func (r *incidentRepo) UpdateHeartbeat(ctx context.Context, id string, lastSeenAt time.Time, failureCount int) error {
	query := `
		UPDATE admin_incidents
		SET last_seen_at        = $2,
		    probe_failure_count = $3,
		    updated_at          = NOW()
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, id, lastSeenAt, failureCount)
	if err != nil {
		return fmt.Errorf("incident_repo: update heartbeat: %w", err)
	}
	return nil
}

// Resolve transitions the incident to RESOLVED and stores the MTTR duration.
func (r *incidentRepo) Resolve(ctx context.Context, id string, resolvedAt time.Time, mttrSeconds float64, rootCause string) error {
	query := `
		UPDATE admin_incidents
		SET status       = 'RESOLVED',
		    resolved_at  = $2,
		    mttr_seconds = $3,
		    root_cause   = CASE WHEN $4 <> '' THEN $4 ELSE root_cause END,
		    updated_at   = NOW()
		WHERE id = $1 AND status IN ('OPEN', 'INVESTIGATING')
	`
	tag, err := r.db.Exec(ctx, query, id, resolvedAt, mttrSeconds, rootCause)
	if err != nil {
		return fmt.Errorf("incident_repo: resolve: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrIncidentAlreadyResolved
	}
	return nil
}

// List queries incidents matching the filter criteria.
func (r *incidentRepo) List(ctx context.Context, filter repository.IncidentFilter) ([]*domain.Incident, error) {
	var conditions []string
	var args []any
	argIdx := 1

	if filter.ServiceName != "" {
		conditions = append(conditions, fmt.Sprintf("service_name = $%d", argIdx))
		args = append(args, filter.ServiceName)
		argIdx++
	}
	if filter.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, string(filter.Status))
		argIdx++
	}
	if filter.Severity != "" {
		conditions = append(conditions, fmt.Sprintf("severity = $%d", argIdx))
		args = append(args, string(filter.Severity))
		argIdx++
	}
	if filter.Since != nil {
		conditions = append(conditions, fmt.Sprintf("triggered_at >= $%d", argIdx))
		args = append(args, *filter.Since)
		argIdx++
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(`
		SELECT id, service_name, severity, status, title, root_cause,
		       triggered_at, detected_at, resolved_at, last_seen_at,
		       probe_failure_count, mttd_seconds, mttr_seconds, metadata, created_at, updated_at
		FROM admin_incidents
		%s
		ORDER BY triggered_at DESC, id DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)

	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("incident_repo: list: %w", err)
	}
	defer rows.Close()

	var incidents []*domain.Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("incident_repo: scan list row: %w", err)
		}
		incidents = append(incidents, inc)
	}
	return incidents, nil
}

// GetStats calculates incident counts and average MTTD/MTTR since a given timestamp.
func (r *incidentRepo) GetStats(ctx context.Context, since time.Time) (*repository.IncidentStats, error) {
	query := `
		SELECT
			COUNT(*) AS total_incidents,
			COUNT(*) FILTER (WHERE status IN ('OPEN', 'INVESTIGATING')) AS open_incidents,
			COUNT(*) FILTER (WHERE status = 'RESOLVED') AS resolved_incidents,
			AVG(mttd_seconds) AS avg_mttd,
			COALESCE(AVG(mttr_seconds) FILTER (WHERE status = 'RESOLVED'), 0.0) AS avg_mttr
		FROM admin_incidents
		WHERE triggered_at >= $1
	`
	row := r.db.QueryRow(ctx, query, since)
	var stats repository.IncidentStats
	err := row.Scan(
		&stats.TotalIncidents,
		&stats.OpenIncidents,
		&stats.ResolvedIncidents,
		&stats.AverageMTTDSeconds,
		&stats.AverageMTTRSeconds,
	)
	if err != nil {
		return nil, fmt.Errorf("incident_repo: get stats: %w", err)
	}
	return &stats, nil
}

func scanIncident(row pgx.Row) (*domain.Incident, error) {
	var inc domain.Incident
	var severityStr, statusStr string
	var metaBytes []byte

	err := row.Scan(
		&inc.ID,
		&inc.ServiceName,
		&severityStr,
		&statusStr,
		&inc.Title,
		&inc.RootCause,
		&inc.TriggeredAt,
		&inc.DetectedAt,
		&inc.ResolvedAt,
		&inc.LastSeenAt,
		&inc.ProbeFailureCount,
		&inc.MTTDSeconds,
		&inc.MTTRSeconds,
		&metaBytes,
		&inc.CreatedAt,
		&inc.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	inc.Severity = domain.IncidentSeverity(severityStr)
	inc.Status = domain.IncidentStatus(statusStr)

	if len(metaBytes) > 0 {
		if err := json.Unmarshal(metaBytes, &inc.Metadata); err != nil {
			return nil, fmt.Errorf("incident_repo: decode metadata: %w", err)
		}
	}
	return &inc, nil
}
