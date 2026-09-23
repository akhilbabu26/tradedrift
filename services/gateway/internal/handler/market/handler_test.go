package market_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"

	marketv1 "tradedrift/platform/api/gen/market/v1"
	markethandler "tradedrift/services/gateway/internal/handler/market"
)

type mockMarketClient struct {
	marketv1.MarketServiceClient
	getMarketsOverviewFunc func(ctx context.Context, in *marketv1.GetMarketsOverviewRequest, opts ...grpc.CallOption) (*marketv1.GetMarketsOverviewResponse, error)
	getCandlesFunc         func(ctx context.Context, in *marketv1.GetCandlesRequest, opts ...grpc.CallOption) (*marketv1.GetCandlesResponse, error)
}

func (m *mockMarketClient) GetMarketsOverview(ctx context.Context, in *marketv1.GetMarketsOverviewRequest, opts ...grpc.CallOption) (*marketv1.GetMarketsOverviewResponse, error) {
	if m.getMarketsOverviewFunc != nil {
		return m.getMarketsOverviewFunc(ctx, in, opts...)
	}
	return &marketv1.GetMarketsOverviewResponse{}, nil
}

func (m *mockMarketClient) GetCandles(ctx context.Context, in *marketv1.GetCandlesRequest, opts ...grpc.CallOption) (*marketv1.GetCandlesResponse, error) {
	if m.getCandlesFunc != nil {
		return m.getCandlesFunc(ctx, in, opts...)
	}
	return &marketv1.GetCandlesResponse{}, nil
}

func TestHandler_GetMarketsOverview(t *testing.T) {
	var capturedResolution marketv1.CandleResolution
	var capturedLimit int32

	client := &mockMarketClient{
		getMarketsOverviewFunc: func(ctx context.Context, in *marketv1.GetMarketsOverviewRequest, opts ...grpc.CallOption) (*marketv1.GetMarketsOverviewResponse, error) {
			capturedResolution = in.GetResolution()
			capturedLimit = in.GetLimit()

			return &marketv1.GetMarketsOverviewResponse{
				Markets: []*marketv1.MarketOverviewItem{
					{
						MarketId:               "BTC-USDT",
						Symbol:                 "BTC/USDT",
						BaseAsset:              "BTC",
						QuoteAsset:             "USDT",
						LastPrice:              "96450.00",
						High_24H:               "97000.00",
						Low_24H:                "95000.00",
						Volume_24H:             "123.45",
						QuoteVolume_24H:        "11906752.50",
						PriceChange_24HPercent: "1.52",
						Trend:                  []string{"95100.00", "95500.00", "96450.00"},
					},
				},
			}, nil
		},
	}

	handler := markethandler.NewHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/markets/overview?resolution=4h&limit=100", nil)
	rec := httptest.NewRecorder()

	handler.GetMarketsOverview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if capturedResolution != marketv1.CandleResolution_CANDLE_RESOLUTION_4H {
		t.Errorf("expected CANDLE_RESOLUTION_4H, got %v", capturedResolution)
	}

	if capturedLimit != 100 {
		t.Errorf("expected limit 100, got %d", capturedLimit)
	}

	var body struct {
		Markets []markethandler.MarketOverviewItemDTO `json:"markets"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(body.Markets) != 1 {
		t.Fatalf("expected 1 market, got %d", len(body.Markets))
	}

	m := body.Markets[0]
	if m.MarketID != "BTC-USDT" || m.Symbol != "BTC/USDT" || m.LastPrice != "96450.00" || len(m.Trend) != 3 {
		t.Errorf("unexpected market dto content: %+v", m)
	}
}

func TestHandler_GetCandles_4H(t *testing.T) {
	var capturedResolution marketv1.CandleResolution

	client := &mockMarketClient{
		getCandlesFunc: func(ctx context.Context, in *marketv1.GetCandlesRequest, opts ...grpc.CallOption) (*marketv1.GetCandlesResponse, error) {
			capturedResolution = in.GetResolution()
			return &marketv1.GetCandlesResponse{}, nil
		},
	}

	handler := markethandler.NewHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/markets/BTC-USDT/candles?resolution=4h", nil)
	rec := httptest.NewRecorder()

	handler.GetCandles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if capturedResolution != marketv1.CandleResolution_CANDLE_RESOLUTION_4H {
		t.Errorf("expected CANDLE_RESOLUTION_4H, got %v", capturedResolution)
	}
}
