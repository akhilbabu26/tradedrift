import { useNavigate } from 'react-router-dom'
import { Zap, Check, Settings, ChevronRight } from 'lucide-react'
import toast from 'react-hot-toast'
import { useNotificationStore } from '../../store/notificationStore'

export default function NotificationQuickActionsCard() {
  const navigate = useNavigate()
  const markAllAsRead = useNotificationStore((s) => s.markAllAsRead)

  const handleMarkAll = () => {
    markAllAsRead()
    toast.success('All notifications marked as read')
  }

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-4 sm:p-5">
      {/* Header */}
      <div className="flex items-center gap-2 mb-3.5">
        <Zap size={16} className="text-amber-400" />
        <h2 className="text-xs sm:text-sm font-bold text-[#f5f7fa] tracking-tight">
          Quick Actions
        </h2>
      </div>

      {/* Action List */}
      <div className="space-y-2">
        {/* Action 1: Mark all as read */}
        <button
          type="button"
          onClick={handleMarkAll}
          className="w-full flex items-center justify-between p-3 rounded-lg bg-[#0a0b0e] border border-[#1e2530] hover:border-slate-600 hover:bg-white/[0.02] transition-colors cursor-pointer text-left group"
        >
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-[#111318] border border-[#1e2530] flex items-center justify-center text-slate-300 group-hover:text-[#10b981] group-hover:border-[#10b981]/30 transition-colors flex-shrink-0">
              <Check size={14} />
            </div>
            <div>
              <p className="text-xs font-semibold text-[#f5f7fa] group-hover:text-[#10b981] transition-colors">
                Mark all as read
              </p>
              <p className="text-[11px] text-slate-400 mt-0.5">
                Clear all unread notifications
              </p>
            </div>
          </div>
          <ChevronRight size={15} className="text-slate-500 group-hover:text-slate-300 group-hover:translate-x-0.5 transition-all flex-shrink-0" />
        </button>

        {/* Action 2: Notification Settings */}
        <button
          type="button"
          onClick={() => navigate('/settings')}
          className="w-full flex items-center justify-between p-3 rounded-lg bg-[#0a0b0e] border border-[#1e2530] hover:border-slate-600 hover:bg-white/[0.02] transition-colors cursor-pointer text-left group"
        >
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-[#111318] border border-[#1e2530] flex items-center justify-center text-slate-300 group-hover:text-[#10b981] group-hover:border-[#10b981]/30 transition-colors flex-shrink-0">
              <Settings size={14} />
            </div>
            <div>
              <p className="text-xs font-semibold text-[#f5f7fa] group-hover:text-[#10b981] transition-colors">
                Notification Settings
              </p>
              <p className="text-[11px] text-slate-400 mt-0.5">
                Customize your preferences
              </p>
            </div>
          </div>
          <ChevronRight size={15} className="text-slate-500 group-hover:text-slate-300 group-hover:translate-x-0.5 transition-all flex-shrink-0" />
        </button>
      </div>
    </div>
  )
}
