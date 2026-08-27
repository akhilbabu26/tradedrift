import { useState } from 'react'
import { Search, Bell, ChevronDown } from 'lucide-react'
import { Link, useNavigate } from 'react-router-dom'
import { useAuthStore } from '../../store/authStore'

interface TickerPill {
  symbol: string
  price: string
  change: string
  up: boolean
  iconBg: string
  iconColor: string
  symbolLetter: string
}

const LIVE_TICKERS: TickerPill[] = [
  {
    symbol: 'BTC/USDT',
    price: '$96,450.00',
    change: '+3.20%',
    up: true,
    iconBg: 'bg-[#f7931a]/15 text-[#f7931a] border-[#f7931a]/30',
    iconColor: 'text-[#f7931a]',
    symbolLetter: '₿',
  },
  {
    symbol: 'ETH/USDT',
    price: '$2,780.50',
    change: '+1.80%',
    up: true,
    iconBg: 'bg-[#627eea]/15 text-[#627eea] border-[#627eea]/30',
    iconColor: 'text-[#627eea]',
    symbolLetter: 'Ξ',
  },
  {
    symbol: 'SOL/USDT',
    price: '$188.20',
    change: '-0.60%',
    up: false,
    iconBg: 'bg-[#00e5ff]/15 text-[#00e5ff] border-[#00e5ff]/30',
    iconColor: 'text-[#00e5ff]',
    symbolLetter: 'S',
  },
]

export default function AppHeader() {
  const user = useAuthStore((s) => s.user)
  const navigate = useNavigate()
  const [searchQuery, setSearchQuery] = useState('')

  const displayName = user?.username || 'Akhil Babu'
  const initials = displayName
    .split(' ')
    .map((n) => n[0])
    .join('')
    .substring(0, 2)
    .toUpperCase() || 'AB'

  return (
    <header className="h-16 bg-[#07090e] border-b border-[#1b2230] px-6 flex items-center justify-between flex-shrink-0 z-30 select-none">
      {/* ── Left: Greeting / Title ── */}
      <div className="flex flex-col min-w-[200px]">
        <span className="text-[11px] font-medium text-slate-400 leading-tight">Welcome back,</span>
        <h1 className="text-base font-bold text-white tracking-tight flex items-center gap-1.5 leading-tight">
          {displayName} <span className="inline-block animate-pulse">👋</span>
        </h1>
      </div>

      {/* ── Center: Global Search Bar ── */}
      <div className="flex-1 max-w-md mx-6">
        <div className="relative flex items-center">
          <Search size={15} className="absolute left-3.5 text-slate-500 pointer-events-none" />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search markets, assets, pairs..."
            className="w-full pl-10 pr-20 py-2 bg-[#0e121b] border border-[#1b2230] rounded-xl text-xs text-white placeholder-slate-500 font-sans focus:outline-none focus:border-[#00e5ff]/60 focus:ring-1 focus:ring-[#00e5ff]/30 transition-all"
          />
          <div className="absolute right-2.5 flex items-center gap-1 px-1.5 py-0.5 rounded-md bg-[#1b2230]/60 border border-[#1b2230] text-[10px] font-mono text-slate-400">
            <span>Ctrl</span>
            <span>+</span>
            <span>K</span>
          </div>
        </div>
      </div>

      {/* ── Right: Live Tickers & User Controls ── */}
      <div className="flex items-center gap-4">
        {/* Live Market Tickers */}
        <div className="hidden xl:flex items-center gap-3">
          {LIVE_TICKERS.map((t) => (
            <Link
              key={t.symbol}
              to={`/trade?market=${t.symbol.replace('/', '-')}`}
              className="flex items-center gap-2 px-2.5 py-1.5 rounded-lg bg-[#0e121b] border border-[#1b2230] hover:border-[#00e5ff]/40 hover:bg-[#141a26] transition-all cursor-pointer group"
            >
              <span className={`w-5 h-5 rounded-full border flex items-center justify-center font-bold text-[10px] ${t.iconBg}`}>
                {t.symbolLetter}
              </span>
              <div className="flex flex-col text-right">
                <span className="text-[11px] font-semibold text-slate-300 group-hover:text-white transition-colors">
                  {t.symbol}
                </span>
                <div className="flex items-center gap-1 font-mono text-[10px]">
                  <span className="text-white font-medium">{t.price}</span>
                  <span className={t.up ? 'text-[#00e676]' : 'text-[#ff3366]'}>{t.change}</span>
                </div>
              </div>
            </Link>
          ))}
        </div>

        {/* Vertical Divider */}
        <div className="h-6 w-px bg-[#1b2230] hidden sm:block" />

        {/* Notification Bell */}
        <button
          type="button"
          aria-label="Notifications"
          className="relative p-2 rounded-xl bg-[#0e121b] border border-[#1b2230] text-slate-400 hover:text-white hover:border-slate-600 transition-colors"
        >
          <Bell size={16} />
          <span className="absolute -top-1 -right-1 w-4 h-4 rounded-full bg-[#ff3366] text-white text-[9px] font-bold font-mono flex items-center justify-center shadow-[0_0_8px_rgba(255,51,102,0.6)]">
            3
          </span>
        </button>

        {/* User Avatar Chip */}
        <div
          onClick={() => navigate('/settings')}
          className="flex items-center gap-2 pl-1 pr-2 py-1 rounded-xl bg-[#0e121b] border border-[#1b2230] hover:border-[#00e5ff]/40 transition-colors cursor-pointer group"
        >
          <div className="w-8 h-8 rounded-lg bg-[#00e5ff]/10 border border-[#00e5ff]/30 text-[#00e5ff] font-mono font-bold text-xs flex items-center justify-center shadow-[0_0_10px_rgba(0,229,255,0.15)]">
            {initials}
          </div>
          <ChevronDown size={14} className="text-slate-500 group-hover:text-slate-300 transition-colors" />
        </div>
      </div>
    </header>
  )
}
