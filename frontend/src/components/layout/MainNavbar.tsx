import { useState, useEffect, useRef } from 'react'
import { Link, useNavigate, useLocation } from 'react-router-dom'
import {
  LayoutDashboard,
  TrendingUp,
  BarChart2,
  Briefcase,
  Wallet,
  ClipboardList,
  LineChart,
  Search,
  Bell,
  ChevronDown,
  User,
  Settings,
  LogOut,
  X,
  ArrowRight,
} from 'lucide-react'
import { useAuthStore } from '../../store/authStore'
import { useNotificationStore, selectUnreadCount } from '../../store/notificationStore'
import { authApi } from '../../api/auth'
import { wsService, type ConnectionStatus } from '../../api/ws'
import StatusIndicator from '../dashboard/shared/StatusIndicator'

interface NavItem {
  to: string
  label: string
  icon: React.ElementType
}

const NAV_ITEMS: NavItem[] = [
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/trade', label: 'Trade', icon: TrendingUp },
  { to: '/markets', label: 'Markets', icon: BarChart2 },
  { to: '/portfolio', label: 'Portfolio', icon: Briefcase },
  { to: '/wallet', label: 'Wallet', icon: Wallet },
  { to: '/orders', label: 'Orders', icon: ClipboardList },
  { to: '/analytics', label: 'Analytics', icon: LineChart },
]

interface SearchableMarket {
  id: string
  pair: string
  base: string
  name: string
  symbol: string
  iconBg: string
  iconBorder: string
  iconColor: string
}

const SEARCHABLE_MARKETS: SearchableMarket[] = [
  {
    id: 'BTC-USDT',
    pair: 'BTC/USDT',
    base: 'BTC',
    name: 'Bitcoin',
    symbol: '₿',
    iconBg: 'bg-[#f7931a]/15',
    iconBorder: 'border-[#f7931a]/30',
    iconColor: 'text-[#f7931a]',
  },
  {
    id: 'ETH-USDT',
    pair: 'ETH/USDT',
    base: 'ETH',
    name: 'Ethereum',
    symbol: 'Ξ',
    iconBg: 'bg-[#627eea]/15',
    iconBorder: 'border-[#627eea]/30',
    iconColor: 'text-[#627eea]',
  },
  {
    id: 'SOL-USDT',
    pair: 'SOL/USDT',
    base: 'SOL',
    name: 'Solana',
    symbol: 'S',
    iconBg: 'bg-[#9945ff]/15',
    iconBorder: 'border-[#9945ff]/30',
    iconColor: 'text-[#9945ff]',
  },
]

/**
 * Shared horizontal navbar for all authenticated application pages.
 *
 * - Reuses the wsService singleton — no new WebSocket connection.
 * - Reads the authenticated user from useAuthStore — username is never hardcoded.
 * - Route-aware active state via useLocation().
 */
