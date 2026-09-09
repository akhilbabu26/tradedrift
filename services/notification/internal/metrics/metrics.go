package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	NotificationsCreatedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "notification",
			Name:      "created_total",
			Help:      "Total number of notifications created by type",
		},
		[]string{"type"},
	)

	OutboxEventsPublishedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "notification",
			Name:      "outbox_published_total",
			Help:      "Total number of outbox events successfully published to Redis",
		},
		[]string{"channel"},
	)

	OutboxPublishErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "notification",
			Name:      "outbox_publish_errors_total",
			Help:      "Total number of outbox publication errors",
		},
		[]string{"channel"},
	)

	KafkaEventsConsumedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "notification",
			Name:      "kafka_events_consumed_total",
			Help:      "Total number of Kafka events consumed by topic and status",
		},
		[]string{"topic", "status"},
	)
)
