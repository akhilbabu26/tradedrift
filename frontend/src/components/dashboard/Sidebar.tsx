import { useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import {
  LayoutDashboard, BarChart2, ArrowLeftRight, ClipboardList,
  PieChart, Wallet, Star, TrendingUp, Bell, Settings,
  Gem, LogOut, X,
} from 'lucide-react'
import { useAuthStore } from '../../store/authStore'
import { authApi } from '../../api/auth'

const NAV = [
  { to: '/dashboard',   label: 'Dashboard', icon: LayoutDashboard },
  { to: '/markets',     label: 'Markets',   icon: BarChart2 },
  { to: '/trade',       label: 'Trade',     icon: ArrowLeftRight },
  { to: '/orders',      label: 'Orders',    icon: ClipboardList },
  { to: '/portfolio',   label: 'Portfolio', icon: PieChart },
  { to: '/wallet',      label: 'Wallet',    icon: Wallet },
  { to: '/watchlist',   label: 'Watchlist', icon: Star },
  { to: '/analytics',   label: 'Analytics', icon: TrendingUp },
  { to: '/alerts',      label: 'Alerts',    icon: Bell },
  { to: '/settings',    label: 'Settings',  icon: Settings },
]

export default function Sidebar() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const logout = useAuthStore((s) => s.logout)

  const handleLogout = async () => {
    try { await authApi.logout() } catch { /* ignore */ }
    logout()
    navigate('/login')
  }

const [showProCard, setShowProCard] = useState(true)

  return (
    <nav className="w-64 bg-[#111318] border-r border-[#1f2229] flex flex-col flex-shrink-0 z-20">
      {/* Brand */}
      <div className="h-16 flex items-center px-5 gap-3 border-b border-[#1f2229]/50">
        <Link to="/">
          <img src="/logo.png" alt="TradeDrift" className="h-8 w-auto object-contain" />
        </Link>
      </div>

      {/* Nav links */}
      <div className="flex-1 overflow-y-auto py-4 px-2 space-y-0.5">
        {NAV.map(({ to, label, icon: Icon }) => {
          const active = pathname === to
          return (
            <Link
              key={to}
              to={to}
              className={`flex items-center gap-3 px-4 py-2.5 rounded-r-lg border-l-4 transition-all duration-150 text-sm font-medium ${
                active
                  ? 'bg-[#10b981]/10 text-[#10b981] border-[#10b981]'
                  : 'text-slate-400 hover:text-white hover:bg-[#1e2025] border-transparent'
              }`}
            >
              <Icon size={18} strokeWidth={active ? 2.5 : 1.8} />
              {label}
            </Link>
          )
        })}
      </div>

      {/* Pro upgrade card */}
      {showProCard && (
        <div className="p-4 mx-4 mb-3 bg-[#1e2025] rounded-xl border border-[#1f2229] relative group">
          <button
            onClick={() => setShowProCard(false)}
            className="absolute top-2.5 right-2.5 text-slate-500 hover:text-white transition-colors"
            title="Dismiss"
          >
            <X size={14} />
          </button>
          <div className="flex items-center gap-1.5 mb-1.5 pr-4">
            <span className="text-xs font-semibold text-white">Upgrade to Pro</span>
            <Gem size={14} className="text-[#10b981]" />
          </div>
          <p className="text-[11px] text-slate-400 mb-3 leading-relaxed">
            Unlock advanced charts, lower fees and more.
          </p>
          <button className="w-full bg-[#10b981] text-black font-bold text-xs py-2 rounded-lg hover:bg-[#0e9f6e] transition-colors shadow-[0_0_12px_rgba(16,185,129,0.35)]">
            Upgrade Now
          </button>
        </div>
      )}

      {/* Logout */}
      <button
        onClick={handleLogout}
        className="flex items-center gap-3 px-6 py-4 border-t border-[#1f2229] text-slate-500 hover:text-red-400 hover:bg-[#1e2025] transition-colors text-sm"
      >
        <LogOut size={16} />
        Sign Out
      </button>
    </nav>
  )
}
