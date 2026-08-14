package wallet

import (
	"net/http"
	"time"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/services/gateway/internal/handler/common"
	"tradedrift/services/gateway/internal/middleware"
	"tradedrift/services/gateway/internal/response"
)

type Handler struct {
	client walletv1.WalletServiceClient
}

func NewHandler(client walletv1.WalletServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) GetSupportedAssets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetSupportedAssets(ctx, &walletv1.GetSupportedAssetsRequest{})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	assets := make([]AssetDTO, 0, len(res.GetAssets()))
	for _, a := range res.GetAssets() {
		assets = append(assets, assetDTO(a))
	}

	response.WriteJSON(w, http.StatusOK, map[string]any{"assets": assets})
}

func (h *Handler) GetBalances(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetBalances(ctx, &walletv1.GetBalancesRequest{UserId: userID})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	balances := make([]BalanceDTO, 0, len(res.GetBalances()))
	for _, b := range res.GetBalances() {
		balances = append(balances, balanceDTO(b))
	}

	response.WriteJSON(w, http.StatusOK, map[string]any{"balances": balances})
}

func (h *Handler) GetBalance(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	asset := r.PathValue("asset")
	if asset == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "asset is required")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetBalance(ctx, &walletv1.GetBalanceRequest{
		UserId: userID,
		Asset:  asset,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, balanceDTO(res.GetBalance()))
}
