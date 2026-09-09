package streamer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws/protocol"
)

var (
	// ErrMalformedJSON is returned when a Redis user stream payload cannot be unmarshaled.
	ErrMalformedJSON = errors.New("malformed user stream json")
	// ErrChannelMismatch is returned when the payload envelope's channel disagrees with the subscribed topic.
	ErrChannelMismatch = errors.New("channel mismatch between envelope and topic")
	// ErrInvalidStreamChannel is returned when the channel does not conform to a valid private user stream.
	ErrInvalidStreamChannel = errors.New("invalid or malformed user stream channel")
	// ErrInvalidNotification is returned when a notification envelope lacks required fields.
	ErrInvalidNotification = errors.New("invalid notification envelope: event_id, notification_id, and non-empty data required")
	// ErrInvalidPortfolio is returned when a portfolio envelope lacks a data payload.
	ErrInvalidPortfolio = errors.New("invalid portfolio envelope: non-empty data required")
	// ErrDuplicateNotification is returned when an identical notification was recently processed.
	ErrDuplicateNotification = errors.New("duplicate notification suppressed")
)

// RedisNotificationEnvelope models the standard notification envelope emitted by Notification Service.
type RedisNotificationEnvelope struct {
	EventID        string          `json:"event_id"`
	NotificationID string          `json:"notification_id"`
	Type           string          `json:"type"`
	Channel        string          `json:"channel"`
	Timestamp      time.Time       `json:"timestamp"`
	Data           json.RawMessage `json:"data"`
}

// DedupCache tracks recently seen (event_id, notification_id) pairs to drop duplicate transmissions.
type DedupCache struct {
	mu      sync.Mutex
	entries map[string]time.Time // composite key -> expiration
}

// NewDedupCache constructs a thread-safe DedupCache.
func NewDedupCache() *DedupCache {
	return &DedupCache{
		entries: make(map[string]time.Time),
	}
}

// IsDuplicate checks if key is already cached within its TTL, or stores it until now + ttl.
func (c *DedupCache) IsDuplicate(key string, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	// Periodic cleanup if cache size exceeds threshold
	if len(c.entries) > 5000 {
		for k, exp := range c.entries {
			if now.After(exp) {
				delete(c.entries, k)
			}
		}
	}

	if exp, exists := c.entries[key]; exists && now.Before(exp) {
		return true
	}

	c.entries[key] = now.Add(ttl)
	return false
}

// ProcessUserStreamPayload unmarshals, validates, and deduplicates user stream payloads.
// Returns the outbound payload bytes, stream type, and an error if the message is invalid or duplicate.
func ProcessUserStreamPayload(
	raw []byte,
	channel string,
	dedup *DedupCache,
) ([]byte, string, error) {
	var env RedisNotificationEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}

	// Invariant: Channel must conform to a strictly valid user private stream
	streamType, _, ok := protocol.ValidateStream(channel)
	if !ok || (streamType != protocol.StreamTypeNotification && streamType != protocol.StreamTypePortfolio) {
		return nil, "", fmt.Errorf("%w: %q", ErrInvalidStreamChannel, channel)
	}

	// Invariant: If envelope specifies an internal channel, it must match the subscribed Redis topic
	if env.Channel != "" && env.Channel != channel {
		return nil, "", fmt.Errorf("%w: topic=%q payload=%q", ErrChannelMismatch, channel, env.Channel)
	}

	// Type-specific validation and deduplication rules
	switch streamType {
	case protocol.StreamTypeNotification:
		// Notifications must contain valid event_id, notification_id, and non-empty data payload
		if env.EventID == "" || env.NotificationID == "" || len(env.Data) == 0 || string(env.Data) == "null" {
			return nil, "", ErrInvalidNotification
		}

		// Deduplicate persistent notifications on composite key (event_id:notification_id) with 60s TTL
		if dedup != nil {
			dedupKey := env.EventID + ":" + env.NotificationID
			if dedup.IsDuplicate(dedupKey, 60*time.Second) {
				return nil, "", ErrDuplicateNotification
			}
		}

	case protocol.StreamTypePortfolio:
		// Portfolio snapshots require data, but are intentionally duplicate-tolerant state updates (no dedup suppression)
		if len(env.Data) == 0 || string(env.Data) == "null" {
			return nil, "", ErrInvalidPortfolio
		}
	}

	// Construct outbound frame
	outbound := protocol.OutboundEnvelope{
		Stream: channel,
		Data:   env.Data,
	}
	payload, err := json.Marshal(outbound)
	if err != nil {
		return nil, "", fmt.Errorf("marshal outbound envelope: %w", err)
	}

	return payload, streamType, nil
}

// runUserStreamRelay subscribes to user private channels on Redis Pub/Sub,
// deduplicates messages by (event_id, notification_id), and broadcasts to active subscribers.
func (s *Streamer) runUserStreamRelay(ctx context.Context) {
	s.logger.Info("Starting WebSocket user private stream relay")

	dedup := NewDedupCache()

	for {
		if ctx.Err() != nil {
			s.logger.Info("User private stream relay stopped (context cancelled)")
			return
		}

		pubsub := s.redisClient.PSubscribe(ctx, "user:notifications:*", "user:portfolio:*")
		ch := pubsub.Channel()

		s.logger.Info("Subscribed to Redis user channels: user:notifications:*, user:portfolio:*")

		closed := false
		for !closed {
			select {
			case <-ctx.Done():
				_ = pubsub.Close()
				return

			case msg, ok := <-ch:
				if !ok {
					closed = true
					break
				}

				if msg == nil {
					continue
				}

				channel := msg.Channel
				// Only process if there are active subscribers connected to Gateway for this channel
				if s.broadcaster == nil || !s.broadcaster.HasSubscribers(channel) {
					continue
				}

				payload, streamType, err := ProcessUserStreamPayload([]byte(msg.Payload), channel, dedup)
				if err != nil {
					atomic.AddInt64(&s.redisUserDropsTotal, 1)
					if errors.Is(err, ErrDuplicateNotification) {
						s.logger.Debug("Dropped duplicate notification in Gateway",
							zap.String("channel", channel),
						)
					} else {
						s.logger.Warn("Dropped invalid Redis user stream message in Gateway",
							zap.String("channel", channel),
							zap.Error(err),
						)
					}
					continue
				}

				s.broadcaster.Broadcast(channel, payload, streamType)
			}
		}

		_ = pubsub.Close()
		// Reconnect backoff if Redis connection dropped
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
			atomic.AddInt64(&s.redisUserReconnectsTotal, 1)
			s.logger.Warn("Redis Pub/Sub disconnected; reconnecting user stream relay...",
				zap.Int64("reconnects_total", atomic.LoadInt64(&s.redisUserReconnectsTotal)),
			)
		}
	}
}
