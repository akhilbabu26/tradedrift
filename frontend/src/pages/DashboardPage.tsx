import { useEffect, useState } from 'react'
import { Search, Bell, Eye, EyeOff, MoreHorizontal, Info, CheckCircle, Wifi, Gauge, Clock, Maximize2 } from 'lucide-react'
import Sidebar from '../components/dashboard/Sidebar'
import { useAuthStore } from '../store/authStore'
import { walletApi, type Balance } from '../api/wallet'

// ─── Static mock data (replace with real API later) ──────────────────────────
const TICKERS = [
  { symbol: 'BTC/USDT', change: '+2.45%', up: true,  color: 'text-orange-400' },
  { symbol: 'ETH/USDT', change: '+1.32%', up: true,  color: 'text-blue-400' },
  { symbol: 'SOL/USDT', change: '+3.37%', up: true,  color: 'text-purple-400' },
  { symbol: 'BNB/USDT', change: '+0.89%', up: true,  color: 'text-yellow-400' },
]

const TOP_MARKETS = [
  { symbol: 'BTC/USDT', price: '33,909.80', change: '+2.45%', up: true,  vol: '1.24B',   dot: 'bg-orange-400' },
  { symbol: 'ETH/USDT', price: '2,117.93',  change: '+1.32%', up: true,  vol: '845.21M', dot: 'bg-blue-400' },
  { symbol: 'SOL/USDT', price: '45.88',     change: '+3.37%', up: true,  vol: '523.18M', dot: 'bg-purple-400' },
  { symbol: 'BNB/USDT', price: '312.45',    change: '+0.89%', up: true,  vol: '312.67M', dot: 'bg-yellow-400' },
  { symbol: 'XRP/USDT', price: '0.5287',    change: '-0.45%', up: false, vol: '256.91M', dot: 'bg-slate-400' },
]

const RECENT_ORDERS = [
  { type: 'Limit',  pair: 'BTC/USDT', side: 'Buy',  price: '33,500.00', status: 'Filled',    time: '2m ago' },
  { type: 'Limit',  pair: 'ETH/USDT', side: 'Sell', price: '2,150.00',  status: 'Filled',    time: '5m ago' },
  { type: 'Market', pair: 'SOL/USDT', side: 'Buy',  price: '45.50',     status: 'Filled',    time: '8m ago' },
  { type: 'Limit',  pair: 'BTC/USDT', side: 'Buy',  price: '33,200.00', status: 'Open',      time: '12m ago' },
  { type: 'Limit',  pair: 'ETH/USDT', side: 'Buy',  price: '2,100.00',  status: 'Cancelled', time: '15m ago' },
]

const ASSET_META: Record<string, { label: string; color: string; dot: string }> = {
  USDT: { label: 'Tether',   color: 'text-[#10b981]', dot: 'bg-[#10b981]' },
  BTC:  { label: 'Bitcoin',  color: 'text-orange-400', dot: 'bg-orange-400' },
  ETH:  { label: 'Ethereum', color: 'text-blue-400',   dot: 'bg-blue-400' },
  SOL:  { label: 'Solana',   color: 'text-purple-400', dot: 'bg-purple-400' },
}

// Status badge helper
function StatusBadge({ status }: { status: string }) {
  const map: Record<string, string> = {
    Filled:    'bg-[#10b981]/10 text-[#10b981]',
    Open:      'bg-blue-500/10 text-blue-400',
    Cancelled: 'bg-slate-500/10 text-slate-400',
  }
  return (
    <span className={`px-2 py-0.5 rounded text-[10px] font-medium ${map[status] ?? map.Cancelled}`}>
      {status}
    </span>
  )
}

// Sparkline SVG
function Sparkline({ up }: { up: boolean }) {
  const path = up
    ? 'M0,25 L10,20 L20,22 L30,15 L40,18 L50,5 L60,10'
    : 'M0,5 L10,10 L20,8 L30,15 L40,12 L50,25 L60,20'
  return (
    <svg width="60" height="30" viewBox="0 0 60 30">
      <path d={path} fill="none" stroke={up ? '#10b981' : '#ef4444'} strokeWidth="2" />
    </svg>
  )
}

