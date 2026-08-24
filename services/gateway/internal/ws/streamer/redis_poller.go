package streamer

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws/protocol"
)

// ─── Redis Depth Poller ───────────────────────────────────────────────────────

func (s *Streamer) runDepthPoller(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollActiveMarketDepth(ctx)
		}
	}
}

func (s *Streamer) pollActiveMarketDepth(ctx context.Context) {
	if s.redisClient == nil || s.broadcaster == nil {
		return
	}

	activeMarkets := s.broadcaster.GetActiveMarketIDs()
	if len(activeMarkets) == 0 {
		return
	}

	for _, mkt := range activeMarkets {
		channel := "market:orderbook:" + mkt
		if !s.broadcaster.HasSubscribers(channel) {
			continue
		}

		reqCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		val, err := s.redisClient.Get(reqCtx, "depth:"+mkt).Result()
		cancel()

		if err != nil {
			if !errors.Is(err, redis.Nil) {
				if s.transitionDepthUnavailable(mkt) {
					s.logger.Warn("Depth Redis failure: notifying subscribers",
						zap.String("market", mkt), zap.Error(err))
					s.broadcaster.Broadcast(channel, s.marshalUnavailable(channel), protocol.StreamTypeControl)
				}
			}
			continue
		}

		wasUnavailable := s.clearDepthUnavailable(mkt)

		hash := xxhash.Sum64String(val)
		s.hashesMu.Lock()
		prev := s.depthHashes[mkt]
		s.depthHashes[mkt] = hash
		s.hashesMu.Unlock()

		// If data is unchanged and we didn't just recover from an outage, skip broadcast
		if hash == prev && !wasUnavailable {
			continue
		}

		var raw rawRedisDepth
		if err := json.Unmarshal([]byte(val), &raw); err != nil {
			continue
		}

		if raw.Sequence == 0 {
			s.logger.Debug("Skipping depth snapshot with missing sequence from Redis", zap.String("market", mkt))
			continue
		}

		payload := convertDepthPayload(mkt, raw)
		payload.Sequence = raw.Sequence

		env := protocol.OutboundEnvelope{Stream: channel, Data: payload}
		b, err := json.Marshal(env)
		if err != nil {
			continue
		}
		s.broadcaster.Broadcast(channel, b, protocol.StreamTypeOrderBook)
	}
}

// ─── Redis Ticker Poller ─────────────────────────────────────────────────────

func (s *Streamer) runTickerPoller(ctx context.Context) {
	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollActiveMarketTickers(ctx)
		}
	}
}

func (s *Streamer) pollActiveMarketTickers(ctx context.Context) {
	if s.redisClient == nil || s.broadcaster == nil {
		return
	}

	activeMarkets := s.broadcaster.GetActiveMarketIDs()
	if len(activeMarkets) == 0 {
		return
	}

	for _, mkt := range activeMarkets {
		channel := "market:ticker:" + mkt
		if !s.broadcaster.HasSubscribers(channel) {
			continue
		}

		reqCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		val, err := s.redisClient.Get(reqCtx, "ticker:"+mkt).Result()
		cancel()

		if err != nil {
			if !errors.Is(err, redis.Nil) {
				if s.transitionTickerUnavailable(mkt) {
					s.logger.Warn("Ticker Redis failure: notifying subscribers",
						zap.String("market", mkt), zap.Error(err))
					s.broadcaster.Broadcast(channel, s.marshalUnavailable(channel), protocol.StreamTypeControl)
				}
			}
			continue
		}
		wasUnavailable := s.clearTickerUnavailable(mkt)

		hash := xxhash.Sum64String(val)
		s.hashesMu.Lock()
		prev := s.tickerHashes[mkt]
		s.tickerHashes[mkt] = hash
		s.hashesMu.Unlock()

		if hash == prev && !wasUnavailable {
			continue
		}

		var payload protocol.TickerPayload
		if err := json.Unmarshal([]byte(val), &payload); err != nil {
			continue
		}
		if payload.MarketID == "" {
			payload.MarketID = mkt
		}

		env := protocol.OutboundEnvelope{Stream: channel, Data: payload}
		b, err := json.Marshal(env)
		if err != nil {
			continue
		}
		s.broadcaster.Broadcast(channel, b, protocol.StreamTypeTicker)
	}
}

// ─── Availability State Machine ───────────────────────────────────────────────

func (s *Streamer) TransitionDepthUnavailable(marketID string) bool {
	return s.transitionDepthUnavailable(marketID)
}

func (s *Streamer) transitionDepthUnavailable(marketID string) bool {
	s.availMu.Lock()
	defer s.availMu.Unlock()
	if s.depthAvailability[marketID] == stateUnavailable {
		return false
	}
	s.depthAvailability[marketID] = stateUnavailable
	return true
}

func (s *Streamer) ClearDepthUnavailable(marketID string) bool {
	return s.clearDepthUnavailable(marketID)
}

func (s *Streamer) clearDepthUnavailable(marketID string) bool {
	s.availMu.Lock()
	defer s.availMu.Unlock()
	was := s.depthAvailability[marketID] == stateUnavailable
	s.depthAvailability[marketID] = stateAvailable
	return was
}

func (s *Streamer) TransitionTickerUnavailable(marketID string) bool {
	return s.transitionTickerUnavailable(marketID)
}

func (s *Streamer) transitionTickerUnavailable(marketID string) bool {
	s.availMu.Lock()
	defer s.availMu.Unlock()
	if s.tickerAvailability[marketID] == stateUnavailable {
		return false
	}
	s.tickerAvailability[marketID] = stateUnavailable
	return true
}

func (s *Streamer) ClearTickerUnavailable(marketID string) bool {
	return s.clearTickerUnavailable(marketID)
}

func (s *Streamer) clearTickerUnavailable(marketID string) bool {
	s.availMu.Lock()
	defer s.availMu.Unlock()
	was := s.tickerAvailability[marketID] == stateUnavailable
	s.tickerAvailability[marketID] = stateAvailable
	return was
}
