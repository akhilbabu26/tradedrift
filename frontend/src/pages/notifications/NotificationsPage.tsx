import { useEffect } from 'react'
import NotificationsHeader from '../../components/notifications/NotificationsHeader'
import NotificationCategoryTabs from '../../components/notifications/NotificationCategoryTabs'
import NotificationsList from '../../components/notifications/NotificationsList'
import NotificationSettingsCard from '../../components/notifications/NotificationSettingsCard'
import NotificationStatsCard from '../../components/notifications/NotificationStatsCard'
import NotificationQuickActionsCard from '../../components/notifications/NotificationQuickActionsCard'
import NotificationsFooter from '../../components/notifications/NotificationsFooter'
import { useNotificationStore } from '../../store/notificationStore'

/**
 * Notifications page — authenticated route at /notifications.
 *
 * Loads real notification data via GET /api/v1/notifications on mount.
 * Subscribes to user:notifications WebSocket channel for live push.
 * Layout provided by MainLayout (App.tsx) via <Outlet />.
 */
export default function NotificationsPage() {
  const loadNotifications = useNotificationStore((s) => s.loadNotifications)
  const subscribeToWs = useNotificationStore((s) => s.subscribeToWs)

  useEffect(() => {
    // Load initial notifications from backend
    loadNotifications(true)
    // Subscribe to live push via WebSocket
    const unsub = subscribeToWs()
    return () => unsub()
  }, [loadNotifications, subscribeToWs])

  return (
    <div className="flex-1 overflow-y-auto bg-[#0a0b0e]">
      <div className="max-w-[1400px] mx-auto px-4 sm:px-6 py-6 sm:py-8">
        <NotificationsHeader />
        <NotificationCategoryTabs />

        {/* 2-Column Responsive Grid */}
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
          {/* Left Column: Notifications Feed (7 / 8 cols) */}
          <div className="lg:col-span-7 xl:col-span-8">
            <NotificationsList />
          </div>

          {/* Right Column: Settings, Stats, Quick Actions (5 / 4 cols) */}
          <div className="lg:col-span-5 xl:col-span-4 space-y-4">
            <NotificationSettingsCard />
            <NotificationStatsCard />
            <NotificationQuickActionsCard />
          </div>
        </div>

        <NotificationsFooter />
      </div>
    </div>
  )
}
