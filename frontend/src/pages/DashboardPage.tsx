import { useState } from 'react'
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
  ExternalLink,
  Droplets,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import toast from 'react-hot-toast'
import AppLayout from '../components/layout/AppLayout'

export default function DashboardPage() {
  const [balanceVisible, setBalanceVisible] = useState(true)
  const [timeframe, setTimeframe] = useState<'1H' | '24H' | '7D' | '30D' | '90D'>('24H')
  const [activityTab, setActivityTab] = useState<'All' | 'Trades' | 'Deposits' | 'Withdrawals' | 'Transfers'>('All')
  const [faucetLoading, setFaucetLoading] = useState(false)

  const handleClaimFaucet = async () => {
    setFaucetLoading(true)
    setTimeout(() => {
      toast.success('Successfully credited +10,000 USDT to your spot wallet!')
      setFaucetLoading(false)
    }, 600)
  }

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

            {/* Middle: Net Worth value + Chart */}
            <div className="grid grid-cols-1 md:grid-cols-12 gap-6 items-end my-2">
              {/* Left values */}
              <div className="md:col-span-5 flex flex-col justify-center">
                <span className="text-xs text-slate-400 font-medium">Net Worth</span>
                <div className="flex items-baseline gap-2 mt-1">
                  <h2 className="font-mono text-3xl xl:text-4xl font-black text-white tracking-tight">
                    {balanceVisible ? '$124,850.25' : '••••••••••'}
                  </h2>
                  <span className="text-xs font-mono text-slate-400 font-semibold">USD</span>
                </div>

                <div className="mt-2.5 flex items-center gap-2">
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-lg bg-[#00e676]/10 border border-[#00e676]/30 text-[#00e676] font-mono text-xs font-bold shadow-[0_0_10px_rgba(0,230,118,0.15)]">
                    <TrendingUp size={13} />
                    +8.42% (+$9,680.10)
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
                      {balanceVisible ? '$98,420.00' : '••••••'}
                    </span>
                  </div>

                  <div className="flex flex-col">
                    <div className="flex items-center gap-1.5 text-[11px] text-slate-400">
                      <span className="w-1.5 h-1.5 rounded-full bg-[#f7931a]" />
                      <span>In-Orders (Locked)</span>
                    </div>
                    <span className="font-mono text-sm font-bold text-white mt-0.5">
                      {balanceVisible ? '$26,430.25' : '••••••'}
                    </span>
                  </div>
                </div>
              </div>

              {/* Right: High-Fidelity Performance Spline Chart */}
              <div className="md:col-span-7 flex flex-col justify-end h-44 relative">
                {/* High Price Pin Tag */}
                <div className="absolute right-6 top-0 flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-[#00e676]/15 border border-[#00e676]/40 text-[#00e676] font-mono text-[10px] font-bold">
                  <span>$124.85K</span>
                  <span className="w-1.5 h-1.5 rounded-full bg-[#00e676] animate-ping" />
                </div>

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

          {/* ── 1. BTC / USDT Card ── */}
          <div className="p-5 rounded-2xl bg-[#0e121b] border border-[#1b2230] shadow-lg hover:border-[#f7931a]/40 transition-all flex flex-col justify-between">
            <div>
              {/* Header: Icon + Pair + Star */}
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-2.5">
                  <div className="w-8 h-8 rounded-full bg-[#f7931a]/15 border border-[#f7931a]/30 text-[#f7931a] font-bold text-sm flex items-center justify-center shadow-[0_0_10px_rgba(247,147,26,0.2)]">
                    ₿
                  </div>
                  <div>
                    <h4 className="text-sm font-bold text-white tracking-tight">BTC / USDT</h4>
                  </div>
                </div>
                <button type="button" aria-label="Favorite" className="text-slate-500 hover:text-amber-400 transition-colors">
                  <Star size={15} />
                </button>
              </div>

              {/* Price + Sparkline */}
              <div className="flex items-center justify-between my-2">
                <div>
                  <div className="font-mono text-2xl font-black text-white">$96,450.00</div>
                  <div className="font-mono text-xs font-bold text-[#00e676] mt-0.5 flex items-center gap-0.5">
                    <TrendingUp size={12} /> +3.20%
                  </div>
                </div>
                {/* Green Sparkline */}
                <div className="w-28 h-10">
                  <svg className="w-full h-full" viewBox="0 0 100 35">
                    <path
                      d="M0,28 Q20,30 35,20 T65,15 T100,5"
                      fill="none"
                      stroke="#00e676"
                      strokeWidth="2"
                    />
                  </svg>
                </div>
              </div>

              {/* 4 Stats Grid */}
              <div className="grid grid-cols-4 gap-2 py-3 my-2 border-y border-[#1b2230]/60 text-[10px] font-mono">
                <div>
                  <span className="text-slate-500 block">24h High</span>
                  <span className="text-white font-medium">$97,120.50</span>
                </div>
                <div>
                  <span className="text-slate-500 block">24h Low</span>
                  <span className="text-white font-medium">$92,340.10</span>
                </div>
                <div>
                  <span className="text-slate-500 block">24h Volume</span>
                  <span className="text-white font-medium">$1.42B</span>
                </div>
                <div>
                  <span className="text-slate-500 block">Market Cap</span>
                  <span className="text-white font-medium">$1.89T</span>
                </div>
              </div>
            </div>

            {/* CTA Button */}
            <Link
              to="/trade?market=BTC-USDT"
              className="w-full mt-2 py-2.5 px-4 rounded-xl bg-[#00e676]/10 border border-[#00e676]/30 text-[#00e676] hover:bg-[#00e676] hover:text-black font-semibold text-xs flex items-center justify-center gap-2 transition-all shadow-[0_0_12px_rgba(0,230,118,0.15)]"
            >
              <span>Trade BTC/USDT</span>
              <ChevronRight size={14} />
            </Link>
          </div>

          {/* ── 2. ETH / USDT Card ── */}
          <div className="p-5 rounded-2xl bg-[#0e121b] border border-[#1b2230] shadow-lg hover:border-[#627eea]/40 transition-all flex flex-col justify-between">
            <div>
              {/* Header: Icon + Pair + Star */}
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-2.5">
                  <div className="w-8 h-8 rounded-full bg-[#627eea]/15 border border-[#627eea]/30 text-[#627eea] font-bold text-sm flex items-center justify-center shadow-[0_0_10px_rgba(98,126,234,0.2)]">
                    Ξ
                  </div>
                  <div>
                    <h4 className="text-sm font-bold text-white tracking-tight">ETH / USDT</h4>
                  </div>
                </div>
                <button type="button" aria-label="Favorite" className="text-slate-500 hover:text-amber-400 transition-colors">
                  <Star size={15} />
                </button>
              </div>

              {/* Price + Sparkline */}
              <div className="flex items-center justify-between my-2">
                <div>
                  <div className="font-mono text-2xl font-black text-white">$2,780.50</div>
                  <div className="font-mono text-xs font-bold text-[#00e676] mt-0.5 flex items-center gap-0.5">
                    <TrendingUp size={12} /> +1.80%
                  </div>
                </div>
                {/* Cyan Sparkline */}
                <div className="w-28 h-10">
                  <svg className="w-full h-full" viewBox="0 0 100 35">
                    <path
                      d="M0,25 Q25,28 45,15 T75,18 T100,8"
                      fill="none"
                      stroke="#00e5ff"
                      strokeWidth="2"
                    />
                  </svg>
                </div>
              </div>

              {/* 4 Stats Grid */}
              <div className="grid grid-cols-4 gap-2 py-3 my-2 border-y border-[#1b2230]/60 text-[10px] font-mono">
                <div>
                  <span className="text-slate-500 block">24h High</span>
                  <span className="text-white font-medium">$2,840.90</span>
                </div>
                <div>
                  <span className="text-slate-500 block">24h Low</span>
                  <span className="text-white font-medium">$2,690.20</span>
                </div>
                <div>
                  <span className="text-slate-500 block">24h Volume</span>
                  <span className="text-white font-medium">$1.12B</span>
                </div>
                <div>
                  <span className="text-slate-500 block">Market Cap</span>
                  <span className="text-white font-medium">$334.67B</span>
                </div>
              </div>
            </div>

            {/* CTA Button */}
            <Link
              to="/trade?market=ETH-USDT"
              className="w-full mt-2 py-2.5 px-4 rounded-xl bg-[#00e5ff]/10 border border-[#00e5ff]/30 text-[#00e5ff] hover:bg-[#00e5ff] hover:text-black font-semibold text-xs flex items-center justify-center gap-2 transition-all shadow-[0_0_12px_rgba(0,229,255,0.15)]"
            >
              <span>Trade ETH/USDT</span>
              <ChevronRight size={14} />
            </Link>
          </div>

          {/* ── 3. SOL / USDT Card ── */}
          <div className="p-5 rounded-2xl bg-[#0e121b] border border-[#1b2230] shadow-lg hover:border-[#ff3366]/40 transition-all flex flex-col justify-between">
            <div>
              {/* Header: Icon + Pair + Star */}
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-2.5">
                  <div className="w-8 h-8 rounded-full bg-[#ff3366]/15 border border-[#ff3366]/30 text-[#ff3366] font-bold text-sm flex items-center justify-center shadow-[0_0_10px_rgba(255,51,102,0.2)]">
                    S
                  </div>
                  <div>
                    <h4 className="text-sm font-bold text-white tracking-tight">SOL / USDT</h4>
                  </div>
                </div>
                <button type="button" aria-label="Favorite" className="text-slate-500 hover:text-amber-400 transition-colors">
                  <Star size={15} />
                </button>
              </div>

              {/* Price + Sparkline */}
              <div className="flex items-center justify-between my-2">
                <div>
                  <div className="font-mono text-2xl font-black text-white">$188.20</div>
                  <div className="font-mono text-xs font-bold text-[#ff3366] mt-0.5 flex items-center gap-0.5">
                    <TrendingDown size={12} /> -0.60%
                  </div>
                </div>
                {/* Red Sparkline */}
                <div className="w-28 h-10">
                  <svg className="w-full h-full" viewBox="0 0 100 35">
                    <path
                      d="M0,8 Q20,12 40,22 T70,18 T100,30"
                      fill="none"
                      stroke="#ff3366"
                      strokeWidth="2"
                    />
                  </svg>
                </div>
              </div>

              {/* 4 Stats Grid */}
              <div className="grid grid-cols-4 gap-2 py-3 my-2 border-y border-[#1b2230]/60 text-[10px] font-mono">
                <div>
                  <span className="text-slate-500 block">24h High</span>
                  <span className="text-white font-medium">$190.40</span>
                </div>
                <div>
                  <span className="text-slate-500 block">24h Low</span>
                  <span className="text-white font-medium">$185.10</span>
                </div>
                <div>
                  <span className="text-slate-500 block">24h Volume</span>
                  <span className="text-white font-medium">$320.45M</span>
                </div>
                <div>
                  <span className="text-slate-500 block">Market Cap</span>
                  <span className="text-white font-medium">$84.78B</span>
                </div>
              </div>
            </div>

            {/* CTA Button */}
            <Link
              to="/trade?market=SOL-USDT"
              className="w-full mt-2 py-2.5 px-4 rounded-xl bg-[#ff3366]/10 border border-[#ff3366]/30 text-[#ff3366] hover:bg-[#ff3366] hover:text-white font-semibold text-xs flex items-center justify-center gap-2 transition-all shadow-[0_0_12px_rgba(255,51,102,0.15)]"
            >
              <span>Trade SOL/USDT</span>
              <ChevronRight size={14} />
            </Link>
          </div>
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

              {/* Donut Chart + Slices */}
              <div className="flex items-center justify-center my-4">
                <div className="relative w-36 h-36">
                  <svg className="w-full h-full -rotate-90" viewBox="0 0 42 42">
                    {/* USDT: 45% (Teal / #00e5ff) */}
                    <circle
                      cx="21" cy="21" r="15.915"
                      fill="transparent"
                      stroke="#00e5ff"
                      strokeWidth="5"
                      strokeDasharray="45 55"
                      strokeDashoffset="0"
                    />
                    {/* BTC: 35% (Orange / #f7931a) */}
                    <circle
                      cx="21" cy="21" r="15.915"
                      fill="transparent"
                      stroke="#f7931a"
                      strokeWidth="5"
                      strokeDasharray="35 65"
                      strokeDashoffset="-45"
                    />
                    {/* ETH: 15% (Purple / #627eea) */}
                    <circle
                      cx="21" cy="21" r="15.915"
                      fill="transparent"
                      stroke="#627eea"
                      strokeWidth="5"
                      strokeDasharray="15 85"
                      strokeDashoffset="-80"
                    />
                    {/* SOL: 5% (Violet / #9945ff) */}
                    <circle
                      cx="21" cy="21" r="15.915"
                      fill="transparent"
                      stroke="#9945ff"
                      strokeWidth="5"
                      strokeDasharray="5 95"
                      strokeDashoffset="-95"
                    />
                  </svg>
                  {/* Center Text */}
                  <div className="absolute inset-0 flex flex-col items-center justify-center text-center">
                    <span className="text-[9px] text-slate-400 font-medium">Total</span>
                    <span className="font-mono text-xs font-black text-white mt-0.5">
                      {balanceVisible ? '$124,850.25' : '••••••'}
                    </span>
                    <span className="text-[8px] font-mono text-slate-500">USD</span>
                  </div>
                </div>
              </div>

              {/* Asset List Details */}
              <div className="space-y-2.5 mt-2">
                {[
                  { symbol: 'USDT', name: 'Tether', pct: '45%', amount: '$56,182.61', pnl: '+$2,841.20', dot: 'bg-[#00e5ff]' },
                  { symbol: 'BTC', name: 'Bitcoin', pct: '35%', amount: '$43,697.59', pnl: '+$5,247.80', dot: 'bg-[#f7931a]' },
                  { symbol: 'ETH', name: 'Ethereum', pct: '15%', amount: '$18,727.54', pnl: '+$1,403.10', dot: 'bg-[#627eea]' },
                  { symbol: 'SOL', name: 'Solana', pct: '5%', amount: '$6,242.51', pnl: '+$188.00', dot: 'bg-[#9945ff]' },
                ].map((a) => (
                  <div key={a.symbol} className="flex items-center justify-between text-xs py-1 px-1.5 rounded-lg hover:bg-[#141a26] transition-colors">
                    <div className="flex items-center gap-2">
                      <span className={`w-2 h-2 rounded-full ${a.dot}`} />
                      <span className="font-bold text-white">{a.symbol}</span>
                      <span className="text-[10px] text-slate-400">{a.name}</span>
                    </div>
                    <div className="flex items-center gap-3 font-mono">
                      <span className="text-slate-400 text-[11px]">{a.pct}</span>
                      <span className="text-white font-medium text-[11px]">
                        {balanceVisible ? a.amount : '••••••'}
                      </span>
                      <span className="text-[#00e676] text-[10px] font-semibold">{a.pnl}</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>

            {/* Footer: Last updated */}
            <div className="pt-3 mt-3 border-t border-[#1b2230]/60 flex items-center justify-between text-[10px] font-mono text-slate-500">
              <span>Last Updated: 12:34:56 (UTC)</span>
              <button
                type="button"
                onClick={() => toast.success('Balances refreshed')}
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
                <Link to="/history" className="text-xs font-semibold text-[#00e5ff] hover:underline flex items-center gap-0.5">
                  <span>View All</span>
                  <ChevronRight size={13} />
                </Link>
              </div>

              {/* Filter Tabs */}
              <div className="flex items-center gap-1 mb-4 overflow-x-auto pb-1">
                {(['All', 'Trades', 'Deposits', 'Withdrawals', 'Transfers'] as const).map((tab) => (
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
                <table className="w-full text-left text-xs font-mono">
                  <thead>
                    <tr className="border-b border-[#1b2230] text-[10px] text-slate-500 uppercase tracking-wider">
                      <th className="pb-2 font-semibold">Type</th>
                      <th className="pb-2 font-semibold">Asset / Pair</th>
                      <th className="pb-2 font-semibold text-right">Amount</th>
                      <th className="pb-2 font-semibold text-center">Status</th>
                      <th className="pb-2 font-semibold text-right">Time</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#1b2230]/40">
                    {[
                      { type: 'Buy', pair: 'BTC / USDT', amount: '0.2500 BTC', sub: '$24,112.50', status: 'Filled', time: '12:34:56', side: 'buy' },
                      { type: 'Sell', pair: 'ETH / USDT', amount: '2.1500 ETH', sub: '$5,977.07', status: 'Filled', time: '12:32:18', side: 'sell' },
                      { type: 'Deposit', pair: 'USDT', amount: '+10,000.00 USDT', sub: '$10,000.00', status: 'Filled', time: '12:20:41', side: 'deposit' },
                      { type: 'Withdraw', pair: 'USDT', amount: '-2,500.00 USDT', sub: '$2,500.00', status: 'Pending', time: '11:58:33', side: 'withdraw' },
                      { type: 'Trade', pair: 'SOL / USDT', amount: '15.0000 SOL', sub: '$2,823.00', status: 'Filled', time: '11:45:09', side: 'buy' },
                    ].map((row, i) => (
                      <tr key={i} className="hover:bg-[#141a26]/70 transition-colors">
                        {/* Type */}
                        <td className="py-2.5">
                          <span
                            className={`font-semibold ${
                              row.side === 'buy' || row.side === 'deposit'
                                ? 'text-[#00e676]'
                                : row.side === 'sell'
                                ? 'text-[#ff3366]'
                                : 'text-amber-400'
                            }`}
                          >
                            {row.type}
                          </span>
                        </td>
                        {/* Asset / Pair */}
                        <td className="py-2.5 font-sans font-medium text-white">{row.pair}</td>
                        {/* Amount */}
                        <td className="py-2.5 text-right font-medium">
                          <div className="text-white">{row.amount}</div>
                          <div className="text-[10px] text-slate-500">{row.sub}</div>
                        </td>
                        {/* Status */}
                        <td className="py-2.5 text-center">
                          <span
                            className={`px-2 py-0.5 rounded text-[10px] font-bold ${
                              row.status === 'Filled'
                                ? 'bg-[#00e676]/10 text-[#00e676] border border-[#00e676]/30'
                                : 'bg-amber-400/10 text-amber-400 border border-amber-400/30'
                            }`}
                          >
                            {row.status}
                          </span>
                        </td>
                        {/* Time */}
                        <td className="py-2.5 text-right text-slate-400 text-[11px] font-sans">
                          <div>{row.time}</div>
                          <div className="text-[9px] text-slate-500">May 26, 2026</div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>

          {/* ── Column C (3 Cols): Market Insights ── */}
          <div className="lg:col-span-3 p-5 rounded-2xl bg-[#0e121b] border border-[#1b2230] shadow-xl flex flex-col justify-between">
            <div>
              {/* Header */}
              <div className="flex items-center justify-between mb-3">
                <h3 className="text-sm font-semibold text-slate-300">Market Insights</h3>
                <Link to="/analytics" className="text-xs font-semibold text-[#00e5ff] hover:underline flex items-center gap-0.5">
                  <span>View All</span>
                  <ChevronRight size={13} />
                </Link>
              </div>

              {/* Fear & Greed Speedometer Gauge */}
              <div className="p-3.5 rounded-xl bg-[#07090e] border border-[#1b2230] my-2">
                <div className="flex items-center justify-between text-[11px] text-slate-400 mb-2">
                  <span>Fear & Greed Index</span>
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
                    {/* Needle Indicator */}
                    <line x1="80" y1="80" x2="115" y2="35" stroke="#ffffff" strokeWidth="2.5" strokeLinecap="round" />
                    <circle cx="80" cy="80" r="5" fill="#ffffff" />
                  </svg>

                  {/* Gauge Value */}
                  <div className="text-center -mt-3">
                    <span className="font-mono text-2xl font-black text-white">68</span>
                    <div className="text-[11px] font-bold text-[#00e676]">Greed</div>
                  </div>
                </div>
              </div>

              {/* Top Crypto News Feed */}
              <div className="space-y-2.5 mt-3">
                {[
                  { title: 'Bitcoin breaks $96K resistance', time: '2h ago', icon: '₿', color: 'text-[#f7931a]' },
                  { title: 'Ethereum network upgrade', time: '4h ago', icon: 'Ξ', color: 'text-[#627eea]' },
                  { title: 'Solana DeFi TVL hits new high', time: '6h ago', icon: 'S', color: 'text-[#00e5ff]' },
                ].map((news, i) => (
                  <div key={i} className="flex items-center justify-between p-2 rounded-xl bg-[#141a26]/40 hover:bg-[#141a26] border border-[#1b2230]/60 transition-colors cursor-pointer group">
                    <div className="flex items-center gap-2.5">
                      <span className={`font-bold font-mono text-xs ${news.color}`}>{news.icon}</span>
                      <div className="flex flex-col">
                        <span className="text-xs font-semibold text-slate-200 group-hover:text-white transition-colors line-clamp-1">
                          {news.title}
                        </span>
                        <span className="text-[10px] text-slate-500">{news.time}</span>
                      </div>
                    </div>
                    <MoreVertical size={13} className="text-slate-600 group-hover:text-slate-400" />
                  </div>
                ))}
              </div>
            </div>

            {/* Bottom CTA */}
            <Link
              to="/analytics"
              className="w-full mt-4 py-2.5 px-3 rounded-xl bg-[#141a26] hover:bg-[#1b2230] border border-[#1b2230] text-xs font-semibold text-slate-300 hover:text-white flex items-center justify-center gap-1.5 transition-colors"
            >
              <span>View All News</span>
              <ExternalLink size={12} />
            </Link>
          </div>
        </div>

      </div>
    </AppLayout>
  )
}
