package service

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
)

type mockRedisClient struct {
	val string
	err error
}

func (m *mockRedisClient) Get(ctx context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)
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

func TestPriceFilter_MarketBuyBelowAsk(t *testing.T) {
	mockRedis := &mockRedisClient{val: sampleDepthJSON}
	filter := NewPriceFilter(mockRedis, "0.05", zap.NewNop())

	// Submitted 50000 for BUY MARKET — below best ask (96488.58)
	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeMarket, decimal.NewFromInt(50000))
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
	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeMarket, decimal.NewFromInt(97000))
	require.NoError(t, err)
}

func TestPriceFilter_MarketBuyAboveMaxAllowed(t *testing.T) {
	mockRedis := &mockRedisClient{val: sampleDepthJSON}
	filter := NewPriceFilter(mockRedis, "0.05", zap.NewNop())

	// Submitted 110000 for BUY MARKET — above 5% upper bound
	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeMarket, decimal.NewFromInt(110000))
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Contains(t, violation.Reason, "exceeds maximum allowed price")
}

func TestPriceFilter_LimitFatFinger(t *testing.T) {
	mockRedis := &mockRedisClient{val: sampleDepthJSON}
	filter := NewPriceFilter(mockRedis, "0.05", zap.NewNop())

	// Submitted 50000 for BUY LIMIT — way below 5% band around mid (96444.29)
	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeLimit, decimal.NewFromInt(50000))
	require.Error(t, err)

	var violation *PriceFilterViolation
	require.ErrorAs(t, err, &violation)
	assert.Contains(t, violation.Reason, "outside the allowed price band")
}

func TestPriceFilter_EmptyBookBypass(t *testing.T) {
	mockRedis := &mockRedisClient{val: `{"market_id":"BTC-USDT","bids":[],"asks":[]}`}
	filter := NewPriceFilter(mockRedis, "0.05", zap.NewNop())

	err := filter.ValidatePriceBand(context.Background(), "BTC-USDT", repository.SideBuy, repository.TypeMarket, decimal.NewFromInt(50000))
	assert.NoError(t, err)
}
