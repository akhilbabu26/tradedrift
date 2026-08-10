package handler

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orderv1 "tradedrift/platform/api/gen/order/v1"
	"tradedrift/services/order/internal/service"
)

type GRPCHandler struct {
	orderv1.UnimplementedOrderServiceServer
	svc    service.Service
	logger *zap.Logger
}

func NewGRPCHandler(svc service.Service, logger *zap.Logger) *GRPCHandler {
	return &GRPCHandler{
		svc:    svc,
		logger: logger,
	}
}

func (h *GRPCHandler) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.MarketId == "" {
		return nil, status.Error(codes.InvalidArgument, "market_id is required")
	}
	if req.Quantity == "" {
		return nil, status.Error(codes.InvalidArgument, "quantity is required")
	}

	side := parseProtoSide(req.Side)
	orderType := parseProtoType(req.OrderType)

	params := &service.CreateOrderParams{
		UserID:         req.UserId,
		MarketID:       req.MarketId,
		Side:           side,
		OrderType:      orderType,
		Price:          req.Price,
		Quantity:       req.Quantity,
		IdempotencyKey: req.IdempotencyKey,
	}

	order, err := h.svc.CreateOrder(ctx, params)
	if err != nil {
		h.logger.Error("CreateOrder failed", zap.Error(err))
		return nil, mapServiceError(err)
	}

	return &orderv1.CreateOrderResponse{
		Order: toProtoOrder(order),
	}, nil
}

func (h *GRPCHandler) CancelOrder(ctx context.Context, req *orderv1.CancelOrderRequest) (*orderv1.CancelOrderResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.OrderId == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	order, err := h.svc.CancelOrder(ctx, req.OrderId, req.UserId)
	if err != nil {
		h.logger.Error("CancelOrder failed", zap.String("order_id", req.OrderId), zap.Error(err))
		return nil, mapServiceError(err)
	}

	return &orderv1.CancelOrderResponse{
		Order: toProtoOrder(order),
	}, nil
}

func (h *GRPCHandler) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.OrderId == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	order, err := h.svc.GetOrder(ctx, req.OrderId, req.UserId)
	if err != nil {
		h.logger.Error("GetOrder failed", zap.String("order_id", req.OrderId), zap.Error(err))
		return nil, mapServiceError(err)
	}

	return &orderv1.GetOrderResponse{
		Order: toProtoOrder(order),
	}, nil
}

func (h *GRPCHandler) ListOrders(ctx context.Context, req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	side := parseProtoSide(req.Side)
	orderStatus := parseProtoStatus(req.Status)

	var fromTime, toTime *time.Time
	if req.From != nil {
		t := req.From.AsTime()
		fromTime = &t
	}
	if req.To != nil {
		t := req.To.AsTime()
		toTime = &t
	}

	orders, err := h.svc.ListOrders(ctx, req.UserId, req.MarketId, req.Cursor, side, orderStatus, fromTime, toTime, req.Limit)
	if err != nil {
		h.logger.Error("ListOrders failed", zap.String("user_id", req.UserId), zap.Error(err))
		return nil, mapServiceError(err)
	}

	protoOrders := make([]*orderv1.Order, 0, len(orders))
	var nextCursor string

	for _, o := range orders {
		protoOrders = append(protoOrders, toProtoOrder(o))
	}

	if len(orders) > 0 {
		last := orders[len(orders)-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
	}

	return &orderv1.ListOrdersResponse{
		Orders:     protoOrders,
		NextCursor: nextCursor,
	}, nil
}

func (h *GRPCHandler) CancelAllOrders(ctx context.Context, req *orderv1.CancelAllOrdersRequest) (*orderv1.CancelAllOrdersResponse, error) {
	// TODO: Implement bulk cancellation for market halt / system reset saga
	return nil, status.Error(codes.Unimplemented, "bulk cancel all orders is not implemented yet")
}
