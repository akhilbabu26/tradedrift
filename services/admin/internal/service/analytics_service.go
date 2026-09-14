package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

// RiskSeverity characterizes the potential impact of an administrative signal.
type RiskSeverity string

const (
	RiskSeverityLow    RiskSeverity = "LOW"
	RiskSeverityMedium RiskSeverity = "MEDIUM"
	RiskSeverityHigh   RiskSeverity = "HIGH"
)

// AdministrativeRiskSignal represents an operational anomaly or burst pattern.
type AdministrativeRiskSignal struct {
	SignalType  string       `json:"signal_type"`
	Severity    RiskSeverity `json:"severity"`
	AdminID     string       `json:"admin_id,omitempty"`
	Description string       `json:"description"`
	ActionCount int          `json:"action_count"`
	DetectedAt  time.Time    `json:"detected_at"`
}

// RiskSignalsReport aggregates operational risk indicators across recent administrative actions.
type RiskSignalsReport struct {
	EvaluatedAt time.Time                  `json:"evaluated_at"`
	Window      string                     `json:"window"`
	OverallRisk RiskSeverity               `json:"overall_risk"`
	Signals     []AdministrativeRiskSignal `json:"signals"`
}

// OperationsOverviewResponse provides business metrics derived directly from PostgreSQL.
type OperationsOverviewResponse struct {
	GeneratedAt                time.Time                       `json:"generated_at"`
	Window                     string                          `json:"window"`
	TotalOperations            int                             `json:"total_operations"`
	CompletedOperations        int                             `json:"completed_operations"`
	FailedOperations           int                             `json:"failed_operations"`
	PendingOperations          int                             `json:"pending_operations"`
	ProcessingOperations       int                             `json:"processing_operations"`
	OldestProcessingAgeSeconds float64                         `json:"oldest_processing_age_seconds"`
	SuccessRatePercent         float64                         `json:"success_rate_percent"`
	ByType                     map[string]int                  `json:"by_type"`
	AuditStats                 *repository.AuditAnalyticsStats `json:"audit_stats,omitempty"`
}

// AnalyticsService provides PostgreSQL-backed operational business intelligence and risk telemetry.
type AnalyticsService struct {
	opsRepo   repository.OperationsRepository
	auditRepo repository.AuditRepository
	log       *zap.Logger
}

// NewAnalyticsService constructs an AnalyticsService.
func NewAnalyticsService(
	opsRepo repository.OperationsRepository,
	auditRepo repository.AuditRepository,
	log *zap.Logger,
) *AnalyticsService {
	return &AnalyticsService{
		opsRepo:   opsRepo,
		auditRepo: auditRepo,
		log:       log,
	}
}

// GetOperationsOverview compiles transactional metrics for business dashboards.
// Enforces query bounds: minimum 1h, default 24h, maximum 720h (30 days).
func (s *AnalyticsService) GetOperationsOverview(ctx context.Context, since time.Time) (*OperationsOverviewResponse, error) {
	now := time.Now().UTC()
	const (
		minWindow = 1 * time.Hour
		maxWindow = 720 * time.Hour
	)

	if since.IsZero() {
		since = now.Add(-24 * time.Hour)
	} else {
		windowDuration := now.Sub(since)
		if windowDuration < minWindow {
			since = now.Add(-minWindow)
		} else if windowDuration > maxWindow {
			since = now.Add(-maxWindow)
		}
	}

	opsStats, err := s.opsRepo.GetOperationsSummary(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("analytics_service: operations summary: %w", err)
	}

	auditStats, err := s.auditRepo.GetAuditStats(ctx, since)
	if err != nil {
		s.log.Error("analytics_service: failed to fetch audit stats", zap.Error(err))
	}

	var successRate float64
	if opsStats.TotalOperations > 0 {
		successRate = float64(opsStats.CompletedOperations) / float64(opsStats.TotalOperations) * 100.0
	}

	windowStr := fmt.Sprintf("%.0fh", now.Sub(since).Hours())

	return &OperationsOverviewResponse{
		GeneratedAt:                now,
		Window:                     windowStr,
		TotalOperations:            opsStats.TotalOperations,
		CompletedOperations:        opsStats.CompletedOperations,
		FailedOperations:           opsStats.FailedOperations,
		PendingOperations:          opsStats.PendingOperations,
		ProcessingOperations:       opsStats.ProcessingOperations,
		OldestProcessingAgeSeconds: opsStats.OldestProcessingAgeSeconds,
		SuccessRatePercent:         successRate,
		ByType:                     opsStats.ByType,
		AuditStats:                 auditStats,
	}, nil
}

