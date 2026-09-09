package publisher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"tradedrift/services/notification/internal/metrics"
	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/repository"
)

// RedisClient abstracts redis publishing for easy testing and decoupling.
type RedisClient interface {
	Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd
}

// Config defines the tuning parameters for the Transactional Outbox publisher.
type Config struct {
	BatchSize         int
	PollInterval      time.Duration
	IdleInterval      time.Duration
	LeaseTimeout      time.Duration
	RecoveryInterval  time.Duration
	MaxPublishRetries int
}

// DefaultConfig returns safe, production-grade defaults.
func DefaultConfig() Config {
	return Config{
		BatchSize:         50,
		PollInterval:      500 * time.Millisecond,
		IdleInterval:      2 * time.Second,
		LeaseTimeout:      60 * time.Second,
		RecoveryInterval:  15 * time.Second,
		MaxPublishRetries: 3,
	}
}

// Publisher reads claimed pending records from notification_outbox and publishes them to Redis Pub/Sub.
type Publisher struct {
	repo  repository.NotificationRepository
	redis RedisClient
	log   *zap.Logger
	cfg   Config
}

func NewPublisher(repo repository.NotificationRepository, redis RedisClient, log *zap.Logger, cfg Config) *Publisher {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.IdleInterval <= 0 {
		cfg.IdleInterval = 2 * time.Second
	}
	if cfg.LeaseTimeout <= 0 {
		cfg.LeaseTimeout = 60 * time.Second
	}
	if cfg.RecoveryInterval <= 0 {
		cfg.RecoveryInterval = 15 * time.Second
	}
	if cfg.MaxPublishRetries <= 0 {
		cfg.MaxPublishRetries = 3
	}

	return &Publisher{
		repo:  repo,
		redis: redis,
		log:   log,
		cfg:   cfg,
	}
}

// Start runs the background publisher loop and periodic stale claim recovery until ctx is cancelled.
func (p *Publisher) Start(ctx context.Context) error {
	p.log.Info("Starting Transactional Outbox Publisher",
		zap.Int("batch_size", p.cfg.BatchSize),
		zap.Duration("poll_interval", p.cfg.PollInterval),
		zap.Duration("idle_interval", p.cfg.IdleInterval),
		zap.Duration("lease_timeout", p.cfg.LeaseTimeout),
		zap.Duration("recovery_interval", p.cfg.RecoveryInterval),
	)

	// Ticker for periodic recovery of abandoned claims (e.g. from crashed instances)
	recoveryTicker := time.NewTicker(p.cfg.RecoveryInterval)
	defer recoveryTicker.Stop()

	// Initial stale claim recovery on startup
	if recovered, err := p.repo.RecoverStaleOutboxClaims(ctx, p.cfg.LeaseTimeout); err != nil {
		p.log.Warn("Failed initial stale outbox claim recovery", zap.Error(err))
	} else if recovered > 0 {
		p.log.Info("Recovered abandoned outbox claims on startup", zap.Int64("count", recovered))
	}

	for {
		select {
		case <-ctx.Done():
			p.log.Info("Outbox Publisher stopped by context cancellation")
			return ctx.Err()

		case <-recoveryTicker.C:
			if recovered, err := p.repo.RecoverStaleOutboxClaims(ctx, p.cfg.LeaseTimeout); err != nil {
				p.log.Warn("Failed to recover stale outbox claims", zap.Error(err))
			} else if recovered > 0 {
				p.log.Info("Recovered abandoned outbox claims", zap.Int64("count", recovered))
			}

		default:
			count, err := p.ProcessBatch(ctx)
			if err != nil {
				p.log.Error("Failed processing outbox batch; backing off", zap.Error(err))
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(p.cfg.IdleInterval):
					continue
				}
			}

			// If we processed items, check if there may be more immediately, else sleep PollInterval
			if count > 0 {
				if count < p.cfg.BatchSize {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(p.cfg.PollInterval):
					}
				}
				// If count == BatchSize, immediately loop to drain next batch without sleeping
			} else {
				// No items found; idle sleep
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(p.cfg.IdleInterval):
				}
			}
		}
	}
}

