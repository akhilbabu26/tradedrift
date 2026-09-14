package service

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

// IncidentService manages operational incident tracking, manual resolution, and audit correlation.
type IncidentService struct {
	incidentRepo repository.IncidentRepository
	auditRepo    repository.AuditRepository
	topologyEng  *TopologyEngine
	log          *zap.Logger
}

// NewIncidentService constructs a new IncidentService.
func NewIncidentService(
	incidentRepo repository.IncidentRepository,
	auditRepo repository.AuditRepository,
	topologyEng *TopologyEngine,
	log *zap.Logger,
) *IncidentService {
	return &IncidentService{
		incidentRepo: incidentRepo,
		auditRepo:    auditRepo,
		topologyEng:  topologyEng,
		log:          log,
	}
}

// ListIncidents queries incidents by status, service, severity, and date range.
func (s *IncidentService) ListIncidents(ctx context.Context, filter repository.IncidentFilter) ([]*domain.Incident, error) {
	return s.incidentRepo.List(ctx, filter)
}

// GetIncidentByID fetches a single incident record.
func (s *IncidentService) GetIncidentByID(ctx context.Context, id string) (*domain.Incident, error) {
	inc, err := s.incidentRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("incident_service: get by id: %w", err)
	}
	if inc == nil {
		return nil, domain.ErrIncidentNotFound
	}
	return inc, nil
}

// GetCorrelatedIncident gathers temporal context around an incident by fetching administrative audit
// actions that occurred within its time window and enumerating downstream topological dependencies.
// Note: This provides time-correlated forensic context for operator review rather than automated root-cause certainty.
func (s *IncidentService) GetCorrelatedIncident(ctx context.Context, id string, windowBefore, windowAfter time.Duration) (*domain.CorrelatedIncident, error) {
	inc, err := s.GetIncidentByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Validate and clamp correlation windows (minimum 1m, maximum 24h, default 15m)
	const (
		minWindow     = 1 * time.Minute
		maxWindow     = 24 * time.Hour
		defaultWindow = 15 * time.Minute
	)

	if windowBefore < minWindow {
		windowBefore = defaultWindow
	} else if windowBefore > maxWindow {
		windowBefore = maxWindow
	}

	if windowAfter < minWindow {
		windowAfter = defaultWindow
	} else if windowAfter > maxWindow {
		windowAfter = maxWindow
	}

	now := time.Now().UTC()
	winStart := inc.TriggeredAt.Add(-windowBefore)
	var winEnd time.Time
	if inc.ResolvedAt != nil {
		winEnd = inc.ResolvedAt.Add(windowAfter)
	} else {
		// Cap ongoing incident correlation window at current UTC time to prevent looking arbitrarily into the future
		winEnd = now
	}

	var auditLogs []domain.AuditLog
	if s.auditRepo != nil {
		logs, err := s.auditRepo.GetAuditLogsInWindow(ctx, winStart, winEnd, 100)
		if err != nil {
			s.log.Error("incident_service: failed to fetch correlated audit logs", zap.Error(err))
		} else {
			auditLogs = logs
		}
	}

	var impacted []string
	if s.topologyEng != nil {
		impacted = s.topologyEng.GetRelatedDependencies(inc.ServiceName)
	}

	return &domain.CorrelatedIncident{
		Incident:               *inc,
		AffectedDependencies:   impacted,
		CorrelatedAuditLogs:    auditLogs,
		CorrelationWindowStart: winStart,
		CorrelationWindowEnd:   winEnd,
	}, nil
}

// ResolveIncident allows an operator to manually resolve an incident and record root cause findings.
func (s *IncidentService) ResolveIncident(ctx context.Context, id string, rootCause string) error {
	inc, err := s.GetIncidentByID(ctx, id)
	if err != nil {
		return err
	}

	// Guard: check if the incident is already resolved before calculating MTTR
	if inc.Status == domain.IncidentStatusResolved {
		return domain.ErrIncidentAlreadyResolved
	}

	now := time.Now().UTC()
	mttr := now.Sub(inc.TriggeredAt).Seconds()
	if mttr < 0 {
		mttr = 0
	}

	err = s.incidentRepo.Resolve(ctx, id, now, mttr, rootCause)
	if err != nil {
		return fmt.Errorf("incident_service: resolve: %w", err)
	}
	s.log.Info("incident_service: operator resolved incident",
		zap.String("incident_id", id),
		zap.String("service", inc.ServiceName),
		zap.Float64("mttr_seconds", mttr),
		zap.String("root_cause", rootCause),
	)
	return nil
}

// GetIncidentStats returns MTTA/MTTD/MTTR performance metrics.
func (s *IncidentService) GetIncidentStats(ctx context.Context, since time.Time) (*repository.IncidentStats, error) {
	return s.incidentRepo.GetStats(ctx, since)
}
