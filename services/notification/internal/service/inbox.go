package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/notification/internal/model"
)

// CreateNotification allows internal microservices (Auth, Admin, Wallet) to dispatch
// user alerts directly via gRPC. The notification is persisted and staged for real-time
// delivery through the Transactional Outbox → Redis pipeline.
func (s *Service) CreateNotification(ctx context.Context, input model.CreateNotificationInput) (*model.Notification, error) {
	if input.UserID == "" {
		return nil, ErrInvalidUserID
	}
	if input.Title == "" {
		return nil, ErrEmptyTitle
	}
	if input.Message == "" {
		return nil, ErrEmptyMessage
	}

	now := time.Now().UTC()
	notifID, _ := platformuuid.New()
	outboxID, _ := platformuuid.New()

	notifType := input.Type
	if notifType == "" {
		notifType = model.TypeInfo
	}

	notif := &model.Notification{
		ID:            notifID,
		UserID:        input.UserID,
		Title:         input.Title,
		Message:       input.Message,
		Type:          notifType,
		ReferenceID:   input.ReferenceID,
		ReferenceType: input.ReferenceType,
		IsRead:        false,
		CreatedAt:     now,
	}

	env := model.RedisEnvelope{
		EventID:        notifID,
		NotificationID: notifID,
		Type:           "notification.created",
		Channel:        "user:notifications",
		Timestamp:      now,
		Data:           notif,
	}
	payload, _ := json.Marshal(env)

	outbox := &model.OutboxEvent{
		ID:            outboxID,
		EventType:     "NotificationCreated",
		Payload:       payload,
		TargetChannel: "user:notifications:" + input.UserID,
		CreatedAt:     now,
	}

	if err := s.repo.CreateWithDedupTx(ctx, notif, notifID, outbox); err != nil {
		return nil, fmt.Errorf("create notification: %w", err)
	}

	return notif, nil
}

// GetNotifications retrieves a paginated inbox for the given user using keyset pagination.
func (s *Service) GetNotifications(ctx context.Context, filter model.PaginationFilter) ([]*model.Notification, error) {
	if filter.UserID == "" {
		return nil, ErrInvalidUserID
	}
	return s.repo.GetByUserID(ctx, filter)
}

// MarkAsRead marks a single notification as read, enforcing that the notification belongs
// to the authenticated user (ownership enforced at the DB layer via user_id predicate).
func (s *Service) MarkAsRead(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	if userID == "" {
		return nil, ErrInvalidUserID
	}
	if notificationID == "" {
		return nil, errors.New("invalid notification_id")
	}
	return s.repo.MarkAsRead(ctx, userID, notificationID)
}

// MarkAllAsRead marks all unread notifications as read for a user.
// Returns the number of rows updated.
func (s *Service) MarkAllAsRead(ctx context.Context, userID string) (int32, error) {
	if userID == "" {
		return 0, ErrInvalidUserID
	}
	return s.repo.MarkAllAsRead(ctx, userID)
}

// GetUnreadCount returns the count of unread notifications for a user.
// Used by the gRPC handler to populate badge counts.
func (s *Service) GetUnreadCount(ctx context.Context, userID string) (int32, error) {
	if userID == "" {
		return 0, ErrInvalidUserID
	}
	return s.repo.GetUnreadCount(ctx, userID)
}
