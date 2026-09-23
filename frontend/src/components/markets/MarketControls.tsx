import { Search, Star, ChevronDown } from 'lucide-react'
import type { MarketFilter } from '../../hooks/useMarkets'

interface MarketControlsProps {
  search: string
  onSearchChange: (v: string) => void
  filter: MarketFilter
  onFilterChange: (v: MarketFilter) => void
  quoteCurrency: string
  onQuoteCurrencyChange: (v: string) => void
}

/**
 * Controls row: search input, Favorites / All Markets toggle, Quote Currency selector.
 *
 * Quote Currency: currently USDT-only. The selector is built as a UI-ready control
 * for future multi-quote support — clicking it does nothing beyond visual state
 * until the backend supports additional quote assets.
 */
export default function MarketControls({
  search,
  onSearchChange,
  filter,
  onFilterChange,
  quoteCurrency,
}: MarketControlsProps) {
  return (
    <div className="flex flex-col sm:flex-row items-start sm:items-center gap-3 mb-4">
      {/* Search */}
      <div className="relative flex-1 min-w-0 max-w-sm">
        <Search
          size={13}
          className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none"
        />
        <input
          type="text"
          id="markets-search"
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Search ticker or coin, e.g. BTC, ETH, SOL..."
          aria-label="Search markets"
          className="w-full pl-9 pr-3 py-2 bg-[#0a0b0e] border border-[#1e2530] rounded-md text-xs text-slate-300 placeholder-slate-600 focus:outline-none focus:border-[#10b981]/40 transition-colors"
        />
      </div>

      {/* Filter tabs */}
      <div className="flex items-center gap-1.5">
        {/* Favorites */}
        <button
          type="button"
          id="markets-filter-favorites"
          onClick={() => onFilterChange(filter === 'favorites' ? 'all' : 'favorites')}
          aria-pressed={filter === 'favorites'}
          className={`flex items-center gap-1.5 px-3 py-2 rounded-md border text-xs font-medium transition-colors ${
            filter === 'favorites'
              ? 'bg-amber-500/10 border-amber-500/30 text-amber-400'
              : 'bg-[#0a0b0e] border-[#1e2530] text-slate-400 hover:text-[#f5f7fa] hover:border-slate-600'
          }`}
        >
          <Star
            size={12}
            className={filter === 'favorites' ? 'fill-amber-400 text-amber-400' : ''}
          />
          Favorites
        </button>

        {/* All Markets */}
        <button
          type="button"
          id="markets-filter-all"
          onClick={() => onFilterChange('all')}
          aria-pressed={filter === 'all'}
          className={`px-3 py-2 rounded-md border text-xs font-medium transition-colors ${
            filter === 'all'
              ? 'bg-[#10b981]/10 border-[#10b981]/30 text-[#10b981]'
              : 'bg-[#0a0b0e] border-[#1e2530] text-slate-400 hover:text-[#f5f7fa] hover:border-slate-600'
          }`}
        >
          All Markets
        </button>
      </div>

      {/* Spacer */}
      <div className="hidden sm:block flex-1" />

      {/* Quote Currency */}
      <div className="flex items-center gap-2">
        <span className="text-xs text-slate-500 font-medium">Quote Currency</span>
        <button
          type="button"
          id="markets-quote-currency"
          aria-label="Quote currency selector — currently USDT only"
          title="Only USDT is currently supported"
          className="flex items-center gap-1.5 px-3 py-2 bg-[#0a0b0e] border border-[#1e2530] rounded-md text-xs font-semibold text-[#f5f7fa] hover:border-slate-600 transition-colors cursor-default"
        >
          {quoteCurrency}
          <ChevronDown size={11} className="text-slate-500" />
        </button>
      </div>
    </div>
  )
}
