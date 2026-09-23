import type { Order } from '../../types/trade'
import { formatPrice, formatQuantity } from '../../utils/formatters'
import { toDecimal } from '../../utils/decimal'

interface OrdersTableProps {
  orders: Order[]
  cancellingId: string | null
  onCancel: (id: string) => void
}

function FilledBar({ filled, total }: { filled: string; total: string }) {
  const pct = (() => {
    try {
      const f = toDecimal(filled || '0')
      const t = toDecimal(total  || '0')
      if (t.lte(0)) return 0
      return Math.min(100, f.dividedBy(t).times(100).toNumber())
    } catch { return 0 }
  })()
  return (
    <div className="flex items-center gap-1.5">
      <div className="w-16 h-1.5 rounded-full bg-[#1e2530] overflow-hidden flex-shrink-0">
        <div
          className="h-full bg-[#10b981] rounded-full transition-all"
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className="text-[11px] text-slate-400 font-mono whitespace-nowrap">{pct.toFixed(0)}%</span>
    </div>
  )
}

export default function OrdersTable({ orders, cancellingId, onCancel }: OrdersTableProps) {
  if (orders.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-8 text-center">
        <div className="w-10 h-10 rounded-full bg-[#1e2530] flex items-center justify-center mb-2">
          <span className="text-slate-600 text-base">📋</span>
        </div>
        <p className="text-xs text-slate-500">No open orders</p>
        <p className="text-[10px] text-slate-600 mt-0.5">Your open orders will appear here</p>
      </div>
    )
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs min-w-[800px]">
        <thead>
          <tr className="border-b border-[#1e2530]">
            {['Time', 'Pair', 'Type', 'Side', 'Price', 'Amount', 'Filled', 'Remaining', 'Status', 'Action'].map((col) => (
              <th
                key={col}
                className="px-3 py-2 text-left text-[10px] font-medium text-slate-600 uppercase tracking-wider whitespace-nowrap"
              >
                {col}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-[#1e2530]/50">
          {orders.map((order) => {
            const pair    = order.market_id.replace('-', '/')
            const isBuy   = order.side === 'BUY'
            const isOpen  = order.status === 'OPEN' || order.status === 'PARTIALLY_FILLED'
            const remaining = (() => {
              try {
                return toDecimal(order.quantity).minus(toDecimal(order.filled_quantity || '0')).toFixed(4)
              } catch { return order.quantity }
            })()

            return (
              <tr key={order.id} className="hover:bg-white/[0.015] transition-colors">
                {/* Time */}
                <td className="px-3 py-2 text-slate-500 font-mono whitespace-nowrap">
                  {new Date(order.created_at).toLocaleString('en-US', {
                    month: '2-digit', day: '2-digit', year: '2-digit',
                    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
                  })}
                </td>
                {/* Pair */}
                <td className="px-3 py-2 font-semibold text-[#f5f7fa] whitespace-nowrap">{pair}</td>
                {/* Type */}
                <td className="px-3 py-2 text-slate-400 whitespace-nowrap capitalize">{order.order_type.toLowerCase()}</td>
                {/* Side */}
                <td className={`px-3 py-2 font-semibold whitespace-nowrap ${isBuy ? 'text-[#10b981]' : 'text-[#ef4444]'}`}>
                  {isBuy ? 'Buy' : 'Sell'}
                </td>
                {/* Price */}
                <td className="px-3 py-2 font-mono text-slate-200 whitespace-nowrap">
                  {order.price ? formatPrice(order.price) : 'Market'}
                </td>
                {/* Amount */}
                <td className="px-3 py-2 font-mono text-slate-200 whitespace-nowrap">
                  {formatQuantity(order.quantity, 4)}
                </td>
                {/* Filled */}
                <td className="px-3 py-2 whitespace-nowrap">
                  <FilledBar filled={order.filled_quantity || '0'} total={order.quantity} />
                </td>
                {/* Remaining */}
                <td className="px-3 py-2 font-mono text-slate-400 whitespace-nowrap">
                  {remaining}
                </td>
                {/* Status */}
                <td className="px-3 py-2 whitespace-nowrap">
                  <span className={`px-2 py-0.5 rounded text-[10px] font-semibold border ${
                    order.status === 'FILLED'
                      ? 'text-[#10b981] bg-[#10b981]/10 border-[#10b981]/20'
                      : order.status === 'CANCELLED'
                        ? 'text-slate-500 bg-slate-700/20 border-slate-700/30'
                        : order.status === 'PARTIALLY_FILLED'
                          ? 'text-amber-400 bg-amber-500/10 border-amber-500/20'
                          : 'text-slate-300 bg-[#0a0b0e] border-[#1e2530]'
                  }`}>
                    {order.status === 'PARTIALLY_FILLED' ? 'Partial' : order.status.charAt(0) + order.status.slice(1).toLowerCase()}
                  </span>
                </td>
                {/* Action */}
                <td className="px-3 py-2 whitespace-nowrap">
                  {isOpen ? (
                    <button
                      type="button"
                      onClick={() => onCancel(order.id)}
                      disabled={cancellingId === order.id}
                      className="px-2.5 py-1 rounded border border-[#ef4444]/40 text-[#ef4444] text-[11px] font-semibold hover:bg-[#ef4444]/10 hover:border-[#ef4444]/60 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                      aria-label={`Cancel order ${order.id}`}
                    >
                      {cancellingId === order.id ? (
                        <span className="flex items-center gap-1">
                          <span className="w-2.5 h-2.5 border border-[#ef4444] border-t-transparent rounded-full animate-spin" />
                          Cancelling
                        </span>
                      ) : (
                        'Cancel'
                      )}
                    </button>
                  ) : (
                    <span className="text-slate-600 text-[11px]">—</span>
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
