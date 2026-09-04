package handler

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	portfoliov1 "tradedrift/platform/api/gen/portfolio/v1"
	"tradedrift/services/portfolio/internal/metrics"
	"tradedrift/services/portfolio/internal/service"
)

type Handler struct {
	portfoliov1.UnimplementedPortfolioServiceServer
	svc    *service.Service
	logger *zap.Logger
}

func New(svc *service.Service, logger *zap.Logger) *Handler {
	return &Handler{
		svc:    svc,
		logger: logger,
	}
}

// GetPortfolioSummary returns the total equity, cash balance, and PnL revaluations for a user.
func (h *Handler) GetPortfolioSummary(
	ctx context.Context,
	req *portfoliov1.GetPortfolioSummaryRequest,
) (*portfoliov1.PortfolioSummaryResponse, error) {
	start := time.Now()
	var statusCode codes.Code = codes.OK
	defer func() {
		metrics.GRPCRequestsTotal.WithLabelValues("GetPortfolioSummary", statusCode.String()).Inc()
		metrics.GRPCDurationSeconds.WithLabelValues("GetPortfolioSummary").Observe(time.Since(start).Seconds())
	}()

	userID := req.GetUserId()
	if userID == "" {
		statusCode = codes.InvalidArgument
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if _, err := uuid.Parse(userID); err != nil {
		statusCode = codes.InvalidArgument
		return nil, status.Errorf(codes.InvalidArgument, "invalid user_id UUID: %v", err)
	}

	summary, err := h.svc.GetPortfolioSummary(ctx, userID)
	if err != nil {
		h.logger.Error("failed to get portfolio summary", zap.String("user_id", userID), zap.Error(err))
		statusCode = codes.Internal
		return nil, status.Errorf(codes.Internal, "failed to compute portfolio summary: %v", err)
	}

	return &portfoliov1.PortfolioSummaryResponse{
		UserId:        summary.UserID,
		TotalValue:    summary.TotalValue,
		RealizedPnl:   summary.RealizedPnL,
		UnrealizedPnl: summary.UnrealizedPnL,
		CashBalance:   summary.CashBalance,
		UpdatedAt:     summary.UpdatedAt,
	}, nil
}

// GetPortfolioHoldings returns the active crypto asset breakdown for a user.
func (h *Handler) GetPortfolioHoldings(
	ctx context.Context,
	req *portfoliov1.GetPortfolioHoldingsRequest,
) (*portfoliov1.PortfolioHoldingsResponse, error) {
	start := time.Now()
	var statusCode codes.Code = codes.OK
	defer func() {
		metrics.GRPCRequestsTotal.WithLabelValues("GetPortfolioHoldings", statusCode.String()).Inc()
		metrics.GRPCDurationSeconds.WithLabelValues("GetPortfolioHoldings").Observe(time.Since(start).Seconds())
	}()

	userID := req.GetUserId()
	if userID == "" {
		statusCode = codes.InvalidArgument
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if _, err := uuid.Parse(userID); err != nil {
		statusCode = codes.InvalidArgument
		return nil, status.Errorf(codes.InvalidArgument, "invalid user_id UUID: %v", err)
	}

	holdingsData, err := h.svc.GetPortfolioHoldings(ctx, userID)
	if err != nil {
		h.logger.Error("failed to get portfolio holdings", zap.String("user_id", userID), zap.Error(err))
		statusCode = codes.Internal
		return nil, status.Errorf(codes.Internal, "failed to fetch portfolio holdings: %v", err)
	}

	protoHoldings := make([]*portfoliov1.HoldingDetail, 0, len(holdingsData.Holdings))
	for _, h := range holdingsData.Holdings {
		protoHoldings = append(protoHoldings, &portfoliov1.HoldingDetail{
			Asset:             h.Asset,
			TotalQuantity:     h.TotalQuantity,
			AverageEntryPrice: h.AverageEntryPrice,
			CurrentPrice:      h.CurrentPrice,
			UnrealizedPnl:     h.UnrealizedPnL,
		})
	}

	return &portfoliov1.PortfolioHoldingsResponse{
		UserId:   holdingsData.UserID,
		Holdings: protoHoldings,
	}, nil
}
