import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { NotificationItem, NotificationCategory } from '../types/notifications'
import { INITIAL_NOTIFICATIONS } from '../data/notificationsMock'

export interface NotificationState {
  notifications: NotificationItem[]
  activeTab: NotificationCategory
  setActiveTab: (tab: NotificationCategory) => void
  markAsRead: (id: string) => void
  toggleRead: (id: string) => void
  markAllAsRead: () => void
  deleteNotification: (id: string) => void
  resetToDefault: () => void
}

export const useNotificationStore = create<NotificationState>()(
  persist(
    (set) => ({
      notifications: INITIAL_NOTIFICATIONS,
      activeTab: 'all',

      setActiveTab: (activeTab) => set({ activeTab }),

      markAsRead: (id) =>
        set((state) => ({
          notifications: state.notifications.map((n) =>
            n.id === id ? { ...n, isRead: true } : n
          ),
        })),

      toggleRead: (id) =>
        set((state) => ({
          notifications: state.notifications.map((n) =>
            n.id === id ? { ...n, isRead: !n.isRead } : n
          ),
        })),

      markAllAsRead: () =>
        set((state) => ({
          notifications: state.notifications.map((n) => ({ ...n, isRead: true })),
        })),

      deleteNotification: (id) =>
        set((state) => ({
          notifications: state.notifications.filter((n) => n.id !== id),
        })),

      resetToDefault: () =>
        set({ notifications: INITIAL_NOTIFICATIONS, activeTab: 'all' }),
    }),
    {
      name: 'tradedrift_notifications',
    }
  )
)

export const selectUnreadCount = (state: NotificationState) =>
  state.notifications.filter((n) => !n.isRead).length

export const selectCategoryUnreadCount = (
  state: NotificationState,
  category: NotificationCategory
) => {
  if (category === 'all') {
    return state.notifications.filter((n) => !n.isRead).length
  }
  return state.notifications.filter((n) => n.category === category && !n.isRead).length
}
