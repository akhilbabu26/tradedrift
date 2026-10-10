import { useState, useRef, useEffect, useMemo } from 'react'
import {
  TrendingUp,
  TrendingDown,
  BarChart2,
  Mail,
  ShieldCheck,
  FileText,
  AlertTriangle,
  Bell,
  MoreVertical,
  CheckCircle,
  BellOff,
  Loader2,
  RefreshCw,
  Calendar,
} from 'lucide-react'
import toast from 'react-hot-toast'
import { useNotificationStore } from '../../store/notificationStore'
import type { NotificationIconType, NotificationItem } from '../../types/notifications'
import { formatRelativeTime } from '../../utils/formatters'

function getDateGroup(timestamp: string): string {
  try {
    const then = new Date(timestamp).getTime()
    if (isNaN(then) || then <= 0) {
      if (timestamp.toLowerCase().includes('ago') || timestamp.toLowerCase().includes('just now')) {
        return 'Today'
      }
      return 'Recent'
    }

    const now = new Date()
    const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
    const itemDate = new Date(then)
    const startOfItemDay = new Date(itemDate.getFullYear(), itemDate.getMonth(), itemDate.getDate()).getTime()

    const dayDiff = Math.round((startOfToday - startOfItemDay) / (1000 * 60 * 60 * 24))

    if (dayDiff <= 0) return 'Today'
    if (dayDiff === 1) return 'Yesterday'
    if (dayDiff > 1 && dayDiff <= 7) return 'This Week'
    if (dayDiff > 7 && dayDiff <= 30) return 'Earlier this Month'
    return itemDate.toLocaleDateString('en-US', { month: 'short', year: 'numeric' })
  } catch {
    return 'Recent'
  }
}

function getNotificationIcon(iconType: NotificationIconType) {
  switch (iconType) {
    case 'buy':
      return (
        <div className="w-10 h-10 rounded-xl bg-[#10b981]/10 border border-[#10b981]/20 flex items-center justify-center text-[#10b981] flex-shrink-0">
          <TrendingUp size={18} />
        </div>
      )
    case 'sell':
      return (
        <div className="w-10 h-10 rounded-xl bg-[#ef4444]/10 border border-[#ef4444]/20 flex items-center justify-center text-[#ef4444] flex-shrink-0">
          <TrendingDown size={18} />
        </div>
      )
    case 'price_alert':
      return (
        <div className="w-10 h-10 rounded-xl bg-sky-500/10 border border-sky-500/20 flex items-center justify-center text-sky-400 flex-shrink-0">
          <BarChart2 size={18} />
        </div>
      )
    case 'welcome':
      return (
        <div className="w-10 h-10 rounded-xl bg-sky-500/10 border border-sky-500/20 flex items-center justify-center text-sky-400 flex-shrink-0">
          <Mail size={18} />
        </div>
      )
    case 'password':
      return (
        <div className="w-10 h-10 rounded-xl bg-[#10b981]/10 border border-[#10b981]/20 flex items-center justify-center text-[#10b981] flex-shrink-0">
          <ShieldCheck size={18} />
        </div>
      )
    case 'market':
      return (
        <div className="w-10 h-10 rounded-xl bg-sky-500/10 border border-sky-500/20 flex items-center justify-center text-sky-400 flex-shrink-0">
          <FileText size={18} />
        </div>
      )
    case 'risk':
      return (
        <div className="w-10 h-10 rounded-xl bg-amber-500/10 border border-amber-500/20 flex items-center justify-center text-amber-400 flex-shrink-0">
          <AlertTriangle size={18} />
        </div>
      )
    case 'partial_fill':
    default:
      return (
        <div className="w-10 h-10 rounded-xl bg-sky-500/10 border border-sky-500/20 flex items-center justify-center text-sky-400 flex-shrink-0">
          <Bell size={18} />
        </div>
      )
  }
}

