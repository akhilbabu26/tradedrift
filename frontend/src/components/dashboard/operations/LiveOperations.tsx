import { ArrowRight } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { ActiveOrder } from '../../../types/dashboard'
import { formatPrice, formatQuantity } from '../../../utils/formatters'
import DashboardCard from '../shared/DashboardCard'
import SectionHeader from '../shared/SectionHeader'

interface LiveOperationsProps {
  orders: ActiveOrder[]
}

/**
 * Live Operations card — active orders table with Cancel buttons.
 * IMPORTANT: Cancel button always shows text "Cancel" — never icon-only.
 */
export default function LiveOperations({ orders }: LiveOperationsProps) {
  const handleCancel = (orderId: string) => {
    // TODO: connect to orderApi.cancelOrder(orderId) when backend is ready
    console.info('Cancel order:', orderId)
  }

  return (
    <DashboardCard>
      {/* Header */}
      <SectionHeader
        title={
          <span className="flex items-center gap-2">
            Live Operations
            <span className="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-[#10b981]/10 text-[#10b981] border border-[#10b981]/20">
              {orders.length} Active Orders
            </span>
          </span>
        }
        action={
          <Link to="/orders" className="flex items-center gap-0.5 text-xs text-slate-400 hover:text-[#10b981] transition-colors">
            View All <ArrowRight size={11} />
          </Link>
        }
      />

      {/* Table */}
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="text-slate-500 text-[10px] uppercase tracking-wider border-b border-[#1e2530]">
              <th className="text-left pb-2 font-medium">Type</th>
              <th className="text-left pb-2 font-medium">Pair</th>
              <th className="text-right pb-2 font-medium">Price</th>
              <th className="text-right pb-2 font-medium">Amount</th>
              <th className="text-right pb-2 font-medium">Status</th>
              <th className="text-right pb-2 font-medium">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1e2530]/40">
            {orders.map((order) => {
              const isBuy = order.side === 'BUY'
              return (
                <tr key={order.id} className="hover:bg-white/[0.02] transition-colors">
                  {/* Type */}
                  <td className="py-2.5 pr-2">
                    <span className={`font-semibold ${isBuy ? 'text-[#10b981]' : 'text-[#ef4444]'}`}>
                      {order.type}
                    </span>
                  </td>
                  {/* Pair */}
                  <td className="py-2.5 text-slate-300 font-medium">{order.pair}</td>
                  {/* Price */}
                  <td className="py-2.5 text-right font-mono text-slate-200">
                    {formatPrice(order.price)}
                  </td>
                  {/* Amount */}
                  <td className="py-2.5 text-right font-mono text-slate-300">
                    {formatQuantity(order.amount)}
                  </td>
                  {/* Status */}
                  <td className="py-2.5 text-right">
                    <span className="inline-block px-2 py-0.5 rounded text-[10px] font-semibold border text-[#10b981] border-[#10b981]/40 bg-[#10b981]/8">
                      {order.status}
                    </span>
                  </td>
                  {/* Cancel button — always text "Cancel", never icon-only */}
                  <td className="py-2.5 text-right pl-2">
                    <button
                      type="button"
                      onClick={() => handleCancel(order.id)}
                      aria-label={`Cancel order for ${order.pair}`}
                      className="px-2.5 py-1 rounded-md border border-[#ef4444]/50 text-[#ef4444] text-xs font-semibold hover:bg-[#ef4444]/10 hover:border-[#ef4444] transition-colors"
                    >
                      Cancel
                    </button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </DashboardCard>
  )
}
