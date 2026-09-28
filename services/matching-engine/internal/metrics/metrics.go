// Package metrics provides Prometheus metric collectors for Matching Engine.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// DepthHeartbeatTotal counts the total number of successful periodic depth heartbeats pushed to Redis.
	DepthHeartbeatTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "matching_engine_depth_heartbeat_total",
			Help: "Total number of successful periodic depth heartbeats pushed to Redis",
		},
		[]string{"market_id"},
	)

	// DepthHeartbeatErrorsTotal counts the total number of failed periodic depth heartbeats to Redis.
	DepthHeartbeatErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "matching_engine_depth_heartbeat_errors_total",
			Help: "Total number of failed periodic depth heartbeats to Redis",
		},
		[]string{"market_id"},
	)

	// DepthHeartbeatLastTimestamp records the Unix timestamp in seconds of the last successful depth heartbeat.
	DepthHeartbeatLastTimestamp = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "matching_engine_depth_heartbeat_last_timestamp_seconds",
			Help: "Unix timestamp in seconds of the last successful depth heartbeat push",
		},
		[]string{"market_id"},
	)
)
