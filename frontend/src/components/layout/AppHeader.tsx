import { useState, useEffect, useRef, memo } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import {
  Search,
  Bell,
  User,
  Settings,
  X,
  Menu,
  ArrowRight,
  TrendingUp,
  BarChart2,
  Briefcase,
  Wallet,
  ClipboardList,
  LineChart,
  LayoutDashboard,
} from 'lucide-react'
import { useNotificationStore, selectUnreadCount } from '../../store/notificationStore'
import { wsService, type ConnectionStatus } from '../../api/ws'
import StatusIndicator from '../dashboard/shared/StatusIndicator'
import { TradeDriftSymbol } from '../brand/TradeDriftLogo'

export interface AppHeaderProps {
  onOpenMobile?: () => void
}

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

const SEARCHABLE_PAGES = [
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/trade', label: 'Trade', icon: TrendingUp },
  { to: '/markets', label: 'Markets', icon: BarChart2 },
  { to: '/portfolio', label: 'Portfolio', icon: Briefcase },
  { to: '/wallet', label: 'Wallet', icon: Wallet },
  { to: '/orders', label: 'Orders', icon: ClipboardList },
  { to: '/analytics', label: 'Analytics', icon: LineChart },
  { to: '/settings', label: 'Settings', icon: Settings },
  { to: '/notifications', label: 'Notifications', icon: Bell },
  { to: '/profile', label: 'Profile', icon: User },
]

/**
 * TradeDrift Top Header
 *
 * Slim, cohesive top header providing global search, real-time WebSocket
 * connectivity status, notification bell, and user profile management.
 */
