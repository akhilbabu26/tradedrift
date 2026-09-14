package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/metrics"
)

// processIncidentTransitions inspects service state changes across all platform
// components and coordinates the incident lifecycle:
//
//   - DOWN / TIMEOUT  → create a new incident (or heartbeat an existing one)
//   - DEGRADED × ≥3  → open a sustained-degradation incident (or heartbeat)
//   - UP              → auto-resolve any lingering active incident
//
// The method is a no-op when incidentRepo is nil (incident tracking disabled).
func (w *HealthWorker) processIncidentTransitions(
	ctx context.Context,
	runTime time.Time,
	services, adminComponents map[string]ServiceStatusReport,
) {
	if w.incidentRepo == nil {
		return
	}

	// Merge external-service and admin-infrastructure reports into one map.
	allReports := make(map[string]ServiceStatusReport, len(services)+len(adminComponents))
	for k, v := range services {
		allReports[k] = v
	}
	for k, v := range adminComponents {
		allReports[k] = v
	}

	for name, rep := range allReports {
		currStatus := rep.Status

		isDown := currStatus == "DOWN" || currStatus == "TIMEOUT"
		isDegraded := currStatus == "DEGRADED"
		isUp := currStatus == "UP"

		// Maintain rolling degraded-probe counter; reset on any non-DEGRADED status.
		if isDegraded {
			w.degradedCount[name]++
		} else {
			w.degradedCount[name] = 0
		}

		switch {
		case isDown:
			w.handleDownTransition(ctx, name, rep, runTime)
		case isDegraded && w.degradedCount[name] >= 3:
			// 2. Sustained degradation (≥3 consecutive DEGRADED probes ≈ 45 s)
			w.handleSustainedDegradation(ctx, name, rep, runTime)
		case isUp:
			w.handleRecovery(ctx, name, runTime)
		}

		w.prevStatus[name] = currStatus
	}
}

// handleDownTransition opens a new outage incident or heartbeats an ongoing one.
func (w *HealthWorker) handleDownTransition(ctx context.Context, name string, rep ServiceStatusReport, runTime time.Time) {
	activeInc, err := w.incidentRepo.GetActiveByService(ctx, name)
	if err != nil {
		metrics.RecordIncidentWorkerError("lookup")
		w.log.Error("health worker: failed to query active incident",
			zap.String("service", name),
			zap.Error(err),
		)
		return
	}

	if activeInc != nil {
		// Ongoing or escalating failure: advance heartbeat and probe count.
		newCount := activeInc.ProbeFailureCount + 1
		if hbErr := w.incidentRepo.UpdateHeartbeat(ctx, activeInc.ID, runTime, newCount); hbErr != nil {
			metrics.RecordIncidentWorkerError("heartbeat")
			w.log.Error("health worker: failed to update incident heartbeat",
				zap.String("service", name),
				zap.String("incident_id", activeInc.ID),
				zap.Error(hbErr),
			)
		}
		return
	}

	// No active incident — create one with impact-aware severity.
	severity := outageIncidentSeverity(name)
	meta := map[string]any{
		"latency_ms": rep.LatencyMs,
		"error":      rep.Error,
	}
	if rep.HTTPStatus > 0 {
		meta["http_status"] = rep.HTTPStatus
	}

	newInc := &domain.Incident{
		ID:                domain.MustNewV7(),
		ServiceName:       name,
		Severity:          severity,
		Status:            domain.IncidentStatusOpen,
		Title:             fmt.Sprintf("%s service outage: %s", strings.ToUpper(name), rep.Error),
		TriggeredAt:       runTime,
		DetectedAt:        runTime,
		LastSeenAt:        runTime,
		ProbeFailureCount: 1,
		Metadata:          meta,
		CreatedAt:         runTime,
		UpdatedAt:         runTime,
	}

	if err := w.incidentRepo.Create(ctx, newInc); err != nil {
		if errors.Is(err, domain.ErrActiveIncidentExists) {
			w.log.Debug("health worker: active incident already exists (concurrent worker deduplicated)",
				zap.String("service", name),
			)
		} else {
			metrics.RecordIncidentWorkerError("create")
			w.log.Error("health worker: failed to create incident",
				zap.String("service", name),
				zap.Error(err),
			)
		}
		return
	}

	w.log.Warn("health worker: created new incident for service outage",
		zap.String("service", name),
		zap.String("incident_id", newInc.ID),
		zap.String("severity", string(severity)),
	)
}

