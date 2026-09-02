package handler

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tradev1 "tradedrift/platform/api/gen/trade/v1"
	"tradedrift/services/trade/internal/repository"
	"tradedrift/services/trade/internal/service"
)

// GRPCHandler implements tradev1.TradeServiceServer.
type GRPCHandler struct {
	tradev1.UnimplementedTradeServiceServer
	svc *service.Service
	log *zap.Logger
}

// NewGRPCHandler creates a GRPCHandler.
func NewGRPCHandler(svc *service.Service, log *zap.Logger) *GRPCHandler {
	return &GRPCHandler{svc: svc, log: log}
}

// GetTrade returns a single trade by ID.
// The API Gateway forwards caller_user_id from the JWT so Trade Service can
// enforce TI-8 (only buyer or seller may view their own trade).
func (h *GRPCHandler) GetTrade(ctx context.Context, req *tradev1.GetTradeRequest) (*tradev1.GetTradeResponse, error) {
	if req.TradeId == "" {
		return nil, status.Error(codes.InvalidArgument, "trade_id is required")
	}
	tradeID, err := uuid.Parse(req.TradeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid trade_id: %v", err)
	}

	// caller_user_id may be empty for admin callers routed via service mesh;
	// when present, enforce party membership (TI-8).
	var callerID uuid.UUID
	if req.CallerUserId != "" {
		callerID, err = uuid.Parse(req.CallerUserId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid caller_user_id: %v", err)
		}
	}

	t, err := h.svc.GetTrade(ctx, tradeID, callerID)
	if err != nil {
		if errors.Is(err, service.ErrNotParty) {
			return nil, status.Error(codes.PermissionDenied, "caller is not a party to this trade")
		}
		h.log.Error("GetTrade failed", zap.String("trade_id", req.TradeId), zap.Error(err))
		return nil, status.Error(codes.Internal, "internal error")
	}
	if t == nil {
		return nil, status.Errorf(codes.NotFound, "trade %s not found", req.TradeId)
	}
	return &tradev1.GetTradeResponse{Trade: toProtoTrade(t)}, nil
}

// ListUserTrades returns the authenticated user's trade history, newest-first.
// Cursor-paginated — pass next_cursor from the previous response to advance pages.
func (h *GRPCHandler) ListUserTrades(ctx context.Context, req *tradev1.ListUserTradesRequest) (*tradev1.ListUserTradesResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid user_id: %v", err)
	}

	trades, nextCursor, err := h.svc.ListUserTrades(ctx, userID, req.MarketId, req.Cursor, req.Limit)
	if err != nil {
		h.log.Error("ListUserTrades failed", zap.String("user_id", req.UserId), zap.Error(err))
		return nil, status.Error(codes.Internal, "internal error")
	}

	protoTrades := make([]*tradev1.Trade, len(trades))
	for i := range trades {
		protoTrades[i] = toProtoTrade(&trades[i])
	}
	return &tradev1.ListUserTradesResponse{
		Trades:     protoTrades,
		NextCursor: nextCursor,
	}, nil
}

// ListMarketTrades returns the public market trade tape.
// buyer_id and seller_id are NOT included in the response (MarketTrade message).
func (h *GRPCHandler) ListMarketTrades(ctx context.Context, req *tradev1.ListMarketTradesRequest) (*tradev1.ListMarketTradesResponse, error) {
	if req.MarketId == "" {
		return nil, status.Error(codes.InvalidArgument, "market_id is required")
	}

	trades, nextCursor, err := h.svc.ListMarketTrades(ctx, req.MarketId, req.Cursor, req.Limit)
	if err != nil {
		h.log.Error("ListMarketTrades failed", zap.String("market_id", req.MarketId), zap.Error(err))
		return nil, status.Error(codes.Internal, "internal error")
	}

	protoTrades := make([]*tradev1.MarketTrade, len(trades))
	for i := range trades {
		protoTrades[i] = toProtoMarketTrade(&trades[i])
	}
	return &tradev1.ListMarketTradesResponse{
		Trades:     protoTrades,
		NextCursor: nextCursor,
	}, nil
}

// ── Mappers ───────────────────────────────────────────────────────────────────

// toProtoTrade maps a repository.Trade to the full Trade proto message.
// Includes buyer_id and seller_id — only returned to authenticated parties.
func toProtoTrade(t *repository.Trade) *tradev1.Trade {
	return &tradev1.Trade{
		TradeId:     t.ID.String(),
		BuyerId:     t.BuyerID.String(),
		SellerId:    t.SellerID.String(),
		BuyOrderId:  t.BuyOrderID.String(),
		SellOrderId: t.SellOrderID.String(),
		MarketId:    t.MarketID,
		BaseAsset:   t.BaseAsset,
		QuoteAsset:  t.QuoteAsset,
		Price:       t.Price.String(),
		Quantity:    t.Quantity.String(),
		ExecutedAt:  t.ExecutedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		SettledAt:   t.SettledAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

// toProtoMarketTrade maps a repository.Trade to the public MarketTrade proto message.
// TI-7: buyer_id and seller_id are NEVER included — this is the public market tape.
func toProtoMarketTrade(t *repository.Trade) *tradev1.MarketTrade {
	return &tradev1.MarketTrade{
		TradeId:    t.ID.String(),
		MarketId:   t.MarketID,
		BaseAsset:  t.BaseAsset,
		QuoteAsset: t.QuoteAsset,
		Price:      t.Price.String(),
		Quantity:   t.Quantity.String(),
		ExecutedAt: t.ExecutedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

// Compile-time interface check.
var _ tradev1.TradeServiceServer = (*GRPCHandler)(nil)
