import { useState } from 'react'
import { Copy, Check } from 'lucide-react'
import toast from 'react-hot-toast'

export interface OrderRowItem {
  id: string
  orderId: string
  marketId: string
  marketSymbol: string
  iconChar: string
  iconBg: string
  iconText: string
  side: 'BUY' | 'SELL'
  type: string
  price: string
  priceNum: number
  filledQty: number
  totalQty: number
  assetSymbol: string
  totalUsd: string
  triggerPrice?: string
  timeUtc: string
}

interface OpenOrdersTableProps {
  orders: OrderRowItem[]
  activeTab: string
  onTabChange: (tab: string) => void
  onCancelOrder: (id: string) => void
}

export default function OpenOrdersTable({
  orders,
  activeTab,
  onTabChange,
  onCancelOrder,
}: OpenOrdersTableProps) {
  const [copiedId, setCopiedId] = useState<string | null>(null)

  const copyOrderId = (id: string, text: string) => {
    navigator.clipboard.writeText(text)
    setCopiedId(id)
    toast.success('Order ID copied to clipboard')
    setTimeout(() => setCopiedId((prev) => (prev === id ? null : prev)), 2000)
  }

  const TABS = [
    { id: 'open', label: `Open Orders (${orders.length})` },
    { id: 'trigger', label: 'Trigger Orders (2)' },
    { id: 'history', label: 'Order History (250+)' },
    { id: 'fills', label: 'Trade Fills' },
  ]

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden shadow-xl select-none">
      {/* ── Order Category Tabs ── */}
      <div className="p-3 border-b border-[#1b2230] flex items-center gap-2 overflow-x-auto bg-[#07090e]/40">
        {TABS.map((tab) => {
          const isActive = activeTab === tab.id
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => onTabChange(tab.id)}
              className={`px-3.5 py-1.5 rounded-lg text-xs font-semibold transition-all ${
                isActive
                  ? 'bg-[#00e5ff]/15 text-[#00e5ff] border border-[#00e5ff]/40 shadow-[0_0_12px_rgba(0,229,255,0.15)]'
                  : 'text-slate-400 hover:text-white hover:bg-[#141a26] border border-transparent'
              }`}
            >
              {tab.label}
            </button>
          )
        })}
      </div>

      {/* ── Dense Open Orders Table ── */}
      <div className="overflow-x-auto custom-scrollbar">
        <table className="w-full text-xs font-mono text-left border-collapse">
          <thead>
            <tr className="border-b border-[#1b2230] bg-[#07090e]/60 text-slate-400 text-[11px] font-sans font-medium">
              <th className="py-3 px-4">Order ID</th>
              <th className="py-3 px-4">Market</th>
              <th className="py-3 px-4 text-center">Side</th>
              <th className="py-3 px-4 text-center">Type</th>
              <th className="py-3 px-4 text-right">Price ($)</th>
              <th className="py-3 px-4">Filled / Amount</th>
              <th className="py-3 px-4 text-right">Total ($)</th>
              <th className="py-3 px-4 text-center">Trigger Price</th>
              <th className="py-3 px-4">Time (UTC)</th>
              <th className="py-3 px-4 text-right">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1b2230]/60">
            {orders.map((o) => {
              const isBuy = o.side === 'BUY'
              const fillPct = o.totalQty > 0 ? (o.filledQty / o.totalQty) * 100 : 0

              return (
                <tr key={o.id} className="hover:bg-[#141a26]/60 transition-colors group">
                  {/* Order ID with Copy */}
                  <td className="py-3.5 px-4 font-mono">
                    <div className="flex items-center gap-1.5">
                      <span className="text-[#00e5ff] hover:underline cursor-pointer">
                        {o.orderId}
                      </span>
                      <button
                        type="button"
                        onClick={() => copyOrderId(o.id, o.orderId)}
                        aria-label="Copy Order ID"
                        className="text-slate-500 hover:text-slate-300 p-0.5 rounded transition-colors"
                      >
                        {copiedId === o.id ? (
                          <Check size={12} className="text-[#00e676]" />
                        ) : (
                          <Copy size={12} />
                        )}
                      </button>
                    </div>
                  </td>

                  {/* Market (Coin Badge + Pair) */}
                  <td className="py-3.5 px-4 font-sans">
                    <div className="flex items-center gap-2.5">
                      <div
                        className={`w-6 h-6 rounded-full flex items-center justify-center font-bold text-[10px] flex-shrink-0 border ${o.iconBg} ${o.iconText}`}
                      >
                        {o.iconChar}
                      </div>
                      <span className="font-bold text-white text-xs tracking-tight">
                        {o.marketSymbol}
                      </span>
                    </div>
                  </td>

                  {/* Side */}
                  <td className="py-3.5 px-4 text-center font-sans">
                    <span
                      className={`inline-block px-2 py-0.5 rounded text-[10px] font-bold ${
                        isBuy
                          ? 'bg-[#00e676]/10 text-[#00e676] border border-[#00e676]/20'
                          : 'bg-[#ff3366]/10 text-[#ff3366] border border-[#ff3366]/20'
                      }`}
                    >
                      {o.side}
                    </span>
                  </td>

                  {/* Type */}
                  <td className="py-3.5 px-4 text-center text-slate-300 font-sans text-[11px]">
                    {o.type}
                  </td>

                  {/* Price */}
                  <td className="py-3.5 px-4 text-right font-bold text-white">
                    {o.price}
                  </td>

                  {/* Filled / Amount + Progress Bar */}
                  <td className="py-3.5 px-4 min-w-[170px]">
                    <div className="flex items-center justify-between text-[11px] font-mono mb-1">
                      <span className="text-white">
                        {o.filledQty.toFixed(4)} / {o.totalQty.toFixed(4)} {o.assetSymbol}
                      </span>
                      <span className="text-slate-400 text-[10px]">
                        {fillPct.toFixed(2)}%
                      </span>
                    </div>
                    {/* Thin progress bar */}
                    <div className="w-full h-1 bg-[#141a26] rounded-full overflow-hidden">
                      <div
                        className="h-full bg-[#00e5ff] rounded-full transition-all duration-300"
                        style={{ width: `${Math.min(100, fillPct)}%` }}
                      />
                    </div>
                  </td>

                  {/* Total ($) */}
                  <td className="py-3.5 px-4 text-right font-semibold text-slate-200">
                    {o.totalUsd}
                  </td>

                  {/* Trigger Price */}
                  <td className="py-3.5 px-4 text-center text-slate-500">
                    {o.triggerPrice || '—'}
                  </td>

                  {/* Time (UTC) */}
                  <td className="py-3.5 px-4 text-slate-400 text-[11px]">
                    {o.timeUtc}
                  </td>

                  {/* Action (Cancel) */}
                  <td className="py-3.5 px-4 text-right font-sans">
                    <button
                      type="button"
                      onClick={() => onCancelOrder(o.id)}
                      className="px-2.5 py-1 rounded-lg text-[11px] font-bold text-[#ff3366] bg-[#ff3366]/10 border border-[#ff3366]/30 hover:bg-[#ff3366]/20 hover:border-[#ff3366]/60 transition-all shadow-[0_0_8px_rgba(255,51,102,0.15)] active:scale-95"
                    >
                      Cancel
                    </button>
                  </td>
                </tr>
              )
            })}

            {orders.length === 0 && (
              <tr>
                <td colSpan={10} className="py-8 text-center text-slate-500 font-sans">
                  No active open orders found matching the filter criteria.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
