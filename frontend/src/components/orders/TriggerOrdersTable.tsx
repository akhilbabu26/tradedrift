import { useState } from 'react'
import { Copy, Check } from 'lucide-react'
import toast from 'react-hot-toast'

export interface TriggerOrderItem {
  id: string
  orderId: string
  marketSymbol: string
  iconChar: string
  iconBg: string
  iconText: string
  side: 'BUY' | 'SELL'
  type: string
  triggerPrice: string
  price: string
  amountStr: string
  totalUsd: string
  timeUtc: string
  status: 'ACTIVE' | 'TRIGGERED' | 'CANCELLED'
}

interface TriggerOrdersTableProps {
  triggerOrders: TriggerOrderItem[]
  onCancelTrigger: (id: string) => void
}

export default function TriggerOrdersTable({
  triggerOrders,
  onCancelTrigger,
}: TriggerOrdersTableProps) {
  const [copiedId, setCopiedId] = useState<string | null>(null)

  const copyOrderId = (id: string, text: string) => {
    navigator.clipboard.writeText(text)
    setCopiedId(id)
    toast.success('Trigger Order ID copied to clipboard')
    setTimeout(() => setCopiedId((prev) => (prev === id ? null : prev)), 2000)
  }

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden shadow-xl select-none">
      {/* ── Section Header ── */}
      <div className="p-4 border-b border-[#1b2230] bg-[#07090e]/40">
        <h2 className="text-sm font-bold text-white font-sans tracking-tight">
          Trigger Orders ({triggerOrders.length})
        </h2>
      </div>

      {/* ── Dense Trigger Table ── */}
      <div className="overflow-x-auto custom-scrollbar">
        <table className="w-full text-xs font-mono text-left border-collapse">
          <thead>
            <tr className="border-b border-[#1b2230] bg-[#07090e]/60 text-slate-400 text-[11px] font-sans font-medium">
              <th className="py-3 px-4">Order ID</th>
              <th className="py-3 px-4">Market</th>
              <th className="py-3 px-4 text-center">Side</th>
              <th className="py-3 px-4 text-center">Type</th>
              <th className="py-3 px-4 text-right">Trigger Price ($)</th>
              <th className="py-3 px-4 text-right">Price ($)</th>
              <th className="py-3 px-4 text-right">Amount</th>
              <th className="py-3 px-4 text-right">Total ($)</th>
              <th className="py-3 px-4">Time (UTC)</th>
              <th className="py-3 px-4 text-center">Status</th>
              <th className="py-3 px-4 text-right">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1b2230]/60">
            {triggerOrders.map((t) => {
              const isBuy = t.side === 'BUY'

              return (
                <tr key={t.id} className="hover:bg-[#141a26]/60 transition-colors group">
                  {/* Order ID with Copy */}
                  <td className="py-3.5 px-4 font-mono">
                    <div className="flex items-center gap-1.5">
                      <span className="text-[#00e5ff] hover:underline cursor-pointer">
                        {t.orderId}
                      </span>
                      <button
                        type="button"
                        onClick={() => copyOrderId(t.id, t.orderId)}
                        aria-label="Copy Order ID"
                        className="text-slate-500 hover:text-slate-300 p-0.5 rounded transition-colors"
                      >
                        {copiedId === t.id ? (
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
                        className={`w-6 h-6 rounded-full flex items-center justify-center font-bold text-[10px] flex-shrink-0 border ${t.iconBg} ${t.iconText}`}
                      >
                        {t.iconChar}
                      </div>
                      <span className="font-bold text-white text-xs tracking-tight">
                        {t.marketSymbol}
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
                      {t.side}
                    </span>
                  </td>

                  {/* Type */}
                  <td className="py-3.5 px-4 text-center text-slate-300 font-sans text-[11px]">
                    {t.type}
                  </td>

                  {/* Trigger Price */}
                  <td className="py-3.5 px-4 text-right font-mono text-slate-200">
                    {t.triggerPrice}
                  </td>

                  {/* Price */}
                  <td className="py-3.5 px-4 text-right font-bold text-white">
                    {t.price}
                  </td>

                  {/* Amount */}
                  <td className="py-3.5 px-4 text-right font-semibold text-slate-200">
                    {t.amountStr}
                  </td>

                  {/* Total ($) */}
                  <td className="py-3.5 px-4 text-right font-semibold text-slate-200">
                    {t.totalUsd}
                  </td>

                  {/* Time (UTC) */}
                  <td className="py-3.5 px-4 text-slate-400 text-[11px]">
                    {t.timeUtc}
                  </td>

                  {/* Status */}
                  <td className="py-3.5 px-4 text-center font-sans">
                    <span className="inline-block px-2 py-0.5 rounded text-[10px] font-bold bg-[#00e676]/10 text-[#00e676] border border-[#00e676]/20">
                      {t.status}
                    </span>
                  </td>

                  {/* Action (Cancel) */}
                  <td className="py-3.5 px-4 text-right font-sans">
                    <button
                      type="button"
                      onClick={() => onCancelTrigger(t.id)}
                      className="px-2.5 py-1 rounded-lg text-[11px] font-bold text-[#ff3366] bg-[#ff3366]/10 border border-[#ff3366]/30 hover:bg-[#ff3366]/20 hover:border-[#ff3366]/60 transition-all shadow-[0_0_8px_rgba(255,51,102,0.15)] active:scale-95"
                    >
                      Cancel
                    </button>
                  </td>
                </tr>
              )
            })}

            {triggerOrders.length === 0 && (
              <tr>
                <td colSpan={11} className="py-6 text-center text-slate-500 font-sans">
                  No active trigger orders.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
