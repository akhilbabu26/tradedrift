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
// Field names verified against backend docs. Backend returns snake_case.

export interface BackendNotification {
  id: string
  type: string       // e.g. "ORDER_FILLED", "ORDER_CANCELLED", "WELCOME", "SYSTEM"
  title: string
  message: string    // notification body text (backend field is "message")
  is_read: boolean
  created_at: string // RFC3339 timestamp
}

export interface NotificationsListResponse {
  notifications: BackendNotification[]
  next_cursor_time?: string  // RFC3339; present when more pages exist
  next_cursor_id?: string    // UUID; present when more pages exist
}

export interface GetNotificationsParams {
  limit?: number       // default: 20, max: 100
  cursor_time?: string // RFC3339 — from previous next_cursor_time
  cursor_id?: string   // UUID — from previous next_cursor_id
  type?: string        // filter by notification type
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
    // Backend may wrap in { data: ... }; handle both shapes
    const body = (res.data as unknown as { data?: NotificationsListResponse }) 
    return body?.data ?? res.data
  },

  /**
   * POST /api/v1/notifications/{id}/read
   * Marks a single notification as read.
   * Requires: Bearer JWT
   */
  markAsRead: async (id: string): Promise<void> => {
    await client.post(`/api/v1/notifications/${id}/read`)
  },

  /**
   * POST /api/v1/notifications/read-all
   * Marks all notifications as read for the authenticated user.
   * Requires: Bearer JWT
   */
  markAllAsRead: async (): Promise<void> => {
    await client.post('/api/v1/notifications/read-all')
  },
}
