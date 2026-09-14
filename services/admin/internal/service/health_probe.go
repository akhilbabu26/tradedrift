package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/grpc/codes"
	statusRpc "google.golang.org/grpc/status"

	"tradedrift/services/admin/internal/metrics"
)

// probeHTTPService performs a synchronous GET against the given health URL and
// translates the HTTP response or transport error into a ServiceStatusReport.
func (w *HealthWorker) probeHTTPService(ctx context.Context, name, url string) ServiceStatusReport {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ServiceStatusReport{Name: name, Status: "DOWN", Error: err.Error()}
	}

	resp, err := w.httpClient.Do(req)
	elapsed := time.Since(start)
	durationMs := elapsed.Milliseconds()
	if err != nil {
		if ctx.Err() != nil {
			return ServiceStatusReport{Name: name, Status: "TIMEOUT", Latency: elapsed, LatencyMs: durationMs, Error: "request timed out"}
		}
		return ServiceStatusReport{Name: name, Status: "DOWN", Latency: elapsed, LatencyMs: durationMs, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return ServiceStatusReport{Name: name, Status: "UP", Latency: elapsed, LatencyMs: durationMs, HTTPStatus: resp.StatusCode}
	}

	if resp.StatusCode >= 500 {
		return ServiceStatusReport{
			Name:       name,
			Status:     "DOWN",
			Latency:    elapsed,
			LatencyMs:  durationMs,
			HTTPStatus: resp.StatusCode,
			Error:      fmt.Sprintf("service reported outage (HTTP %d)", resp.StatusCode),
		}
	}

	return ServiceStatusReport{
		Name:       name,
		Status:     "DEGRADED",
		Latency:    elapsed,
		LatencyMs:  durationMs,
		HTTPStatus: resp.StatusCode,
		Error:      fmt.Sprintf("service reported degradation (HTTP %d)", resp.StatusCode),
	}
}

// probeAuthGRPC checks Auth gRPC transport liveness and appends the result via addReport.
func (w *HealthWorker) probeAuthGRPC(ctx context.Context, addReport func(string, ServiceStatusReport)) {
	if w.authCli != nil {
		start := time.Now()
		status := metrics.StatusUP
		var errStr string
		if err := w.authCli.Ping(ctx); err != nil {
			if statusRpc.Code(err) == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
				status = metrics.StatusTimeout
			} else {
				status = metrics.StatusDown
			}
			errStr = err.Error()
		}
		elapsed := time.Since(start)
		addReport(metrics.ServiceAuth, ServiceStatusReport{
			Name:      metrics.ServiceAuth,
			Status:    status,
			Latency:   elapsed,
			LatencyMs: elapsed.Milliseconds(),
			Error:     errStr,
		})
		return
	}
	if w.cfg.AuthGRPCAddr != "" {
		addReport(metrics.ServiceAuth, ServiceStatusReport{
			Name:   metrics.ServiceAuth,
			Status: "DOWN",
			Error:  "auth gRPC client not initialized",
		})
		return
	}
	addReport(metrics.ServiceAuth, ServiceStatusReport{
		Name:   metrics.ServiceAuth,
		Status: "UNKNOWN",
		Error:  "auth gRPC address not configured",
	})
}

// probeWalletGRPC checks Wallet gRPC application health and appends the result via addReport.
func (w *HealthWorker) probeWalletGRPC(ctx context.Context, addReport func(string, ServiceStatusReport)) {
	if w.walletCli != nil {
		start := time.Now()
		status := metrics.StatusUP
		var errStr string
		if err := w.walletCli.Ping(ctx); err != nil {
			if statusRpc.Code(err) == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
				status = metrics.StatusTimeout
			} else {
				status = metrics.StatusDown
			}
			errStr = err.Error()
		}
		elapsed := time.Since(start)
		addReport(metrics.ServiceWallet, ServiceStatusReport{
			Name:      metrics.ServiceWallet,
			Status:    status,
			Latency:   elapsed,
			LatencyMs: elapsed.Milliseconds(),
			Error:     errStr,
		})
		return
	}
	if w.cfg.WalletGRPCAddr != "" {
		addReport(metrics.ServiceWallet, ServiceStatusReport{
			Name:   metrics.ServiceWallet,
			Status: "DOWN",
			Error:  "wallet gRPC client not initialized",
		})
		return
	}
	addReport(metrics.ServiceWallet, ServiceStatusReport{
		Name:   metrics.ServiceWallet,
		Status: "UNKNOWN",
		Error:  "wallet gRPC address not configured",
	})
}

// probePostgres pings the admin PostgreSQL pool and appends the result via addReport.
func (w *HealthWorker) probePostgres(ctx context.Context, addReport func(string, ServiceStatusReport)) {
	if w.dbPool == nil {
		addReport(metrics.ServicePostgres, ServiceStatusReport{
			Name:   metrics.ServicePostgres,
			Status: "UNKNOWN",
			Error:  "postgres connection pool not configured",
		})
		return
	}
	start := time.Now()
	status := "UP"
	var errStr string
	if err := w.dbPool.Ping(ctx); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			status = metrics.StatusTimeout
		} else {
			status = metrics.StatusDown
		}
		errStr = err.Error()
	}
	elapsed := time.Since(start)
	addReport(metrics.ServicePostgres, ServiceStatusReport{
		Name:      metrics.ServicePostgres,
		Status:    status,
		Latency:   elapsed,
		LatencyMs: elapsed.Milliseconds(),
		Error:     errStr,
	})
}

// probeKafka dials each configured broker and marks UP on the first successful connection.
// The probe is considered DOWN (or TIMEOUT) only if all brokers fail.
func probeKafka(ctx context.Context, brokers []string) ServiceStatusReport {
	start := time.Now()
	status := "DOWN"
	var errStr string

	if len(brokers) > 0 {
		for _, broker := range brokers {
			brokerCtx, brokerCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			conn, err := kafka.DialContext(brokerCtx, "tcp", broker)
			brokerCancel()
			if err == nil {
				_ = conn.Close()
				status = "UP"
				errStr = ""
				break
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(brokerCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
				status = metrics.StatusTimeout
			} else {
				status = metrics.StatusDown
			}
			errStr = err.Error()
		}
	} else {
		status = "UNKNOWN"
		errStr = "no kafka brokers configured"
	}

	elapsed := time.Since(start)
	return ServiceStatusReport{
		Name:      metrics.ServiceKafka,
		Status:    status,
		Latency:   elapsed,
		LatencyMs: elapsed.Milliseconds(),
		Error:     errStr,
	}
}

// classifyFailureReason maps a ServiceStatusReport to a human-readable failure category
// used as a Prometheus label so dashboards can group failure modes.
func classifyFailureReason(rep ServiceStatusReport) string {
	if rep.Status == "TIMEOUT" || strings.Contains(strings.ToLower(rep.Error), "timeout") {
		return "timeout"
	}
	if rep.HTTPStatus == http.StatusServiceUnavailable {
		return "http_503"
	}
	if rep.HTTPStatus >= 500 {
		return "http_5xx"
	}
	if strings.Contains(strings.ToLower(rep.Error), "connection refused") ||
		strings.Contains(strings.ToLower(rep.Error), "no such host") ||
		strings.Contains(strings.ToLower(rep.Error), "dial") {
		return "connection_error"
	}
	if strings.Contains(strings.ToLower(rep.Error), "grpc") {
		return "grpc_error"
	}
	return "unknown"
}
