import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useNotificationStore } from './notificationStore'
import { notificationsApi } from '../api/notifications'
import { wsService } from '../api/ws'
import { useAuthStore } from './authStore'

vi.mock('../api/notifications', () => ({
  notificationsApi: {
    getNotifications: vi.fn(),
    getUnreadCount: vi.fn(),
    markAsRead: vi.fn(),
    markAllAsRead: vi.fn(),
  },
}))

describe('Notification Synchronization & Authoritative Unread Count (Phase B)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useNotificationStore.getState().resetToDefault()
    useAuthStore.setState({
      user: { userId: '00000000-0000-0000-0000-000000000001', email: 'test@trade.com', username: 'trader1' },
      isAuthenticated: true,
      accessToken: 'token',
      refreshToken: 'refresh',
    })
  })

  it('fetchUnreadCount retrieves authoritative count from backend endpoint', async () => {
    vi.mocked(notificationsApi.getUnreadCount).mockResolvedValueOnce({ unreadCount: 42 })

    await useNotificationStore.getState().fetchUnreadCount()

    expect(notificationsApi.getUnreadCount).toHaveBeenCalledTimes(1)
    expect(useNotificationStore.getState().unreadCount).toBe(42)
  })

  it('loadNotifications adopts authoritative unread count even when list is paginated', async () => {
    // Return only 2 in-memory notifications, but authoritative server unreadCount = 35
    vi.mocked(notificationsApi.getNotifications).mockResolvedValueOnce({
      notifications: [
        { id: 'n1', type: 'TRADE_FILL', title: 'Buy 1', message: 'Filled', isRead: false, createdAt: '2026-10-11T10:00:00Z' },
        { id: 'n2', type: 'TRADE_FILL', title: 'Buy 2', message: 'Filled', isRead: false, createdAt: '2026-10-11T10:05:00Z' },
      ],
      nextCursorId: 'n2',
      nextCursorTime: '2026-10-11T10:05:00Z',
      hasMore: true,
    })
    vi.mocked(notificationsApi.getUnreadCount).mockResolvedValueOnce({ unreadCount: 35 })

    await useNotificationStore.getState().loadNotifications(true)

    const state = useNotificationStore.getState()
    expect(state.notifications).toHaveLength(2)
    // Authoritative count should be 35, NOT truncated to 2
    expect(state.unreadCount).toBe(35)
    expect(state.hasMore).toBe(true)
    expect(state.isDemoData).toBe(false)
  })

  it('incoming live WebSocket notification increments unreadCount by 1 without clobbering to in-memory count', () => {
    let wsHandler: ((data: unknown) => void) | undefined
    vi.spyOn(wsService, 'subscribe').mockImplementation((_channel, handler) => {
      wsHandler = handler as (data: unknown) => void
      return () => {}
    })

    // Pre-seed state with authoritative count = 35, and 2 loaded notifications
    useNotificationStore.setState({
      notifications: [
        { id: 'n1', category: 'trading', iconType: 'buy', title: 'Buy 1', description: 'Filled', isRead: false, timestamp: '2026-10-11T10:00:00Z' },
        { id: 'n2', category: 'trading', iconType: 'buy', title: 'Buy 2', description: 'Filled', isRead: false, timestamp: '2026-10-11T10:05:00Z' },
      ],
      unreadCount: 35,
    })

    const unsub = useNotificationStore.getState().subscribeToWs()
    expect(wsHandler).toBeDefined()

    // Receive incoming new unread trade notification over WS
    wsHandler!({
      id: 'n3',
      type: 'TRADE_FILL',
      title: 'Buy 3',
      message: 'New trade executed',
      is_read: false,
      created_at: '2026-10-11T10:10:00Z',
    })

    const state = useNotificationStore.getState()
    expect(state.notifications).toHaveLength(3)
    expect(state.notifications[0].id).toBe('n3')
    // Crucial: unreadCount should be 35 + 1 = 36, NOT 3 (the in-memory slice length)
    expect(state.unreadCount).toBe(36)

    unsub()
  })

  it('markAsRead decrements unreadCount by 1 and reverts on network failure', async () => {
    useNotificationStore.setState({
      notifications: [
        { id: 'n1', category: 'trading', iconType: 'buy', title: 'Buy 1', description: 'Filled', isRead: false, timestamp: '2026-10-11T10:00:00Z' },
      ],
      unreadCount: 36,
    })

    vi.mocked(notificationsApi.markAsRead).mockResolvedValueOnce({
      notificationId: 'n1',
      isRead: true,
      readAt: '2026-10-11T10:15:00Z',
    })

    await useNotificationStore.getState().markAsRead('n1')

    let state = useNotificationStore.getState()
    expect(state.notifications[0].isRead).toBe(true)
    // Decrements from 36 to 35
    expect(state.unreadCount).toBe(35)

    // Now test failure rollback
    useNotificationStore.setState({
      notifications: [
        { id: 'n2', category: 'trading', iconType: 'buy', title: 'Buy 2', description: 'Filled', isRead: false, timestamp: '2026-10-11T10:00:00Z' },
      ],
      unreadCount: 35,
    })

    vi.mocked(notificationsApi.markAsRead).mockRejectedValueOnce(new Error('Network offline'))

    await useNotificationStore.getState().markAsRead('n2')

    state = useNotificationStore.getState()
    // Reverted back to false and count preserved at 35
    expect(state.notifications[0].isRead).toBe(false)
    expect(state.unreadCount).toBe(35)
  })

  it('markAllAsRead sets unreadCount to 0 and marks all in-memory items read', async () => {
    useNotificationStore.setState({
      notifications: [
        { id: 'n1', category: 'trading', iconType: 'buy', title: 'Buy 1', description: 'Filled', isRead: false, timestamp: '2026-10-11T10:00:00Z' },
        { id: 'n2', category: 'trading', iconType: 'buy', title: 'Buy 2', description: 'Filled', isRead: false, timestamp: '2026-10-11T10:05:00Z' },
      ],
      unreadCount: 35,
    })

    vi.mocked(notificationsApi.markAllAsRead).mockResolvedValueOnce({ markedCount: 35 })

    await useNotificationStore.getState().markAllAsRead()

    const state = useNotificationStore.getState()
    expect(state.unreadCount).toBe(0)
    expect(state.notifications.every((n) => n.isRead)).toBe(true)
  })

  it('reports errors honestly without fabricating mock notifications on failure', async () => {
    vi.mocked(notificationsApi.getNotifications).mockRejectedValueOnce(new Error('500 Internal Server Error'))

    await useNotificationStore.getState().loadNotifications(true)

    const state = useNotificationStore.getState()
    expect(state.error).toContain('500 Internal Server Error')
    expect(state.isDemoData).toBe(false)
    expect(state.notifications).toEqual([])
  })
})
