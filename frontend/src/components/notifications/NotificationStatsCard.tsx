import { useState } from 'react'
import { BarChart3, ChevronDown, Mail, TrendingUp, ShieldCheck, FileText, ArrowUp } from 'lucide-react'
import { useNotificationStore } from '../../store/notificationStore'

export default function NotificationStatsCard() {
  const [range, setRange] = useState('Last 30 Days')
  const [dropdownOpen, setDropdownOpen] = useState(false)
  const notifications = useNotificationStore((s) => s.notifications)

  const ranges = ['Last 7 Days', 'Last 30 Days', 'Last 90 Days']

  const total = notifications.length
  const trading = notifications.filter((n) => n.category === 'trading').length
  const account = notifications.filter((n) => n.category === 'account').length
  const system = notifications.filter((n) => n.category === 'system').length

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-4 sm:p-5">
      {/* Header */}
      <div className="flex items-center justify-between gap-2 mb-4">
        <div className="flex items-center gap-2.5">
          <BarChart3 size={16} className="text-[#10b981]" />
          <h2 className="text-xs sm:text-sm font-bold text-[#f5f7fa] tracking-tight">
            Notification Stats
          </h2>
        </div>

        {/* Date Selector Dropdown */}
        <div className="relative">
          <button
            type="button"
            onClick={() => setDropdownOpen((o) => !o)}
            className="flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-[#0a0b0e] border border-[#1e2530] hover:border-slate-600 text-[11px] font-medium text-slate-300 transition-colors cursor-pointer"
          >
            <span>{range}</span>
            <ChevronDown size={12} className={`text-slate-500 transition-transform ${dropdownOpen ? 'rotate-180' : ''}`} />
          </button>

          {dropdownOpen && (
            <div className="absolute right-0 top-full mt-1 w-32 bg-[#111318] border border-[#1e2530] rounded-lg shadow-xl z-30 py-1 overflow-hidden">
              {ranges.map((r) => (
                <button
                  key={r}
                  type="button"
                  onClick={() => {
                    setRange(r)
                    setDropdownOpen(false)
                  }}
                  className={`w-full text-left px-3 py-1.5 text-xs transition-colors ${
                    range === r ? 'text-[#10b981] bg-[#10b981]/10' : 'text-slate-300 hover:bg-white/5'
                  }`}
                >
                  {r}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* 2x2 KPI Grid */}
      <div className="grid grid-cols-2 gap-3">
        {/* Total Notifications */}
        <div className="p-3 bg-[#0a0b0e] border border-[#1e2530] rounded-xl">
          <div className="flex items-center justify-between mb-2">
            <div className="w-7 h-7 rounded-lg bg-sky-500/10 border border-sky-500/20 text-sky-400 flex items-center justify-center">
              <Mail size={13} />
            </div>
          </div>
          <p className="text-[11px] text-slate-400">Total Notifications</p>
          <p className="text-xl font-bold text-[#f5f7fa] tracking-tight mt-0.5">
            {total}
          </p>
          <p className="text-[10px] font-medium text-[#10b981] flex items-center gap-0.5 mt-1">
            <ArrowUp size={10} />
            Active
          </p>
        </div>

        {/* Trading Alerts */}
        <div className="p-3 bg-[#0a0b0e] border border-[#1e2530] rounded-xl">
          <div className="flex items-center justify-between mb-2">
            <div className="w-7 h-7 rounded-lg bg-[#ef4444]/10 border border-[#ef4444]/20 text-[#ef4444] flex items-center justify-center">
              <TrendingUp size={13} />
            </div>
          </div>
          <p className="text-[11px] text-slate-400">Trading Alerts</p>
          <p className="text-xl font-bold text-[#f5f7fa] tracking-tight mt-0.5">
            {trading}
          </p>
          <p className="text-[10px] font-medium text-[#10b981] flex items-center gap-0.5 mt-1">
            <ArrowUp size={10} />
            Trading
          </p>
        </div>

        {/* Account Updates */}
        <div className="p-3 bg-[#0a0b0e] border border-[#1e2530] rounded-xl">
          <div className="flex items-center justify-between mb-2">
            <div className="w-7 h-7 rounded-lg bg-[#10b981]/10 border border-[#10b981]/20 text-[#10b981] flex items-center justify-center">
              <ShieldCheck size={13} />
            </div>
          </div>
          <p className="text-[11px] text-slate-400">Account Updates</p>
          <p className="text-xl font-bold text-[#f5f7fa] tracking-tight mt-0.5">
            {account}
          </p>
          <p className="text-[10px] font-medium text-[#10b981] flex items-center gap-0.5 mt-1">
            <ArrowUp size={10} />
            Account
          </p>
        </div>

        {/* System Messages */}
        <div className="p-3 bg-[#0a0b0e] border border-[#1e2530] rounded-xl">
          <div className="flex items-center justify-between mb-2">
            <div className="w-7 h-7 rounded-lg bg-sky-500/10 border border-sky-500/20 text-sky-400 flex items-center justify-center">
              <FileText size={13} />
            </div>
          </div>
          <p className="text-[11px] text-slate-400">System Messages</p>
          <p className="text-xl font-bold text-[#f5f7fa] tracking-tight mt-0.5">
            {system}
          </p>
          <p className="text-[10px] font-medium text-slate-500 flex items-center gap-0.5 mt-1">
            <ArrowUp size={10} />
            System
          </p>
        </div>
      </div>
    </div>
  )
}
