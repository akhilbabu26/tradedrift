import { useState, useMemo } from 'react'
import type { TradeFillItem, OrderFilterState } from '../../types/orders'
import OrderFilters from './OrderFilters'

interface TradeFillsSectionProps {
  fills: TradeFillItem[]
}

export default function TradeFillsSection({ fills }: TradeFillsSectionProps) {
  const [filters, setFilters] = useState<OrderFilterState>({
    market: 'All Markets',
    side: 'All Sides',
    timeRange: 'Last 7 Days',
    searchQuery: '',
  })

  // Filter trade fills
  const filteredFills = useMemo(() => {
    return fills.filter((item) => {
      // Market filter
      if (filters.market !== 'All Markets' && item.pair !== filters.market) {
        return false
      }
      // Side filter
      if (filters.side !== 'All Sides' && item.side !== filters.side) {
        return false
      }
      // Search query (matches pair, tradeId, or amount)
      if (filters.searchQuery.trim()) {
        const q = filters.searchQuery.trim().toLowerCase()
        const matchPair = item.pair.toLowerCase().includes(q)
        const matchId = item.tradeId.toLowerCase().includes(q)
        const matchAmount = item.filledAmount.toLowerCase().includes(q)
        if (!matchPair && !matchId && !matchAmount) {
          return false
        }
      }
      return true
    })
  }, [fills, filters])

  return (
    <div
      id="trade-fills-section"
      className="rounded-xl border border-[#1e2530] bg-[#111318] p-4 sm:p-5 flex flex-col gap-4 shadow-sm"
    >
      {/* ── Section Header + Filters ───────────────────────────────────────── */}
      <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-4 border-b border-[#1e2530] pb-4">
        <h2 className="text-base font-bold text-[#f5f7fa]" style={{ color: '#f5f7fa' }}>
          Trade Fills
        </h2>

        <OrderFilters
          filters={filters}
          onChange={setFilters}
          showTimeRange
          searchPlaceholder="Search trades..."
        />
      </div>

      {/* ── Table ──────────────────────────────────────────────────────────── */}
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs min-w-[960px]">
          <thead>
            <tr className="border-b border-[#1e2530]/80 text-slate-400 uppercase tracking-wider text-[11px] font-semibold">
              <th className="py-2.5 px-3">Time</th>
              <th className="py-2.5 px-3">Trade ID</th>
              <th className="py-2.5 px-3">Pair</th>
              <th className="py-2.5 px-3">Side</th>
              <th className="py-2.5 px-3 text-right">Execution Price</th>
              <th className="py-2.5 px-3 text-right">Filled Amount</th>
              <th className="py-2.5 px-3 text-right">Total Cost (USDT)</th>
              <th className="py-2.5 px-3 text-right">Fee (USDT)</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1e2530]/50 font-sans">
            {filteredFills.length === 0 ? (
              <tr>
                <td colSpan={8} className="py-10 text-center">
                  <div className="flex flex-col items-center justify-center">
                    <p className="text-xs text-slate-400 font-medium">No trade fills found</p>
                    <p className="text-[11px] text-slate-500 mt-1">
                      {filters.searchQuery || filters.market !== 'All Markets' || filters.side !== 'All Sides'
                        ? 'No executions match your filter criteria'
                        : 'Your order fill history will appear here'}
                    </p>
                  </div>
                </td>
              </tr>
            ) : (
              filteredFills.map((fill) => {
                const isBuy = fill.side === 'BUY'

                return (
                  <tr key={fill.id} className="hover:bg-white/[0.02] transition-colors">
                    {/* Time */}
                    <td className="py-3 px-3 text-slate-400 font-mono text-[11px] whitespace-nowrap">
                      {fill.time}
                    </td>

                    {/* Trade ID */}
                    <td className="py-3 px-3 font-mono text-xs text-slate-300 whitespace-nowrap">
                      {fill.tradeId}
                    </td>

                    {/* Pair */}
                    <td className="py-3 px-3 font-bold text-xs text-[#f5f7fa] whitespace-nowrap">
                      {fill.pair}
                    </td>

                    {/* Side */}
                    <td className="py-3 px-3 whitespace-nowrap">
                      <span
                        className={`inline-block px-2 py-0.5 rounded text-[10px] font-bold ${
                          isBuy
                            ? 'bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30'
                            : 'bg-[#ef4444]/15 text-[#ef4444] border border-[#ef4444]/30'
                        }`}
                      >
                        {fill.side}
                      </span>
                    </td>

                    {/* Execution Price */}
                    <td className="py-3 px-3 text-right font-mono text-xs font-semibold text-[#f5f7fa] whitespace-nowrap">
                      {fill.executionPrice}
                    </td>

                    {/* Filled Amount */}
                    <td className="py-3 px-3 text-right font-mono text-xs text-slate-200 whitespace-nowrap">
                      {fill.filledAmount}
                    </td>

                    {/* Total Cost (USDT) */}
                    <td className="py-3 px-3 text-right font-mono text-xs font-bold text-[#f5f7fa] whitespace-nowrap">
                      {fill.totalCostUSDT}
                    </td>

                    {/* Fee (USDT) */}
                    <td className="py-3 px-3 text-right font-mono text-xs text-slate-400 whitespace-nowrap">
                      {fill.feeUSDT}
                    </td>
                  </tr>
                )
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
