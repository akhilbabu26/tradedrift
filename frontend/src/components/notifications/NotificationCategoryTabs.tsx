import { useNotificationStore } from '../../store/notificationStore'
import type { NotificationCategory } from '../../types/notifications'

export default function NotificationCategoryTabs() {
  const activeTab = useNotificationStore((s) => s.activeTab)
  const setActiveTab = useNotificationStore((s) => s.setActiveTab)
  const notifications = useNotificationStore((s) => s.notifications)

  const getUnreadCount = (cat: NotificationCategory) => {
    if (cat === 'all') {
      return notifications.filter((n) => !n.isRead).length
    }
    return notifications.filter((n) => n.category === cat && !n.isRead).length
  }

  const tabs: { id: NotificationCategory; label: string }[] = [
    { id: 'all', label: 'All' },
    { id: 'trading', label: 'Trading' },
    { id: 'account', label: 'Account' },
    { id: 'system', label: 'System' },
  ]

  return (
    <div className="flex items-center gap-2 mb-5 overflow-x-auto pb-1 scrollbar-none">
      {tabs.map((tab) => {
        const active = activeTab === tab.id
        const count = getUnreadCount(tab.id)

        return (
          <button
            key={tab.id}
            type="button"
            onClick={() => setActiveTab(tab.id)}
            className={`flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-all cursor-pointer whitespace-nowrap ${
              active
                ? 'bg-[#10b981]/10 border border-[#10b981]/40 text-[#10b981]'
                : 'bg-[#111318] border border-[#1e2530] text-slate-400 hover:text-[#f5f7fa] hover:border-slate-600'
            }`}
          >
            <span>{tab.label}</span>
            <span
              className={`text-[10px] px-1.5 py-0.2 rounded-full font-bold transition-colors ${
                active
                  ? 'bg-[#10b981] text-[#0a0b0e]'
                  : 'bg-[#0a0b0e] text-slate-400 border border-[#1e2530]'
              }`}
            >
              {count}
            </span>
          </button>
        )
      })}
    </div>
  )
}
