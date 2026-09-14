package domain

import (
	"time"
)

type IncidentSeverity string

const (
	SeverityP1Critical IncidentSeverity = "P1_CRITICAL"
	SeverityP2High     IncidentSeverity = "P2_HIGH"
	SeverityP3Moderate IncidentSeverity = "P3_MODERATE"
)

type IncidentStatus string

const (
	IncidentStatusOpen          IncidentStatus = "OPEN"
	IncidentStatusInvestigating IncidentStatus = "INVESTIGATING"
	IncidentStatusResolved      IncidentStatus = "RESOLVED"
)

// Incident represents an operational outage, degradation, or disruption event.
type Incident struct {
	ID                string           `json:"id"`
	ServiceName       string           `json:"service_name"`
	Severity          IncidentSeverity `json:"severity"`
	Status            IncidentStatus   `json:"status"`
	Title             string           `json:"title"`
	RootCause         *string          `json:"root_cause,omitempty"`
	TriggeredAt       time.Time        `json:"triggered_at"`
	DetectedAt        time.Time        `json:"detected_at"`
	ResolvedAt        *time.Time       `json:"resolved_at,omitempty"`
	LastSeenAt        time.Time        `json:"last_seen_at"`
	ProbeFailureCount int              `json:"probe_failure_count"`
	MTTDSeconds       *float64         `json:"mttd_seconds,omitempty"`
	MTTRSeconds       *float64         `json:"mttr_seconds,omitempty"`
	Metadata          map[string]any   `json:"metadata,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

func (i *Incident) IsActive() bool {
	return i.Status == IncidentStatusOpen || i.Status == IncidentStatusInvestigating
}

// Resolve marks the incident resolved, assigns the resolution timestamp, and calculates MTTR.
func (i *Incident) Resolve(resolvedAt time.Time, rootCause string) {
	i.Status = IncidentStatusResolved
	i.ResolvedAt = &resolvedAt
	i.UpdatedAt = resolvedAt
	if rootCause != "" {
		i.RootCause = &rootCause
	}

	mttr := resolvedAt.Sub(i.TriggeredAt).Seconds()
	if mttr < 0 {
		mttr = 0
	}
	i.MTTRSeconds = &mttr
}

// RecordFailureHeartbeat updates the last probe timestamp and increments failure count.
func (i *Incident) RecordFailureHeartbeat(now time.Time) {
	i.LastSeenAt = now
	i.ProbeFailureCount++
	i.UpdatedAt = now
}

// NewIncident constructs an initialized Incident with a UUIDv7 ID.
// Note on MTTD: For autonomous probe-detected incidents, MTTDSeconds is left nil (and NULL in PostgreSQL).
// TriggeredAt and DetectedAt both record the probe observation time. MTTD is only measurable when
// an independent pre-detection failure timestamp is available.
func NewIncident(serviceName string, severity IncidentSeverity, title string, triggeredAt time.Time) *Incident {
	id := MustNewV7()
	return &Incident{
		ID:                id,
		ServiceName:       serviceName,
		Severity:          severity,
		Status:            IncidentStatusOpen,
		Title:             title,
		TriggeredAt:       triggeredAt,
		DetectedAt:        triggeredAt,
		LastSeenAt:        triggeredAt,
		ProbeFailureCount: 1,
		CreatedAt:         triggeredAt,
		UpdatedAt:         triggeredAt,
	}
}

// CorrelatedIncident aggregates an incident with topology impact and time-correlated administrative actions.
// Note: CorrelatedAuditLogs represents administrative activity that occurred within the incident time window
// for forensic inspection by human operators, rather than an automated assertion of causality.
type CorrelatedIncident struct {
	Incident               Incident   `json:"incident"`
	AffectedDependencies   []string   `json:"affected_dependencies"`
	CorrelatedAuditLogs    []AuditLog `json:"correlated_audit_logs"`
	CorrelationWindowStart time.Time  `json:"correlation_window_start"`
	CorrelationWindowEnd   time.Time  `json:"correlation_window_end"`
}
