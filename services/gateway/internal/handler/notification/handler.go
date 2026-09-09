package notification

import (
	"net/http"
	"strconv"
	"time"

	notificationv1 "tradedrift/platform/api/gen/notification/v1"
	"tradedrift/services/gateway/internal/handler/common"
	"tradedrift/services/gateway/internal/middleware"
	"tradedrift/services/gateway/internal/response"
)

type Handler struct {
	client notificationv1.NotificationServiceClient
}

func NewHandler(client notificationv1.NotificationServiceClient) *Handler {
	return &Handler{client: client}
}

// ListNotifications — GET /api/v1/notifications
func (h *Handler) ListNotifications(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	q := r.URL.Query()
	limit := 20
	if l := q.Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 {
			limit = val
		}
	}

	req := &notificationv1.GetNotificationsRequest{
		UserId:     userID,
		CursorTime: q.Get("cursor_time"),
		CursorId:   q.Get("cursor_id"),
		Limit:      int32(limit),
		TypeFilter: q.Get("type"),
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetNotifications(ctx, req)
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	items := make([]NotificationItemDTO, 0, len(res.GetNotifications()))
	for _, n := range res.GetNotifications() {
		items = append(items, toNotificationDTO(n))
	}

	response.WriteJSON(w, http.StatusOK, GetNotificationsResponseDTO{
		Notifications:  items,
		NextCursorTime: res.GetNextCursorTime(),
		NextCursorID:   res.GetNextCursorId(),
		HasMore:        res.GetHasMore(),
		UnreadCount:    res.GetUnreadCount(),
	})
}

// MarkAsRead — POST /api/v1/notifications/{id}/read
func (h *Handler) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	notificationID := r.PathValue("id")
	if notificationID == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_NOTIFICATION_ID", "notification id is required")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.MarkAsRead(ctx, &notificationv1.MarkAsReadRequest{
		UserId:         userID,
		NotificationId: notificationID,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, MarkAsReadResponseDTO{
		NotificationID: res.GetNotificationId(),
		IsRead:         res.GetIsRead(),
		ReadAt:         res.GetReadAt(),
	})
}

// MarkAllAsRead — POST /api/v1/notifications/read-all
func (h *Handler) MarkAllAsRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.MarkAllAsRead(ctx, &notificationv1.MarkAllAsReadRequest{
		UserId: userID,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, MarkAllAsReadResponseDTO{
		MarkedCount: res.GetMarkedCount(),
	})
}

// GetUnreadCount — GET /api/v1/notifications/unread-count
func (h *Handler) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == "" {
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_TOKEN", "missing user identity")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.GetUnreadCount(ctx, &notificationv1.GetUnreadCountRequest{
		UserId: userID,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, UnreadCountResponseDTO{
		UnreadCount: res.GetUnreadCount(),
	})
}
