package handler

import (
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/services/gateway/internal/middleware"
	"tradedrift/services/gateway/internal/response"
)

type WalletHandler struct {
	client walletv1.WalletServiceClient
}

func NewWalletHandler(client walletv1.WalletServiceClient) *WalletHandler {
	return &WalletHandler{client: client}
}

// GetBalances — GET /api/v1/wallet/balances (protected)
func (h *WalletHandler) GetBalances(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	ctx, cancel := outgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetBalances(ctx, &walletv1.GetBalancesRequest{
		UserId: userID,
	})
	if err != nil {
		writeWalletGRPCError(w, err)
		return
	}

	balances := make([]AssetBalanceDTO, 0, len(res.Balances))
	for _, b := range res.Balances {
		balances = append(balances, assetBalanceDTO(b))
	}

	response.WriteJSON(w, http.StatusOK, GetBalancesResponse{Balances: balances})
}

// GetBalance — GET /api/v1/wallet/balances/{asset} (protected)
func (h *WalletHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	asset := r.PathValue("asset")
	if asset == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "asset path parameter is required")
		return
	}

	ctx, cancel := outgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetBalance(ctx, &walletv1.GetBalanceRequest{
		UserId: userID,
		Asset:  asset,
	})
	if err != nil {
		writeWalletGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, GetBalanceResponse{
		Balance: assetBalanceDTO(res.Balance),
	})
}

// GetSupportedAssets — GET /api/v1/wallet/assets (public)
func (h *WalletHandler) GetSupportedAssets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := outgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetSupportedAssets(ctx, &walletv1.GetSupportedAssetsRequest{})
	if err != nil {
		writeWalletGRPCError(w, err)
		return
	}

	assets := make([]AssetInfoDTO, 0, len(res.Assets))
	for _, a := range res.Assets {
		assets = append(assets, assetInfoDTO(a))
	}

	response.WriteJSON(w, http.StatusOK, GetSupportedAssetsResponse{Assets: assets})
}

// writeWalletGRPCError maps gRPC status codes to HTTP status + error code JSON.
func writeWalletGRPCError(w http.ResponseWriter, err error) {
	st, _ := status.FromError(err)
	switch st.Code() {
	case codes.NotFound:
		response.WriteError(w, http.StatusNotFound, "NOT_FOUND", st.Message())
	case codes.InvalidArgument:
		response.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", st.Message())
	case codes.FailedPrecondition:
		response.WriteError(w, http.StatusUnprocessableEntity, "INSUFFICIENT_FUNDS", st.Message())
	case codes.AlreadyExists:
		response.WriteError(w, http.StatusConflict, "ALREADY_EXISTS", st.Message())
	case codes.ResourceExhausted:
		response.WriteError(w, http.StatusTooManyRequests, "API_RATE_LIMIT_EXCEEDED", st.Message())
	case codes.DeadlineExceeded:
		response.WriteError(w, http.StatusGatewayTimeout, "TIMEOUT", st.Message())
	case codes.Unavailable:
		response.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "wallet service is temporarily unavailable")
	default:
		response.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an internal error occurred")
	}
}
