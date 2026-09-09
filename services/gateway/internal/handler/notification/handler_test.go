package notification_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	notificationv1 "tradedrift/platform/api/gen/notification/v1"
	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/gateway/internal/handler/notification"
)

type mockNotificationClient struct {
	getNotificationsFunc func(ctx context.Context, in *notificationv1.GetNotificationsRequest, opts ...grpc.CallOption) (*notificationv1.GetNotificationsResponse, error)
	markAsReadFunc       func(ctx context.Context, in *notificationv1.MarkAsReadRequest, opts ...grpc.CallOption) (*notificationv1.MarkAsReadResponse, error)
	markAllAsReadFunc    func(ctx context.Context, in *notificationv1.MarkAllAsReadRequest, opts ...grpc.CallOption) (*notificationv1.MarkAllAsReadResponse, error)
	getUnreadCountFunc   func(ctx context.Context, in *notificationv1.GetUnreadCountRequest, opts ...grpc.CallOption) (*notificationv1.GetUnreadCountResponse, error)
}

func (m *mockNotificationClient) CreateNotification(ctx context.Context, in *notificationv1.CreateNotificationRequest, opts ...grpc.CallOption) (*notificationv1.CreateNotificationResponse, error) {
	return nil, nil
}

func (m *mockNotificationClient) GetNotifications(ctx context.Context, in *notificationv1.GetNotificationsRequest, opts ...grpc.CallOption) (*notificationv1.GetNotificationsResponse, error) {
	if m.getNotificationsFunc != nil {
		return m.getNotificationsFunc(ctx, in, opts...)
	}
	return &notificationv1.GetNotificationsResponse{}, nil
}

func (m *mockNotificationClient) MarkAsRead(ctx context.Context, in *notificationv1.MarkAsReadRequest, opts ...grpc.CallOption) (*notificationv1.MarkAsReadResponse, error) {
	if m.markAsReadFunc != nil {
		return m.markAsReadFunc(ctx, in, opts...)
	}
	return &notificationv1.MarkAsReadResponse{}, nil
}

func (m *mockNotificationClient) MarkAllAsRead(ctx context.Context, in *notificationv1.MarkAllAsReadRequest, opts ...grpc.CallOption) (*notificationv1.MarkAllAsReadResponse, error) {
	if m.markAllAsReadFunc != nil {
		return m.markAllAsReadFunc(ctx, in, opts...)
	}
	return &notificationv1.MarkAllAsReadResponse{}, nil
}

func (m *mockNotificationClient) GetUnreadCount(ctx context.Context, in *notificationv1.GetUnreadCountRequest, opts ...grpc.CallOption) (*notificationv1.GetUnreadCountResponse, error) {
	if m.getUnreadCountFunc != nil {
		return m.getUnreadCountFunc(ctx, in, opts...)
	}
	return &notificationv1.GetUnreadCountResponse{}, nil
}

func withUserContext(r *http.Request, userID string) *http.Request {
	claims := &platformjwt.Claims{UserID: userID}
	ctx := platformjwt.WithClaims(r.Context(), claims)
	return r.WithContext(ctx)
}

func TestHandler_ListNotifications(t *testing.T) {
	client := &mockNotificationClient{
		getNotificationsFunc: func(ctx context.Context, in *notificationv1.GetNotificationsRequest, opts ...grpc.CallOption) (*notificationv1.GetNotificationsResponse, error) {
			assert.Equal(t, "user-123", in.UserId)
			assert.Equal(t, int32(20), in.Limit)
			return &notificationv1.GetNotificationsResponse{
				Notifications: []*notificationv1.NotificationItem{
					{
						Id:        "notif-1",
						UserId:    "user-123",
						Title:     "Order Filled",
						Message:   "Filled BTC-USDT",
						Type:      "TRADE_FILL",
						IsRead:    false,
						CreatedAt: "2026-09-08T14:30:00Z",
					},
				},
				HasMore:     false,
				UnreadCount: 1,
			}, nil
		},
	}

	h := notification.NewHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	req = withUserContext(req, "user-123")
	rec := httptest.NewRecorder()

	h.ListNotifications(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp notification.GetNotificationsResponseDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.Notifications, 1)
	assert.Equal(t, "notif-1", resp.Notifications[0].ID)
	assert.Equal(t, int32(1), resp.UnreadCount)
}

func TestHandler_ListNotifications_Unauthorized(t *testing.T) {
	client := &mockNotificationClient{}
	h := notification.NewHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	rec := httptest.NewRecorder()

	h.ListNotifications(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandler_MarkAsRead(t *testing.T) {
	client := &mockNotificationClient{
		markAsReadFunc: func(ctx context.Context, in *notificationv1.MarkAsReadRequest, opts ...grpc.CallOption) (*notificationv1.MarkAsReadResponse, error) {
			assert.Equal(t, "user-123", in.UserId)
			assert.Equal(t, "notif-1", in.NotificationId)
			return &notificationv1.MarkAsReadResponse{
				NotificationId: "notif-1",
				IsRead:         true,
				ReadAt:         "2026-09-08T14:35:00Z",
			}, nil
		},
	}

	h := notification.NewHandler(client)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/notif-1/read", nil)
	req.SetPathValue("id", "notif-1")
	req = withUserContext(req, "user-123")
	rec := httptest.NewRecorder()

	h.MarkAsRead(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp notification.MarkAsReadResponseDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "notif-1", resp.NotificationID)
	assert.True(t, resp.IsRead)
}

func TestHandler_MarkAllAsRead(t *testing.T) {
	client := &mockNotificationClient{
		markAllAsReadFunc: func(ctx context.Context, in *notificationv1.MarkAllAsReadRequest, opts ...grpc.CallOption) (*notificationv1.MarkAllAsReadResponse, error) {
			assert.Equal(t, "user-123", in.UserId)
			return &notificationv1.MarkAllAsReadResponse{
				MarkedCount: 5,
			}, nil
		},
	}

	h := notification.NewHandler(client)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/read-all", nil)
	req = withUserContext(req, "user-123")
	rec := httptest.NewRecorder()

	h.MarkAllAsRead(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp notification.MarkAllAsReadResponseDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, int32(5), resp.MarkedCount)
}

func TestHandler_GetUnreadCount(t *testing.T) {
	client := &mockNotificationClient{
		getUnreadCountFunc: func(ctx context.Context, in *notificationv1.GetUnreadCountRequest, opts ...grpc.CallOption) (*notificationv1.GetUnreadCountResponse, error) {
			assert.Equal(t, "user-123", in.UserId)
			return &notificationv1.GetUnreadCountResponse{
				UnreadCount: 3,
			}, nil
		},
	}

	h := notification.NewHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
	req = withUserContext(req, "user-123")
	rec := httptest.NewRecorder()

	h.GetUnreadCount(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp notification.UnreadCountResponseDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, int32(3), resp.UnreadCount)
}
