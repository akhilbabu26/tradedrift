/**
 * src/api/notifications.ts
 *
 * Notifications REST API service.
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * Documented endpoints (Gateway → Notification Service gRPC):
 *   GET  /api/v1/notifications              — paginated list
 *   POST /api/v1/notifications/{id}/read    — mark single as read
 *   POST /api/v1/notifications/read-all     — mark all as read
 *
 * NOTE: There is NO documented REST endpoint for unread-count.
 * Derive the count client-side from the notification list (is_read === false).
 *
 * Pagination: keyset cursor
 *   Query params: limit, cursor_time (RFC3339), cursor_id, type
 *   Response includes next_cursor_time and next_cursor_id when more pages exist.
 *
 * All endpoints require Bearer JWT authentication.
 */

import client from './client'

// ── Backend response DTOs ────────────────────────────────────────────────────
// Backend returns camelCase (with fallback support for snake_case).

export interface BackendNotification {
  id: string
  userId?: string
  type: string       // e.g. "TRADE_FILL", "SYSTEM", "ACCOUNT", "INFO", "ORDER_FILLED"
  title: string
  message: string    // notification body text
  referenceId?: string
  referenceType?: string
  isRead?: boolean
  is_read?: boolean
  readAt?: string
  read_at?: string
  createdAt?: string // RFC3339 timestamp
  created_at?: string
}

export interface NotificationsListResponse {
  notifications: BackendNotification[]
  nextCursorTime?: string  // RFC3339; present when more pages exist
  next_cursor_time?: string
  nextCursorId?: string    // UUID; present when more pages exist
  next_cursor_id?: string
  hasMore?: boolean
  unreadCount?: number
}

export interface GetNotificationsParams {
  limit?: number       // default: 20, max: 100
  cursor_time?: string // RFC3339 — from previous nextCursorTime
  cursor_id?: string   // UUID — from previous nextCursorId
  type?: string        // filter by notification type
}

export interface MarkAsReadResponse {
  notificationId: string
  isRead: boolean
  readAt: string
}

export interface MarkAllAsReadResponse {
  markedCount: number
}

export interface UnreadCountResponse {
  unreadCount: number
}

// ── API service ──────────────────────────────────────────────────────────────

export const notificationsApi = {
  /**
   * GET /api/v1/notifications
   * Returns paginated list of notifications for the authenticated user.
   * Requires: Bearer JWT
   */
  getNotifications: async (params?: GetNotificationsParams): Promise<NotificationsListResponse> => {
    const res = await client.get<NotificationsListResponse>('/api/v1/notifications', { params })
    const body = (res.data as unknown as { data?: NotificationsListResponse })
    return body?.data ?? res.data
  },

  /**
   * POST /api/v1/notifications/{id}/read
   * Marks a single notification as read.
   * Requires: Bearer JWT
   */
  markAsRead: async (id: string): Promise<MarkAsReadResponse> => {
    const res = await client.post<MarkAsReadResponse>(`/api/v1/notifications/${id}/read`)
    return res.data
  },

  /**
   * POST /api/v1/notifications/read-all
   * Marks all notifications as read for the authenticated user.
   * Requires: Bearer JWT
   */
  markAllAsRead: async (): Promise<MarkAllAsReadResponse> => {
    const res = await client.post<MarkAllAsReadResponse>('/api/v1/notifications/read-all')
    return res.data
  },

  /**
   * GET /api/v1/notifications/unread-count
   * Returns unread notifications count for badges.
   * Requires: Bearer JWT
   */
  getUnreadCount: async (): Promise<UnreadCountResponse> => {
    const res = await client.get<UnreadCountResponse>('/api/v1/notifications/unread-count')
    return res.data
  },
}
