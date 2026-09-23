import { Bell, Check } from 'lucide-react'
import toast from 'react-hot-toast'
import { useNotificationStore } from '../../store/notificationStore'

export default function NotificationsHeader() {
  const markAllAsRead = useNotificationStore((s) => s.markAllAsRead)

  const handleMarkAll = () => {
    markAllAsRead()
    toast.success('All notifications marked as read')
  }

  return (
    <div className="flex flex-col md:flex-row md:items-center md:justify-between gap-4 mb-6">
      {/* Left: Icon & Titles */}
      <div className="flex items-center gap-3.5">
        <div className="w-10 h-10 rounded-xl bg-[#111318] border border-[#1e2530] flex items-center justify-center text-slate-300 flex-shrink-0 shadow-xs">
          <Bell size={18} />
        </div>
        <div>
          <h1 className="text-2xl font-bold text-[#f5f7fa] tracking-tight">Notifications</h1>
          <p className="text-xs text-slate-400 mt-0.5">
            Stay updated with your trading activity, account alerts, and important updates.
          </p>
        </div>
      </div>

      {/* Right: Quote & Action */}
      <div className="flex items-center gap-4 self-start md:self-center">
        <div className="hidden lg:block text-right">
          <p className="text-xs italic text-slate-400 leading-tight">
            &ldquo;Good information<br />
            leads to better decisions.&rdquo;
          </p>
        </div>

        <button
          type="button"
          onClick={handleMarkAll}
          className="flex items-center gap-2 px-3.5 py-2 rounded-lg bg-[#111318] hover:bg-white/5 border border-[#1e2530] hover:border-slate-600 text-xs font-medium text-[#f5f7fa] transition-colors cursor-pointer flex-shrink-0"
        >
          <Check size={14} className="text-slate-300" />
          Mark all as read
        </button>
      </div>
    </div>
  )
}
