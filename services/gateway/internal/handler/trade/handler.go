package trade

import (
	"net/http"
	"strconv"
	"time"

	tradev1 "tradedrift/platform/api/gen/trade/v1"
	"tradedrift/services/gateway/internal/handler/common"
	"tradedrift/services/gateway/internal/middleware"
	"tradedrift/services/gateway/internal/response"
)

// Handler provides HTTP handlers for the Trade Service gateway routes.
type Handler struct {
	client tradev1.TradeServiceClient
}

// NewHandler creates a Handler backed by a connected Trade Service gRPC client.
func NewHandler(client tradev1.TradeServiceClient) *Handler {
	return &Handler{client: client}
}

// GetTrade — GET /api/v1/trades/{id} (protected)
//
// Returns the full trade record including buyer_id and seller_id.
// Returns 403 PERMISSION_DENIED if the caller is neither buyer nor seller.
// Returns 404 NOT_FOUND if the trade does not exist.
func (h *Handler) GetTrade(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	tradeID := r.PathValue("id")
	if tradeID == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "trade id is required")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetTrade(ctx, &tradev1.GetTradeRequest{
		TradeId:      tradeID,
		CallerUserId: userID, // Trade Service enforces TI-8: caller must be buyer or seller
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, tradeDTO(res.GetTrade()))
}

// ListUserTrades — GET /api/v1/trades (protected)
//
// Returns the authenticated user's fill history, newest-first.
// Query params:
//   - cursor    : keyset cursor from previous response next_cursor (default: first page)
//   - limit     : max results per page (default 20, max 100)
//   - market_id : optional — restrict to a single market
func (h *Handler) ListUserTrades(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	cursor   := r.URL.Query().Get("cursor")
	marketID := r.URL.Query().Get("market_id")
	var limit int32 = 20
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if l, err := strconv.Atoi(ls); err == nil && l > 0 {
			limit = int32(l)
		}
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.ListUserTrades(ctx, &tradev1.ListUserTradesRequest{
		UserId:   userID,
		Cursor:   cursor,
		Limit:    limit,
		MarketId: marketID,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	trades := make([]TradeDTO, 0, len(res.GetTrades()))
	for _, t := range res.GetTrades() {
		trades = append(trades, tradeDTO(t))
	}

	response.WriteJSON(w, http.StatusOK, map[string]any{
		"trades":      trades,
		"next_cursor": res.GetNextCursor(),
	})
}

// ListMarketTrades — GET /api/v1/markets/{id}/trades (public — no auth required)
//
// Returns the public market trade tape for a market, newest-first.
// TI-7: buyer_id and seller_id are NEVER present in the response.
// Query params:
//   - cursor : keyset cursor from previous response next_cursor (default: first page)
//   - limit  : max results per page (default 50, max 200)
func (h *Handler) ListMarketTrades(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	if marketID == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "market id is required")
		return
	}

	cursor := r.URL.Query().Get("cursor")
	var limit int32 = 50
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if l, err := strconv.Atoi(ls); err == nil && l > 0 {
			limit = int32(l)
		}
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.ListMarketTrades(ctx, &tradev1.ListMarketTradesRequest{
		MarketId: marketID,
		Cursor:   cursor,
		Limit:    limit,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	trades := make([]MarketTradeDTO, 0, len(res.GetTrades()))
	for _, t := range res.GetTrades() {
		trades = append(trades, marketTradeDTO(t))
	}

	response.WriteJSON(w, http.StatusOK, map[string]any{
		"trades":      trades,
		"next_cursor": res.GetNextCursor(),
	})
}
