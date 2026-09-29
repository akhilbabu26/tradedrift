import { useState, useMemo, useEffect } from 'react'
import { Copy, Check, ChevronLeft, ChevronRight } from 'lucide-react'
import type { OrderHistoryItem, OrderFilterState } from '../../types/orders'
import OrderFilters from './OrderFilters'
import toast from 'react-hot-toast'

interface OrderHistorySectionProps {
  history: OrderHistoryItem[]
}

const PAGE_SIZE = 10
const SEVEN_DAYS_MS = 7 * 24 * 60 * 60 * 1000
const THIRTY_DAYS_MS = 30 * 24 * 60 * 60 * 1000

export default function OrderHistorySection({ history }: OrderHistorySectionProps) {
  const [filters, setFilters] = useState<OrderFilterState>({
    market: 'All Markets',
    side: 'All Sides',
    timeRange: 'Last 7 Days',
    searchQuery: '',
  })

  const [currentPage, setCurrentPage] = useState<number>(1)
  const [copiedId, setCopiedId] = useState<string | null>(null)

  // ── 1. Reset pagination to Page 1 whenever any filter changes ──────────────
  useEffect(() => {
    setCurrentPage(1)
  }, [filters.market, filters.side, filters.timeRange, filters.searchQuery])

  // ── 2. Copy full order ID to clipboard ────────────────────────────────────
  const handleCopyOrderId = (fullId: string) => {
    if (navigator.clipboard) {
      navigator.clipboard.writeText(fullId)
      setCopiedId(fullId)
      toast.success('Order ID copied to clipboard')
      setTimeout(() => setCopiedId(null), 2000)
    }
  }

  // ── 3. Combined Filter Logic ──────────────────────────────────────────────
  const filteredHistory = useMemo(() => {
    const now = Date.now()

    return history.filter((item) => {
      // 1. Market filter (normalizes slashes and dashes e.g. "BTC/USDT" vs "BTC-USDT")
      if (filters.market && filters.market !== 'All Markets') {
        const itemMarket = item.pair.replace('-', '/').toUpperCase()
        const targetMarket = filters.market.replace('-', '/').toUpperCase()
        if (itemMarket !== targetMarket) {
          return false
        }
      }

      // 2. Side filter (BUY vs SELL)
      if (filters.side && filters.side !== 'All Sides') {
        if (item.side.toUpperCase() !== filters.side.toUpperCase()) {
          return false
        }
      }

      // 3. Date range filter based on actual timestamp
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

      // 4. Search query (case-insensitive across pair, orderId, type, side)
      const q = filters.searchQuery.trim().toLowerCase()
      if (q) {
        const matchPair = item.pair.toLowerCase().includes(q)
        const matchOrderId = item.orderId.toLowerCase().includes(q)
        const matchType = item.type.toLowerCase().includes(q)
        const matchSide = item.side.toLowerCase().includes(q)
        const matchStatus = item.status.toLowerCase().includes(q)

        if (!matchPair && !matchOrderId && !matchType && !matchSide && !matchStatus) {
          return false
        }
      }

      return true
    })
  }, [history, filters])

  // ── 4. Pagination Calculation ─────────────────────────────────────────────
  const totalRecords = filteredHistory.length
  const totalPages = Math.max(1, Math.ceil(totalRecords / PAGE_SIZE))
  const validCurrentPage = Math.min(Math.max(1, currentPage), totalPages)

  const startIndex = (validCurrentPage - 1) * PAGE_SIZE
  const endIndex = Math.min(startIndex + PAGE_SIZE, totalRecords)
  const paginatedHistory = filteredHistory.slice(startIndex, endIndex)

  return (
    <div
      id="order-history-section"
      className="rounded-xl border border-[#1e2530] bg-[#111318] p-4 sm:p-5 flex flex-col gap-4 shadow-sm"
    >
      {/* ── Section Header + Filters ───────────────────────────────────────── */}
      <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-4 border-b border-[#1e2530] pb-4">
        <h2 className="text-base font-bold text-[#f5f7fa]" style={{ color: '#f5f7fa' }}>
          Order History
        </h2>

        <OrderFilters
          filters={filters}
          onChange={setFilters}
          showTimeRange
          searchPlaceholder="Search orders..."
        />
      </div>

      {/* ── Table ──────────────────────────────────────────────────────────── */}
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs min-w-[960px]">
          <thead>
            <tr className="border-b border-[#1e2530]/80 text-slate-400 uppercase tracking-wider text-[11px] font-semibold">
              <th className="py-2.5 px-3">Time</th>
              <th className="py-2.5 px-3">Pair</th>
              <th className="py-2.5 px-3">Type</th>
              <th className="py-2.5 px-3">Side</th>
              <th className="py-2.5 px-3 text-right">Avg. Filled Price</th>
              <th className="py-2.5 px-3 text-right">Executed / Total</th>
              <th className="py-2.5 px-3 text-right">Total Value (USDT)</th>
              <th className="py-2.5 px-3">Status</th>
              <th className="py-2.5 px-3 text-right">Order ID</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1e2530]/50 font-sans">
            {totalRecords === 0 ? (
              <tr>
                <td colSpan={9} className="py-12 text-center">
                  <div className="flex flex-col items-center justify-center">
                    <p className="text-sm font-semibold text-[#f5f7fa]" style={{ color: '#f5f7fa' }}>
                      No order history found
                    </p>
                    <p className="text-xs text-slate-400 mt-1">
                      Try changing your filters or search query.
                    </p>
                  </div>
                </td>
              </tr>
            ) : (
              paginatedHistory.map((item) => {
                const isBuy = item.side === 'BUY'
                const isCopied = copiedId === item.orderId

                // Visual truncation of order ID (ord_7f2a9c...)
                const displayOrderId =
                  item.orderId.length > 12
                    ? `${item.orderId.substring(0, 10)}...`
                    : item.orderId

                return (
                  <tr key={item.id} className="hover:bg-white/[0.02] transition-colors">
                    {/* Time */}
                    <td className="py-3 px-3 text-slate-400 font-mono text-[11px] whitespace-nowrap">
                      {item.time}
                    </td>

                    {/* Pair */}
                    <td className="py-3 px-3 font-bold text-xs text-[#f5f7fa] whitespace-nowrap">
                      {item.pair}
                    </td>

                    {/* Type */}
                    <td className="py-3 px-3 text-slate-300 font-medium text-xs whitespace-nowrap">
                      {item.type}
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
                        {item.side}
                      </span>
                    </td>

                    {/* Avg Filled Price */}
                    <td className="py-3 px-3 text-right font-mono text-xs font-semibold text-[#f5f7fa] whitespace-nowrap">
                      {item.avgFilledPrice}
                    </td>

                    {/* Executed / Total */}
                    <td className="py-3 px-3 text-right font-mono text-xs text-slate-300 whitespace-nowrap">
                      {item.executedTotal}
                    </td>

                    {/* Total Value (USDT) */}
                    <td className="py-3 px-3 text-right font-mono text-xs font-bold text-[#f5f7fa] whitespace-nowrap">
                      {item.totalValueUSDT}
                    </td>

                    {/* Status Badge */}
                    <td className="py-3 px-3 whitespace-nowrap">
                      {item.status === 'Filled' ? (
                        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-[#10b981]/10 border border-[#10b981]/25 text-[#10b981] text-[11px] font-semibold">
                          <span className="w-1.5 h-1.5 rounded-full bg-[#10b981]" />
                          Filled
                        </span>
                      ) : item.status === 'Canceled' ? (
                        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-white/[0.04] border border-white/10 text-slate-400 text-[11px] font-semibold">
                          <span className="w-1.5 h-1.5 rounded-full bg-slate-400" />
                          Canceled
                        </span>
                      ) : item.status === 'Partially Filled' ? (
                        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-amber-500/10 border border-amber-500/20 text-amber-400 text-[11px] font-semibold">
                          <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
                          Partially Filled
                        </span>
                      ) : item.status === 'Rejected' ? (
                        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-[#ef4444]/10 border border-[#ef4444]/25 text-[#ef4444] text-[11px] font-semibold">
                          <span className="w-1.5 h-1.5 rounded-full bg-[#ef4444]" />
                          Rejected
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-amber-500/10 border border-amber-500/20 text-amber-400 text-[11px] font-semibold">
                          <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
                          {item.status}
                        </span>
                      )}
                    </td>

                    {/* Order ID + Copy Action */}
                    <td className="py-3 px-3 text-right whitespace-nowrap">
                      <div className="inline-flex items-center gap-1.5 font-mono text-xs text-slate-400 justify-end">
                        <span title={item.orderId} className="cursor-default">
                          {displayOrderId}
                        </span>
                        <button
                          type="button"
                          onClick={() => handleCopyOrderId(item.orderId)}
                          className="p-1 rounded hover:bg-white/10 text-slate-400 hover:text-[#f5f7fa] transition-colors cursor-pointer"
                          aria-label={`Copy order ID ${item.orderId}`}
                          title="Copy full Order ID"
                        >
                          {isCopied ? (
                            <Check size={12} className="text-[#10b981]" />
                          ) : (
                            <Copy size={12} />
                          )}
                        </button>
                      </div>
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
