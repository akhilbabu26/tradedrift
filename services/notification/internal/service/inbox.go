package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/notification/internal/metrics"
	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/repository"
)

// CreateNotification allows internal microservices (Auth, Admin, Wallet) to dispatch
// user alerts directly via gRPC. The notification is persisted and staged for real-time
// delivery through the Transactional Outbox → Redis pipeline.
//
// Idempotency:
//
//	If input.IdempotencyKey is provided, it is used as the sourceEventID in
//	processed_events. A retry with the same key returns the original notification
//	instead of creating a duplicate. This protects against lost gRPC responses and
//	caller retries.
//
//	Contract: An idempotency key must be unique within the calling service/operation,
//	and reusing the same key with different request parameters is invalid.
//
//	The idempotency_key must be a canonical UUID if provided.
func (s *Service) CreateNotification(ctx context.Context, input model.CreateNotificationInput) (*model.Notification, error) {
	if input.UserID == "" {
		return nil, ErrInvalidUserID
	}
	if err := validateUUID("user_id", input.UserID); err != nil {
		return nil, err
	}
	if input.Title == "" {
		return nil, ErrEmptyTitle
	}
	if input.Message == "" {
		return nil, ErrEmptyMessage
	}
	// Validate notification type against closed enum set
	switch input.Type {
	case "", model.TypeInfo, model.TypeTradeFill, model.TypeSystem, model.TypeAccount:
	default:
		return nil, fmt.Errorf("invalid notification type: %q (allowed: %s, %s, %s, %s)",
			input.Type, model.TypeInfo, model.TypeTradeFill, model.TypeSystem, model.TypeAccount)
	}
	// Validate reference_id (must be UUID when provided)
	if input.ReferenceID != "" {
		if err := validateUUID("reference_id", input.ReferenceID); err != nil {
			return nil, err
		}
	}
	// Validate reference_type against closed enum set
	if input.ReferenceType != "" {
		switch input.ReferenceType {
		case model.RefTypeTrade, model.RefTypeOrder, model.RefTypeDeposit:
		default:
			return nil, fmt.Errorf("invalid reference_type: %q (allowed: %s, %s, %s)",
				input.ReferenceType, model.RefTypeTrade, model.RefTypeOrder, model.RefTypeDeposit)
		}
	}
	// Validate idempotency key if provided — it is stored as UUID in processed_events.
	if input.IdempotencyKey != "" {
		if err := validateUUID("idempotency_key", input.IdempotencyKey); err != nil {
			return nil, err
		}
	}

	notifID, err := platformuuid.New()
	if err != nil {
		return nil, fmt.Errorf("generate notification ID: %w", err)
	}
	outboxID, err := platformuuid.New()
	if err != nil {
		return nil, fmt.Errorf("generate outbox ID: %w", err)
	}

	now := time.Now().UTC()
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
		// The notification's own ID is used as the event envelope ID for gRPC-created
		// notifications because there is no external Kafka event ID. This is safe because
		// idempotency is handled via processed_events.idempotency_key, not the envelope ID.
		EventID:        notifID,
		NotificationID: notifID,
		Type:           "notification.created",
		Channel:        "user:notifications",
		Timestamp:      now,
		Data:           notif,
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal notification envelope: %w", err)
	}

	outbox := &model.OutboxEvent{
		ID:            outboxID,
		EventType:     "NotificationCreated",
		Payload:       payload,
		TargetChannel: "user:notifications:" + input.UserID,
		CreatedAt:     now,
	}

	// Use idempotency_key as the sourceEventID when provided. CreateWithDedupTx stores
	// both the key and the notification_id in processed_events atomically.
	sourceEventID := input.IdempotencyKey
	if sourceEventID == "" {
		// No idempotency key — use the notification ID as a one-time sentinel so the
		// processed_events slot is still filled (prevents any accidental re-use).
		sourceEventID = notifID
	}

	if err := s.repo.CreateWithDedupTx(ctx, notif, sourceEventID, outbox); err != nil {
		if errors.Is(err, repository.ErrAlreadyProcessed) && input.IdempotencyKey != "" {
			// Idempotent retry: look up the notification_id from processed_events,
			// then return the original notification rather than a duplicate.
			existing, lookupErr := s.lookupByIdempotencyKey(ctx, input.UserID, input.IdempotencyKey)
			if lookupErr != nil {
				// Lookup failure is unexpected — surface the original dedup error with context.
				return nil, fmt.Errorf("idempotent retry lookup failed (idempotency_key=%s): %w", input.IdempotencyKey, lookupErr)
			}
			s.log.Debug("CreateNotification idempotent retry: returning existing notification",
				zap.String("idempotency_key", input.IdempotencyKey),
				zap.String("notification_id", existing.ID),
			)
			return existing, nil
		}
		return nil, fmt.Errorf("create notification: %w", err)
	}

	metrics.NotificationsCreatedTotal.WithLabelValues(string(notifType)).Inc()
	return notif, nil
}

// lookupByIdempotencyKey retrieves the notification_id stored in processed_events for a
// given idempotency_key and then fetches and returns the full notification row.
func (s *Service) lookupByIdempotencyKey(ctx context.Context, userID, idempotencyKey string) (*model.Notification, error) {
	notifID, err := s.repo.GetNotificationIDByEventID(ctx, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("lookup notification_id from processed_events: %w", err)
	}
	return s.repo.GetNotificationByID(ctx, userID, notifID)
}

// GetNotifications retrieves a paginated inbox for the given user using keyset pagination.
// The repository returns limit+1 rows; the gRPC handler slices back to limit and sets
// has_more = true when the extra row is present.
func (s *Service) GetNotifications(ctx context.Context, filter model.PaginationFilter) ([]*model.Notification, error) {
	if filter.UserID == "" {
		return nil, ErrInvalidUserID
	}
	if err := validateUUID("user_id", filter.UserID); err != nil {
		return nil, err
	}
	// cursor_id must be a valid UUID when present (it is sent to PostgreSQL as uuid type).
	if filter.CursorID != "" {
		if err := validateUUID("cursor_id", filter.CursorID); err != nil {
			return nil, err
		}
	}
	return s.repo.GetByUserID(ctx, filter)
}

// MarkAsRead marks a single notification as read, enforcing that the notification belongs
// to the authenticated user (ownership enforced at the DB layer via user_id predicate).
func (s *Service) MarkAsRead(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	if userID == "" {
		return nil, ErrInvalidUserID
	}
	if err := validateUUID("user_id", userID); err != nil {
		return nil, err
	}
	if notificationID == "" {
		return nil, errors.New("invalid notification_id")
	}
	if err := validateUUID("notification_id", notificationID); err != nil {
		return nil, err
	}
	return s.repo.MarkAsRead(ctx, userID, notificationID)
}

// MarkAllAsRead marks all unread notifications as read for a user.
// Returns the number of rows updated.
func (s *Service) MarkAllAsRead(ctx context.Context, userID string) (int32, error) {
	if userID == "" {
		return 0, ErrInvalidUserID
	}
	if err := validateUUID("user_id", userID); err != nil {
		return 0, err
	}
	return s.repo.MarkAllAsRead(ctx, userID)
}

// GetUnreadCount returns the count of unread notifications for a user.
// Used by the gRPC handler to populate badge counts.
func (s *Service) GetUnreadCount(ctx context.Context, userID string) (int32, error) {
	if userID == "" {
		return 0, ErrInvalidUserID
	}
	if err := validateUUID("user_id", userID); err != nil {
		return 0, err
	}
	return s.repo.GetUnreadCount(ctx, userID)
}
