package handler

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notificationv1 "tradedrift/platform/api/gen/notification/v1"
	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/repository"
	"tradedrift/services/notification/internal/service"
)

type NotificationHandler struct {
	notificationv1.UnimplementedNotificationServiceServer
	svc *service.Service
	log *zap.Logger
}

func NewNotificationHandler(svc *service.Service, log *zap.Logger) *NotificationHandler {
	return &NotificationHandler{
		svc: svc,
		log: log,
	}
}

// CreateNotification allows internal microservices (Auth, Admin, Wallet) to dispatch user alerts.
func (h *NotificationHandler) CreateNotification(ctx context.Context, req *notificationv1.CreateNotificationRequest) (*notificationv1.CreateNotificationResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.Title == "" {
		return nil, status.Error(codes.InvalidArgument, "title is required")
	}
	if req.Message == "" {
		return nil, status.Error(codes.InvalidArgument, "message is required")
	}

	notif, err := h.svc.CreateNotification(ctx, model.CreateNotificationInput{
		UserID:         req.UserId,
		Title:          req.Title,
		Message:        req.Message,
		Type:           req.Type,
		ReferenceID:    req.ReferenceId,
		ReferenceType:  req.ReferenceType,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalidUserID) || errors.Is(err, service.ErrEmptyTitle) || errors.Is(err, service.ErrEmptyMessage) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		h.log.Error("Failed to create notification", zap.Error(err))
		return nil, status.Error(codes.Internal, "internal error creating notification")
	}

	return &notificationv1.CreateNotificationResponse{
		NotificationId: notif.ID,
		Success:        true,
	}, nil
}

// GetNotifications retrieves a keyset-paginated list of notifications for the authenticated user.
func (h *NotificationHandler) GetNotifications(ctx context.Context, req *notificationv1.GetNotificationsRequest) (*notificationv1.GetNotificationsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}

	// Cursor validation: both fields are required together; providing only one is an error.
	// Providing neither means first page.
	var cursorTime *time.Time
	hasCursorTime := req.CursorTime != ""
	hasCursorID := req.CursorId != ""
	if hasCursorTime != hasCursorID {
		return nil, status.Error(codes.InvalidArgument, "cursor_time and cursor_id must both be provided or both omitted")
	}
	if hasCursorTime {
		t, err := time.Parse(time.RFC3339Nano, req.CursorTime)
		if err != nil {
			t, err = time.Parse(time.RFC3339, req.CursorTime)
		}
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid cursor_time format; expected RFC3339")
		}
		cursorTime = &t
	}

	filter := model.PaginationFilter{
		UserID:     req.UserId,
		CursorTime: cursorTime,
		CursorID:   req.CursorId,
		Limit:      limit,
		TypeFilter: req.TypeFilter,
	}

	// The repository queries limit+1 rows so we can determine has_more exactly.
	notifs, err := h.svc.GetNotifications(ctx, filter)
	if err != nil {
		if errors.Is(err, service.ErrInvalidUserID) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		h.log.Error("Failed to fetch notifications", zap.Error(err))
		return nil, status.Error(codes.Internal, "internal error fetching notifications")
	}

	// Determine exact has_more from the extra row returned by the repo.
	hasMore := len(notifs) > limit
	if hasMore {
		notifs = notifs[:limit] // trim the sentinel row before building the response
	}

	unreadCount, err := h.svc.GetUnreadCount(ctx, req.UserId)
	if err != nil {
		h.log.Warn("Failed to fetch unread count for notifications response", zap.Error(err))
	}

	var items []*notificationv1.NotificationItem
	for _, n := range notifs {
		readAtStr := ""
		if n.ReadAt != nil {
			readAtStr = n.ReadAt.Format(time.RFC3339)
		}

		items = append(items, &notificationv1.NotificationItem{
			Id:            n.ID,
			UserId:        n.UserID,
			Title:         n.Title,
			Message:       n.Message,
			Type:          string(n.Type),
			ReferenceId:   n.ReferenceID,
			ReferenceType: string(n.ReferenceType),
			IsRead:        n.IsRead,
			ReadAt:        readAtStr,
			CreatedAt:     n.CreatedAt.Format(time.RFC3339),
		})
	}

	// Next cursor points at the last RETURNED item, not the sentinel row.
	var nextCursorTime, nextCursorID string
	if len(notifs) > 0 {
		last := notifs[len(notifs)-1]
		nextCursorTime = last.CreatedAt.Format(time.RFC3339Nano)
		nextCursorID = last.ID
	}

	return &notificationv1.GetNotificationsResponse{
		Notifications:  items,
		NextCursorTime: nextCursorTime,
		NextCursorId:   nextCursorID,
		HasMore:        hasMore,
		UnreadCount:    unreadCount,
	}, nil
}


// MarkAsRead marks a single notification as read for the authenticated user.
func (h *NotificationHandler) MarkAsRead(ctx context.Context, req *notificationv1.MarkAsReadRequest) (*notificationv1.MarkAsReadResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.NotificationId == "" {
		return nil, status.Error(codes.InvalidArgument, "notification_id is required")
	}

	notif, err := h.svc.MarkAsRead(ctx, req.UserId, req.NotificationId)
	if err != nil {
		if errors.Is(err, repository.ErrNotificationNotFound) {
			// Return NotFound to prevent user enumeration
			return nil, status.Error(codes.NotFound, "notification not found")
		}
		if errors.Is(err, service.ErrInvalidUserID) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		h.log.Error("Failed to mark notification as read", zap.Error(err))
		return nil, status.Error(codes.Internal, "internal error marking notification read")
	}

	readAtStr := ""
	if notif.ReadAt != nil {
		readAtStr = notif.ReadAt.Format(time.RFC3339)
	}

	return &notificationv1.MarkAsReadResponse{
		NotificationId: notif.ID,
		IsRead:         true,
		ReadAt:         readAtStr,
	}, nil
}

// MarkAllAsRead marks all unread notifications as read for the authenticated user.
func (h *NotificationHandler) MarkAllAsRead(ctx context.Context, req *notificationv1.MarkAllAsReadRequest) (*notificationv1.MarkAllAsReadResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	count, err := h.svc.MarkAllAsRead(ctx, req.UserId)
	if err != nil {
		if errors.Is(err, service.ErrInvalidUserID) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		h.log.Error("Failed to mark all notifications as read", zap.Error(err))
		return nil, status.Error(codes.Internal, "internal error marking all notifications read")
	}

	return &notificationv1.MarkAllAsReadResponse{
		MarkedCount: count,
	}, nil
}

// GetUnreadCount returns the current count of unread notifications for badge rendering.
func (h *NotificationHandler) GetUnreadCount(ctx context.Context, req *notificationv1.GetUnreadCountRequest) (*notificationv1.GetUnreadCountResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	count, err := h.svc.GetUnreadCount(ctx, req.UserId)
	if err != nil {
		if errors.Is(err, service.ErrInvalidUserID) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		h.log.Error("Failed to get unread count", zap.Error(err))
		return nil, status.Error(codes.Internal, "internal error getting unread count")
	}

	return &notificationv1.GetUnreadCountResponse{
		UnreadCount: count,
	}, nil
}
