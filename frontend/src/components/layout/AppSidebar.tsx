import type { ElementType } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import {
  LayoutDashboard,
  TrendingUp,
  BarChart2,
  Wallet,
  ClipboardList,
  History,
  Activity,
  Settings,
  MoreVertical,
  ChevronDown,
  Sun,
  Bell,
  LogOut,
} from 'lucide-react'
import { useAuthStore } from '../../store/authStore'
import { authApi } from '../../api/auth'

interface NavItem {
  to: string
  label: string
  icon: ElementType
}

const NAV_ITEMS: NavItem[] = [
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/trade', label: 'Trade', icon: TrendingUp },
  { to: '/markets', label: 'Markets', icon: BarChart2 },
  { to: '/wallet', label: 'Wallet', icon: Wallet },
  { to: '/orders', label: 'Orders', icon: ClipboardList },
  { to: '/history', label: 'History', icon: History },
  { to: '/analytics', label: 'Analytics', icon: Activity },
  { to: '/settings', label: 'Settings', icon: Settings },
]

export default function AppSidebar() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { user, logout } = useAuthStore()

  const displayName = user?.username || 'Akhil Babu'
  const initials = displayName
    .split(' ')
    .map((n) => n[0])
    .join('')
    .substring(0, 2)
    .toUpperCase() || 'AB'

  const handleLogout = async () => {
    try {
      await authApi.logout()
    } catch {
      // ignore
    }
    logout()
    navigate('/login')
  }

  return (
    <aside className="w-64 bg-[#07090e] border-r border-[#1b2230] flex flex-col justify-between flex-shrink-0 z-30 select-none h-screen">
      {/* ── Top Section: Logo & Nav Links ── */}
      <div className="flex flex-col flex-1 min-h-0">
        {/* Brand Logo Header */}
        <div className="h-16 flex items-center px-6 gap-3 border-b border-[#1b2230]/60">
          <Link to="/dashboard" className="flex items-center gap-3 group">
            <div className="w-9 h-9 rounded-xl bg-gradient-to-tr from-[#00e5ff] to-[#00b4d8] flex items-center justify-center shadow-[0_0_15px_rgba(0,229,255,0.35)] group-hover:scale-105 transition-transform">
              <span className="text-[#07090e] font-black text-sm tracking-tighter">TD</span>
            </div>
            <div className="flex flex-col">
              <span className="text-base font-bold text-white tracking-tight flex items-center gap-1 font-sans">
                TradeDrift
              </span>
            </div>
          </Link>
        </div>

        {/* Navigation Links */}
        <nav className="flex-1 overflow-y-auto px-3 py-4 space-y-1.5 custom-scrollbar">
          {NAV_ITEMS.map(({ to, label, icon: Icon }) => {
            const active = pathname === to || (to !== '/dashboard' && pathname.startsWith(to))
            return (
              <Link
                key={to}
                to={to}
                className={`flex items-center gap-3 px-3.5 py-2.5 rounded-xl text-xs font-semibold transition-all duration-150 relative group ${
                  active
                    ? 'bg-[#00e5ff]/10 text-[#00e5ff] border border-[#00e5ff]/30 shadow-[0_0_15px_rgba(0,229,255,0.12)]'
                    : 'text-slate-400 hover:text-slate-200 hover:bg-[#0e121b] border border-transparent'
                }`}
              >
                <Icon
                  size={17}
                  className={`transition-colors ${
                    active ? 'text-[#00e5ff]' : 'text-slate-500 group-hover:text-slate-300'
                  }`}
                  strokeWidth={active ? 2.3 : 1.8}
                />
                <span>{label}</span>
                {active && (
                  <div className="absolute right-2.5 w-1.5 h-1.5 rounded-full bg-[#00e5ff] shadow-[0_0_6px_#00e5ff]" />
                )}
              </Link>
            )
          })}
        </nav>
      </div>

      {/* ── Bottom Section: WebSocket Status + Profile + Controls ── */}
      <div className="p-3 space-y-2.5 border-t border-[#1b2230]/80 bg-[#07090e]">
        {/* WebSocket Real-time Status Card */}
        <div className="p-3 rounded-xl bg-[#0e121b] border border-[#1b2230] relative overflow-hidden group">
          {/* Header info */}
          <div className="flex items-center justify-between mb-1.5">
            <div className="flex items-center gap-2">
              <span className="relative flex h-2 w-2">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-[#00e676] opacity-75" />
                <span className="relative inline-flex rounded-full h-2 w-2 bg-[#00e676]" />
              </span>
              <span className="text-[11px] font-semibold text-white">WebSocket</span>
              <span className="text-[10px] text-slate-400">Connected</span>
            </div>
            <button
              type="button"
              aria-label="WebSocket Settings"
              className="text-slate-500 hover:text-slate-300 transition-colors"
            >
              <MoreVertical size={13} />
            </button>
          </div>

          {/* Live Latency & Waveform */}
          <div className="flex items-end justify-between mt-2">
            <div>
              <div className="font-mono text-lg font-bold text-[#00e676] leading-none">18ms</div>
              <div className="text-[9px] font-medium text-slate-400 mt-1 uppercase tracking-wider">
                Live Latency
              </div>
            </div>

            {/* Real-time green sparkline waveform */}
            <div className="w-20 h-6">
              <svg className="w-full h-full" viewBox="0 0 80 24" preserveAspectRatio="none">
                <defs>
                  <linearGradient id="wsGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#00e676" stopOpacity="0.4" />
                    <stop offset="100%" stopColor="#00e676" stopOpacity="0.0" />
                  </linearGradient>
                </defs>
                <path
                  d="M0,16 Q10,12 20,18 T40,10 T60,14 T80,8 L80,24 L0,24 Z"
                  fill="url(#wsGradient)"
                />
                <path
                  d="M0,16 Q10,12 20,18 T40,10 T60,14 T80,8"
                  fill="none"
                  stroke="#00e676"
                  strokeWidth="1.5"
                />
              </svg>
            </div>
          </div>
        </div>

        {/* User Profile Summary Card */}
        <div
          onClick={() => navigate('/settings')}
          className="flex items-center justify-between p-2 rounded-xl bg-[#0e121b] border border-[#1b2230] hover:border-[#00e5ff]/30 transition-colors cursor-pointer group"
        >
          <div className="flex items-center gap-2.5 min-w-0">
            <div className="w-8 h-8 rounded-lg bg-[#00e5ff]/10 border border-[#00e5ff]/30 text-[#00e5ff] font-mono font-bold text-xs flex items-center justify-center flex-shrink-0">
              {initials}
            </div>
            <div className="flex flex-col min-w-0">
              <span className="text-xs font-semibold text-white truncate group-hover:text-[#00e5ff] transition-colors leading-tight">
                {displayName}
              </span>
              <span className="text-[10px] text-slate-400 leading-tight">Pro Trader</span>
            </div>
          </div>
          <ChevronDown size={14} className="text-slate-500 group-hover:text-slate-300 flex-shrink-0" />
        </div>

        {/* Bottom Utility Bar (Theme, Alerts, Logout) */}
        <div className="flex items-center justify-between pt-1 px-1">
          <button
            type="button"
            aria-label="Toggle Theme"
            className="p-1.5 rounded-lg text-slate-500 hover:text-slate-300 hover:bg-[#0e121b] transition-colors"
          >
            <Sun size={15} />
          </button>
          <button
            type="button"
            aria-label="Alerts"
            className="p-1.5 rounded-lg text-slate-500 hover:text-slate-300 hover:bg-[#0e121b] transition-colors"
          >
            <Bell size={15} />
          </button>
          <button
            type="button"
            onClick={handleLogout}
            aria-label="Log Out"
            title="Log Out"
            className="p-1.5 rounded-lg text-slate-500 hover:text-[#ff3366] hover:bg-[#ff3366]/10 transition-colors"
          >
            <LogOut size={15} />
          </button>
        </div>
      </div>
    </aside>
  )
}
