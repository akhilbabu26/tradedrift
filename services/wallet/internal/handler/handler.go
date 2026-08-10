package handler

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/services/wallet/internal/repository"
	"tradedrift/services/wallet/internal/service"
)

// GRPCHandler implements walletv1.WalletServiceServer.
// It translates proto requests → service calls → proto responses.
type GRPCHandler struct {
	walletv1.UnimplementedWalletServiceServer
	svc *service.Service
	log *zap.Logger
}

// NewGRPCHandler creates a new wallet gRPC handler.
func NewGRPCHandler(svc *service.Service, log *zap.Logger) *GRPCHandler {
	return &GRPCHandler{svc: svc, log: log}
}

// ─── RPCs ─────────────────────────────────────────────────────────────────

func (h *GRPCHandler) InitializeWallet(ctx context.Context, req *walletv1.InitializeWalletRequest) (*walletv1.InitializeWalletResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if err := h.svc.InitializeWallet(ctx, req.UserId); err != nil {
		return nil, mapToGRPCError(err)
	}
	return &walletv1.InitializeWalletResponse{Success: true}, nil
}

func (h *GRPCHandler) ReserveFunds(ctx context.Context, req *walletv1.ReserveFundsRequest) (*walletv1.ReserveFundsResponse, error) {
	if req.UserId == "" || req.OrderId == "" || req.Asset == "" || req.Amount == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id, order_id, asset and amount are required")
	}
	reservation, err := h.svc.ReserveFunds(ctx, req.UserId, req.OrderId, req.Asset, req.Amount)
	if err != nil {
		return nil, mapToGRPCError(err)
	}
	return &walletv1.ReserveFundsResponse{
		ReservationId:  reservation.ID,
		ReservedAmount: reservation.ReservedAmount,
		AlreadyExisted: reservation.Status != repository.ReservationActive,
	}, nil
}

func (h *GRPCHandler) ReleaseFunds(ctx context.Context, req *walletv1.ReleaseFundsRequest) (*walletv1.ReleaseFundsResponse, error) {
	if req.OrderId == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if err := h.svc.ReleaseFunds(ctx, req.OrderId); err != nil {
		return nil, mapToGRPCError(err)
	}
	return &walletv1.ReleaseFundsResponse{Success: true}, nil
}

func (h *GRPCHandler) SettleTrade(ctx context.Context, req *walletv1.SettleTradeRequest) (*walletv1.SettleTradeResponse, error) {
	if req.TradeId == "" || req.BuyerId == "" || req.SellerId == "" ||
		req.SellOrderId == "" || req.BaseAsset == "" || req.Quantity == "" {
		return nil, status.Error(codes.InvalidArgument, "trade_id, buyer_id, seller_id, sell_order_id, base_asset and quantity are required")
	}
	settlReq := service.TradeSettlementRequest{
		TradeID:       req.TradeId,
		BuyerUserID:   req.BuyerId,
		SellerUserID:  req.SellerId,
		SellerOrderID: req.SellOrderId,
		BaseAsset:     req.BaseAsset,
		QuoteAsset:    req.QuoteAsset,
		BaseAmount:    req.Quantity, // quantity of base asset (e.g. BTC)
		QuoteAmount:   req.Price,    // Settlement Service computes total; passed as-is
	}
	if err := h.svc.SettleTrade(ctx, settlReq); err != nil {
		return nil, mapToGRPCError(err)
	}
	return &walletv1.SettleTradeResponse{Success: true}, nil
}

func (h *GRPCHandler) GetBalance(ctx context.Context, req *walletv1.GetBalanceRequest) (*walletv1.GetBalanceResponse, error) {
	if req.UserId == "" || req.Asset == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and asset are required")
	}
	wallet, err := h.svc.GetBalance(ctx, req.UserId, req.Asset)
	if err != nil {
		return nil, mapToGRPCError(err)
	}
	return &walletv1.GetBalanceResponse{
		Balance: &walletv1.AssetBalance{
			Asset:            wallet.Asset,
			AvailableBalance: wallet.AvailableBalance,
			ReservedBalance:  wallet.ReservedBalance,
		},
	}, nil
}

func (h *GRPCHandler) GetBalances(ctx context.Context, req *walletv1.GetBalancesRequest) (*walletv1.GetBalancesResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	wallets, err := h.svc.GetBalances(ctx, req.UserId)
	if err != nil {
		return nil, mapToGRPCError(err)
	}
	balances := make([]*walletv1.AssetBalance, 0, len(wallets))
	for _, w := range wallets {
		balances = append(balances, &walletv1.AssetBalance{
			Asset:            w.Asset,
			AvailableBalance: w.AvailableBalance,
			ReservedBalance:  w.ReservedBalance,
		})
	}
	return &walletv1.GetBalancesResponse{Balances: balances}, nil
}

func (h *GRPCHandler) GetSupportedAssets(ctx context.Context, req *walletv1.GetSupportedAssetsRequest) (*walletv1.GetSupportedAssetsResponse, error) {
	assets, err := h.svc.GetSupportedAssets(ctx)
	if err != nil {
		return nil, mapToGRPCError(err)
	}
	out := make([]*walletv1.AssetInfo, 0, len(assets))
	for _, a := range assets {
		out = append(out, &walletv1.AssetInfo{
			AssetCode:    a.AssetCode,
			AssetName:    a.AssetName,
			Decimals:     int32(a.Decimals),
			IsEnabled:    a.IsEnabled,
			SeedAmount:   a.SeedAmount,
			DisplayOrder: int32(a.DisplayOrder),
		})
	}
	return &walletv1.GetSupportedAssetsResponse{Assets: out}, nil
}

func (h *GRPCHandler) Health(ctx context.Context, _ *walletv1.HealthRequest) (*walletv1.HealthResponse, error) {
	return &walletv1.HealthResponse{Ok: true}, nil
}
