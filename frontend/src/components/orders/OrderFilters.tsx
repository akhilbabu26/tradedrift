import { Search, ChevronDown } from 'lucide-react'
import type { OrderFilterState } from '../../types/orders'

interface OrderFiltersProps {
  filters: OrderFilterState
  onChange: (newFilters: OrderFilterState) => void
  showTimeRange?: boolean
  searchPlaceholder?: string
}

export const MARKET_OPTIONS = ['All Markets', 'BTC/USDT', 'ETH/USDT', 'SOL/USDT']
export const SIDE_OPTIONS = ['All Sides', 'BUY', 'SELL']
export const TIME_OPTIONS = ['Last 7 Days', 'Last 30 Days', 'All Time']

export default function OrderFilters({
  filters,
  onChange,
  showTimeRange = false,
  searchPlaceholder = 'Search orders...',
}: OrderFiltersProps) {
  return (
    <div className="flex items-center gap-2.5 flex-wrap">
      {/* Market Selector */}
      <div className="relative">
        <select
          value={filters.market}
          onChange={(e) => onChange({ ...filters, market: e.target.value })}
          className="appearance-none bg-[#0a0b0e] border border-[#1e2530] rounded-lg pl-3 pr-8 py-1.5 text-xs font-medium text-slate-300 hover:text-[#f5f7fa] hover:border-slate-600 focus:outline-none focus:border-[#10b981]/50 transition-colors cursor-pointer"
        >
          {MARKET_OPTIONS.map((m) => (
            <option key={m} value={m} className="bg-[#111318] text-slate-200">
              {m}
            </option>
          ))}
        </select>
        <ChevronDown
          size={13}
          className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none"
        />
      </div>

      {/* Side Selector */}
      <div className="relative">
        <select
          value={filters.side}
          onChange={(e) => onChange({ ...filters, side: e.target.value })}
          className="appearance-none bg-[#0a0b0e] border border-[#1e2530] rounded-lg pl-3 pr-8 py-1.5 text-xs font-medium text-slate-300 hover:text-[#f5f7fa] hover:border-slate-600 focus:outline-none focus:border-[#10b981]/50 transition-colors cursor-pointer"
        >
          {SIDE_OPTIONS.map((s) => (
            <option key={s} value={s} className="bg-[#111318] text-slate-200">
              {s}
            </option>
          ))}
        </select>
        <ChevronDown
          size={13}
          className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none"
        />
      </div>

      {/* Time Range Selector (Optional) */}
      {showTimeRange && (
        <div className="relative">
          <select
            value={filters.timeRange || 'Last 7 Days'}
            onChange={(e) => onChange({ ...filters, timeRange: e.target.value })}
            className="appearance-none bg-[#0a0b0e] border border-[#1e2530] rounded-lg pl-3 pr-8 py-1.5 text-xs font-medium text-slate-300 hover:text-[#f5f7fa] hover:border-slate-600 focus:outline-none focus:border-[#10b981]/50 transition-colors cursor-pointer"
          >
            {TIME_OPTIONS.map((t) => (
              <option key={t} value={t} className="bg-[#111318] text-slate-200">
                {t}
              </option>
            ))}
          </select>
          <ChevronDown
            size={13}
            className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none"
          />
        </div>
      )}

      {/* Search Input */}
      <div className="relative flex items-center">
        <Search size={13} className="absolute left-2.5 text-slate-500 pointer-events-none" />
        <input
          type="text"
          value={filters.searchQuery}
          onChange={(e) => onChange({ ...filters, searchQuery: e.target.value })}
          placeholder={searchPlaceholder}
          className="w-40 sm:w-52 pl-8 pr-3 py-1.5 bg-[#0a0b0e] border border-[#1e2530] rounded-lg text-xs text-[#f5f7fa] placeholder-slate-500 focus:outline-none focus:border-[#10b981]/50 transition-colors"
        />
      </div>
    </div>
  )
}
