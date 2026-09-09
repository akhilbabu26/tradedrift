package notification

import (
	notificationv1 "tradedrift/platform/api/gen/notification/v1"
)

type NotificationItemDTO struct {
	ID            string `json:"id"`
	UserID        string `json:"userId"`
	Title         string `json:"title"`
	Message       string `json:"message"`
	Type          string `json:"type"`
	ReferenceID   string `json:"referenceId,omitempty"`
	ReferenceType string `json:"referenceType,omitempty"`
	IsRead        bool   `json:"isRead"`
	ReadAt        string `json:"readAt,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

type GetNotificationsResponseDTO struct {
	Notifications  []NotificationItemDTO `json:"notifications"`
	NextCursorTime string                `json:"nextCursorTime,omitempty"`
	NextCursorID   string                `json:"nextCursorId,omitempty"`
	HasMore        bool                  `json:"hasMore"`
	UnreadCount    int32                 `json:"unreadCount"`
}

type MarkAsReadResponseDTO struct {
	NotificationID string `json:"notificationId"`
	IsRead         bool   `json:"isRead"`
	ReadAt         string `json:"readAt,omitempty"`
}

type MarkAllAsReadResponseDTO struct {
	MarkedCount int32 `json:"markedCount"`
}

type UnreadCountResponseDTO struct {
	UnreadCount int32 `json:"unreadCount"`
}

func toNotificationDTO(item *notificationv1.NotificationItem) NotificationItemDTO {
	return NotificationItemDTO{
		ID:            item.GetId(),
		UserID:        item.GetUserId(),
		Title:         item.GetTitle(),
		Message:       item.GetMessage(),
		Type:          item.GetType(),
		ReferenceID:   item.GetReferenceId(),
		ReferenceType: item.GetReferenceType(),
		IsRead:        item.GetIsRead(),
		ReadAt:        item.GetReadAt(),
		CreatedAt:     item.GetCreatedAt(),
	}
}
