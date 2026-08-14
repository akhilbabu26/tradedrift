package order

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	orderv1 "tradedrift/platform/api/gen/order/v1"
	"tradedrift/services/gateway/internal/handler/common"
	"tradedrift/services/gateway/internal/middleware"
	"tradedrift/services/gateway/internal/response"
)

type Handler struct {
	client orderv1.OrderServiceClient
}

func NewHandler(client orderv1.OrderServiceClient) *Handler {
	return &Handler{client: client}
}

// CreateOrder — POST /api/v1/orders (protected)
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	var req CreateOrderRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed request payload")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	protoSide := parseProtoSide(req.Side)
	protoType := parseProtoType(req.OrderType)

	res, err := h.client.CreateOrder(ctx, &orderv1.CreateOrderRequest{
		UserId:         userID,
		MarketId:       req.MarketID,
		Side:           protoSide,
		OrderType:      protoType,
		Price:          req.Price,
		Quantity:       req.Quantity,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusCreated, orderDTO(res.GetOrder()))
}

// GetOrder — GET /api/v1/orders/{id} (protected)
func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	orderID := r.PathValue("id")
	if orderID == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "order id is required")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetOrder(ctx, &orderv1.GetOrderRequest{
		UserId:  userID,
		OrderId: orderID,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, orderDTO(res.GetOrder()))
}

// ListOrders — GET /api/v1/orders (protected)
func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	marketID := r.URL.Query().Get("market_id")
	statusStr := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")

	var limit int32 = 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = int32(l)
		}
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.ListOrders(ctx, &orderv1.ListOrdersRequest{
		UserId:   userID,
		MarketId: marketID,
		Status:   parseProtoStatus(statusStr),
		Limit:    limit,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	orders := make([]OrderDTO, 0, len(res.GetOrders()))
	for _, o := range res.GetOrders() {
		orders = append(orders, orderDTO(o))
	}

	response.WriteJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

// CancelOrder — POST /api/v1/orders/{id}/cancel (protected)
func (h *Handler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	orderID := r.PathValue("id")
	if orderID == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "order id is required")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.CancelOrder(ctx, &orderv1.CancelOrderRequest{
		UserId:  userID,
		OrderId: orderID,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, orderDTO(res.GetOrder()))
}

func parseProtoSide(side string) orderv1.OrderSide {
	switch side {
	case "BUY":
		return orderv1.OrderSide_ORDER_SIDE_BUY
	case "SELL":
		return orderv1.OrderSide_ORDER_SIDE_SELL
	default:
		return orderv1.OrderSide_ORDER_SIDE_UNSPECIFIED
	}
}

func parseProtoType(orderType string) orderv1.OrderType {
	switch orderType {
	case "LIMIT":
		return orderv1.OrderType_ORDER_TYPE_LIMIT
	case "MARKET":
		return orderv1.OrderType_ORDER_TYPE_MARKET
	default:
		return orderv1.OrderType_ORDER_TYPE_UNSPECIFIED
	}
}

func parseProtoStatus(statusStr string) orderv1.OrderStatus {
	switch statusStr {
	case "OPEN":
		return orderv1.OrderStatus_ORDER_STATUS_OPEN
	case "PARTIALLY_FILLED":
		return orderv1.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED
	case "FILLED":
		return orderv1.OrderStatus_ORDER_STATUS_FILLED
	case "CANCELLING":
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLING
	case "CANCELLED":
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}