// GetRiskSignals evaluates recent audit log activity for administrative anomalies and burst patterns.
// Enforces evaluation window bounds: minimum 5m, default 1h, maximum 24h.
func (s *AnalyticsService) GetRiskSignals(ctx context.Context, window time.Duration) (*RiskSignalsReport, error) {
	now := time.Now().UTC()
	const (
		minRiskWindow = 5 * time.Minute
		maxRiskWindow = 24 * time.Hour
	)

	if window < minRiskWindow {
		window = 1 * time.Hour
	} else if window > maxRiskWindow {
		window = maxRiskWindow
	}
	start := now.Add(-window)

	logs, err := s.auditRepo.GetAuditLogsInWindow(ctx, start, now, 500)
	if err != nil {
		return nil, fmt.Errorf("analytics_service: get audit window: %w", err)
	}

	var signals []AdministrativeRiskSignal

	// 1. Group actions by Admin and by Action Type
	adminActionCounts := make(map[string]int)
	actionTypeCounts := make(map[string]int)
	adminIPs := make(map[string]map[string]bool)

	for _, l := range logs {
		adminActionCounts[l.AdminID]++
		actionTypeCounts[l.Action]++

		if adminIPs[l.AdminID] == nil {
			adminIPs[l.AdminID] = make(map[string]bool)
		}
		if l.IPAddress != "" {
			adminIPs[l.AdminID][l.IPAddress] = true
		}
	}

	// Heuristic 1: Admin Burst Velocity (> 10 actions within a single 1-minute window by a single admin)
	type adminMinuteKey struct {
		adminID string
		minute  int64 // Unix minute
	}
	minuteBuckets := make(map[adminMinuteKey]int)
	for _, l := range logs {
		minEpoch := l.CreatedAt.Truncate(time.Minute).Unix()
		minuteBuckets[adminMinuteKey{adminID: l.AdminID, minute: minEpoch}]++
	}

	triggeredBurstAdmins := make(map[string]bool)
	for key, count := range minuteBuckets {
		if count > 10 && !triggeredBurstAdmins[key.adminID] {
			triggeredBurstAdmins[key.adminID] = true
			signals = append(signals, AdministrativeRiskSignal{
				SignalType:  "ADMIN_BURST_VELOCITY",
				Severity:    RiskSeverityMedium,
				AdminID:     key.adminID,
				Description: fmt.Sprintf("Admin executed %d actions within a single 1-minute window", count),
				ActionCount: count,
				DetectedAt:  now,
			})
		}
	}

	// Heuristic 2: Mass Account Suspension (> 3 suspensions in window)
	if count := actionTypeCounts[domain.ActionSuspendUser]; count > 3 {
		signals = append(signals, AdministrativeRiskSignal{
			SignalType:  "MASS_USER_SUSPENSION",
			Severity:    RiskSeverityHigh,
			Description: fmt.Sprintf("High rate of account suspensions: %d users suspended in %s", count, window),
			ActionCount: count,
			DetectedAt:  now,
		})
	}

	// Heuristic 3: Multiple Market Halts (> 2 market halts in window)
	if count := actionTypeCounts[domain.ActionHaltMarket]; count > 2 {
		signals = append(signals, AdministrativeRiskSignal{
			SignalType:  "MULTIPLE_MARKET_HALTS",
			Severity:    RiskSeverityHigh,
			Description: fmt.Sprintf("Multiple trading pair halts: %d halts triggered in %s", count, window),
			ActionCount: count,
			DetectedAt:  now,
		})
	}

	// Heuristic 4: Admin IP Diversity (> 2 distinct IP addresses for single admin)
	for adminID, ips := range adminIPs {
		if len(ips) > 2 {
			signals = append(signals, AdministrativeRiskSignal{
				SignalType:  "ADMIN_IP_DIVERSITY",
				Severity:    RiskSeverityMedium,
				AdminID:     adminID,
				Description: fmt.Sprintf("Admin performed operations from %d distinct IP addresses in %s", len(ips), window),
				ActionCount: len(ips),
				DetectedAt:  now,
			})
		}
	}

	// Compute overall risk score
	overall := RiskSeverityLow
	for _, sig := range signals {
		if sig.Severity == RiskSeverityHigh {
			overall = RiskSeverityHigh
			break
		} else if sig.Severity == RiskSeverityMedium {
			overall = RiskSeverityMedium
		}
	}

	// Sort signals deterministically: Severity (HIGH > MEDIUM > LOW), SignalType ASC, AdminID ASC
	severityRank := func(s RiskSeverity) int {
		switch s {
		case RiskSeverityHigh:
			return 3
		case RiskSeverityMedium:
			return 2
		default:
			return 1
		}
	}

	sort.SliceStable(signals, func(i, j int) bool {
		rI, rJ := severityRank(signals[i].Severity), severityRank(signals[j].Severity)
		if rI != rJ {
			return rI > rJ // higher severity first
		}
		if signals[i].SignalType != signals[j].SignalType {
			return signals[i].SignalType < signals[j].SignalType
		}
		return signals[i].AdminID < signals[j].AdminID
	})

	return &RiskSignalsReport{
		EvaluatedAt: now,
		Window:      window.String(),
		OverallRisk: overall,
		Signals:     signals,
	}, nil
}
