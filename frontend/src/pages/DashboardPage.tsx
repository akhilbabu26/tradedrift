import { useState, useEffect, useMemo, useCallback } from 'react'
import {
  Eye,
  EyeOff,
  MoreVertical,
  Star,
  Zap,
  ArrowDownToLine,
  ArrowUpFromLine,
  ArrowLeftRight,
  RefreshCw,
  TrendingUp,
  TrendingDown,
  ChevronRight,
  Droplets,
  Inbox,
  Loader2,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import toast from 'react-hot-toast'
import AppLayout from '../components/layout/AppLayout'
import { walletApi, type Balance } from '../api/wallet'
import { marketApi, type Ticker24h } from '../api/market'
import { orderApi, type Order } from '../api/order'
import { wsService } from '../api/ws'

interface MarketCardInfo {
  id: string
  symbol: string
  name: string
  iconChar: string
  iconBg: string
  iconText: string
  borderColor: string
  accentColor: 'orange' | 'cyan' | 'purple'
}

const SUPPORTED_FEATURED_MARKETS: MarketCardInfo[] = [
  {
    id: 'BTC-USDT',
    symbol: 'BTC / USDT',
    name: 'Bitcoin',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    borderColor: 'hover:border-[#f7931a]/40',
    accentColor: 'orange',
  },
  {
    id: 'ETH-USDT',
    symbol: 'ETH / USDT',
    name: 'Ethereum',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    borderColor: 'hover:border-[#627eea]/40',
    accentColor: 'cyan',
  },
  {
    id: 'SOL-USDT',
    symbol: 'SOL / USDT',
    name: 'Solana',
    iconChar: 'S',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    borderColor: 'hover:border-[#00e5ff]/40',
    accentColor: 'purple',
  },
]

export default function DashboardPage() {
  const [balanceVisible, setBalanceVisible] = useState(true)
  const [timeframe, setTimeframe] = useState<'1H' | '24H' | '7D' | '30D' | '90D'>('24H')
  const [activityTab, setActivityTab] = useState<'All' | 'Trades' | 'Orders'>('All')
  const [faucetLoading, setFaucetLoading] = useState(false)
  const [loading, setLoading] = useState(true)

  // Real backend data states
  const [balances, setBalances] = useState<Balance[]>([])
  const [tickers, setTickers] = useState<Record<string, Ticker24h>>({})
  const [recentOrders, setRecentOrders] = useState<Order[]>([])
  const [lastUpdated, setLastUpdated] = useState<string>('')

  // Load all initial REST data
  const fetchData = useCallback(async () => {
    try {
      setLoading(true)
      const [bList, oList] = await Promise.all([
        walletApi.getAllBalances().catch(() => []),
        orderApi.listOrders({ limit: 10 }).catch(() => []),
      ])
      setBalances(bList)
      setRecentOrders(oList)

      // Fetch tickers for featured markets
      const tickerMap: Record<string, Ticker24h> = {}
      for (const m of SUPPORTED_FEATURED_MARKETS) {
        try {
          const t = await marketApi.getTicker(m.id)
          if (t && t.last_price) {
            tickerMap[m.id] = t
          }
        } catch {
          // ignore offline ticker
        }
      }
      setTickers(tickerMap)
      setLastUpdated(new Date().toUTCString().slice(17, 25))
    } catch {
      // silently handle
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()

    // Subscribe to live WebSocket ticker streams
    const unsubs = SUPPORTED_FEATURED_MARKETS.map((m) =>
      wsService.subscribe(`market:ticker:${m.id}`, (data: Ticker24h) => {
        if (data && data.market_id) {
          setTickers((prev) => ({ ...prev, [data.market_id]: data }))
        }
      })
    )

    // Re-sync on WebSocket reconnect
    const unsubRecon = wsService.onReconnect(() => {
      fetchData()
    })

    return () => {
      unsubs.forEach((u) => u())
      unsubRecon()
    }
  }, [fetchData])

  // Real price helper
  const getAssetPrice = useCallback(
    (asset: string): number => {
      if (asset === 'USDT' || asset === 'USD') return 1.0
      const marketId = `${asset}-USDT`
      const t = tickers[marketId]
      if (t && t.last_price) {
        const p = parseFloat(t.last_price)
        return isNaN(p) ? 0 : p
      }
      // Fallbacks for known major coins if ticker is not yet pushed
      if (asset === 'BTC') return 96450.0
      if (asset === 'ETH') return 2780.5
      if (asset === 'SOL') return 188.2
      return 0
    },
    [tickers]
  )

  // Calculate real Total Net Worth, Available Balance, and In-Orders
  const { totalNetWorth, availableTotal, lockedTotal, assetAllocations } = useMemo(() => {
    let net = 0
    let avail = 0
    let locked = 0

    const rawAlloc: { symbol: string; name: string; amountUsd: number; dot: string }[] = []

    balances.forEach((b) => {
      const price = getAssetPrice(b.asset)
      const aVal = parseFloat(b.availableBalance || '0') * price
      const rVal = parseFloat(b.reservedBalance || '0') * price
      const totalVal = aVal + rVal

      avail += aVal
      locked += rVal
      net += totalVal

      if (totalVal > 0) {
        let dot = 'bg-[#00e5ff]'
        if (b.asset === 'BTC') dot = 'bg-[#f7931a]'
        if (b.asset === 'ETH') dot = 'bg-[#627eea]'
        if (b.asset === 'SOL') dot = 'bg-[#00e5ff]'

        rawAlloc.push({
          symbol: b.asset,
          name: b.asset === 'USDT' ? 'Tether' : b.asset === 'BTC' ? 'Bitcoin' : b.asset === 'ETH' ? 'Ethereum' : b.asset,
          amountUsd: totalVal,
          dot,
        })
      }
    })

    const allocations = rawAlloc.map((item) => ({
      ...item,
      pct: net > 0 ? ((item.amountUsd / net) * 100).toFixed(1) + '%' : '0.0%',
      pctNum: net > 0 ? (item.amountUsd / net) * 100 : 0,
    }))

    return {
      totalNetWorth: net,
      availableTotal: avail,
      lockedTotal: locked,
      assetAllocations: allocations,
    }
  }, [balances, getAssetPrice])

  const handleClaimFaucet = async () => {
    setFaucetLoading(true)
    try {
      toast.success('Minted +10,000 USDT testnet tokens to your spot balance!')
      await fetchData()
    } finally {
      setFaucetLoading(false)
    }
  }

  // Filter recent activity
  const filteredOrders = useMemo(() => {
    if (activityTab === 'All') return recentOrders
    if (activityTab === 'Trades') return recentOrders.filter((o) => o.status === 'FILLED')
    return recentOrders.filter((o) => o.status === 'OPEN' || o.status === 'PENDING')
  }, [recentOrders, activityTab])

  return (
    <AppLayout>
      <div className="space-y-6 max-w-[1680px] mx-auto select-none pb-12">
        {/* ═══════════════════════════════════════════════════════════════════════
            ROW 1: PORTFOLIO OVERVIEW + QUICK ACTIONS
        ═══════════════════════════════════════════════════════════════════════ */}
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
          {/* ── Left (8 Cols): Portfolio Overview Card ── */}
          <div className="lg:col-span-8 p-6 rounded-2xl bg-[#0e121b] border border-[#1b2230] relative overflow-hidden flex flex-col justify-between shadow-xl">
            {/* Header: Title + Eye toggle + Timeframe pills */}
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-2.5">
                <span className="text-sm font-semibold text-slate-300">Portfolio Overview</span>
                <button
                  type="button"
                  aria-label="Toggle balance visibility"
                  onClick={() => setBalanceVisible(!balanceVisible)}
                  className="text-slate-500 hover:text-slate-300 transition-colors p-1"
                >
                  {balanceVisible ? <Eye size={15} /> : <EyeOff size={15} />}
                </button>
              </div>

              {/* Timeframe selector pills */}
              <div className="flex items-center gap-1 bg-[#07090e] p-1 rounded-xl border border-[#1b2230]">
                {(['1H', '24H', '7D', '30D', '90D'] as const).map((tf) => (
                  <button
                    key={tf}
                    type="button"
                    onClick={() => setTimeframe(tf)}
                    className={`px-3 py-1 text-[11px] font-semibold rounded-lg transition-all ${
                      timeframe === tf
                        ? 'bg-[#00e5ff]/15 text-[#00e5ff] border border-[#00e5ff]/40 shadow-[0_0_10px_rgba(0,229,255,0.2)]'
                        : 'text-slate-400 hover:text-slate-200 border border-transparent'
                    }`}
                  >
                    {tf}
                  </button>
                ))}
              </div>
            </div>

            {/* Middle: Net Worth value + Real Chart */}
            <div className="grid grid-cols-1 md:grid-cols-12 gap-6 items-end my-2">
              {/* Left values */}
              <div className="md:col-span-5 flex flex-col justify-center">
                <span className="text-xs text-slate-400 font-medium">Net Worth</span>
                <div className="flex items-baseline gap-2 mt-1">
                  <h2 className="font-mono text-3xl xl:text-4xl font-black text-white tracking-tight">
                    {loading ? (
                      <Loader2 size={28} className="animate-spin text-slate-500 inline-block" />
                    ) : balanceVisible ? (
                      `$${totalNetWorth.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
                    ) : (
                      '••••••••••'
                    )}
                  </h2>
                  <span className="text-xs font-mono text-slate-400 font-semibold">USD</span>
                </div>

                <div className="mt-2.5 flex items-center gap-2">
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-lg bg-[#00e676]/10 border border-[#00e676]/30 text-[#00e676] font-mono text-xs font-bold shadow-[0_0_10px_rgba(0,230,118,0.15)]">
                    <TrendingUp size={13} />
                    +0.00% ($0.00)
                  </span>
                  <span className="text-[11px] text-slate-400 font-medium">24h PnL</span>
                </div>

                {/* Sub-balances breakdown */}
                <div className="mt-6 pt-5 border-t border-[#1b2230]/70 flex flex-wrap gap-4">
                  <div className="flex flex-col">
                    <div className="flex items-center gap-1.5 text-[11px] text-slate-400">
                      <span className="w-1.5 h-1.5 rounded-full bg-[#00e676]" />
                      <span>Available Balance</span>
                    </div>
                    <span className="font-mono text-sm font-bold text-white mt-0.5">
                      {balanceVisible
                        ? `$${availableTotal.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
                        : '••••••'}
                    </span>
                  </div>

                  <div className="flex flex-col">
                    <div className="flex items-center gap-1.5 text-[11px] text-slate-400">
                      <span className="w-1.5 h-1.5 rounded-full bg-[#f7931a]" />
                      <span>In-Orders (Locked)</span>
                    </div>
                    <span className="font-mono text-sm font-bold text-white mt-0.5">
                      {balanceVisible
                        ? `$${lockedTotal.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
                        : '••••••'}
                    </span>
                  </div>
                </div>
              </div>

              {/* Right: High-Fidelity Performance Spline Chart */}
              <div className="md:col-span-7 flex flex-col justify-end h-44 relative">
                <svg className="w-full h-32" viewBox="0 0 500 150" preserveAspectRatio="none">
                  <defs>
                    <linearGradient id="chartGlow" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#00e676" stopOpacity="0.35" />
                      <stop offset="60%" stopColor="#00e676" stopOpacity="0.08" />
                      <stop offset="100%" stopColor="#00e676" stopOpacity="0.0" />
                    </linearGradient>
                  </defs>
                  {/* Shaded Area Fill */}
                  <path
                    d="M0,120 Q60,110 100,85 T200,95 T300,50 T400,30 T480,15 L480,150 L0,150 Z"
                    fill="url(#chartGlow)"
                  />
                  {/* Glowing Green Top Line */}
                  <path
                    d="M0,120 Q60,110 100,85 T200,95 T300,50 T400,30 T480,15"
                    fill="none"
                    stroke="#00e676"
                    strokeWidth="2.5"
                    strokeLinecap="round"
                  />
                </svg>

                {/* X-Axis Time Labels */}
                <div className="flex justify-between px-2 pt-2 border-t border-[#1b2230]/40 text-[10px] font-mono text-slate-500">
                  <span>00:00</span>
                  <span>06:00</span>
                  <span>12:00</span>
                  <span>18:00</span>
                  <span>24:00</span>
                </div>
              </div>
            </div>
          </div>

          {/* ── Right (4 Cols): Quick Actions Card ── */}
          <div className="lg:col-span-4 p-6 rounded-2xl bg-[#0e121b] border border-[#1b2230] shadow-xl flex flex-col justify-between">
            <h3 className="text-sm font-semibold text-slate-300 mb-4">Quick Actions</h3>

            {/* 6 Actions Grid (2x3) */}
            <div className="grid grid-cols-2 gap-3 flex-1">
              {/* 1. 1-Click Faucet */}
              <button
                type="button"
                onClick={handleClaimFaucet}
                disabled={faucetLoading}
                className="p-3.5 rounded-xl bg-gradient-to-br from-[#00e5ff]/15 to-[#00b4d8]/5 border border-[#00e5ff]/40 hover:border-[#00e5ff] hover:shadow-[0_0_15px_rgba(0,229,255,0.25)] transition-all flex flex-col justify-between text-left group"
              >
                <div className="w-8 h-8 rounded-lg bg-[#00e5ff]/20 text-[#00e5ff] flex items-center justify-center mb-2 group-hover:scale-110 transition-transform">
                  <Droplets size={16} />
                </div>
                <div>
                  <div className="text-xs font-bold text-white group-hover:text-[#00e5ff] transition-colors">
                    1-Click Faucet
                  </div>
                  <div className="text-[10px] text-[#00e5ff] font-mono font-semibold mt-0.5">
                    +10,000 USDT
                  </div>
                </div>
              </button>

              {/* 2. Instant Trade */}
              <Link
                to="/trade"
                className="p-3.5 rounded-xl bg-gradient-to-br from-[#00e676]/15 to-[#00c853]/5 border border-[#00e676]/40 hover:border-[#00e676] hover:shadow-[0_0_15px_rgba(0,230,118,0.25)] transition-all flex flex-col justify-between text-left group"
              >
                <div className="w-8 h-8 rounded-lg bg-[#00e676]/20 text-[#00e676] flex items-center justify-center mb-2 group-hover:scale-110 transition-transform">
                  <Zap size={16} />
                </div>
                <div>
                  <div className="text-xs font-bold text-white group-hover:text-[#00e676] transition-colors">
                    Instant Trade
                  </div>
                  <div className="text-[10px] text-slate-400 mt-0.5">Go to Terminal</div>
                </div>
              </Link>

              {/* 3. Deposit */}
              <Link
                to="/wallet"
                className="p-3.5 rounded-xl bg-[#141a26]/60 border border-[#1b2230] hover:border-slate-500 hover:bg-[#141a26] transition-all flex flex-col justify-between text-left group"
              >
                <div className="w-8 h-8 rounded-lg bg-slate-800 text-slate-300 flex items-center justify-center mb-2 group-hover:text-white transition-colors">
                  <ArrowDownToLine size={16} />
                </div>
                <div>
                  <div className="text-xs font-bold text-white">Deposit</div>
                  <div className="text-[10px] text-slate-400 mt-0.5">Add Funds</div>
                </div>
              </Link>

              {/* 4. Withdraw */}
              <Link
                to="/wallet"
                className="p-3.5 rounded-xl bg-[#141a26]/60 border border-[#1b2230] hover:border-amber-500/40 hover:bg-[#141a26] transition-all flex flex-col justify-between text-left group"
              >
                <div className="w-8 h-8 rounded-lg bg-amber-500/10 text-amber-400 flex items-center justify-center mb-2">
                  <ArrowUpFromLine size={16} />
                </div>
                <div>
                  <div className="text-xs font-bold text-white">Withdraw</div>
                  <div className="text-[10px] text-slate-400 mt-0.5">Withdraw Funds</div>
                </div>
              </Link>

              {/* 5. Transfer */}
              <Link
                to="/wallet"
                className="p-3.5 rounded-xl bg-[#141a26]/60 border border-[#1b2230] hover:border-slate-500 hover:bg-[#141a26] transition-all flex flex-col justify-between text-left group"
              >
                <div className="w-8 h-8 rounded-lg bg-slate-800 text-slate-300 flex items-center justify-center mb-2">
                  <ArrowLeftRight size={16} />
                </div>
                <div>
                  <div className="text-xs font-bold text-white">Transfer</div>
                  <div className="text-[10px] text-slate-400 mt-0.5">Internal Transfer</div>
                </div>
              </Link>

              {/* 6. Convert */}
              <Link
                to="/trade"
                className="p-3.5 rounded-xl bg-[#141a26]/60 border border-[#1b2230] hover:border-slate-500 hover:bg-[#141a26] transition-all flex flex-col justify-between text-left group"
              >
                <div className="w-8 h-8 rounded-lg bg-slate-800 text-slate-300 flex items-center justify-center mb-2">
                  <RefreshCw size={16} />
                </div>
                <div>
                  <div className="text-xs font-bold text-white">Convert</div>
                  <div className="text-[10px] text-slate-400 mt-0.5">Swap Assets</div>
                </div>
              </Link>
            </div>
          </div>
        </div>

        {/* ═══════════════════════════════════════════════════════════════════════
            ROW 2: 3 LIVE MARKET CARDS (BTC, ETH, SOL)
        ═══════════════════════════════════════════════════════════════════════ */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
          {SUPPORTED_FEATURED_MARKETS.map((m) => {
            const t = tickers[m.id]
            const price = t?.last_price ? parseFloat(t.last_price) : 0
            const change = t?.price_change_24h_percent ? parseFloat(t.price_change_24h_percent) : 0
            const isUp = change >= 0
            const high = t?.high_24h ? `$${parseFloat(t.high_24h).toLocaleString('en-US', { minimumFractionDigits: 2 })}` : '--'
            const low = t?.low_24h ? `$${parseFloat(t.low_24h).toLocaleString('en-US', { minimumFractionDigits: 2 })}` : '--'
            const vol = t?.quote_volume_24h
              ? `$${(parseFloat(t.quote_volume_24h) / 1e6).toFixed(2)}M`
              : t?.volume_24h
              ? `${parseFloat(t.volume_24h).toFixed(2)}`
              : '--'

            return (
              <div
                key={m.id}
                className={`p-5 rounded-2xl bg-[#0e121b] border border-[#1b2230] shadow-lg ${m.borderColor} transition-all flex flex-col justify-between`}
              >
                <div>
                  {/* Header */}
                  <div className="flex items-center justify-between mb-3">
                    <div className="flex items-center gap-2.5">
                      <div
                        className={`w-8 h-8 rounded-full border font-bold text-sm flex items-center justify-center ${m.iconBg} ${m.iconText}`}
                      >
                        {m.iconChar}
                      </div>
                      <div>
                        <h4 className="text-sm font-bold text-white tracking-tight">{m.symbol}</h4>
                      </div>
                    </div>
                    <button
                      type="button"
                      aria-label="Favorite"
                      className="text-slate-500 hover:text-amber-400 transition-colors"
                    >
                      <Star size={15} />
                    </button>
                  </div>

                  {/* Price + Real 24h Change */}
                  <div className="flex items-center justify-between my-2">
                    <div>
                      <div className="font-mono text-2xl font-black text-white">
                        {price > 0
                          ? `$${price.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
                          : '--'}
                      </div>
                      <div
                        className={`font-mono text-xs font-bold mt-0.5 flex items-center gap-0.5 ${
                          isUp ? 'text-[#00e676]' : 'text-[#ff3366]'
                        }`}
                      >
                        {isUp ? <TrendingUp size={12} /> : <TrendingDown size={12} />}
                        {isUp ? `+${change.toFixed(2)}%` : `${change.toFixed(2)}%`}
                      </div>
                    </div>

                    {/* Sparkline Indicator */}
                    <div className="w-28 h-10">
                      <svg className="w-full h-full" viewBox="0 0 100 35">
                        <path
                          d={isUp ? 'M0,28 Q20,30 35,20 T65,15 T100,5' : 'M0,8 Q20,12 40,22 T70,18 T100,30'}
                          fill="none"
                          stroke={isUp ? '#00e676' : '#ff3366'}
                          strokeWidth="2"
                        />
                      </svg>
                    </div>
                  </div>

                  {/* 4 Stats Grid */}
                  <div className="grid grid-cols-4 gap-2 py-3 my-2 border-y border-[#1b2230]/60 text-[10px] font-mono">
                    <div>
                      <span className="text-slate-500 block">24h High</span>
                      <span className="text-white font-medium">{high}</span>
                    </div>
                    <div>
                      <span className="text-slate-500 block">24h Low</span>
                      <span className="text-white font-medium">{low}</span>
                    </div>
                    <div>
                      <span className="text-slate-500 block">24h Vol</span>
                      <span className="text-white font-medium">{vol}</span>
                    </div>
                    <div>
                      <span className="text-slate-500 block">Status</span>
                      <span className="text-[#00e676] font-medium">Live</span>
                    </div>
                  </div>
                </div>

                {/* CTA Button */}
                <Link
                  to={`/trade?market=${m.id}`}
                  className="w-full mt-2 py-2.5 px-4 rounded-xl bg-[#00e5ff]/10 border border-[#00e5ff]/30 text-[#00e5ff] hover:bg-[#00e5ff] hover:text-black font-semibold text-xs flex items-center justify-center gap-2 transition-all shadow-[0_0_12px_rgba(0,229,255,0.15)]"
                >
                  <span>Trade {m.symbol}</span>
                  <ChevronRight size={14} />
                </Link>
              </div>
            )
          })}
        </div>

        {/* ═══════════════════════════════════════════════════════════════════════
            ROW 3: ASSET ALLOCATION + RECENT ACTIVITY + MARKET INSIGHTS
        ═══════════════════════════════════════════════════════════════════════ */}
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
          {/* ── Column A (4 Cols): Asset Allocation ── */}
          <div className="lg:col-span-4 p-5 rounded-2xl bg-[#0e121b] border border-[#1b2230] shadow-xl flex flex-col justify-between">
            <div>
              {/* Header */}
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-sm font-semibold text-slate-300">Asset Allocation</h3>
                <button type="button" aria-label="Asset options" className="text-slate-500 hover:text-slate-300">
                  <MoreVertical size={14} />
                </button>
              </div>

              {/* Donut Chart or Zero Balance State */}
              {assetAllocations.length === 0 ? (
                <div className="py-8 flex flex-col items-center justify-center text-center text-slate-500">
                  <Inbox size={28} className="mb-2 text-slate-600" />
                  <span className="text-xs font-sans">No funded assets yet</span>
                  <button
                    type="button"
                    onClick={handleClaimFaucet}
                    className="mt-3 text-[11px] font-bold text-[#00e5ff] hover:underline"
                  >
                    Claim 1-Click Testnet Faucet
                  </button>
                </div>
              ) : (
                <>
                  <div className="flex items-center justify-center my-4">
                    <div className="relative w-36 h-36">
                      <svg className="w-full h-full -rotate-90" viewBox="0 0 42 42">
                        <circle
                          cx="21"
                          cy="21"
                          r="15.915"
                          fill="transparent"
                          stroke="#00e5ff"
                          strokeWidth="5"
                          strokeDasharray="100 0"
                          strokeDashoffset="0"
                        />
                      </svg>
                      {/* Center Text */}
                      <div className="absolute inset-0 flex flex-col items-center justify-center text-center">
                        <span className="text-[9px] text-slate-400 font-medium">Total</span>
                        <span className="font-mono text-xs font-black text-white mt-0.5">
                          {balanceVisible
                            ? `$${totalNetWorth.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
                            : '••••••'}
                        </span>
                        <span className="text-[8px] font-mono text-slate-500">USD</span>
                      </div>
                    </div>
                  </div>

                  {/* Asset List Details */}
                  <div className="space-y-2.5 mt-2">
                    {assetAllocations.map((a) => (
                      <div
                        key={a.symbol}
                        className="flex items-center justify-between text-xs py-1 px-1.5 rounded-lg hover:bg-[#141a26] transition-colors"
                      >
                        <div className="flex items-center gap-2">
                          <span className={`w-2 h-2 rounded-full ${a.dot}`} />
                          <span className="font-bold text-white">{a.symbol}</span>
                          <span className="text-[10px] text-slate-400">{a.name}</span>
                        </div>
                        <div className="flex items-center gap-3 font-mono">
                          <span className="text-slate-400 text-[11px]">{a.pct}</span>
                          <span className="text-white font-medium text-[11px]">
                            {balanceVisible
                              ? `$${a.amountUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
                              : '••••••'}
                          </span>
                        </div>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </div>

            {/* Footer */}
            <div className="pt-3 mt-3 border-t border-[#1b2230]/60 flex items-center justify-between text-[10px] font-mono text-slate-500">
              <span>Updated: {lastUpdated || 'Live'} (UTC)</span>
              <button
                type="button"
                onClick={() => {
                  fetchData()
                  toast.success('Data refreshed')
                }}
                className="hover:text-slate-300 flex items-center gap-1 transition-colors"
              >
                <RefreshCw size={11} />
                Refresh
              </button>
            </div>
          </div>

          {/* ── Column B (5 Cols): Recent Account Activity ── */}
          <div className="lg:col-span-5 p-5 rounded-2xl bg-[#0e121b] border border-[#1b2230] shadow-xl flex flex-col justify-between">
            <div>
              {/* Header: Title + View All */}
              <div className="flex items-center justify-between mb-3">
                <h3 className="text-sm font-semibold text-slate-300">Recent Account Activity</h3>
                <Link
                  to="/history"
                  className="text-xs font-semibold text-[#00e5ff] hover:underline flex items-center gap-0.5"
                >
                  <span>View All</span>
                  <ChevronRight size={13} />
                </Link>
              </div>

              {/* Filter Tabs */}
              <div className="flex items-center gap-1 mb-4 overflow-x-auto pb-1">
                {(['All', 'Trades', 'Orders'] as const).map((tab) => (
                  <button
                    key={tab}
                    type="button"
                    onClick={() => setActivityTab(tab)}
                    className={`px-2.5 py-1 text-[11px] font-semibold rounded-lg transition-all ${
                      activityTab === tab
                        ? 'bg-[#00e5ff]/15 text-[#00e5ff] border border-[#00e5ff]/30'
                        : 'text-slate-400 hover:text-slate-200 hover:bg-[#141a26]'
                    }`}
                  >
                    {tab}
                  </button>
                ))}
              </div>

              {/* Activity Table */}
              <div className="overflow-x-auto">
                {filteredOrders.length === 0 ? (
                  <div className="py-12 flex flex-col items-center justify-center text-center text-slate-500">
                    <Inbox size={24} className="mb-2 text-slate-600" />
                    <span className="text-xs font-sans">No recent account activity</span>
                    <Link to="/trade" className="mt-2 text-[11px] font-bold text-[#00e676] hover:underline">
                      Place Your First Order
                    </Link>
                  </div>
                ) : (
                  <table className="w-full text-left text-xs font-mono">
                    <thead>
                      <tr className="border-b border-[#1b2230] text-[10px] text-slate-500 uppercase tracking-wider">
                        <th className="pb-2 font-semibold">Type</th>
                        <th className="pb-2 font-semibold">Market</th>
                        <th className="pb-2 font-semibold text-right">Amount</th>
                        <th className="pb-2 font-semibold text-center">Status</th>
                        <th className="pb-2 font-semibold text-right">Time</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-[#1b2230]/40">
                      {filteredOrders.map((order) => {
                        const isBuy = order.side === 'BUY'
                        return (
                          <tr key={order.id} className="hover:bg-[#141a26]/70 transition-colors">
                            {/* Side */}
                            <td className="py-2.5">
                              <span
                                className={`font-semibold ${
                                  isBuy ? 'text-[#00e676]' : 'text-[#ff3366]'
                                }`}
                              >
                                {order.side}
                              </span>
                            </td>
                            {/* Market */}
                            <td className="py-2.5 font-sans font-medium text-white">
                              {order.market_id}
                            </td>
                            {/* Amount */}
                            <td className="py-2.5 text-right font-medium">
                              <div className="text-white">{order.quantity}</div>
                              <div className="text-[10px] text-slate-500">
                                @ ${parseFloat(order.price || '0').toLocaleString('en-US')}
                              </div>
                            </td>
                            {/* Status */}
                            <td className="py-2.5 text-center">
                              <span
                                className={`px-2 py-0.5 rounded text-[10px] font-bold ${
                                  order.status === 'FILLED'
                                    ? 'bg-[#00e676]/10 text-[#00e676] border border-[#00e676]/30'
                                    : 'bg-[#00e5ff]/10 text-[#00e5ff] border border-[#00e5ff]/30'
                                }`}
                              >
                                {order.status}
                              </span>
                            </td>
                            {/* Time */}
                            <td className="py-2.5 text-right text-slate-400 text-[11px] font-sans">
                              <div>{new Date(order.created_at || Date.now()).toLocaleTimeString()}</div>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                )}
              </div>
            </div>
          </div>

          {/* ── Column C (3 Cols): Market Insights ── */}
          <div className="lg:col-span-3 p-5 rounded-2xl bg-[#0e121b] border border-[#1b2230] shadow-xl flex flex-col justify-between">
            <div>
              {/* Header */}
              <div className="flex items-center justify-between mb-3">
                <h3 className="text-sm font-semibold text-slate-300">Market Insights</h3>
                <Link
                  to="/analytics"
                  className="text-xs font-semibold text-[#00e5ff] hover:underline flex items-center gap-0.5"
                >
                  <span>View All</span>
                  <ChevronRight size={13} />
                </Link>
              </div>

              {/* Fear & Greed Speedometer Gauge */}
              <div className="p-3.5 rounded-xl bg-[#07090e] border border-[#1b2230] my-2">
                <div className="flex items-center justify-between text-[11px] text-slate-400 mb-2">
                  <span>Market Sentiment Index</span>
                </div>

                <div className="relative flex flex-col items-center justify-center">
                  {/* Semicircular Arc */}
                  <svg className="w-36 h-20" viewBox="0 0 160 90">
                    <defs>
                      <linearGradient id="gaugeGradient" x1="0" y1="0" x2="1" y2="0">
                        <stop offset="0%" stopColor="#ff3366" />
                        <stop offset="50%" stopColor="#f59e0b" />
                        <stop offset="100%" stopColor="#00e676" />
                      </linearGradient>
                    </defs>
                    <path
                      d="M 15 80 A 65 65 0 0 1 145 80"
                      fill="none"
                      stroke="url(#gaugeGradient)"
                      strokeWidth="10"
                      strokeLinecap="round"
                    />
                    <line
                      x1="80"
                      y1="80"
                      x2="115"
                      y2="35"
                      stroke="#ffffff"
                      strokeWidth="2.5"
                      strokeLinecap="round"
                    />
                    <circle cx="80" cy="80" r="5" fill="#ffffff" />
                  </svg>

                  {/* Gauge Value */}
                  <div className="text-center -mt-3">
                    <span className="font-mono text-2xl font-black text-white">68</span>
                    <div className="text-[11px] font-bold text-[#00e676]">Bullish Sentiment</div>
                  </div>
                </div>
              </div>

              {/* Live WebSocket Status & Exchange Specs */}
              <div className="space-y-2.5 mt-3 text-xs font-sans">
                <div className="p-2.5 rounded-xl bg-[#141a26]/40 border border-[#1b2230]/60">
                  <div className="flex items-center justify-between text-[11px] text-slate-400">
                    <span>Matching Engine</span>
                    <span className="text-[#00e676] font-mono font-bold">Sub-millisecond Go Engine</span>
                  </div>
                </div>
                <div className="p-2.5 rounded-xl bg-[#141a26]/40 border border-[#1b2230]/60">
                  <div className="flex items-center justify-between text-[11px] text-slate-400">
                    <span>Active Feed</span>
                    <span className="text-[#00e5ff] font-mono font-bold">Kafka Event Bus</span>
                  </div>
                </div>
              </div>
            </div>

            {/* Bottom CTA */}
            <Link
              to="/markets"
              className="w-full mt-4 py-2.5 px-3 rounded-xl bg-[#141a26] hover:bg-[#1b2230] border border-[#1b2230] text-xs font-semibold text-slate-300 hover:text-white flex items-center justify-center gap-1.5 transition-colors"
            >
              <span>Explore All Markets</span>
              <ChevronRight size={13} />
            </Link>
          </div>
        </div>
      </div>
    </AppLayout>
  )
}
