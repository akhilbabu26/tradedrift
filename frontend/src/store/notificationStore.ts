/**
 * src/store/notificationStore.ts
 *
 * Notification store — upgraded from static mock to real backend API.
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * Documented REST endpoints:
 *   GET  /api/v1/notifications          — paginated list (limit, cursor_time, cursor_id, type)
 *   POST /api/v1/notifications/{id}/read
 *   POST /api/v1/notifications/read-all
 *
 * WebSocket channel (requires auth frame):
 *   user:notifications
 *   Server broadcasts: { stream: "user:notifications", data: BackendNotification }
 *
 * IMPORTANT: No /unread-count REST endpoint is documented.
 * unreadCount is derived client-side from notifications where is_read === false.
 *
 * Data policy:
 *   - No persist middleware (real-time data must not be stale in localStorage)
 *   - No silent mock fallback for auth-related failures
 *   - isDemoData=true only when API fails and fallback is explicitly loaded
 */

import { create } from 'zustand'
import { notificationsApi, type BackendNotification } from '../api/notifications'
import { wsService, WsChannels } from '../api/ws'
import { useAuthStore } from './authStore'
import { extractApiError } from '../utils/apiError'
import { INITIAL_NOTIFICATIONS } from '../data/notificationsMock'
import type { NotificationItem, NotificationCategory } from '../types/notifications'

// ── Type mapper: backend → UI ────────────────────────────────────────────────

function mapBackendNotification(n: BackendNotification): NotificationItem {
  // Map backend type string to UI icon type
  const type = (n.type || '').toUpperCase()
  let iconType: NotificationItem['iconType'] = 'partial_fill'
  let category: NotificationItem['category'] = 'system'

  if (type.includes('ORDER_FILLED') || type.includes('BUY')) {
    iconType = 'buy'
    category = 'trading'
  } else if (type.includes('SELL')) {
    iconType = 'sell'
    category = 'trading'
  } else if (type.includes('ORDER_CANCELLED') || type.includes('ORDER')) {
    iconType = 'partial_fill'
    category = 'trading'
  } else if (type.includes('PRICE') || type.includes('ALERT')) {
    iconType = 'price_alert'
    category = 'trading'
  } else if (type.includes('WELCOME') || type.includes('VERIFY')) {
    iconType = 'welcome'
    category = 'account'
  } else if (type.includes('PASSWORD') || type.includes('LOGIN') || type.includes('SECURITY')) {
    iconType = 'password'
    category = 'account'
  } else if (type.includes('MARKET') || type.includes('SYSTEM') || type.includes('HALT')) {
    iconType = 'market'
    category = 'system'
  } else if (type.includes('RISK')) {
    iconType = 'risk'
    category = 'system'
  }

  return {
    id: n.id,
    category,
    title: n.title || 'Notification',
    description: n.message || '',
    timestamp: n.createdAt ?? n.created_at ?? new Date().toISOString(),
    isRead: n.isRead ?? n.is_read ?? false,
    iconType,
  }
}

// ── Store interface ──────────────────────────────────────────────────────────

export interface NotificationState {
  notifications: NotificationItem[]
  activeTab: NotificationCategory
  loading: boolean
  error: string | null
  isDemoData: boolean
  /** Derived client-side: count of notifications where isRead === false */
  unreadCount: number
  /** Cursor for next page, null when no more pages */
  nextCursorTime: string | null
  nextCursorId: string | null
  hasMore: boolean

  setActiveTab: (tab: NotificationCategory) => void
  loadNotifications: (reset?: boolean) => Promise<void>
  loadMore: () => Promise<void>
  markAsRead: (id: string) => Promise<void>
  markAllAsRead: () => Promise<void>
  /** Subscribe to user:notifications WS channel */
  subscribeToWs: () => () => void
  resetToDefault: () => void
}

// ── Store ────────────────────────────────────────────────────────────────────

let wsUnsub: (() => void) | null = null

