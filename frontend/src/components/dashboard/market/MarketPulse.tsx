import { useState } from 'react'
import { ArrowRight } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { MarketRow } from '../../../types/dashboard'
import { formatPrice, formatPercentage } from '../../../utils/formatters'
import DashboardCard from '../shared/DashboardCard'
import SectionHeader from '../shared/SectionHeader'
import Sparkline from '../shared/Sparkline'

interface MarketPulseProps {
  rows: MarketRow[]
}

type MarketTab = 'Watchlist' | 'Gainers' | 'Losers' | 'Volume'
const TABS: MarketTab[] = ['Watchlist', 'Gainers', 'Losers', 'Volume']

/**
 * Market Pulse card — Watchlist/Gainers/Losers/Volume tabs.
 * Only BTC/USDT, ETH/USDT, SOL/USDT rows — never BNB/XRP.
 */
export default function MarketPulse({ rows }: MarketPulseProps) {
  const [activeTab, setActiveTab] = useState<MarketTab>('Watchlist')

  const sortedRows = [...rows].sort((a, b) => {
    if (activeTab === 'Gainers') {
      const aVal = (a.positive ? 1 : -1) * parseFloat(a.change24h || '0')
      const bVal = (b.positive ? 1 : -1) * parseFloat(b.change24h || '0')
      return bVal - aVal
    }
    if (activeTab === 'Losers') {
      const aVal = (a.positive ? 1 : -1) * parseFloat(a.change24h || '0')
      const bVal = (b.positive ? 1 : -1) * parseFloat(b.change24h || '0')
      return aVal - bVal
    }
    if (activeTab === 'Volume') {
      return (b.quoteVolume || 0) - (a.quoteVolume || 0)
    }
    return 0 // Watchlist: retain default market order
  })

  return (
    <DashboardCard>
      {/* Header */}
      <SectionHeader
        title="Market Pulse"
        action={
          <Link to="/markets" className="flex items-center gap-0.5 text-xs text-slate-400 hover:text-[#10b981] transition-colors">
            View All Markets <ArrowRight size={11} />
          </Link>
        }
      />

      {/* Tabs */}
      <div
        className="flex items-center gap-0.5 bg-[#0a0b0e] rounded-md border border-[#1e2530] p-0.5 mb-3 w-fit"
        role="tablist"
        aria-label="Market filter"
      >
        {TABS.map((tab) => (
          <button
            key={tab}
            role="tab"
            aria-selected={activeTab === tab}
            onClick={() => setActiveTab(tab)}
            className={`px-3 py-1 rounded text-xs font-medium transition-colors ${
              activeTab === tab
                ? 'bg-[#10b981] text-[#0a0b0e]'
                : 'text-slate-400 hover:text-[#f5f7fa]'
            }`}
          >
            {tab}
          </button>
        ))}
      </div>

      {/* Table */}
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="text-slate-500 text-[10px] uppercase tracking-wider border-b border-[#1e2530]">
              <th className="text-left pb-2 font-medium">Pair</th>
              <th className="text-right pb-2 font-medium">Price</th>
              <th className="text-right pb-2 font-medium">24h Change</th>
              <th className="text-right pb-2 font-medium">7D Trend</th>
              <th className="text-right pb-2 font-medium">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1e2530]/40">
            {sortedRows.map((row) => {
              const changeColor = row.positive ? 'text-[#10b981]' : 'text-[#ef4444]'
              return (
                <tr key={row.pair} className="hover:bg-white/[0.02] transition-colors">
                  {/* Pair */}
                  <td className="py-2.5 pr-2">
                    <div className="flex items-center gap-2">
                      <span
                        className={`w-6 h-6 rounded-full border flex items-center justify-center text-[11px] font-bold flex-shrink-0 ${row.iconBg}`}
                      >
                        {row.iconLabel}
                      </span>
                      <span className="font-medium text-[#f5f7fa]">{row.pair}</span>
                    </div>
                  </td>
                  {/* Price */}
                  <td className="py-2.5 text-right font-mono text-slate-200">
                    {formatPrice(row.price)}
                  </td>
                  {/* 24h Change */}
                  <td className={`py-2.5 text-right font-mono font-semibold ${changeColor}`}>
                    {row.positive ? '+' : '-'}{formatPercentage(row.change24h, false)}
                  </td>
                  {/* 7D Sparkline */}
                  <td className="py-2.5 text-right">
                    <div className="flex justify-end">
                      <Sparkline points={row.sparklinePoints} positive={row.positive} />
                    </div>
                  </td>
                  {/* Trade button */}
                  <td className="py-2.5 text-right pl-2">
                    <Link
                      to={`/trade?market=${row.pair.replace('/', '-')}`}
                      className="inline-block px-3 py-1 rounded-md bg-[#10b981]/10 border border-[#10b981]/30 text-[#10b981] text-xs font-semibold hover:bg-[#10b981]/20 transition-colors"
                    >
                      Trade
                    </Link>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </DashboardCard>
  )
}
