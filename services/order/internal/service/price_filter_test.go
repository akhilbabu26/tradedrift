package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
)

type mockRedisClient struct {
	val  string
	err  error
	data map[string]string
}

func (m *mockRedisClient) Get(ctx context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)
	if m.data != nil {
		if v, ok := m.data[key]; ok {
			cmd.SetVal(v)
			return cmd
		}
		cmd.SetErr(redis.Nil)
		return cmd
	}
	if m.err != nil {
		cmd.SetErr(m.err)
	} else {
		cmd.SetVal(m.val)
	}
	return cmd
}

const sampleDepthJSON = `{
	"market_id": "BTC-USDT",
	"bids": [{"price": "96400.00", "quantity": "1.0"}],
	"asks": [{"price": "96488.58", "quantity": "1.0"}]
}`

const mmUUID = "00000000-0000-0000-0000-000000000001"
const retailUUID = "user-12345678-0000-0000-0000-000000000000"

func TestPriceFilter_MarketBuyBelowAsk(t *testing.T) {
	mockRedis := &mockRedisClient{val: sampleDepthJSON}
	filter := NewPriceFilter(mockRedis, "0.05", zap.NewNop())

	// Submitted 50000 for BUY MARKET — below best ask (96488.58)
	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeMarket, decimal.NewFromInt(50000), retailUUID)
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Equal(t, "FILTER_FAILURE_PERCENT_PRICE", violation.Code)
	assert.Equal(t, "50000", violation.SubmittedPrice)
	assert.Equal(t, "96488.58", violation.BestAsk)
	assert.Equal(t, "96400", violation.BestBid)
	assert.Contains(t, violation.Reason, "below current market best ask")
}

func TestPriceFilter_MarketBuyWithinBand(t *testing.T) {
	mockRedis := &mockRedisClient{val: sampleDepthJSON}
	filter := NewPriceFilter(mockRedis, "0.05", zap.NewNop())

	// Submitted 97000 for BUY MARKET — >= 96488.58 and <= 101266.50
	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeMarket, decimal.NewFromInt(97000), retailUUID)
	require.NoError(t, err)
}

func TestPriceFilter_MarketBuyAboveMaxAllowed(t *testing.T) {
	mockRedis := &mockRedisClient{val: sampleDepthJSON}
	filter := NewPriceFilter(mockRedis, "0.05", zap.NewNop())

	// Submitted 110000 for BUY MARKET — above 5% upper bound
	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeMarket, decimal.NewFromInt(110000), retailUUID)
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Contains(t, violation.Reason, "exceeds maximum allowed price")
}

func TestPriceFilter_LimitFatFinger(t *testing.T) {
	mockRedis := &mockRedisClient{val: sampleDepthJSON}
	filter := NewPriceFilter(mockRedis, "0.05", zap.NewNop())

	// Submitted 50000 for BUY LIMIT — way below 5% band around mid (96444.29)
	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeLimit, decimal.NewFromInt(50000), retailUUID)
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Contains(t, violation.Reason, "outside the allowed price band")
}

func TestPriceFilter_EmptyBookBypass(t *testing.T) {
	mockRedis := &mockRedisClient{val: `{"market_id":"BTC-USDT","bids":[],"asks":[]}`}
	filter := NewPriceFilter(mockRedis, "0.05", zap.NewNop())

	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeMarket, decimal.NewFromInt(50000), retailUUID)
	assert.NoError(t, err)
}

// ── DUAL AUTHORITY: MM Order Validation Tests ─────────────────────────────────

const sampleOldMEBookSOL = `{
	"market_id": "SOL-USDT",
	"bids": [{"price": "188.10", "quantity": "10.0"}],
	"asks": [{"price": "188.30", "quantity": "10.0"}]
}`

func TestPriceFilter_MM_ValidAnchor_Pass(t *testing.T) {
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	mockRedis := &mockRedisClient{
		data: map[string]string{
			"depth:SOL-USDT": sampleOldMEBookSOL,
			"refprice:anchor:SOL-USDT": fmt.Sprintf(`{
				"market_id": "SOL-USDT",
				"price": "117.01",
				"version": 1,
				"fetched_at": "%s",
				"source": "coingecko",
				"state": "FRESH"
			}`, nowStr),
		},
	}

	filter := NewDualPriceFilter(mockRedis, "0.05", mmUUID, "0.10", 60*time.Second, zap.NewNop())

	// MM submits replacement order at $117.204
	// ME book is at $188 (38% divergence), but MM evaluates against RefPrice anchor ($117.01)
	// $117.204 is within ±10% band of $117.01 [105.309, 128.711] -> MUST PASS!
	err := filter.ValidatePriceBand(context.Background(), "SOL-USDT", repository.SideBuy, repository.TypeLimit, decimal.RequireFromString("117.204"), mmUUID)
	require.NoError(t, err)
}

func TestPriceFilter_MM_RoguePrice_Reject(t *testing.T) {
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	mockRedis := &mockRedisClient{
		data: map[string]string{
			"depth:SOL-USDT": sampleOldMEBookSOL,
			"refprice:anchor:SOL-USDT": fmt.Sprintf(`{
				"market_id": "SOL-USDT",
				"price": "117.01",
				"version": 1,
				"fetched_at": "%s",
				"source": "coingecko",
				"state": "FRESH"
			}`, nowStr),
		},
	}

	filter := NewDualPriceFilter(mockRedis, "0.05", mmUUID, "0.10", 60*time.Second, zap.NewNop())

	// MM submits rogue order at $150.00 (exceeds 10% band: max 128.711) -> REJECT
	err := filter.ValidatePriceBand(context.Background(), "SOL-USDT", repository.SideBuy, repository.TypeLimit, decimal.RequireFromString("150.00"), mmUUID)
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Equal(t, "FILTER_FAILURE_PERCENT_PRICE", violation.Code)
	assert.Contains(t, violation.Reason, "outside allowed reference band")
}

