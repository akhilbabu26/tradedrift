package handler

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	marketv1 "tradedrift/platform/api/gen/market/v1"
	"tradedrift/services/market/internal/service"
)

type GRPCHandler struct {
	marketv1.UnimplementedMarketServiceServer
	svc service.MarketService
	log *zap.Logger
}

func NewGRPCHandler(svc service.MarketService, log *zap.Logger) *GRPCHandler {
	return &GRPCHandler{
		svc: svc,
		log: log,
	}
}

func (h *GRPCHandler) GetMarket(ctx context.Context, req *marketv1.GetMarketRequest) (*marketv1.GetMarketResponse, error) {
	if req.GetMarketId() == "" {
		return nil, status.Error(codes.InvalidArgument, "market_id is required")
	}

	m, err := h.svc.GetMarket(ctx, req.GetMarketId())
	if err != nil {
		h.log.Error("GetMarket failed", zap.String("market_id", req.GetMarketId()), zap.Error(err))
		return nil, mapToGRPCError(err)
	}

	return &marketv1.GetMarketResponse{
		Market: mapDomainMarketToProto(m),
	}, nil
}

func (h *GRPCHandler) ListMarkets(ctx context.Context, _ *marketv1.ListMarketsRequest) (*marketv1.ListMarketsResponse, error) {
	markets, err := h.svc.ListMarkets(ctx)
	if err != nil {
		h.log.Error("ListMarkets failed", zap.Error(err))
		return nil, mapToGRPCError(err)
	}

	protoMarkets := make([]*marketv1.Market, 0, len(markets))
	for _, m := range markets {
		protoMarkets = append(protoMarkets, mapDomainMarketToProto(m))
	}

	return &marketv1.ListMarketsResponse{
		Markets: protoMarkets,
	}, nil
}

func (h *GRPCHandler) GetTicker(ctx context.Context, req *marketv1.GetTickerRequest) (*marketv1.GetTickerResponse, error) {
	if req.GetMarketId() == "" {
		return nil, status.Error(codes.InvalidArgument, "market_id is required")
	}

	ticker, err := h.svc.GetTicker(ctx, req.GetMarketId())
	if err != nil {
		h.log.Error("GetTicker failed", zap.String("market_id", req.GetMarketId()), zap.Error(err))
		return nil, mapToGRPCError(err)
	}

	return &marketv1.GetTickerResponse{
		Ticker: mapDomainTickerToProto(ticker),
	}, nil
}

func (h *GRPCHandler) GetCandles(ctx context.Context, req *marketv1.GetCandlesRequest) (*marketv1.GetCandlesResponse, error) {
	if req.GetMarketId() == "" {
		return nil, status.Error(codes.InvalidArgument, "market_id is required")
	}

	resStr, err := mapProtoResolutionToString(req.GetResolution())
	if err != nil {
		return nil, err
	}

	var fromTime *time.Time
	if req.GetFrom() != nil {
		t := req.GetFrom().AsTime()
		fromTime = &t
	}

	var toTime *time.Time
	if req.GetTo() != nil {
		t := req.GetTo().AsTime()
		toTime = &t
	}

	candles, err := h.svc.GetCandles(ctx, req.GetMarketId(), resStr, fromTime, toTime, int(req.GetLimit()))
	if err != nil {
		h.log.Error("GetCandles failed",
			zap.String("market_id", req.GetMarketId()),
			zap.String("resolution", resStr),
			zap.Error(err),
		)
		return nil, mapToGRPCError(err)
	}

	protoCandles := make([]*marketv1.Candle, 0, len(candles))
	for _, c := range candles {
		protoCandles = append(protoCandles, mapDomainCandleToProto(c))
	}

	return &marketv1.GetCandlesResponse{
		Candles: protoCandles,
	}, nil
}
