package portfolio

import (
	"net/http"
	"time"

	portfoliov1 "tradedrift/platform/api/gen/portfolio/v1"
	"tradedrift/services/gateway/internal/handler/common"
	"tradedrift/services/gateway/internal/middleware"
	"tradedrift/services/gateway/internal/response"
)

type Handler struct {
	client portfoliov1.PortfolioServiceClient
}

func NewHandler(client portfoliov1.PortfolioServiceClient) *Handler {
	return &Handler{client: client}
}

// GetPortfolioSummary — GET /api/v1/portfolio/summary
func (h *Handler) GetPortfolioSummary(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetPortfolioSummary(ctx, &portfoliov1.GetPortfolioSummaryRequest{
		UserId: userID,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, summaryDTO(res))
}

// GetPortfolioHoldings — GET /api/v1/portfolio/holdings
func (h *Handler) GetPortfolioHoldings(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetPortfolioHoldings(ctx, &portfoliov1.GetPortfolioHoldingsRequest{
		UserId: userID,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, holdingsDTO(res))
}
