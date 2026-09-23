package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/market/internal/repository"
)

// SeedVersion identifies the deterministic seed algorithm version for fresh environments.
// Note: SeedVersion affects the pseudo-random generator output for newly seeded markets.
// It does not automatically wipe, re-seed, or migrate existing database records.
const SeedVersion = 1

type HistorySeeder struct {
	marketRepo repository.MarketRepository
	candleRepo repository.CandleRepository
	logger     *zap.Logger
}

func NewHistorySeeder(
	marketRepo repository.MarketRepository,
	candleRepo repository.CandleRepository,
	logger *zap.Logger,
) *HistorySeeder {
	return &HistorySeeder{
		marketRepo: marketRepo,
		candleRepo: candleRepo,
		logger:     logger,
	}
}

var basePrices = map[string]decimal.Decimal{
	"BTC-USDT": decimal.NewFromFloat(96450.00),
	"ETH-USDT": decimal.NewFromFloat(2780.50),
	"SOL-USDT": decimal.NewFromFloat(188.20),
}

var seedSpecs = []struct {
	resolution string
	count      int
	duration   time.Duration
	volatility float64
}{
	{"1d", 90, 24 * time.Hour, 0.025},
	{"4h", 180, 4 * time.Hour, 0.015},
	{"1h", 168, time.Hour, 0.008},
	{"15m", 96, 15 * time.Minute, 0.004},
	{"5m", 120, 5 * time.Minute, 0.0025},
	{"1m", 120, time.Minute, 0.0015},
}

func deterministicSeed(marketID, resolution string) int64 {
	h := sha256.Sum256([]byte(fmt.Sprintf("market_seed_v%d:%s:%s", SeedVersion, marketID, resolution)))
	return int64(binary.BigEndian.Uint64(h[:8]))
}

func getBasePrice(marketID string) decimal.Decimal {
	if p, ok := basePrices[marketID]; ok {
		return p
	}
	return decimal.NewFromFloat(100.00)
}

// truncateStartTime returns the start time of the latest COMPLETED bucket for a given resolution.
// Anchoring to the latest completed bucket guarantees no candle or synthetic trade timestamp
// is ever generated into the future.
func truncateStartTime(t time.Time, res string, dur time.Duration) time.Time {
	t = t.UTC()
	if res == "1d" {
		todayMidnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return todayMidnight.Add(-24 * time.Hour)
	}
	return t.Truncate(dur).Add(-dur)
}

// EnsureSeedHistory checks each active market and resolution, seeding missing historical
// candles and synthetic trades idempotently.
func (s *HistorySeeder) EnsureSeedHistory(ctx context.Context) error {
	markets, err := s.marketRepo.ListMarkets(ctx)
	if err != nil {
		return fmt.Errorf("list markets for seeding: %w", err)
	}

	now := time.Now().UTC()

	for _, m := range markets {
		if m.Status != "ACTIVE" {
			continue
		}

		basePrice := getBasePrice(m.ID)

		for _, spec := range seedSpecs {
			count, err := s.candleRepo.GetCandleCount(ctx, m.ID, spec.resolution)
			if err != nil {
				return fmt.Errorf("check candle count for %s/%s: %w", m.ID, spec.resolution, err)
			}

			if count >= spec.count {
				s.logger.Debug("Market resolution already fully seeded",
					zap.String("market", m.ID),
					zap.String("resolution", spec.resolution),
					zap.Int("count", count),
				)
				continue
			}

			if count > 0 {
				// Partial history detected: preserve existing records to avoid timestamp-window skew
				s.logger.Warn("Partial candle history detected; preserving existing records without regeneration",
					zap.String("market", m.ID),
					zap.String("resolution", spec.resolution),
					zap.Int("existing_count", count),
					zap.Int("expected_count", spec.count),
				)
				continue
			}

			candles := s.generateCandles(m.ID, spec.resolution, spec.count, spec.duration, spec.volatility, basePrice, now)
			if err := s.candleRepo.BulkInsertSeedCandles(ctx, candles); err != nil {
				return fmt.Errorf("bulk insert seed candles for %s/%s: %w", m.ID, spec.resolution, err)
			}

			s.logger.Info("Seeded candles",
				zap.String("market", m.ID),
				zap.String("resolution", spec.resolution),
				zap.Int("inserted", len(candles)),
			)
		}

		// Ensure 24h baseline synthetic trades derived from 1h candles only if market has no recent trades
		// in the rolling 24-hour window. This ensures cold-start deployments have ticker metrics while
		// preventing re-injection when live or recent trades already exist.
		hasRecentTrades, err := s.marketRepo.HasRecentTrades(ctx, m.ID, 24*time.Hour)
		if err != nil {
			return fmt.Errorf("check recent trades existence for %s: %w", m.ID, err)
		}
		if !hasRecentTrades {
			trades := s.generateSyntheticTrades(m.ID, basePrice, now)
			if err := s.marketRepo.BulkInsertSeedTrades(ctx, trades); err != nil {
				return fmt.Errorf("bulk insert seed trades for %s: %w", m.ID, err)
			}
			s.logger.Info("Ensured 24h baseline synthetic trades",
				zap.String("market", m.ID),
				zap.Int("trades", len(trades)),
			)
		} else {
			s.logger.Debug("Market already has recent 24h trades, skipping synthetic trade seeding",
				zap.String("market", m.ID),
			)
		}
	}

	return nil
}