export default function MainNavbar() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { user, logout } = useAuthStore()

  const [wsStatus, setWsStatus] = useState<ConnectionStatus>('connecting')
  const [wsLatency, setWsLatency] = useState<number>(0)
  const [dropdownOpen, setDropdownOpen] = useState(false)
  const [searchValue, setSearchValue] = useState('')
  const [searchOpen, setSearchOpen] = useState(false)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLDivElement>(null)

  // ── Reuse existing wsService singleton — no new connection ──────────────
  useEffect(() => {
    const unsubStatus = wsService.onStatus((_connected, status) => setWsStatus(status))
    const unsubLatency = wsService.onLatency((ms) => setWsLatency(ms))
    return () => { unsubStatus(); unsubLatency() }
  }, [])

  // ── Close popovers on outside click ──────────────────────────────────────
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setDropdownOpen(false)
      }
      if (searchRef.current && !searchRef.current.contains(e.target as Node)) {
        setSearchOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const displayName = user?.username || ''
  const firstName = displayName.split(' ')[0] || ''
  const initials = displayName
    .split(' ')
    .map((n) => n[0])
    .join('')
    .substring(0, 2)
    .toUpperCase() || 'A'

  const handleLogout = async () => {
    setDropdownOpen(false)
    try { await authApi.logout() } catch { /* ignore */ }
    logout()
    navigate('/login')
  }

  const latencyLabel =
    wsStatus === 'connected' && wsLatency > 0 ? `${wsLatency}ms`
      : wsStatus === 'connected' ? '< 20ms'
        : '--'

  const unreadCount = useNotificationStore(selectUnreadCount)

  const query = searchValue.trim().toLowerCase()

  const filteredMarkets = SEARCHABLE_MARKETS.filter((m) =>
    !query ||
    m.pair.toLowerCase().includes(query) ||
    m.base.toLowerCase().includes(query) ||
    m.name.toLowerCase().includes(query) ||
    m.id.toLowerCase().includes(query)
  )

  const SEARCHABLE_PAGES: NavItem[] = [
    ...NAV_ITEMS,
    { to: '/settings', label: 'Settings', icon: Settings },
    { to: '/notifications', label: 'Notifications', icon: Bell },
  ]

  const filteredPages = query
    ? SEARCHABLE_PAGES.filter((item) =>
        item.label.toLowerCase().includes(query)
      )
    : []

  const handleSelectMarket = (marketId: string) => {
    setSearchOpen(false)
    setSearchValue('')
    navigate(`/trade?market=${marketId}`)
  }

  const handleSelectPage = (to: string) => {
    setSearchOpen(false)
    setSearchValue('')
    navigate(to)
  }

  const handleSearchKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      setSearchOpen(false)
    } else if (e.key === 'Enter') {
      if (filteredMarkets.length > 0) {
        handleSelectMarket(filteredMarkets[0].id)
      } else if (filteredPages.length > 0) {
        handleSelectPage(filteredPages[0].to)
      } else {
        setSearchOpen(false)
        navigate('/markets')
      }
    }
  }

  return (
    <header className="h-14 bg-[#111318] border-b border-[#1e2530] flex items-center px-4 gap-4 flex-shrink-0 z-40 select-none">
      {/* ── Logo ──────────────────────────────────────────────────────────── */}
      <Link
        to="/dashboard"
        className="flex items-center gap-2.5 flex-shrink-0 group"
        aria-label="TradeDrift home"
      >
        <div className="w-7 h-7 rounded-md bg-[#10b981] flex items-center justify-center flex-shrink-0">
          <span className="text-[#0a0b0e] font-black text-[11px] tracking-tighter leading-none">TD</span>
        </div>
        <div className="flex flex-col leading-none">
          <span className="text-sm font-bold text-[#f5f7fa] tracking-tight">TradeDrift</span>
          <span className="text-[9px] font-medium text-slate-500 tracking-widest uppercase">
            Simulate • Trade • Learn
          </span>
        </div>
      </Link>

      {/* ── Nav Links ─────────────────────────────────────────────────────── */}
      <nav className="hidden lg:flex items-center gap-0.5 ml-1 xl:ml-2 flex-shrink-0" aria-label="Main navigation">
        {NAV_ITEMS.map(({ to, label, icon: Icon }) => {
          const active = pathname === to || (to !== '/dashboard' && pathname.startsWith(to))
          return (
            <Link
              key={to}
              to={to}
              className={`flex items-center gap-1 xl:gap-1.5 px-2 xl:px-3 py-1.5 rounded-md text-xs font-medium transition-colors whitespace-nowrap ${active
                ? 'bg-[#10b981]/10 text-[#10b981] border border-[#10b981]/20'
                : 'text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 border border-transparent'
                }`}
              aria-current={active ? 'page' : undefined}
            >
              <Icon size={13} strokeWidth={active ? 2.2 : 1.8} />
              {label}
            </Link>
          )
        })}
      </nav>

      {/* ── Spacer ────────────────────────────────────────────────────────── */}
      <div className="flex-1 min-w-2" />

      {/* ── Right-Side Controls ───────────────────────────────────────────── */}
      <div className="flex items-center gap-2 xl:gap-3 flex-shrink-0">
        {/* ── Search ────────────────────────────────────────────────────────── */}
        <div className="hidden md:flex items-center relative flex-shrink-0" ref={searchRef}>
          <Search size={13} className="absolute left-2.5 text-slate-500 pointer-events-none" />
          <input
            type="text"
            value={searchValue}
            onChange={(e) => {
              setSearchValue(e.target.value)
              setSearchOpen(true)
            }}
            onFocus={() => setSearchOpen(true)}
            onKeyDown={handleSearchKeyDown}
            placeholder="Search markets..."
            aria-label="Search markets"
            className="w-32 xl:w-44 pl-8 pr-7 py-1.5 bg-[#0a0b0e] border border-[#1e2530] rounded-md text-xs text-slate-300 placeholder-slate-600 focus:outline-none focus:border-[#10b981]/50 transition-colors"
          />
          {searchValue && (
            <button
              type="button"
              onClick={() => {
                setSearchValue('')
                setSearchOpen(false)
              }}
              className="absolute right-2 text-slate-500 hover:text-slate-300 transition-colors p-0.5"
              aria-label="Clear search"
            >
              <X size={12} />
            </button>
          )}

          {/* Quick-Jump Dropdown */}
          {searchOpen && (
            <div
              className="absolute left-0 top-full mt-1.5 w-64 xl:w-72 bg-[#111318] border border-[#1e2530] rounded-lg shadow-2xl z-50 overflow-hidden"
              role="listbox"
            >
              {/* Markets section */}
              {filteredMarkets.length > 0 && (
                <div className="p-1.5">
                  <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-slate-500">
                    {query ? 'Matching Markets' : 'Popular Markets'}
                  </div>
                  {filteredMarkets.map((m) => (
                    <button
                      key={m.id}
                      type="button"
                      onClick={() => handleSelectMarket(m.id)}
                      className="w-full flex items-center justify-between px-2.5 py-1.5 rounded-md hover:bg-white/5 transition-colors group text-left cursor-pointer"
                    >
                      <div className="flex items-center gap-2">
                        <div className={`w-5 h-5 rounded-full ${m.iconBg} border ${m.iconBorder} flex items-center justify-center flex-shrink-0`}>
                          <span className={`text-[10px] font-bold ${m.iconColor}`}>{m.symbol}</span>
                        </div>
                        <div>
                          <div className="text-xs font-semibold text-[#f5f7fa] group-hover:text-[#10b981] transition-colors flex items-center gap-1.5">
                            {m.pair}
                            <span className="text-[10px] font-normal text-slate-500">{m.name}</span>
                          </div>
                        </div>
                      </div>
                      <span className="text-[11px] font-medium text-slate-500 group-hover:text-[#10b981] flex items-center gap-0.5">
                        Trade <ArrowRight size={10} />
                      </span>
                    </button>
                  ))}
                </div>
              )}

              {/* Pages section */}
              {filteredPages.length > 0 && (
                <div className="p-1.5 border-t border-[#1e2530]">
                  <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-slate-500">
                    Navigation Pages
                  </div>
                  {filteredPages.map((p) => {
                    const PageIcon = p.icon
                    return (
                      <button
                        key={p.to}
                        type="button"
                        onClick={() => handleSelectPage(p.to)}
                        className="w-full flex items-center justify-between px-2.5 py-1.5 rounded-md hover:bg-white/5 transition-colors group text-left cursor-pointer"
                      >
                        <div className="flex items-center gap-2">
                          <PageIcon size={13} className="text-slate-500 group-hover:text-[#10b981]" />
                          <span className="text-xs font-medium text-[#f5f7fa] group-hover:text-[#10b981] transition-colors">
                            {p.label}
                          </span>
                        </div>
                        <span className="text-[10px] text-slate-500">Jump</span>
                      </button>
                    )
                  })}
                </div>
              )}

              {/* No results */}
              {filteredMarkets.length === 0 && filteredPages.length === 0 && (
                <div className="px-3 py-4 text-center">
                  <p className="text-xs text-slate-400">No markets found for "{searchValue}"</p>
                  <button
                    type="button"
                    onClick={() => {
                      setSearchOpen(false)
                      navigate('/markets')
                    }}
                    className="mt-2 text-[11px] text-[#10b981] hover:underline"
                  >
                    View all in Markets Page →
                  </button>
                </div>
              )}

              {/* Footer hint */}
              <div className="px-2.5 py-1.5 bg-[#0a0b0e]/60 border-t border-[#1e2530] flex items-center justify-between text-[10px] text-slate-500">
                <span>Select to open in Trade</span>
                <span className="font-mono text-[9px] bg-white/5 px-1 py-0.5 rounded border border-[#1e2530]">ESC to close</span>
              </div>
            </div>
          )}
        </div>

        {/* ── LIVE indicator ────────────────────────────────────────────────── */}
        <div className="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-[#0a0b0e] border border-[#1e2530] flex-shrink-0">
          <StatusIndicator
            status={wsStatus === 'connected' ? 'live' : wsStatus === 'connecting' ? 'connecting' : 'offline'}
            showPing={wsStatus === 'connected'}
          />
          <span className="text-[11px] font-bold text-[#f5f7fa] tracking-wide">LIVE</span>
          <span className="text-[11px] font-mono text-slate-400">{latencyLabel}</span>
        </div>

        {/* ── Notification Bell ─────────────────────────────────────────────── */}
        <button
          type="button"
          onClick={() => navigate('/notifications')}
          aria-label="Notifications"
          className="relative p-1.5 rounded-md border border-[#1e2530] text-slate-400 hover:text-[#f5f7fa] hover:border-slate-600 transition-colors bg-[#0a0b0e] flex-shrink-0 cursor-pointer"
        >
          <Bell size={15} />
          {unreadCount > 0 && (
            <span className="absolute -top-0.5 -right-0.5 min-w-3.5 h-3.5 px-0.5 rounded-full bg-[#ef4444] text-white text-[8px] font-bold flex items-center justify-center">
              {unreadCount}
            </span>
          )}
        </button>

        {/* ── User Avatar + Dropdown ────────────────────────────────────────── */}
        <div className="relative flex-shrink-0" ref={dropdownRef}>
          <button
            type="button"
            onClick={() => setDropdownOpen((o) => !o)}
            aria-expanded={dropdownOpen}
            aria-haspopup="menu"
            aria-label="User menu"
            className="flex items-center gap-2 pl-1.5 pr-2 py-1 rounded-md bg-[#0a0b0e] border border-[#1e2530] hover:border-slate-600 transition-colors"
          >
            <div className="w-6 h-6 rounded-md bg-[#10b981]/20 border border-[#10b981]/30 text-[#10b981] font-bold text-[11px] flex items-center justify-center flex-shrink-0">
              {initials}
            </div>
            {firstName && (
              <span className="hidden md:block text-xs font-medium text-[#f5f7fa]">
                {firstName}
              </span>
            )}
            <ChevronDown
              size={13}
              className={`text-slate-500 transition-transform duration-200 ${dropdownOpen ? 'rotate-180' : ''}`}
            />
          </button>

          {/* Dropdown Menu */}
          {dropdownOpen && (
            <div
              role="menu"
              className="absolute right-0 top-full mt-1.5 w-44 bg-[#111318] border border-[#1e2530] rounded-lg shadow-xl z-50 overflow-hidden"
            >
              <div className="px-3 py-2.5 border-b border-[#1e2530]">
                <p className="text-xs font-semibold text-[#f5f7fa] truncate">{displayName || 'Trader'}</p>
                <p className="text-[11px] text-slate-500 truncate">{user?.email || ''}</p>
              </div>
              <div className="py-1">
                <button
                  role="menuitem"
                  onClick={() => { setDropdownOpen(false); navigate('/profile') }}
                  className={`w-full flex items-center gap-2.5 px-3 py-2 text-xs transition-colors ${
                    pathname === '/profile'
                      ? 'bg-[#10b981]/10 text-[#10b981] font-medium'
                      : 'text-slate-300 hover:text-[#f5f7fa] hover:bg-white/5'
                  }`}
                >
                  <User size={13} className={pathname === '/profile' ? 'text-[#10b981]' : 'text-slate-500'} />
                  Profile
                </button>
                <button
                  role="menuitem"
                  onClick={() => { setDropdownOpen(false); navigate('/settings') }}
                  className={`w-full flex items-center gap-2.5 px-3 py-2 text-xs transition-colors ${
                    pathname === '/settings'
                      ? 'bg-[#10b981]/10 text-[#10b981] font-medium'
                      : 'text-slate-300 hover:text-[#f5f7fa] hover:bg-white/5'
                  }`}
                >
                  <Settings size={13} className={pathname === '/settings' ? 'text-[#10b981]' : 'text-slate-500'} />
                  Settings
                </button>
              </div>
              <div className="border-t border-[#1e2530] py-1">
                <button
                  role="menuitem"
                  onClick={handleLogout}
                  className="w-full flex items-center gap-2.5 px-3 py-2 text-xs text-[#ef4444] hover:bg-[#ef4444]/8 transition-colors"
                >
                  <LogOut size={13} />
                  Logout
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </header>
  )
}