function AppHeader({ onOpenMobile }: AppHeaderProps) {
  const navigate = useNavigate()

  const [wsStatus, setWsStatus] = useState<ConnectionStatus>('connecting')
  const [wsLatency, setWsLatency] = useState<number>(0)
  const [searchValue, setSearchValue] = useState('')
  const [searchOpen, setSearchOpen] = useState(false)

  const searchRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const unsubStatus = wsService.onStatus((_connected, status) => setWsStatus(status))
    const unsubLatency = wsService.onLatency((ms) => setWsLatency(ms))
    return () => {
      unsubStatus()
      unsubLatency()
    }
  }, [])

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (searchRef.current && !searchRef.current.contains(e.target as Node)) {
        setSearchOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const latencyLabel =
    wsStatus === 'connected' && wsLatency > 0
      ? `${wsLatency}ms`
      : wsStatus === 'connected'
        ? '< 20ms'
        : '--'

  const unreadCount = useNotificationStore(selectUnreadCount)

  const query = searchValue.trim().toLowerCase()

  const filteredMarkets = SEARCHABLE_MARKETS.filter(
    (m) =>
      !query ||
      m.pair.toLowerCase().includes(query) ||
      m.base.toLowerCase().includes(query) ||
      m.name.toLowerCase().includes(query) ||
      m.id.toLowerCase().includes(query)
  )

  const filteredPages = query
    ? SEARCHABLE_PAGES.filter((item) => item.label.toLowerCase().includes(query))
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
    <header className="relative h-14 bg-[#08090B] border-b border-[#292C30] flex items-center justify-between px-3 sm:px-4 lg:px-5 flex-shrink-0 z-20 select-none w-full">
      {/* ── Left Area: Mobile Drawer Toggle & Left-Positioned Search Bar ────── */}
      <div className="flex items-center gap-3 flex-1 min-w-0">
        {/* Mobile menu trigger */}
        <button
          type="button"
          onClick={onOpenMobile}
          aria-label="Open navigation menu"
          className="lg:hidden p-1.5 rounded-lg bg-[#151719] border border-[#292C30] text-[#777E87] hover:text-white transition-colors cursor-pointer"
        >
          <Menu size={16} />
        </button>

        {/* Mobile Brand when sidebar is off-screen */}
        <div className="lg:hidden flex items-center gap-2">
          <Link to="/dashboard" className="flex items-center gap-2">
            <TradeDriftSymbol size={24} />
            <span className="text-base font-bold text-white tracking-tight">TradeDrift</span>
          </Link>
        </div>

        {/* ── Global Search (Left-aligned immediately after sidebar) ───────── */}
        <div className="flex items-center relative w-60 sm:w-80 md:w-96 max-w-full" ref={searchRef}>
          <Search size={14} className="absolute left-3 text-[#777E87] pointer-events-none" />
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
            className="w-full pl-8 pr-7 py-1.5 bg-[#151719] border border-[#292C30] rounded-xl text-xs text-[#F2F3F5] placeholder-[#777E87] shadow-[inset_0_1px_0_rgba(255,255,255,0.05),0_2px_4px_rgba(0,0,0,0.3)] hover:border-[#777E87]/60 focus:outline-none focus:border-[#D9DCE0]/60 transition-all"
          />
          {searchValue && (
            <button
              type="button"
              onClick={() => {
                setSearchValue('')
                setSearchOpen(false)
              }}
              className="absolute right-2.5 text-[#777E87] hover:text-[#F2F3F5] transition-colors p-0.5"
              aria-label="Clear search"
            >
              <X size={12} />
            </button>
          )}

          {/* Quick-Jump Dropdown */}
          {searchOpen && (
            <div
              className="absolute left-0 top-full mt-1.5 w-72 sm:w-80 bg-[#151719] border border-[#292C30] rounded-xl shadow-2xl z-50 overflow-hidden"
              role="listbox"
            >
              {/* Markets */}
              {filteredMarkets.length > 0 && (
                <div className="p-1.5">
                  <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-[#777E87]">
                    {query ? 'Matching Markets' : 'Popular Markets'}
                  </div>
                  {filteredMarkets.map((m) => (
                    <button
                      key={m.id}
                      type="button"
                      onClick={() => handleSelectMarket(m.id)}
                      className="w-full flex items-center justify-between px-2.5 py-1.5 rounded-lg hover:bg-white/5 transition-colors group text-left cursor-pointer"
                    >
                      <div className="flex items-center gap-2">
                        <div
                          className={`w-5 h-5 rounded-full ${m.iconBg} border ${m.iconBorder} flex items-center justify-center flex-shrink-0`}
                        >
                          <span className={`text-[10px] font-bold ${m.iconColor}`}>{m.symbol}</span>
                        </div>
                        <div>
                          <div className="text-xs font-semibold text-[#F2F3F5] group-hover:text-white transition-colors flex items-center gap-1.5">
                            {m.pair}
                            <span className="text-[10px] font-normal text-[#777E87]">{m.name}</span>
                          </div>
                        </div>
                      </div>
                      <span className="text-[11px] font-medium text-[#777E87] group-hover:text-white flex items-center gap-0.5">
                        Trade <ArrowRight size={10} />
                      </span>
                    </button>
                  ))}
                </div>
              )}

              {/* Pages */}
              {filteredPages.length > 0 && (
                <div className="p-1.5 border-t border-[#292C30]">
                  <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-[#777E87]">
                    Navigation Pages
                  </div>
                  {filteredPages.map((p) => {
                    const PageIcon = p.icon
                    return (
                      <button
                        key={p.to}
                        type="button"
                        onClick={() => handleSelectPage(p.to)}
                        className="w-full flex items-center justify-between px-2.5 py-1.5 rounded-lg hover:bg-white/5 transition-colors group text-left cursor-pointer"
                      >
                        <div className="flex items-center gap-2">
                          <PageIcon size={13} className="text-[#777E87] group-hover:text-white" />
                          <span className="text-xs font-medium text-[#F2F3F5] group-hover:text-white transition-colors">
                            {p.label}
                          </span>
                        </div>
                        <span className="text-[10px] text-[#777E87]">Jump</span>
                      </button>
                    )
                  })}
                </div>
              )}

              {filteredMarkets.length === 0 && filteredPages.length === 0 && (
                <div className="px-3 py-4 text-center">
                  <p className="text-xs text-[#A7ADB5]">No markets found for "{searchValue}"</p>
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {/* ── Right Area: LIVE Status & Notifications ───────────────────────── */}
      <div className="flex items-center gap-2.5 sm:gap-3 flex-shrink-0 ml-4">
        {/* ── LIVE Status ─────────────────────────────────────────────────── */}
        <div className="flex items-center gap-2 px-3 py-1.5 rounded-xl bg-[#151719] border border-[#292C30] shadow-[0_2px_4px_rgba(0,0,0,0.3)] flex-shrink-0">
          <StatusIndicator
            status={wsStatus === 'connected' ? 'live' : wsStatus === 'connecting' ? 'connecting' : 'offline'}
            showPing={wsStatus === 'connected'}
          />
          <span className="text-[11px] font-bold text-[#F2F3F5] tracking-wide">LIVE</span>
          <span className="hidden xl:inline text-[11px] font-mono text-[#777E87]">{latencyLabel}</span>
        </div>

        {/* ── Notifications ───────────────────────────────────────────────── */}
        <button
          type="button"
          onClick={() => navigate('/notifications')}
          aria-label="Notifications"
          className="relative p-2 rounded-xl bg-[#151719] border border-[#292C30] text-[#A7ADB5] hover:text-[#F2F3F5] hover:border-[#777E87]/60 shadow-[0_2px_4px_rgba(0,0,0,0.3)] transition-colors flex-shrink-0 cursor-pointer"
        >
          <Bell size={15} />
          {unreadCount > 0 && (
            <span className="absolute -top-1 -right-1 min-w-4 h-4 px-1 rounded-full bg-[#ef4444] text-white text-[9px] font-bold flex items-center justify-center shadow-md">
              {unreadCount}
            </span>
          )}
        </button>
      </div>
    </header>
  )
}

export default memo(AppHeader)
