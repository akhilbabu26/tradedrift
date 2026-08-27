package market

import (
	"net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	marketv1 "tradedrift/platform/api/gen/market/v1"
	"tradedrift/services/gateway/internal/handler/common"
	"tradedrift/services/gateway/internal/response"
)

type Handler struct {
	client marketv1.MarketServiceClient
}

func NewHandler(client marketv1.MarketServiceClient) *Handler {
	return &Handler{client: client}
}

// ListMarkets — GET /api/v1/markets
func (h *Handler) ListMarkets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.ListMarkets(ctx, &marketv1.ListMarketsRequest{})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	markets := make([]MarketDTO, 0, len(res.GetMarkets()))
	for _, m := range res.GetMarkets() {
		markets = append(markets, marketDTO(m))
	}

	response.WriteJSON(w, http.StatusOK, map[string]any{"markets": markets})
}

// GetMarket — GET /api/v1/markets/{id}
func (h *Handler) GetMarket(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	if marketID == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "market id is required")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetMarket(ctx, &marketv1.GetMarketRequest{MarketId: marketID})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, marketDTO(res.GetMarket()))
}

// GetTicker — GET /api/v1/markets/{id}/ticker
func (h *Handler) GetTicker(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	if marketID == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "market id is required")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetTicker(ctx, &marketv1.GetTickerRequest{MarketId: marketID})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, tickerDTO(res.GetTicker()))
}

// GetCandles — GET /api/v1/markets/{id}/candles
func (h *Handler) GetCandles(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	resolutionStr := r.URL.Query().Get("resolution")
	limitStr := r.URL.Query().Get("limit")

	resolution := parseResolution(resolutionStr)
	var limit int32 = 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = int32(l)
		}
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	req := &marketv1.GetCandlesRequest{
		MarketId:   marketID,
		Resolution: resolution,
		Limit:      limit,
	}

	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			req.From = timestamppb.New(t)
		}
	}
	if toStr := r.URL.Query().Get("to"); toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			req.To = timestamppb.New(t)
		}
	}

	res, err := h.client.GetCandles(ctx, req)
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	candles := make([]CandleDTO, 0, len(res.GetCandles()))
	for _, c := range res.GetCandles() {
		candles = append(candles, candleDTO(c))
	}

	response.WriteJSON(w, http.StatusOK, map[string]any{"candles": candles})
}

func parseResolution(res string) marketv1.CandleResolution {
	switch res {
	case "1m", "1M":
		return marketv1.CandleResolution_CANDLE_RESOLUTION_1M
	case "5m", "5M":
		return marketv1.CandleResolution_CANDLE_RESOLUTION_5M
	case "15m", "15M":
		return marketv1.CandleResolution_CANDLE_RESOLUTION_15M
	case "1h", "1H", "4h", "4H":
		return marketv1.CandleResolution_CANDLE_RESOLUTION_1H
	case "1d", "1D":
		return marketv1.CandleResolution_CANDLE_RESOLUTION_1D
	default:
		return marketv1.CandleResolution_CANDLE_RESOLUTION_1H
	}
}