// ProcessBatch claims and publishes a batch of pending outbox events.
// Returns the number of events processed or any fatal/transient error encountered.
func (p *Publisher) ProcessBatch(ctx context.Context) (int, error) {
	events, err := p.repo.FetchPendingOutbox(ctx, p.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("fetch pending outbox: %w", err)
	}

	if len(events) == 0 {
		return 0, nil
	}

	for i, ev := range events {
		if err := p.publishWithRetry(ctx, ev); err != nil {
			metrics.OutboxPublishErrorsTotal.WithLabelValues(ev.TargetChannel).Inc()

			// Persist failure metadata before releasing.
			// Pass ev.ClaimToken — only this worker can update the retry counter.
			if retryErr := p.repo.IncrementOutboxRetry(ctx, ev.ID, err.Error(), ev.ClaimToken); retryErr != nil {
				if errors.Is(retryErr, repository.ErrOutboxClaimLost) {
					p.log.Warn("Outbox claim lost while incrementing retry counter; another worker owns the row",
						zap.String("outbox_id", ev.ID),
					)
				} else {
					p.log.Warn("Failed to increment outbox retry counter",
						zap.String("outbox_id", ev.ID),
						zap.Error(retryErr),
					)
				}
			}

			p.log.Error("Failed to publish outbox event to Redis; aborting batch and releasing remaining claims",
				zap.String("outbox_id", ev.ID),
				zap.String("target_channel", ev.TargetChannel),
				zap.Error(err),
			)

			// Collect remaining unhandled event IDs (including the failing one) to immediately release back to PENDING.
			// Pass ev.ClaimToken — only the worker holding the token can release these rows.
			// If the lease has expired and another worker re-claimed them, the token won't match and they are left alone.
			var remainingIDs []string
			for j := i; j < len(events); j++ {
				remainingIDs = append(remainingIDs, events[j].ID)
			}
			if releaseErr := p.repo.ReleaseOutboxClaims(ctx, remainingIDs, ev.ClaimToken); releaseErr != nil {
				p.log.Error("Failed to release outbox claims", zap.Error(releaseErr))
			}

			return i, fmt.Errorf("publish event %s failed: %w", ev.ID, err)
		}

		// Mark successfully published in PostgreSQL.
		// Pass ev.ClaimToken so only this worker can transition the row to PROCESSED.
		if err := p.repo.MarkOutboxPublished(ctx, ev.ID, ev.ClaimToken); err != nil {
			if errors.Is(err, repository.ErrOutboxClaimLost) {
				// Normal concurrency condition: our lease expired and another worker re-claimed
				// the row. The new owner will mark it PROCESSED. Log at Warn and continue.
				p.log.Warn("Outbox claim lost after Redis publish; another worker owns the row",
					zap.String("outbox_id", ev.ID),
				)
			} else {
				// Real DB failure — release current and remaining events that this worker owns
				// so they return to PENDING immediately rather than stalling for the 60s lease timeout.
				p.log.Error("Failed to mark outbox event published; releasing remaining batch claims", zap.String("outbox_id", ev.ID), zap.Error(err))
				var remainingIDs []string
				for j := i; j < len(events); j++ {
					remainingIDs = append(remainingIDs, events[j].ID)
				}
				if releaseErr := p.repo.ReleaseOutboxClaims(ctx, remainingIDs, ev.ClaimToken); releaseErr != nil {
					p.log.Error("Failed to release outbox claims after mark published failure", zap.Error(releaseErr))
				}
				return i + 1, fmt.Errorf("mark published %s: %w", ev.ID, err)
			}
		}
		metrics.OutboxEventsPublishedTotal.WithLabelValues(ev.TargetChannel).Inc()
	}

	return len(events), nil
}

// publishWithRetry attempts to publish to Redis up to MaxPublishRetries with exponential backoff.
func (p *Publisher) publishWithRetry(ctx context.Context, ev *model.OutboxEvent) error {
	var lastErr error
	backoff := 50 * time.Millisecond

	for attempt := 1; attempt <= p.cfg.MaxPublishRetries; attempt++ {
		cmd := p.redis.Publish(ctx, ev.TargetChannel, ev.Payload)
		if cmd.Err() == nil {
			return nil
		}

		lastErr = cmd.Err()
		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return lastErr
		}

		p.log.Warn("Transient failure publishing outbox event to Redis; retrying",
			zap.String("outbox_id", ev.ID),
			zap.String("target_channel", ev.TargetChannel),
			zap.Int("attempt", attempt),
			zap.Duration("backoff", backoff),
			zap.Error(lastErr),
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}

	return fmt.Errorf("exhausted %d retries: %w", p.cfg.MaxPublishRetries, lastErr)
}

