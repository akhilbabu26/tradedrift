package streamer

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws/protocol"
)

// redisNotificationEnvelope models the standard notification envelope emitted by Notification Service.
type redisNotificationEnvelope struct {
	EventID        string          `json:"event_id"`
	NotificationID string          `json:"notification_id"`
	Type           string          `json:"type"`
	Channel        string          `json:"channel"`
	Timestamp      time.Time       `json:"timestamp"`
	Data           json.RawMessage `json:"data"`
}

// dedupCache tracks recently seen (event_id, notification_id) pairs to drop duplicate transmissions.
type dedupCache struct {
	mu      sync.Mutex
	entries map[string]time.Time // composite key -> expiration
}

func newDedupCache() *dedupCache {
	return &dedupCache{
		entries: make(map[string]time.Time),
	}
}

func (c *dedupCache) isDuplicate(key string, ttl time.Duration) bool {
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

// runUserStreamRelay subscribes to user private channels on Redis Pub/Sub,
// deduplicates messages by (event_id, notification_id), and broadcasts to active subscribers.
func (s *Streamer) runUserStreamRelay(ctx context.Context) {
	s.logger.Info("Starting WebSocket user private stream relay")

	dedup := newDedupCache()

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

				var env redisNotificationEnvelope
				if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
					s.logger.Warn("Failed to unmarshal Redis user stream envelope",
						zap.String("channel", channel),
						zap.Error(err),
					)
					continue
				}

				// Deduplication check: composite key = (event_id + ":" + notification_id)
				if env.EventID != "" {
					dedupKey := env.EventID + ":" + env.NotificationID
					if dedup.isDuplicate(dedupKey, 60*time.Second) {
						s.logger.Debug("Dropped duplicate user stream message in Gateway",
							zap.String("channel", channel),
							zap.String("event_id", env.EventID),
							zap.String("notification_id", env.NotificationID),
						)
						continue
					}
				}

				// Determine stream type
				streamType := protocol.StreamTypeNotification
				if strings.HasPrefix(channel, "user:portfolio:") {
					streamType = protocol.StreamTypePortfolio
				}

				// Construct outbound frame
				outbound := protocol.OutboundEnvelope{
					Stream: channel,
					Data:   env.Data,
				}
				payload, err := json.Marshal(outbound)
				if err != nil {
					s.logger.Error("Failed to marshal outbound user stream envelope", zap.Error(err))
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
			s.logger.Warn("Redis Pub/Sub disconnected; reconnecting user stream relay...")
		}
	}
}
