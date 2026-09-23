import { useNavigate } from 'react-router-dom'
import { Settings, ChevronRight } from 'lucide-react'

export default function NotificationSettingsCard() {
  const navigate = useNavigate()

  return (
    <button
      type="button"
      onClick={() => navigate('/settings')}
      className="w-full bg-[#111318] border border-[#1e2530] rounded-xl p-4 sm:p-5 flex items-center justify-between gap-4 hover:border-slate-600 hover:bg-white/[0.02] transition-all cursor-pointer text-left group"
    >
      <div className="flex items-center gap-3">
        <div className="w-9 h-9 rounded-xl bg-[#0a0b0e] border border-[#1e2530] flex items-center justify-center text-slate-300 group-hover:text-[#10b981] group-hover:border-[#10b981]/30 transition-colors flex-shrink-0">
          <Settings size={16} />
        </div>
        <div>
          <h2 className="text-xs sm:text-sm font-bold text-[#f5f7fa] tracking-tight group-hover:text-[#10b981] transition-colors">
            Notification Settings
          </h2>
          <p className="text-xs text-slate-400 mt-0.5">
            Manage your notification preferences.
          </p>
        </div>
      </div>

      <ChevronRight size={16} className="text-slate-500 group-hover:text-slate-300 group-hover:translate-x-0.5 transition-all flex-shrink-0" />
    </button>
  )
}
