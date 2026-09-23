import { useState, useMemo } from 'react'
import { X } from 'lucide-react'
import type { OpenOrderItem, OrderFilterState } from '../../types/orders'
import OrderFilters from './OrderFilters'

interface OpenOrdersSectionProps {
  orders: OpenOrderItem[]
  cancellingId: string | null
  onCancel: (id: string) => void
  onScrollToSection: (sectionId: string) => void
}

export default function OpenOrdersSection({
  orders,
  cancellingId,
  onCancel,
  onScrollToSection,
}: OpenOrdersSectionProps) {
  const [filters, setFilters] = useState<OrderFilterState>({
    market: 'All Markets',
    side: 'All Sides',
    searchQuery: '',
  })

  // Filter open orders locally
  const filteredOrders = useMemo(() => {
    return orders.filter((o) => {
      // Market filter
      if (filters.market !== 'All Markets' && o.pair !== filters.market) {
        return false
      }
      // Side filter
      if (filters.side !== 'All Sides' && o.side !== filters.side) {
        return false
      }
      // Search query (matches pair, type, price, or id)
      if (filters.searchQuery.trim()) {
        const q = filters.searchQuery.trim().toLowerCase()
        const matchPair = o.pair.toLowerCase().includes(q)
        const matchId = o.id.toLowerCase().includes(q)
        const matchAmount = o.amount.toLowerCase().includes(q)
        if (!matchPair && !matchId && !matchAmount) {
          return false
        }
      }
      return true
    })
  }, [orders, filters])

  return (
    <div
      id="open-orders-section"
      className="rounded-xl border border-[#1e2530] bg-[#111318] p-4 sm:p-5 flex flex-col gap-4 shadow-sm"
    >
      {/* ── Top Tabs + Filter Bar ─────────────────────────────────────────── */}
      <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-4 border-b border-[#1e2530] pb-4">
        {/* Navigation Tabs (Scroll triggers) */}
        <div className="flex items-center gap-1.5 select-none overflow-x-auto pb-1 lg:pb-0 scrollbar-none">
          <button
            type="button"
            className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30 text-xs font-bold cursor-default"
          >
            <span>Open Orders</span>
            <span className="w-5 h-5 rounded-full bg-[#10b981] text-[#0a0b0e] text-[10px] font-black flex items-center justify-center">
              {orders.length}
            </span>
          </button>

          <button
            type="button"
            onClick={() => onScrollToSection('order-history-section')}
            className="px-3 py-1.5 rounded-lg text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 text-xs font-semibold transition-colors cursor-pointer"
          >
            Order History
          </button>

          <button
            type="button"
            onClick={() => onScrollToSection('trade-fills-section')}
            className="px-3 py-1.5 rounded-lg text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 text-xs font-semibold transition-colors cursor-pointer"
          >
            Trade Fills
          </button>
        </div>

        {/* Filter Bar */}
        <OrderFilters
          filters={filters}
          onChange={setFilters}
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
              <th className="py-2.5 px-3 text-right">Price (USDT)</th>
              <th className="py-2.5 px-3 text-right">Amount</th>
              <th className="py-2.5 px-3 text-right">Filled / Remaining</th>
              <th className="py-2.5 px-3">Progress</th>
              <th className="py-2.5 px-3">Status</th>
              <th className="py-2.5 px-3 text-center">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1e2530]/50 font-sans">
            {filteredOrders.length === 0 ? (
              <tr>
                <td colSpan={10} className="py-10 text-center">
                  <div className="flex flex-col items-center justify-center">
                    <p className="text-xs text-slate-400 font-medium">No open orders</p>
                    <p className="text-[11px] text-slate-500 mt-1">
                      {filters.searchQuery || filters.market !== 'All Markets' || filters.side !== 'All Sides'
                        ? 'No orders match your filter criteria'
                        : 'Your open orders will appear here'}
                    </p>
                  </div>
                </td>
              </tr>
            ) : (
              filteredOrders.map((o) => {
                const isBuy = o.side === 'BUY'
                const isCancelling = cancellingId === o.id

                return (
                  <tr key={o.id} className="hover:bg-white/[0.02] transition-colors">
                    {/* Time */}
                    <td className="py-3 px-3 text-slate-400 font-mono text-[11px] whitespace-nowrap">
                      {o.time}
                    </td>

                    {/* Pair */}
                    <td className="py-3 px-3 font-bold text-xs text-[#f5f7fa] whitespace-nowrap">
                      {o.pair}
                    </td>

                    {/* Type */}
                    <td className="py-3 px-3 text-slate-300 font-medium text-xs whitespace-nowrap">
                      {o.type}
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
                        {o.side}
                      </span>
                    </td>

                    {/* Price */}
                    <td className="py-3 px-3 text-right font-mono text-xs font-semibold text-[#f5f7fa] whitespace-nowrap">
                      {o.price}
                    </td>

                    {/* Amount */}
                    <td className="py-3 px-3 text-right font-mono text-xs text-slate-200 whitespace-nowrap">
                      {o.amount}
                    </td>

                    {/* Filled / Remaining */}
                    <td className="py-3 px-3 text-right font-mono text-xs text-slate-300 whitespace-nowrap">
                      {o.filledRemaining}
                    </td>

                    {/* Progress Bar + % */}
                    <td className="py-3 px-3 whitespace-nowrap">
                      <div className="flex items-center gap-2">
                        <div className="w-16 sm:w-20 h-1.5 rounded-full bg-[#1e2530] overflow-hidden">
                          <div
                            className="h-full bg-gradient-to-r from-[#10b981] to-[#06b6d4] transition-all duration-300"
                            style={{ width: `${o.progress}%` }}
                          />
                        </div>
                        <span className="font-mono text-[11px] text-slate-400 min-w-[28px]">
                          {o.progress}%
                        </span>
                      </div>
                    </td>

                    {/* Status */}
                    <td className="py-3 px-3 whitespace-nowrap">
                      <span className="inline-flex items-center gap-1.5 text-xs font-semibold text-[#10b981]">
                        <span className="w-1.5 h-1.5 rounded-full bg-[#10b981]" />
                        {o.status}
                      </span>
                    </td>

                    {/* Action: Cancel button */}
                    <td className="py-3 px-3 text-center whitespace-nowrap">
                      <button
                        type="button"
                        disabled={isCancelling}
                        onClick={() => onCancel(o.id)}
                        className="inline-flex items-center gap-1 px-2.5 py-1 rounded border border-[#ef4444]/30 bg-[#ef4444]/10 hover:bg-[#ef4444]/20 disabled:opacity-50 text-[#ef4444] text-xs font-semibold transition-colors cursor-pointer"
                        title="Cancel this order"
                      >
                        {isCancelling ? (
                          <span className="w-3 h-3 border-2 border-[#ef4444] border-t-transparent rounded-full animate-spin" />
                        ) : (
                          <X size={12} strokeWidth={2.5} />
                        )}
                        <span>Cancel</span>
                      </button>
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
