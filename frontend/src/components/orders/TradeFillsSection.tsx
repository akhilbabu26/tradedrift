import { useState, useMemo, useEffect } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import type { TradeFillItem, OrderFilterState } from '../../types/orders'
import OrderFilters from './OrderFilters'

interface TradeFillsSectionProps {
  fills: TradeFillItem[]
}

const PAGE_SIZE = 10
const SEVEN_DAYS_MS = 7 * 24 * 60 * 60 * 1000
const THIRTY_DAYS_MS = 30 * 24 * 60 * 60 * 1000

export default function TradeFillsSection({ fills }: TradeFillsSectionProps) {
  const [filters, setFilters] = useState<OrderFilterState>({
    market: 'All Markets',
    side: 'All Sides',
    timeRange: 'Last 7 Days',
    searchQuery: '',
  })
  const [currentPage, setCurrentPage] = useState<number>(1)

  // Reset to page 1 whenever filters change
  useEffect(() => {
    setCurrentPage(1)
  }, [filters.market, filters.side, filters.timeRange, filters.searchQuery])

  // Filter trade fills
  const filteredFills = useMemo(() => {
    const now = Date.now()

    return fills.filter((item) => {
      // Market filter (normalizes slashes and dashes e.g. "BTC/USDT" vs "BTC-USDT")
      if (filters.market && filters.market !== 'All Markets') {
        const itemMarket = item.pair.replace('-', '/').toUpperCase()
        const targetMarket = filters.market.replace('-', '/').toUpperCase()
        if (itemMarket !== targetMarket) {
          return false
        }
      }

      // Side filter
      if (filters.side && filters.side !== 'All Sides') {
        if (item.side.toUpperCase() !== filters.side.toUpperCase()) {
          return false
        }
      }

      // Time range filter
      const itemTs = item.timestamp || 0
      if (filters.timeRange === 'Last 7 Days') {
        if (itemTs > 0 && now - itemTs > SEVEN_DAYS_MS) {
          return false
        }
      } else if (filters.timeRange === 'Last 30 Days') {
        if (itemTs > 0 && now - itemTs > THIRTY_DAYS_MS) {
          return false
        }
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

  // Pagination calculation
  const totalRecords = filteredFills.length
  const totalPages = Math.max(1, Math.ceil(totalRecords / PAGE_SIZE))
  const validCurrentPage = Math.min(currentPage, totalPages)
  const startIndex = (validCurrentPage - 1) * PAGE_SIZE
  const endIndex = Math.min(startIndex + PAGE_SIZE, totalRecords)
  const paginatedFills = filteredFills.slice(startIndex, endIndex)

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
            {paginatedFills.length === 0 ? (
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
              paginatedFills.map((fill) => {
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

      {/* ── Pagination Controls ────────────────────────────────────────────── */}
      {totalRecords > 0 && (
        <div className="flex flex-col sm:flex-row items-center justify-between gap-3 pt-4 border-t border-[#1e2530] text-xs">
          {/* Result Summary */}
          <span className="text-slate-400 font-medium">
            Showing <span className="text-[#f5f7fa] font-semibold">{startIndex + 1}</span>–
            <span className="text-[#f5f7fa] font-semibold">{endIndex}</span> of{' '}
            <span className="text-[#f5f7fa] font-semibold">{totalRecords}</span>
          </span>

          {/* Pagination Buttons */}
          <div className="flex items-center gap-1.5 select-none">
            <button
              type="button"
              disabled={validCurrentPage <= 1}
              onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
              className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md border border-[#1e2530] bg-[#0a0b0e] text-slate-300 hover:text-[#f5f7fa] hover:border-slate-600 disabled:opacity-30 disabled:cursor-not-allowed transition-colors font-medium cursor-pointer"
            >
              <ChevronLeft size={13} />
              <span>Previous</span>
            </button>

            {Array.from({ length: totalPages }, (_, i) => i + 1).map((pageNum) => {
              const isActive = pageNum === validCurrentPage
              return (
                <button
                  key={pageNum}
                  type="button"
                  onClick={() => setCurrentPage(pageNum)}
                  className={`w-7 h-7 rounded-md text-xs font-mono font-semibold transition-colors cursor-pointer flex items-center justify-center ${
                    isActive
                      ? 'bg-[#10b981] text-[#0a0b0e] shadow-sm font-bold'
                      : 'border border-[#1e2530] bg-[#0a0b0e] text-slate-400 hover:text-[#f5f7fa] hover:border-slate-600'
                  }`}
                >
                  {pageNum}
                </button>
              )
            })}

            <button
              type="button"
              disabled={validCurrentPage >= totalPages}
              onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
              className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md border border-[#1e2530] bg-[#0a0b0e] text-slate-300 hover:text-[#f5f7fa] hover:border-slate-600 disabled:opacity-30 disabled:cursor-not-allowed transition-colors font-medium cursor-pointer"
            >
              <span>Next</span>
              <ChevronRight size={13} />
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