// Donut chart
function DonutChart() {
  return (
    <div className="relative w-24 h-24">
      <svg className="w-full h-full -rotate-90" viewBox="0 0 36 36">
        <circle cx="18" cy="18" r="15.915" fill="transparent" stroke="#1f2229" strokeWidth="4" />
        <circle cx="18" cy="18" r="15.915" fill="transparent" stroke="#10b981" strokeWidth="4" strokeDasharray="50.2 100" strokeDashoffset="0" />
        <circle cx="18" cy="18" r="15.915" fill="transparent" stroke="#f59e0b" strokeWidth="4" strokeDasharray="35.8 100" strokeDashoffset="-50.2" />
        <circle cx="18" cy="18" r="15.915" fill="transparent" stroke="#3b82f6" strokeWidth="4" strokeDasharray="6.2 100" strokeDashoffset="-86" />
        <circle cx="18" cy="18" r="15.915" fill="transparent" stroke="#8b5cf6" strokeWidth="4" strokeDasharray="5.8 100" strokeDashoffset="-92.2" />
        <circle cx="18" cy="18" r="15.915" fill="transparent" stroke="#6b7280" strokeWidth="4" strokeDasharray="2 100" strokeDashoffset="-98" />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span className="text-[8px] text-slate-500">Total Value</span>
        <span className="text-[9px] text-white font-bold">$33,909.80</span>
      </div>
    </div>
  )
}