func TestPriceFilter_MM_AnchorUnavailable_DoesNotFallbackToME(t *testing.T) {
	// ME book exists at $188, but refprice:anchor is MISSING in Redis
	mockRedis := &mockRedisClient{
		data: map[string]string{
			"depth:SOL-USDT": sampleOldMEBookSOL,
		},
	}

	filter := NewDualPriceFilter(mockRedis, "0.05", mmUUID, "0.10", 60*time.Second, zap.NewNop())

	// MM submits $117.01
	// INVARIANT: MM must NOT fall back to ME book! Doing so would resurrect the deadlock or accept bad quotes.
	// Must fail-closed and REJECT.
	err := filter.ValidatePriceBand(context.Background(), "SOL-USDT", repository.SideBuy, repository.TypeLimit, decimal.RequireFromString("117.01"), mmUUID)
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Equal(t, "FILTER_FAILURE_PERCENT_PRICE", violation.Code)
	assert.Contains(t, violation.Reason, "reference price anchor unavailable in Redis")
}

func TestPriceFilter_MM_StaleAnchor_DoesNotFallbackToME(t *testing.T) {
	// Anchor timestamp is 65 seconds old (exceeds 60s max age)
	staleTime := time.Now().Add(-65 * time.Second).UTC().Format(time.RFC3339Nano)
	mockRedis := &mockRedisClient{
		data: map[string]string{
			"depth:SOL-USDT": sampleOldMEBookSOL,
			"refprice:anchor:SOL-USDT": fmt.Sprintf(`{
				"market_id": "SOL-USDT",
				"price": "117.01",
				"version": 1,
				"fetched_at": "%s",
				"source": "coingecko",
				"state": "FRESH"
			}`, staleTime),
		},
	}

	filter := NewDualPriceFilter(mockRedis, "0.05", mmUUID, "0.10", 60*time.Second, zap.NewNop())

	// MM submits $117.01 with stale anchor -> Must fail-closed and REJECT
	err := filter.ValidatePriceBand(context.Background(), "SOL-USDT", repository.SideBuy, repository.TypeLimit, decimal.RequireFromString("117.01"), mmUUID)
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Equal(t, "FILTER_FAILURE_PERCENT_PRICE", violation.Code)
	assert.Contains(t, violation.Reason, "exceeds maximum allowed age")
}

func TestPriceFilter_MM_NonFreshState_Reject(t *testing.T) {
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	mockRedis := &mockRedisClient{
		data: map[string]string{
			"refprice:anchor:SOL-USDT": fmt.Sprintf(`{
				"market_id": "SOL-USDT",
				"price": "117.01",
				"version": 1,
				"fetched_at": "%s",
				"source": "coingecko",
				"state": "STALE"
			}`, nowStr),
		},
	}

	filter := NewDualPriceFilter(mockRedis, "0.05", mmUUID, "0.10", 60*time.Second, zap.NewNop())

	// Non-FRESH anchor must be rejected
	err := filter.ValidatePriceBand(context.Background(), "SOL-USDT", repository.SideBuy, repository.TypeLimit, decimal.RequireFromString("117.01"), mmUUID)
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Equal(t, "FILTER_FAILURE_PERCENT_PRICE", violation.Code)
	assert.Contains(t, violation.Reason, "state (STALE) is not FRESH")
}

// TestPriceFilter_MM_AnchorMarketIDMismatch_Reject tests OS-06:
// A Redis anchor at refprice:anchor:SOL-USDT that contains market_id:"ETH-USDT"
// must be rejected — cross-market contamination guard.
func TestPriceFilter_MM_AnchorMarketIDMismatch_Reject(t *testing.T) {
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	mockRedis := &mockRedisClient{
		data: map[string]string{
			"depth:SOL-USDT": sampleOldMEBookSOL,
			// Redis key is for SOL-USDT, but the JSON payload says ETH-USDT
			"refprice:anchor:SOL-USDT": fmt.Sprintf(`{
				"market_id": "ETH-USDT",
				"price": "2675.00",
				"version": 5,
				"fetched_at": "%s",
				"source": "coingecko",
				"state": "FRESH"
			}`, nowStr),
		},
	}

	filter := NewDualPriceFilter(mockRedis, "0.05", mmUUID, "0.10", 60*time.Second, zap.NewNop())

	// MM submits $117.01 for SOL-USDT — anchor claims it's ETH → must REJECT
	err := filter.ValidatePriceBand(context.Background(), "SOL-USDT", repository.SideBuy, repository.TypeLimit, decimal.RequireFromString("117.01"), mmUUID)
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Equal(t, "FILTER_FAILURE_PERCENT_PRICE", violation.Code)
	assert.Contains(t, violation.Reason, "anchor market_id")
	assert.Contains(t, violation.Reason, "ETH-USDT")
	assert.Contains(t, violation.Reason, "SOL-USDT")
}