export default function NotificationsList() {
  const notifications = useNotificationStore((s) => s.notifications)
  const activeTab = useNotificationStore((s) => s.activeTab)
  const setActiveTab = useNotificationStore((s) => s.setActiveTab)
  const markAsRead = useNotificationStore((s) => s.markAsRead)
  const loading = useNotificationStore((s) => s.loading)
  const error = useNotificationStore((s) => s.error)
  const hasMore = useNotificationStore((s) => s.hasMore)
  const loadMore = useNotificationStore((s) => s.loadMore)
  const isDemoData = useNotificationStore((s) => s.isDemoData)

  const [menuOpenId, setMenuOpenId] = useState<string | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleOutside = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpenId(null)
      }
    }
    document.addEventListener('mousedown', handleOutside)
    return () => document.removeEventListener('mousedown', handleOutside)
  }, [])

  const filtered = useMemo(() => {
    return notifications.filter((item) => {
      if (activeTab === 'all') return true
      return item.category === activeTab
    })
  }, [notifications, activeTab])

  const groupedNotifications = useMemo(() => {
    const groups: { [key: string]: NotificationItem[] } = {}
    const order: string[] = []

    for (const item of filtered) {
      const groupName = getDateGroup(item.timestamp)
      if (!groups[groupName]) {
        groups[groupName] = []
        order.push(groupName)
      }
      groups[groupName].push(item)
    }

    return order.map((name) => ({
      name,
      items: groups[name],
    }))
  }, [filtered])

  // Loading state
  if (loading && notifications.length === 0) {
    return (
      <div className="bg-[#111318] border border-[#1e2530] rounded-xl py-16 flex flex-col items-center gap-3">
        <Loader2 size={22} className="text-[#10b981] animate-spin" />
        <p className="text-xs text-slate-500">Loading notifications...</p>
      </div>
    )
  }

  // Error state
  if (error && notifications.length === 0) {
    return (
      <div className="bg-[#111318] border border-[#1e2530] rounded-xl py-16 flex flex-col items-center gap-3">
        <RefreshCw size={20} className="text-slate-500" />
        <p className="text-sm font-semibold text-[#f5f7fa]">Unable to load notifications</p>
        <p className="text-xs text-slate-500">{error}</p>
      </div>
    )
  }

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl overflow-hidden shadow-xs flex flex-col">
      {isDemoData && (
        <div className="px-4 py-2 bg-amber-500/10 border-b border-amber-500/20">
          <p className="text-xs text-amber-400">⚠ Demo data — backend connection unavailable</p>
        </div>
      )}

      {/* List Card Header Bar */}
      <div className="px-4 sm:px-5 py-3.5 bg-[#111318] border-b border-[#1e2530] flex items-center justify-between gap-3 select-none">
        <div className="flex items-center gap-2.5">
          <h2 className="text-xs sm:text-sm font-bold text-[#f5f7fa] tracking-tight">
            Activity Feed
          </h2>
          <span className="text-[10px] font-semibold px-2 py-0.5 rounded-full bg-[#10b981]/10 text-[#10b981] border border-[#10b981]/25">
            {filtered.length} {filtered.length === 1 ? 'item' : 'items'}
          </span>
          {activeTab !== 'all' && (
            <span className="text-[10px] font-medium text-slate-400 capitalize hidden sm:inline">
              ({activeTab})
            </span>
          )}
        </div>

        <div className="flex items-center gap-2 text-[11px] text-slate-400">
          <span className="w-1.5 h-1.5 rounded-full bg-[#10b981] animate-pulse" />
          <span className="hidden sm:inline">Real-time Stream</span>
        </div>
      </div>

      {filtered.length === 0 ? (
        <div className="py-16 px-6 text-center flex flex-col items-center justify-center">
          <div className="w-12 h-12 rounded-xl bg-[#0a0b0e] border border-[#1e2530] flex items-center justify-center text-slate-500 mb-3 shadow-inner">
            <BellOff size={20} className="text-slate-400" />
          </div>
          <h3 className="text-sm font-bold text-[#f5f7fa] tracking-tight">
            {activeTab === 'all' ? 'No notifications yet' : `No ${activeTab} notifications`}
          </h3>
          <p className="text-xs text-slate-400 mt-1 max-w-sm leading-relaxed">
            {activeTab === 'all'
              ? "You're all caught up! When you execute trades or account updates occur, they'll appear here live."
              : `You don't have any ${activeTab} notifications right now.`}
          </p>
          {activeTab !== 'all' && (
            <button
              type="button"
              onClick={() => setActiveTab('all')}
              className="mt-3.5 px-3 py-1.5 rounded-lg bg-[#0a0b0e] hover:bg-white/5 border border-[#1e2530] hover:border-slate-600 text-xs text-slate-300 font-medium transition-colors cursor-pointer"
            >
              View all notifications
            </button>
          )}
        </div>
      ) : (
        <div>
          {groupedNotifications.map((group) => (
            <div key={group.name} className="border-b border-[#1e2530]/60 last:border-b-0">
              {/* Sticky Date Group Header */}
              <div className="px-4 sm:px-5 py-2 bg-[#0d0f14]/85 border-b border-[#1e2530]/40 flex items-center justify-between text-[11px] font-semibold text-slate-400 uppercase tracking-wider select-none sticky top-0 z-10 backdrop-blur-xs">
                <span className="flex items-center gap-1.5 text-slate-300">
                  <Calendar size={12} className="text-[#10b981]" />
                  {group.name}
                </span>
                <span className="text-[10px] text-slate-500 font-normal lowercase">
                  {group.items.length} {group.items.length === 1 ? 'update' : 'updates'}
                </span>
              </div>

              {/* Group Items */}
              <div className="divide-y divide-[#1e2530]/40">
                {group.items.map((item) => {
                  const isMenuOpen = menuOpenId === item.id

                  return (
                    <div
                      key={item.id}
                      className="flex items-center gap-3 sm:gap-3.5 px-4 sm:px-5 py-3.5 hover:bg-white/[0.02] transition-colors group relative"
                    >
                      {/* Left Unread Indicator Dot */}
                      <div className="flex-shrink-0 flex items-center justify-center w-2.5">
                        {!item.isRead ? (
                          <span className="w-2 h-2 rounded-full bg-[#10b981] shadow-xs shadow-[#10b981]/50" />
                        ) : (
                          <span className="w-2 h-2 rounded-full bg-transparent" />
                        )}
                      </div>

                      {/* Category Icon */}
                      {getNotificationIcon(item.iconType)}

                      {/* Content */}
                      <div className="flex-1 min-w-0 pr-2">
                        <h3 className="text-xs sm:text-sm font-bold text-[#f5f7fa] tracking-tight">
                          {item.title}
                        </h3>
                        <p className="text-xs text-slate-400 mt-0.5 leading-relaxed break-words">
                          {item.description}
                        </p>
                      </div>

                      {/* Relative Timestamp */}
                      <div className="text-right flex-shrink-0">
                        <span className="text-xs text-slate-400 whitespace-nowrap">
                          {formatRelativeTime ? formatRelativeTime(item.timestamp) : item.timestamp}
                        </span>
                      </div>

                      {/* Status Dot */}
                      <div className="flex-shrink-0 flex items-center justify-center w-2">
                        <span
                          className={`w-2 h-2 rounded-full ${
                            !item.isRead ? 'bg-[#10b981]' : 'bg-slate-600'
                          }`}
                        />
                      </div>

                      {/* Three-Dot Menu Button & Dropdown */}
                      {!item.isRead && (
                        <div className="relative flex-shrink-0" ref={isMenuOpen ? menuRef : undefined}>
                          <button
                            type="button"
                            onClick={() => setMenuOpenId(isMenuOpen ? null : item.id)}
                            className="p-1 rounded text-slate-500 hover:text-slate-200 hover:bg-white/5 transition-colors cursor-pointer"
                            aria-label="More options"
                          >
                            <MoreVertical size={15} />
                          </button>

                          {isMenuOpen && (
                            <div className="absolute right-0 top-full mt-1 w-36 bg-[#111318] border border-[#1e2530] rounded-lg shadow-xl z-50 py-1 overflow-hidden">
                              <button
                                type="button"
                                onClick={() => {
                                  markAsRead(item.id)
                                  setMenuOpenId(null)
                                  toast.success('Marked as read')
                                }}
                                className="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-slate-300 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors text-left"
                              >
                                <CheckCircle size={13} className="text-[#10b981]" />
                                Mark as read
                              </button>
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            </div>
          ))}

          {/* End-of-feed closure indicator */}
          {!hasMore && (
            <div className="px-4 py-3 bg-[#0d0f14]/40 border-t border-[#1e2530]/40 flex items-center justify-center gap-2 text-xs text-slate-500 select-none">
              <CheckCircle size={13} className="text-[#10b981]/80" />
              <span>You're all caught up with your notifications</span>
            </div>
          )}
        </div>
      )}

      {/* Load More */}
      {hasMore && (
        <div className="px-4 py-3 border-t border-[#1e2530]">
          <button
            type="button"
            onClick={() => loadMore()}
            disabled={loading}
            className="w-full py-2 text-xs text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 rounded-lg transition-colors flex items-center justify-center gap-2 disabled:opacity-50"
          >
            {loading ? <Loader2 size={12} className="animate-spin" /> : null}
            Load more notifications
          </button>
        </div>
      )}
    </div>
  )
}