export default function DashboardPage() {
  const user = useAuthStore((s) => s.user)
  const [balances, setBalances] = useState<Balance[]>([])
  const [balanceVisible, setBalanceVisible] = useState(true)
  const [timeframe, setTimeframe] = useState('4H')

  useEffect(() => {
    walletApi.getAllBalances()
      .then(setBalances)
      .catch(() => { /* use placeholder data if API fails */ })
  }, [])

  // Compute total USD balance from wallets (fallback to static)
  const totalBalance = '$33,909.80'

  // Map fetched balances by asset for display
  const balanceMap: Record<string, Balance> = {}
  balances.forEach((b) => { balanceMap[b.asset] = b })

  const ASSETS = [
    { asset: 'USDT', amount: balanceMap['USDT']?.availableBalance ?? '33,909.80', usd: '$33,909.80', change: '+2.45%' },
    { asset: 'BTC',  amount: balanceMap['BTC']?.availableBalance  ?? '0.8452',    usd: '$28,662.45', change: '+1.32%' },
    { asset: 'ETH',  amount: balanceMap['ETH']?.availableBalance  ?? '1.3488',    usd: '$2,117.93',  change: '+3.21%' },
    { asset: 'SOL',  amount: balanceMap['SOL']?.availableBalance  ?? '45.88',     usd: '$2,102.34',  change: '+4.22%' },
  ]

  return (
    <div className="bg-[#0a0b0e] text-white h-screen w-screen overflow-hidden flex font-sans text-sm select-none">
      <Sidebar />

      {/* Main workspace */}
      <div className="flex-1 flex flex-col min-w-0">

        {/* ── Top header ── */}
        <header className="h-16 bg-[#111318]/80 backdrop-blur-md border-b border-[#1f2229] flex items-center justify-between px-6 flex-shrink-0">
          <div className="flex flex-col">
            <span className="font-bold text-white text-base">
              Welcome back, {user?.username ?? 'Trader'} 👋
            </span>
            <span className="text-[11px] text-slate-400">Ready to drift the markets today?</span>
          </div>

          {/* Ticker tape */}
          <div className="hidden lg:flex items-center gap-6">
            {TICKERS.map((t) => (
              <div key={t.symbol} className="flex items-center gap-1.5 text-xs font-mono">
                <span className={`w-2 h-2 rounded-full ${t.up ? 'bg-[#10b981]' : 'bg-red-500'}`} />
                <span className="text-slate-300">{t.symbol}</span>
                <span className={t.up ? 'text-[#10b981]' : 'text-red-400'}>{t.change}</span>
              </div>
            ))}
          </div>

          {/* Right actions */}
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-1.5 px-3 py-1 bg-[#1e2025] rounded-full border border-[#1f2229] text-xs">
              <span className="w-1.5 h-1.5 rounded-full bg-[#10b981] animate-pulse" />
              <span className="text-slate-400">Market: <span className="text-[#10b981]">Open</span></span>
            </div>
            <button className="w-9 h-9 flex items-center justify-center rounded-full bg-[#1e2025] border border-[#1f2229] text-slate-400 hover:text-white transition-colors">
              <Search size={16} />
            </button>
            <button className="w-9 h-9 flex items-center justify-center rounded-full bg-[#1e2025] border border-[#1f2229] text-slate-400 hover:text-white transition-colors">
              <Bell size={16} />
            </button>
            <div className="w-9 h-9 rounded-full bg-[#10b981]/20 border border-[#10b981] text-[#10b981] text-xs font-bold flex items-center justify-center">
              {(user?.username ?? 'TD').slice(0, 2).toUpperCase()}
            </div>
          </div>
        </header>

        {/* ── Main content ── */}
        <main className="flex-1 overflow-y-auto p-4 flex gap-4">

          {/* Left column */}
          <div className="flex-1 flex flex-col gap-4 min-w-0">

            {/* Chart panel */}
            <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5 flex flex-col h-[480px]">
              {/* Chart header */}
              <div className="flex items-start justify-between mb-4">
                <div className="flex items-center gap-3">
                  <div className="w-10 h-10 rounded-full bg-orange-500/20 flex items-center justify-center text-orange-400 font-bold text-sm">₿</div>
                  <div>
                    <h2 className="font-semibold text-white">BTC/USDT</h2>
                    <div className="flex items-baseline gap-3 mt-0.5">
                      <span className="text-2xl font-black text-white">33,909.80 <span className="text-sm font-normal text-slate-400">USDT</span></span>
                      <span className="text-xs text-[#10b981] font-mono">+812.45</span>
                      <span className="text-xs text-[#10b981] font-mono">+2.45%</span>
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <div className="flex bg-[#1e2025] rounded-lg p-1 border border-[#1f2229]">
                    {['1H','4H','1D','1W','1M'].map((tf) => (
                      <button
                        key={tf}
                        onClick={() => setTimeframe(tf)}
                        className={`px-3 py-1 text-[11px] rounded-md transition-colors ${
                          timeframe === tf
                            ? 'bg-[#10b981]/20 text-[#10b981] font-semibold'
                            : 'text-slate-400 hover:text-white'
                        }`}
                      >
                        {tf}
                      </button>
                    ))}
                  </div>
                  <button className="w-8 h-8 flex items-center justify-center rounded-lg bg-[#1e2025] border border-[#1f2229] text-slate-400 hover:text-white">
                    <Maximize2 size={14} />
                  </button>
                </div>
              </div>

              {/* Chart area */}
              <div className="flex-1 relative border-t border-b border-[#1f2229]/40 py-3 mb-3">
                {/* Y-axis */}
                <div className="absolute right-0 top-0 bottom-0 w-16 flex flex-col justify-between text-right pr-2 text-[10px] text-slate-500 font-mono">
                  {['36,000','35,000','34,000','33,909','33,000','32,000','31,000','30,000'].map((v, i) =>
                    v === '33,909' ? (
                      <span key={i} className="bg-[#10b981] text-black px-1 rounded text-[9px] self-end">33,909.80</span>
                    ) : (
                      <span key={i}>{v}</span>
                    )
                  )}
                </div>

                {/* Fake candlestick SVG */}
                <div className="absolute inset-0 mr-16 px-4 pb-10 pt-4">
                  <svg width="100%" height="100%" preserveAspectRatio="none">
                    {/* Candles */}
                    <line x1="5%"  x2="5%"  y1="60%" y2="80%" stroke="#ef4444" strokeWidth="1"/>
                    <rect x="4.2%" y="65%" width="1.6%" height="10%" fill="#ef4444"/>
                    <line x1="12%" x2="12%" y1="50%" y2="72%" stroke="#10b981" strokeWidth="1"/>
                    <rect x="11.2%" y="53%" width="1.6%" height="15%" fill="#10b981"/>
                    <line x1="19%" x2="19%" y1="42%" y2="62%" stroke="#10b981" strokeWidth="1"/>
                    <rect x="18.2%" y="45%" width="1.6%" height="12%" fill="#10b981"/>
                    <line x1="26%" x2="26%" y1="55%" y2="80%" stroke="#ef4444" strokeWidth="1"/>
                    <rect x="25.2%" y="60%" width="1.6%" height="15%" fill="#ef4444"/>
                    <line x1="33%" x2="33%" y1="35%" y2="58%" stroke="#10b981" strokeWidth="1"/>
                    <rect x="32.2%" y="38%" width="1.6%" height="18%" fill="#10b981"/>
                    <line x1="40%" x2="40%" y1="48%" y2="70%" stroke="#ef4444" strokeWidth="1"/>
                    <rect x="39.2%" y="52%" width="1.6%" height="13%" fill="#ef4444"/>
                    <line x1="47%" x2="47%" y1="30%" y2="52%" stroke="#10b981" strokeWidth="1"/>
                    <rect x="46.2%" y="33%" width="1.6%" height="15%" fill="#10b981"/>
                    <line x1="54%" x2="54%" y1="40%" y2="62%" stroke="#ef4444" strokeWidth="1"/>
                    <rect x="53.2%" y="44%" width="1.6%" height="14%" fill="#ef4444"/>
                    <line x1="61%" x2="61%" y1="22%" y2="44%" stroke="#10b981" strokeWidth="1"/>
                    <rect x="60.2%" y="25%" width="1.6%" height="15%" fill="#10b981"/>
                    <line x1="68%" x2="68%" y1="28%" y2="50%" stroke="#10b981" strokeWidth="1"/>
                    <rect x="67.2%" y="31%" width="1.6%" height="14%" fill="#10b981"/>
                    <line x1="75%" x2="75%" y1="38%" y2="60%" stroke="#ef4444" strokeWidth="1"/>
                    <rect x="74.2%" y="42%" width="1.6%" height="13%" fill="#ef4444"/>
                    <line x1="82%" x2="82%" y1="18%" y2="42%" stroke="#10b981" strokeWidth="1"/>
                    <rect x="81.2%" y="21%" width="1.6%" height="17%" fill="#10b981"/>
                    <line x1="89%" x2="89%" y1="24%" y2="46%" stroke="#10b981" strokeWidth="1"/>
                    <rect x="88.2%" y="27%" width="1.6%" height="14%" fill="#10b981"/>
                    {/* Current price line */}
                    <line x1="0" x2="100%" y1="35%" y2="35%" stroke="#10b981" strokeDasharray="4,4" strokeWidth="0.8" opacity="0.6"/>
                  </svg>
                </div>

                {/* Volume bars */}
                <div className="absolute bottom-0 left-4 right-16 h-10 flex items-end justify-around opacity-25">
                  {[4,8,6,3,10,7,5,9,6,4,8,7].map((h, i) => (
                    <div key={i} className={`w-1.5 ${i % 3 === 1 ? 'bg-red-500' : 'bg-[#10b981]'}`} style={{ height: `${h * 10}%` }} />
                  ))}
                </div>
              </div>

              {/* X-axis */}
              <div className="flex justify-between text-[10px] text-slate-500 font-mono pr-16 pl-2">
                {['12:00','5 May','12:00','6 May','12:00','7 May','12:00','8 May'].map((l, i) => (
                  <span key={i}>{l}</span>
                ))}
              </div>
            </div>

            {/* Metric cards */}
            <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
              {[
                { label: '24h Volume',  value: '1.24B USDT', up: true },
                { label: '24h High',    value: '34,120.50',  up: true },
                { label: '24h Low',     value: '32,450.10',  up: false },
                { label: 'Market Cap',  value: '667.89B USDT', up: true },
              ].map(({ label, value, up }) => (
                <div key={label} className="bg-[#111318] border border-[#1f2229] rounded-xl p-4 flex justify-between items-center">
                  <div>
                    <p className="text-[11px] text-slate-400 mb-1">{label}</p>
                    <p className="text-sm font-mono font-semibold text-white">{value}</p>
                  </div>
                  <Sparkline up={up} />
                </div>
              ))}
            </div>

            {/* Bottom tables */}
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">

              {/* Top Markets */}
              <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5">
                <div className="flex justify-between items-center mb-4">
                  <h3 className="font-semibold text-white">Top Markets</h3>
                  <button className="text-[11px] text-[#10b981] hover:underline">View All</button>
                </div>
                <table className="w-full text-xs font-mono">
                  <thead className="text-slate-500 border-b border-[#1f2229]/50">
                    <tr>
                      <th className="pb-2 text-left font-medium">Pair</th>
                      <th className="pb-2 text-right font-medium">Price</th>
                      <th className="pb-2 text-right font-medium">24h</th>
                      <th className="pb-2 text-right font-medium">Volume</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#1f2229]/40">
                    {TOP_MARKETS.map((m) => (
                      <tr key={m.symbol} className="hover:bg-[#1e2025] transition-colors">
                        <td className="py-2.5 flex items-center gap-2">
                          <span className={`w-2 h-2 rounded-full ${m.dot}`} />
                          <span className="text-white">{m.symbol}</span>
                        </td>
                        <td className="py-2.5 text-right text-white">{m.price}</td>
                        <td className={`py-2.5 text-right ${m.up ? 'text-[#10b981]' : 'text-red-400'}`}>{m.change}</td>
                        <td className="py-2.5 text-right text-slate-400">{m.vol}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <div className="mt-3 text-center">
                  <button className="text-[11px] text-[#10b981] hover:underline">View All Markets</button>
                </div>
              </div>

              {/* Recent Orders */}
              <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5">
                <div className="flex justify-between items-center mb-4">
                  <h3 className="font-semibold text-white">Recent Orders</h3>
                  <button className="text-[11px] text-[#10b981] hover:underline">View All</button>
                </div>
                <table className="w-full text-xs font-mono">
                  <thead className="text-slate-500 border-b border-[#1f2229]/50">
                    <tr>
                      <th className="pb-2 text-left font-medium">Type</th>
                      <th className="pb-2 text-left font-medium">Pair</th>
                      <th className="pb-2 text-left font-medium">Side</th>
                      <th className="pb-2 text-right font-medium">Price</th>
                      <th className="pb-2 text-center font-medium">Status</th>
                      <th className="pb-2 text-right font-medium">Time</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#1f2229]/40">
                    {RECENT_ORDERS.map((o, i) => (
                      <tr key={i} className="hover:bg-[#1e2025] transition-colors">
                        <td className="py-2.5 text-slate-400">{o.type}</td>
                        <td className="py-2.5 text-white">{o.pair}</td>
                        <td className={`py-2.5 ${o.side === 'Buy' ? 'text-[#10b981]' : 'text-red-400'}`}>{o.side}</td>
                        <td className="py-2.5 text-right text-white">{o.price}</td>
                        <td className="py-2.5 text-center"><StatusBadge status={o.status} /></td>
                        <td className="py-2.5 text-right text-slate-500">{o.time}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>

          {/* ── Right panel ── */}
          <div className="w-72 flex flex-col gap-4 flex-shrink-0">

            {/* Account Overview */}
            <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5">
              <div className="flex justify-between items-center mb-4">
                <h3 className="font-semibold text-white flex items-center gap-1.5">
                  Account Overview
                  <button onClick={() => setBalanceVisible(!balanceVisible)} className="text-slate-400 hover:text-white">
                    {balanceVisible ? <Eye size={14} /> : <EyeOff size={14} />}
                  </button>
                </h3>
                <button className="text-slate-400 hover:text-white"><MoreHorizontal size={16} /></button>
              </div>

              <div className="mb-4">
                <p className="text-[11px] text-slate-400 flex items-center gap-1 mb-1">
                  Total Balance <Info size={10} />
                </p>
                <h2 className="text-2xl font-black text-white">{balanceVisible ? totalBalance : '••••••'}</h2>
                <div className="flex gap-2 text-xs text-[#10b981] font-mono mt-1">
                  <span>+2.45%</span><span>+$812.45 (24h)</span>
                </div>
              </div>

              {/* Mini sparkline */}
              <div className="h-10 mb-5">
                <svg width="100%" height="100%" preserveAspectRatio="none" viewBox="0 0 100 40">
                  <path d="M0,35 L15,28 L30,30 L45,18 L60,22 L75,8 L100,12" fill="none" stroke="#10b981" strokeWidth="2" vectorEffect="non-scaling-stroke"/>
                </svg>
              </div>

              <div className="grid grid-cols-3 gap-2">
                <button className="bg-[#10b981] text-black font-bold text-xs py-2 rounded-lg hover:bg-[#0e9f6e] transition-colors shadow-[0_0_12px_rgba(16,185,129,0.3)]">
                  Deposit
                </button>
                <button className="bg-[#1e2025] border border-[#1f2229] text-white text-xs py-2 rounded-lg hover:bg-[#282a2f] transition-colors">
                  Withdraw
                </button>
                <button className="bg-[#1e2025] border border-[#1f2229] text-white text-xs py-2 rounded-lg hover:bg-[#282a2f] transition-colors">
                  Transfer
                </button>
              </div>
            </div>

            {/* Assets */}
            <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5">
              <div className="flex justify-between items-center mb-4">
                <h3 className="font-semibold text-white">Assets</h3>
                <button className="text-[11px] text-[#10b981] hover:underline">View All</button>
              </div>
              <div className="space-y-3">
                {ASSETS.map(({ asset, amount, usd, change }) => {
                  const meta = ASSET_META[asset]
                  return (
                    <div key={asset} className="flex items-center justify-between p-2 -mx-2 rounded-lg hover:bg-[#1e2025] transition-colors cursor-pointer">
                      <div className="flex items-center gap-2.5">
                        <div className={`w-8 h-8 rounded-full ${meta.dot}/20 flex items-center justify-center`}>
                          <span className={`text-xs font-bold ${meta.color}`}>{asset[0]}</span>
                        </div>
                        <div>
                          <p className="text-xs font-semibold text-white">{asset}</p>
                          <p className="text-[10px] text-slate-500">{meta.label}</p>
                        </div>
                      </div>
                      <div className="text-right">
                        <p className="text-xs font-mono text-white">{balanceVisible ? amount : '••••'}</p>
                        <p className="text-[10px] text-slate-400">{balanceVisible ? usd : '••••'}</p>
                      </div>
                      <span className="text-[10px] bg-[#10b981]/10 text-[#10b981] px-1.5 py-0.5 rounded font-mono">
                        {change}
                      </span>
                    </div>
                  )
                })}
              </div>
            </div>

            {/* Portfolio */}
            <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5">
              <div className="flex justify-between items-center mb-4">
                <h3 className="font-semibold text-white">My Portfolio</h3>
                <button className="text-[11px] text-[#10b981] hover:underline">View All</button>
              </div>
              <div className="flex items-center gap-4 mb-5">
                <DonutChart />
                <div className="flex flex-col gap-1.5 text-[10px] font-mono flex-1">
                  {[
                    { label: 'USDT', pct: '50.2%', dot: 'bg-[#10b981]' },
                    { label: 'BTC',  pct: '35.8%', dot: 'bg-orange-400' },
                    { label: 'ETH',  pct: '6.2%',  dot: 'bg-blue-400' },
                    { label: 'SOL',  pct: '5.8%',  dot: 'bg-purple-400' },
                    { label: 'Others', pct: '2.2%', dot: 'bg-slate-500' },
                  ].map(({ label, pct, dot }) => (
                    <div key={label} className="flex justify-between items-center">
                      <div className="flex items-center gap-1.5">
                        <span className={`w-2 h-2 rounded-full ${dot}`} />
                        <span className="text-white">{label}</span>
                      </div>
                      <span className="text-slate-400">{pct}</span>
                    </div>
                  ))}
                </div>
              </div>
              <div className="border-t border-[#1f2229] pt-4 flex justify-between items-end">
                <div>
                  <p className="text-[11px] text-slate-400 mb-1">Today's PnL</p>
                  <div className="flex gap-2 text-xs font-mono text-[#10b981] font-semibold">
                    <span>+$812.45</span><span>+2.45%</span>
                  </div>
                </div>
                <div className="w-20 h-8">
                  <svg width="100%" height="100%" preserveAspectRatio="none" viewBox="0 0 60 30">
                    <path d="M0,25 L10,20 L20,22 L30,15 L40,18 L50,5 L60,10" fill="none" stroke="#10b981" strokeWidth="2" vectorEffect="non-scaling-stroke"/>
                  </svg>
                </div>
              </div>
            </div>
          </div>
        </main>

        {/* ── Footer status bar ── */}
        <footer className="h-9 bg-[#0c0e13] border-t border-[#1f2229] flex items-center justify-between px-6 flex-shrink-0">
          <div className="flex items-center gap-1.5 text-[11px] text-slate-400">
            <CheckCircle size={12} className="text-[#10b981]" />
            System Status: <span className="text-[#10b981]">All Systems Operational</span>
          </div>
          <div className="flex items-center gap-8 text-[11px] text-slate-400">
            <div className="flex items-center gap-1.5"><Wifi size={11} /> Network: <span className="text-[#10b981]">Stable</span></div>
            <div className="flex items-center gap-1.5"><Gauge size={11} /> Latency: 23ms</div>
            <div className="flex items-center gap-1.5"><Clock size={11} /> Server Time: May 8, 2024 12:45:30 UTC</div>
          </div>
        </footer>
      </div>
    </div>
  )
}