// handleSustainedDegradation opens a sustained-degradation incident or heartbeats
// an existing one once the service has been DEGRADED for ≥3 consecutive probes.
func (w *HealthWorker) handleSustainedDegradation(ctx context.Context, name string, rep ServiceStatusReport, runTime time.Time) {
	activeInc, err := w.incidentRepo.GetActiveByService(ctx, name)
	if err != nil {
		metrics.RecordIncidentWorkerError("lookup")
		w.log.Error("health worker: failed to query active incident for degraded service",
			zap.String("service", name),
			zap.Error(err),
		)
		return
	}

	if activeInc != nil {
		// Already tracking — keep the heartbeat alive.
		if hbErr := w.incidentRepo.UpdateHeartbeat(ctx, activeInc.ID, runTime, activeInc.ProbeFailureCount+1); hbErr != nil {
			metrics.RecordIncidentWorkerError("heartbeat")
			w.log.Error("health worker: failed to update degraded incident heartbeat",
				zap.String("service", name),
				zap.String("incident_id", activeInc.ID),
				zap.Error(hbErr),
			)
		}
		return
	}

	newInc := &domain.Incident{
		ID:          domain.MustNewV7(),
		ServiceName: name,
		Severity:    domain.SeverityP2High,
		Status:      domain.IncidentStatusOpen,
		Title:       fmt.Sprintf("%s sustained degradation: %s", strings.ToUpper(name), rep.Error),
		TriggeredAt: runTime,
		DetectedAt:  runTime,
		LastSeenAt:  runTime,
		ProbeFailureCount: w.degradedCount[name],
		Metadata: map[string]any{
			"latency_ms": rep.LatencyMs,
			"error":      rep.Error,
			"degraded":   true,
		},
		CreatedAt: runTime,
		UpdatedAt: runTime,
	}

	if err := w.incidentRepo.Create(ctx, newInc); err != nil {
		if errors.Is(err, domain.ErrActiveIncidentExists) {
			w.log.Debug("health worker: active degradation incident already exists",
				zap.String("service", name),
			)
		} else {
			metrics.RecordIncidentWorkerError("create")
			w.log.Error("health worker: failed to create degradation incident",
				zap.String("service", name),
				zap.Error(err),
			)
		}
		return
	}

	w.log.Warn("health worker: created incident for sustained degradation",
		zap.String("service", name),
		zap.String("incident_id", newInc.ID),
	)
}

// handleRecovery auto-resolves a lingering active incident when the service
// returns to UP, computing MTTR from the original triggered_at timestamp.
func (w *HealthWorker) handleRecovery(ctx context.Context, name string, runTime time.Time) {
	activeInc, err := w.incidentRepo.GetActiveByService(ctx, name)
	if err != nil || activeInc == nil {
		return
	}

	mttr := runTime.Sub(activeInc.TriggeredAt).Seconds()
	if mttr < 0 {
		mttr = 0
	}

	if err := w.incidentRepo.Resolve(ctx, activeInc.ID, runTime, mttr, "Automatic recovery detected by HealthWorker"); err != nil {
		metrics.RecordIncidentWorkerError("resolve")
		w.log.Error("health worker: failed to resolve incident",
			zap.String("service", name),
			zap.Error(err),
		)
		return
	}

	w.log.Info("health worker: resolved incident on service recovery",
		zap.String("service", name),
		zap.String("incident_id", activeInc.ID),
		zap.Float64("mttr_seconds", mttr),
	)
}

// outageIncidentSeverity returns the impact-appropriate severity for a hard outage.
//
//   - P1 Critical: core trading path (postgres, auth, wallet, trade engine)
//   - P2 High:     event backbone and liquidity (kafka, liquidity engine)
//   - P3 Moderate: reporting and notification services
func outageIncidentSeverity(serviceName string) domain.IncidentSeverity {
	switch serviceName {
	case metrics.ServicePostgres, metrics.ServiceAuth, metrics.ServiceWallet, metrics.ServiceTrade:
		return domain.SeverityP1Critical
	case metrics.ServiceKafka, metrics.ServiceLiquidityEngine:
		return domain.SeverityP2High
	case metrics.ServicePortfolio, metrics.ServiceNotification:
		return domain.SeverityP3Moderate
	default:
		return domain.SeverityP2High
	}
}
