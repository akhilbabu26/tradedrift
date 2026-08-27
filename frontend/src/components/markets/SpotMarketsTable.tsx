import { useState, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { ChevronDown } from 'lucide-react'
import type { Ticker24h } from '../../api/market'

export interface MarketRowData {
  id: string
  symbol: string
  baseAsset: string
  quoteAsset: string
  iconChar: string
  iconBg: string
  iconText: string
  lastPrice: string
  changePercent: string
  isUp: boolean
  high24h: string
  low24h: string
  volume24h: string
  category: string
  sparklinePath: string
}

interface SpotMarketsTableProps {
  liveTickers?: Record<string, Ticker24h>
}

const CATEGORIES = ['All', 'Spot', 'Layer 1', 'DeFi', 'AI', 'Meme']

const INITIAL_MARKETS: MarketRowData[] = [
  {
    id: 'BTC-USDT',
    symbol: 'BTC/USDT',
    baseAsset: 'BTC',
    quoteAsset: 'USDT',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    lastPrice: '$96,450.00',
    changePercent: '+3.20%',
    isUp: true,
    high24h: '$97,200.00',
    low24h: '$94,100.00',
    volume24h: '$1.42B',
    category: 'Layer 1',
    sparklinePath: 'M0,28 Q15,25 30,22 T55,16 T75,12 T100,4',
  },
  {
    id: 'ETH-USDT',
    symbol: 'ETH/USDT',
    baseAsset: 'ETH',
    quoteAsset: 'USDT',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    lastPrice: '$2,780.50',
    changePercent: '+1.80%',
    isUp: true,
    high24h: '$2,850.00',
    low24h: '$2,680.00',
    volume24h: '$845.23M',
    category: 'Layer 1',
    sparklinePath: 'M0,30 Q20,26 35,28 T60,18 T80,14 T100,6',
  },
  {
    id: 'SOL-USDT',
    symbol: 'SOL/USDT',
    baseAsset: 'SOL',
    quoteAsset: 'USDT',
    iconChar: 'S',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    lastPrice: '$188.20',
    changePercent: '+12.45%',
    isUp: true,
    high24h: '$195.30',
    low24h: '$167.80',
    volume24h: '$256.31M',
    category: 'Layer 1',
    sparklinePath: 'M0,34 Q20,30 40,24 T65,18 T85,10 T100,4',
  },
]

export default function SpotMarketsTable({ liveTickers }: SpotMarketsTableProps) {
  const navigate = useNavigate()
  const [activeCategory, setActiveCategory] = useState('All')
  const [rowsPerPage, setRowsPerPage] = useState('5')
  const [currentPage, setCurrentPage] = useState(1)

  // Merge live ticker prices if available from backend
  const displayMarkets = useMemo(() => {
    return INITIAL_MARKETS.map((m) => {
      const live = liveTickers?.[m.id]
      if (live) {
        const numPrice = parseFloat(live.last_price?.replace(/,/g, '')) || 0
        const formattedPrice = numPrice > 0 ? `$${numPrice.toLocaleString('en-US', { minimumFractionDigits: 2 })}` : m.lastPrice
        const change = live.price_change_24h_percent || m.changePercent
        const isUp = !change.startsWith('-')
        return {
          ...m,
          lastPrice: formattedPrice,
          changePercent: change.startsWith('+') || change.startsWith('-') ? change : `+${change}%`,
          isUp,
          high24h: live.high_24h ? `$${parseFloat(live.high_24h).toLocaleString('en-US', { minimumFractionDigits: 2 })}` : m.high24h,
          low24h: live.low_24h ? `$${parseFloat(live.low_24h).toLocaleString('en-US', { minimumFractionDigits: 2 })}` : m.low24h,
        }
      }
      return m
    })
  }, [liveTickers])

  const filteredMarkets = useMemo(() => {
    if (activeCategory === 'All' || activeCategory === 'Spot') return displayMarkets
    return displayMarkets.filter((m) => m.category === activeCategory)
  }, [displayMarkets, activeCategory])

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden shadow-xl select-none">
      {/* ── Header: Title + Subtitle ── */}
      <div className="p-5 pb-3">
        <h2 className="text-base font-bold text-white font-sans tracking-tight">
          Spot Markets
        </h2>
        <p className="text-xs text-slate-400 font-sans mt-0.5">
          Explore all available spot trading pairs
        </p>

        {/* ── Category Tabs ── */}
        <div className="flex items-center gap-2 mt-4 overflow-x-auto pb-1">
          {CATEGORIES.map((cat) => {
            const isActive = activeCategory === cat
            return (
              <button
                key={cat}
                type="button"
                onClick={() => setActiveCategory(cat)}
                className={`px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-all ${
                  isActive
                    ? 'bg-[#00e5ff]/15 text-[#00e5ff] border border-[#00e5ff]/40 shadow-[0_0_12px_rgba(0,229,255,0.15)]'
                    : 'bg-[#07090e] border border-[#1b2230] text-slate-400 hover:text-white hover:border-slate-500'
                }`}
              >
                {cat}
              </button>
            )
          })}
        </div>
      </div>

      {/* ── Dense Markets Table ── */}
      <div className="overflow-x-auto custom-scrollbar">
        <table className="w-full text-xs font-mono text-left border-collapse">
          <thead>
            <tr className="border-b border-[#1b2230] bg-[#07090e]/60 text-slate-400 text-[11px] font-sans font-medium">
              <th className="py-3.5 px-5">Pair</th>
              <th className="py-3.5 px-4">Last Price</th>
              <th className="py-3.5 px-4 text-center">24h Change</th>
              <th className="py-3.5 px-4">24h High</th>
              <th className="py-3.5 px-4">24h Low</th>
              <th className="py-3.5 px-4">24h Volume</th>
              <th className="py-3.5 px-4 text-center">7D Chart</th>
              <th className="py-3.5 px-5 text-right">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1b2230]/60">
            {filteredMarkets.map((m) => {
              const sparkGradientId = `table-spark-${m.id.toLowerCase()}`

              return (
                <tr
                  key={m.id}
                  className="hover:bg-[#141a26]/60 transition-colors group"
                >
                  {/* Pair Name + Coin Badge */}
                  <td className="py-4 px-5 font-sans">
                    <div className="flex items-center gap-3">
                      <div
                        className={`w-7 h-7 rounded-full flex items-center justify-center font-bold text-xs flex-shrink-0 border ${m.iconBg} ${m.iconText}`}
                      >
                        {m.iconChar}
                      </div>
                      <span className="font-bold text-white text-xs tracking-tight">
                        {m.symbol}
                      </span>
                    </div>
                  </td>

                  {/* Last Price */}
                  <td className="py-4 px-4 font-bold text-white text-xs">
                    {m.lastPrice}
                  </td>

                  {/* 24h Change Badge */}
                  <td className="py-4 px-4 text-center font-sans">
                    <span
                      className={`inline-block px-2.5 py-1 rounded-md text-[11px] font-bold font-mono ${
                        m.isUp
                          ? 'bg-[#00e676]/10 text-[#00e676] border border-[#00e676]/20'
                          : 'bg-[#ff3366]/10 text-[#ff3366] border border-[#ff3366]/20'
                      }`}
                    >
                      {m.changePercent}
                    </span>
                  </td>

                  {/* 24h High */}
                  <td className="py-4 px-4 text-slate-200">
                    {m.high24h}
                  </td>

                  {/* 24h Low */}
                  <td className="py-4 px-4 text-slate-200">
                    {m.low24h}
                  </td>

                  {/* 24h Volume */}
                  <td className="py-4 px-4 text-slate-200">
                    {m.volume24h}
                  </td>

                  {/* 7D Mini Sparkline */}
                  <td className="py-4 px-4 text-center">
                    <div className="w-24 h-9 mx-auto">
                      <svg className="w-full h-full" viewBox="0 0 100 35" preserveAspectRatio="none">
                        <defs>
                          <linearGradient id={sparkGradientId} x1="0" y1="0" x2="0" y2="1">
                            <stop offset="0%" stopColor="#00e676" stopOpacity="0.35" />
                            <stop offset="100%" stopColor="#00e676" stopOpacity="0.0" />
                          </linearGradient>
                        </defs>
                        <path
                          d={`${m.sparklinePath} L100,35 L0,35 Z`}
                          fill={`url(#${sparkGradientId})`}
                        />
                        <path
                          d={m.sparklinePath}
                          fill="none"
                          stroke="#00e676"
                          strokeWidth="2"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                        />
                      </svg>
                    </div>
                  </td>

                  {/* Action CTA */}
                  <td className="py-4 px-5 text-right font-sans">
                    <button
                      type="button"
                      onClick={() => navigate(`/trade?market=${m.id}`)}
                      className="px-3.5 py-1.5 rounded-lg text-xs font-bold text-[#00e5ff] bg-[#00e5ff]/10 border border-[#00e5ff]/30 hover:bg-[#00e5ff]/20 hover:border-[#00e5ff]/60 transition-all shadow-[0_0_10px_rgba(0,229,255,0.15)] active:scale-95"
                    >
                      Trade
                    </button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {/* ── Pagination Footer ── */}
      <div className="p-4 border-t border-[#1b2230] flex flex-wrap items-center justify-between gap-3 text-xs text-slate-400 font-sans bg-[#07090e]/40">
        <div>
          Showing 1 to {filteredMarkets.length} of {filteredMarkets.length} results
        </div>

        <div className="flex items-center gap-4">
          {/* Pagination buttons */}
          <div className="flex items-center gap-1 font-mono text-xs">
            <button
              type="button"
              disabled={currentPage === 1}
              onClick={() => setCurrentPage(1)}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center text-slate-500 disabled:opacity-50"
            >
              ‹
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#00e5ff]/15 border border-[#00e5ff]/40 text-[#00e5ff] font-bold flex items-center justify-center shadow-[0_0_8px_rgba(0,229,255,0.15)]"
            >
              1
            </button>
            <button
              type="button"
              disabled={true}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center text-slate-500 disabled:opacity-50"
            >
              ›
            </button>
          </div>

          {/* Show per page dropdown */}
          <div className="flex items-center gap-2 text-xs">
            <div className="relative">
              <select
                value={rowsPerPage}
                onChange={(e) => setRowsPerPage(e.target.value)}
                className="appearance-none bg-[#07090e] border border-[#1b2230] rounded-lg px-3 py-1 pr-7 text-xs text-white font-mono focus:outline-none focus:border-[#00e5ff]/50 cursor-pointer"
              >
                <option value="5">Show 5 per page</option>
                <option value="10">Show 10 per page</option>
                <option value="25">Show 25 per page</option>
              </select>
              <ChevronDown size={12} className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