func (s *HistorySeeder) generateCandles(
	marketID, resolution string,
	count int,
	dur time.Duration,
	volatility float64,
	basePrice decimal.Decimal,
	now time.Time,
) []*repository.OHLCCandle {
	src := rand.NewSource(deterministicSeed(marketID, resolution))
	rng := rand.New(src)

	anchor := truncateStartTime(now, resolution, dur)
	candles := make([]*repository.OHLCCandle, count)

	currClose := basePrice.InexactFloat64()

	// Pre-generate price trajectory so current price is close to basePrice
	pricePoints := make([]float64, count+1)
	pricePoints[0] = currClose
	for i := 1; i <= count; i++ {
		drift := (rng.Float64() - 0.495) * volatility
		pricePoints[i] = pricePoints[i-1] * (1.0 + drift)
	}
	// Scale to ensure pricePoints[count] ends near basePrice
	ratio := currClose / pricePoints[count]
	for i := 0; i <= count; i++ {
		pricePoints[i] *= ratio
	}

	for i := 0; i < count; i++ {
		stepFromEnd := count - 1 - i
		startTime := anchor.Add(-time.Duration(stepFromEnd) * dur)
		closeTime := startTime.Add(dur - time.Second)

		op := pricePoints[i]
		cp := pricePoints[i+1]

		hp := math.Max(op, cp) * (1.0 + rng.Float64()*volatility*0.5)
		lp := math.Min(op, cp) * (1.0 - rng.Float64()*volatility*0.5)

		vol := (1.0 + rng.Float64()*2.0) * (10000.0 / cp)
		if vol < 0.01 {
			vol = 0.01
		}
		quoteVol := vol * ((op + cp) / 2.0)

		candles[i] = &repository.OHLCCandle{
			MarketID:     marketID,
			Resolution:   resolution,
			StartTime:    startTime,
			OpenPrice:    decimal.NewFromFloat(op).Round(2),
			HighPrice:    decimal.NewFromFloat(hp).Round(2),
			LowPrice:     decimal.NewFromFloat(lp).Round(2),
			ClosePrice:   decimal.NewFromFloat(cp).Round(2),
			Volume:       decimal.NewFromFloat(vol).Round(4),
			QuoteVolume:  decimal.NewFromFloat(quoteVol).Round(2),
			OpenTradeAt:  startTime,
			CloseTradeAt: closeTime,
		}
	}

	return candles
}

func (s *HistorySeeder) generateSyntheticTrades(
	marketID string,
	basePrice decimal.Decimal,
	now time.Time,
) []*repository.MarketTrade {
	dur := time.Hour
	candles := s.generateCandles(marketID, "1h", 168, dur, 0.008, basePrice, now)

	// Take the last 24 candles to create 24 hourly baseline trades within the 24h window
	startIdx := len(candles) - 24
	if startIdx < 0 {
		startIdx = 0
	}
	hourlyCandles := candles[startIdx:]

	trades := make([]*repository.MarketTrade, len(hourlyCandles))
	for i, c := range hourlyCandles {
		// Deterministic UUID based on marketID and bucket StartTime
		tradeID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%d", marketID, c.StartTime.Unix())))
		trades[i] = &repository.MarketTrade{
			ID:         tradeID,
			MarketID:   marketID,
			Price:      c.ClosePrice,
			Quantity:   decimal.NewFromFloat(0.25),
			ExecutedAt: c.CloseTradeAt,
		}
	}

	return trades
}
