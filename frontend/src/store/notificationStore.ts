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
import type { NotificationItem, NotificationCategory } from '../types/notifications'

// ── Type mapper: backend → UI ────────────────────────────────────────────────

function mapBackendNotification(n: BackendNotification): NotificationItem {
  // Map backend type string to UI icon type
  const type = (n.type || '').toUpperCase()
  const title = (n.title || '').toUpperCase()
  const msg = (n.message || '').toUpperCase()
  let iconType: NotificationItem['iconType'] = 'partial_fill'
  let category: NotificationItem['category'] = 'system'

  const isBuy = type.includes('BUY') || title.includes('BUY') || msg.includes('BUY')
  const isSell = type.includes('SELL') || title.includes('SELL') || msg.includes('SELL')
  const isTrade = type.includes('TRADE') || type.includes('FILL') || title.includes('TRADE') || title.includes('FILLED')

  if (isTrade || isBuy || isSell) {
    category = 'trading'
    iconType = isBuy ? 'buy' : isSell ? 'sell' : 'partial_fill'
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
  } else if (type.includes('MARKET') || type.includes('HALT')) {
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
  /** Authoritative unread count from GET /api/v1/notifications/unread-count, adjusted reactively */
  unreadCount: number
  /** Cursor for next page, null when no more pages */
  nextCursorTime: string | null
  nextCursorId: string | null
  hasMore: boolean

  setActiveTab: (tab: NotificationCategory) => void
  fetchUnreadCount: () => Promise<void>
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
let wsSubscriptionCount = 0

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

  fetchUnreadCount: async () => {
    try {
      const res = await notificationsApi.getUnreadCount()
      if (typeof res?.unreadCount === 'number') {
        set({ unreadCount: res.unreadCount })
      }
    } catch {
      // Ignore transient errors
    }
  },

  loadNotifications: async (reset = true) => {
    set({ loading: true, error: null })
    try {
      const [res, unreadRes] = await Promise.all([
        notificationsApi.getNotifications({ limit: 20 }),
        Promise.resolve()
          .then(() => notificationsApi.getUnreadCount?.())
          .catch(() => null),
      ])
      const mapped = (res.notifications || []).map(mapBackendNotification)
      const nextTime = res.nextCursorTime ?? res.next_cursor_time ?? null
      const nextId = res.nextCursorId ?? res.next_cursor_id ?? null
      const unread = unreadRes?.unreadCount !== undefined
        ? unreadRes.unreadCount
        : res.unreadCount !== undefined
          ? res.unreadCount
          : mapped.filter((n) => !n.isRead).length
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
      set({
        loading: false,
        error: msg,
        isDemoData: false,
        notifications: reset ? [] : get().notifications,
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
      const hasMorePages = res.hasMore !== undefined ? res.hasMore : !!(nextTime && nextId)

      set({
        notifications: combined,
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
    const target = get().notifications.find((n) => n.id === id)
    const wasUnread = target ? !target.isRead : false

    // Optimistic update
    set((state) => {
      const updated = state.notifications.map((n) =>
        n.id === id ? { ...n, isRead: true } : n
      )
      return {
        notifications: updated,
        unreadCount: wasUnread ? Math.max(0, state.unreadCount - 1) : state.unreadCount,
      }
    })
    try {
      await notificationsApi.markAsRead(id)
    } catch {
      // Revert optimistic update on failure
      set((state) => {
        const reverted = state.notifications.map((n) =>
          n.id === id ? { ...n, isRead: false } : n
        )
        return {
          notifications: reverted,
          unreadCount: wasUnread ? state.unreadCount + 1 : state.unreadCount,
        }
      })
    }
  },

  markAllAsRead: async () => {
    const prevNotifications = get().notifications
    const prevUnreadCount = get().unreadCount

    // Optimistic update
    set((state) => ({
      notifications: state.notifications.map((n) => ({ ...n, isRead: true })),
      unreadCount: 0,
    }))
    try {
      await notificationsApi.markAllAsRead()
    } catch {
      // Revert on failure
      set({
        notifications: prevNotifications,
        unreadCount: prevUnreadCount,
      })
    }
  },

  subscribeToWs: () => {
    wsSubscriptionCount++

    if (wsSubscriptionCount === 1) {
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
              unreadCount: item.isRead ? state.unreadCount : state.unreadCount + 1,
            }
          })
        } catch {
          // Ignore malformed WS payload
        }
      }

      wsUnsub = wsService.subscribe(channel, handler)
    }

    return () => {
      wsSubscriptionCount = Math.max(0, wsSubscriptionCount - 1)
      if (wsSubscriptionCount === 0 && wsUnsub) {
        wsUnsub()
        wsUnsub = null
      }
    }
  },

  resetToDefault: () =>
    set({
      notifications: [],
      unreadCount: 0,
      activeTab: 'all',
      isDemoData: false,
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

// Auto-resync notifications and authoritative unread count on WebSocket reconnection if logged in
wsService.onReconnect(() => {
  if (useAuthStore.getState().isAuthenticated) {
    useNotificationStore.getState().loadNotifications(true)
  }
})
