import { useState, useMemo } from 'react'
import { Search } from 'lucide-react'
import HoldingsTable from './HoldingsTable'
import type { PortfolioHolding, HoldingsFilterTab } from '../../types/portfolio'
import { toDecimal } from '../../utils/decimal'

interface Props {
  holdings: PortfolioHolding[]
  loading: boolean
}

/**
 * Holdings section — search, filters (All / In Profit / In Loss),
 * small-balance toggle, then the Holdings table.
 *
 * All text uses TradeDrift light tokens (text-[#f5f7fa], text-[#94a3b8], etc).
 * No text-black, text-gray-900, text-slate-900, or text-zinc-900 classes.
 *
 * NOTE: AVAX does not appear here unless the live API passes an AVAX holding.
 * The mock data (BTC, ETH, SOL, BNB, USDT) never includes AVAX.
 */
export default function HoldingsSection({ holdings, loading }: Props) {
  const [search, setSearch] = useState('')
  const [activeTab, setActiveTab] = useState<HoldingsFilterTab>('all')
  const [hideSmall, setHideSmall] = useState(false)

  const TABS: { id: HoldingsFilterTab; label: string }[] = [
    { id: 'all', label: 'All' },
    { id: 'profit', label: 'In Profit' },
    { id: 'loss', label: 'In Loss' },
  ]

  const filtered = useMemo(() => {
    let rows = holdings

    const safeDec = (v: string) => {
      try {
        const sanitized = String(v || '0').replace(/[^\d.\-]/g, '').replace(/^-?$/, '0')
        return toDecimal(sanitized)
      } catch {
        return toDecimal(0)
      }
    }

    // 1. Hide small balances (market value < $10)
    if (hideSmall) {
      rows = rows.filter((h) => safeDec(h.marketValue).gte(10))
    }

    // 2. Tab filter
    if (activeTab === 'profit') {
      rows = rows.filter((h) => safeDec(h.unrealizedPnl).gt(0))
    } else if (activeTab === 'loss') {
      rows = rows.filter((h) => safeDec(h.unrealizedPnl).lt(0))
    }

    // 3. Search by asset symbol or name
    if (search.trim()) {
      const q = search.trim().toLowerCase()
      rows = rows.filter(
        (h) =>
          h.asset.toLowerCase().includes(q) ||
          h.name.toLowerCase().includes(q)
      )
    }

    return rows
  }, [holdings, search, activeTab, hideSmall])

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl overflow-hidden">
      {/* Section header */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-5 py-4 border-b border-[#1e2530]">
        <h2 className="text-sm font-semibold text-[#f5f7fa]">
          Holdings
          {holdings.length > 0 && (
            <span className="ml-2 text-xs text-[#475569] font-normal">
              ({holdings.length} asset{holdings.length !== 1 ? 's' : ''})
            </span>
          )}
        </h2>

        <div className="flex flex-wrap items-center gap-3">
          {/* Search */}
          <div className="relative">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-[#475569] pointer-events-none" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search asset..."
              className="pl-8 pr-3 py-1.5 text-xs bg-[#0a0b0e] border border-[#1e2530] rounded-lg text-[#f5f7fa] placeholder-[#475569] focus:outline-none focus:border-[#10b981]/50 w-40 transition-colors"
            />
          </div>

          {/* Hide small balances */}
          <label className="flex items-center gap-2 cursor-pointer select-none group">
            <input
              type="checkbox"
              checked={hideSmall}
              onChange={(e) => setHideSmall(e.target.checked)}
              className="w-3.5 h-3.5 rounded border-[#475569] bg-[#0a0b0e] accent-[#10b981] cursor-pointer"
            />
            <span className="text-xs text-[#64748b] group-hover:text-[#94a3b8] transition-colors whitespace-nowrap">
              Hide small balances (&lt; $10)
            </span>
          </label>

          {/* Filter tabs */}
          <div className="flex items-center gap-1 bg-[#0a0b0e] border border-[#1e2530] rounded-lg p-1">
            {TABS.map((tab) => (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id)}
                className={`px-2.5 py-1 rounded-md text-[11px] font-medium transition-all ${
                  activeTab === tab.id
                    ? 'bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30'
                    : 'text-[#64748b] hover:text-[#94a3b8]'
                }`}
              >
                {tab.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* Table */}
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="flex items-center gap-2">
            <div className="w-4 h-4 border-2 border-[#10b981] border-t-transparent rounded-full animate-spin" />
            <span className="text-xs text-[#475569]">Loading holdings...</span>
          </div>
        </div>
      ) : (
        <HoldingsTable holdings={filtered} />
      )}
    </div>
  )
}