export const useNotificationStore = create<NotificationState>((set, get) => ({
  notifications: [],
  activeTab: 'all',
  loading: false,
  error: null,
  isDemoData: false,
  unreadCount: 0,
  nextCursorTime: null,
  nextCursorId: null,
  hasMore: false,

  setActiveTab: (activeTab) => set({ activeTab }),

  loadNotifications: async (reset = true) => {
    set({ loading: true, error: null })
    try {
      const res = await notificationsApi.getNotifications({ limit: 20 })
      const mapped = (res.notifications || []).map(mapBackendNotification)
      const nextTime = res.nextCursorTime ?? res.next_cursor_time ?? null
      const nextId = res.nextCursorId ?? res.next_cursor_id ?? null
      const unread = res.unreadCount !== undefined ? res.unreadCount : mapped.filter((n) => !n.isRead).length
      const hasMore = res.hasMore !== undefined ? res.hasMore : !!(nextTime && nextId)

      set({
        notifications: reset ? mapped : [...get().notifications, ...mapped],
        unreadCount: unread,
        nextCursorTime: nextTime,
        nextCursorId: nextId,
        hasMore,
        loading: false,
        isDemoData: false,
        error: null,
      })
    } catch (err) {
      const msg = extractApiError(err)
      // Only fall back to demo data on network errors, not auth errors
      const items = INITIAL_NOTIFICATIONS
      const unread = items.filter((n) => !n.isRead).length
      set({
        loading: false,
        error: msg,
        isDemoData: true,
        notifications: items,
        unreadCount: unread,
        hasMore: false,
      })
    }
  },

  loadMore: async () => {
    const { nextCursorTime, nextCursorId, hasMore, loading, notifications } = get()
    if (!hasMore || loading || !nextCursorTime || !nextCursorId) return

    set({ loading: true })
    try {
      const res = await notificationsApi.getNotifications({
        limit: 20,
        cursor_time: nextCursorTime,
        cursor_id: nextCursorId,
      })
      const mapped = (res.notifications || []).map(mapBackendNotification)
      const combined = [...notifications, ...mapped]
      const nextTime = res.nextCursorTime ?? res.next_cursor_time ?? null
      const nextId = res.nextCursorId ?? res.next_cursor_id ?? null
      const unread = res.unreadCount !== undefined ? res.unreadCount : combined.filter((n) => !n.isRead).length
      const hasMorePages = res.hasMore !== undefined ? res.hasMore : !!(nextTime && nextId)

      set({
        notifications: combined,
        unreadCount: unread,
        nextCursorTime: nextTime,
        nextCursorId: nextId,
        hasMore: hasMorePages,
        loading: false,
      })
    } catch (err) {
      set({ loading: false, error: extractApiError(err) })
    }
  },

  markAsRead: async (id: string) => {
    // Optimistic update
    set((state) => {
      const updated = state.notifications.map((n) =>
        n.id === id ? { ...n, isRead: true } : n
      )
      return { notifications: updated, unreadCount: updated.filter((n) => !n.isRead).length }
    })
    try {
      await notificationsApi.markAsRead(id)
    } catch {
      // Revert optimistic update on failure
      set((state) => {
        const reverted = state.notifications.map((n) =>
          n.id === id ? { ...n, isRead: false } : n
        )
        return { notifications: reverted, unreadCount: reverted.filter((n) => !n.isRead).length }
      })
    }
  },

  markAllAsRead: async () => {
    // Optimistic update
    set((state) => ({
      notifications: state.notifications.map((n) => ({ ...n, isRead: true })),
      unreadCount: 0,
    }))
    try {
      await notificationsApi.markAllAsRead()
    } catch {
      // Revert — reload from API
      await get().loadNotifications(true)
    }
  },

  subscribeToWs: () => {
    if (wsUnsub) {
      wsUnsub()
      wsUnsub = null
    }

    const user = useAuthStore.getState().user
    const channel = WsChannels.userNotifications(user?.userId)
    const handler = (data: unknown) => {
      try {
        const n = data as BackendNotification
        if (!n?.id) return
        const item = mapBackendNotification(n)
        set((state) => {
          // Avoid duplicates
          if (state.notifications.some((x) => x.id === item.id)) return state
          const updated = [item, ...state.notifications]
          return {
            notifications: updated,
            unreadCount: updated.filter((x) => !x.isRead).length,
          }
        })
      } catch {
        // Ignore malformed WS payload
      }
    }

    wsUnsub = wsService.subscribe(channel, handler)
    return () => {
      if (wsUnsub) {
        wsUnsub()
        wsUnsub = null
      }
    }
  },

  resetToDefault: () =>
    set({
      notifications: INITIAL_NOTIFICATIONS,
      unreadCount: INITIAL_NOTIFICATIONS.filter((n) => !n.isRead).length,
      activeTab: 'all',
      isDemoData: true,
      error: null,
      hasMore: false,
      nextCursorTime: null,
      nextCursorId: null,
    }),
}))

// ── Selectors ────────────────────────────────────────────────────────────────

export const selectUnreadCount = (state: NotificationState) => state.unreadCount

export const selectCategoryUnreadCount = (
  state: NotificationState,
  category: NotificationCategory
) => {
  if (category === 'all') return state.unreadCount
  return state.notifications.filter((n) => n.category === category && !n.isRead).length
}
