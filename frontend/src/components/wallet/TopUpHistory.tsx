import { ChevronDown } from 'lucide-react'
import type { TopUpHistoryItem } from '../../api/topup'

interface TopUpHistoryProps {
  history: TopUpHistoryItem[]
}

export default function TopUpHistory({ history }: TopUpHistoryProps) {
  return (
    <div className="rounded-xl border border-[#1e2530] bg-[#111318] overflow-hidden shadow-sm flex flex-col">
      {/* Header */}
      <div className="p-4 sm:p-5 border-b border-[#1e2530]">
        <h2 className="text-base font-bold text-[#f5f7fa]" style={{ color: '#f5f7fa' }}>
          Top-Up History
        </h2>
      </div>

      {/* Table */}
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead>
            <tr className="border-b border-[#1e2530]/80 text-slate-300 uppercase tracking-wider text-[11px] font-semibold">
              <th className="py-3 px-4 sm:px-5">
                <span className="flex items-center gap-1">
                  Order ID <ChevronDown size={12} className="text-slate-400" />
                </span>
              </th>
              <th className="py-3 px-4 sm:px-5">Amount Paid (INR)</th>
              <th className="py-3 px-4 sm:px-5">USDT Credited</th>
              <th className="py-3 px-4 sm:px-5">Payment Mode</th>
              <th className="py-3 px-4 sm:px-5">Status</th>
              <th className="py-3 px-4 sm:px-5">Date</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1e2530]/50">
            {history.length === 0 ? (
              <tr>
                <td colSpan={6} className="py-8 text-center text-slate-400">
                  No top-up transactions yet
                </td>
              </tr>
            ) : (
              history.map((item) => {
                const isCredited = item.status === 'Credited'
                const isCompleted = item.status === 'Completed'
                const statusDotColor = isCompleted
                  ? 'bg-[#10b981]'
                  : isCredited
                  ? 'bg-[#14b8a6]'
                  : 'bg-amber-400'

                return (
                  <tr
                    key={item.id}
                    className="hover:bg-white/[0.02] transition-colors"
                  >
                    {/* Order ID */}
                    <td className="py-3 px-4 sm:px-5 font-mono font-medium text-slate-200 whitespace-nowrap">
                      {item.orderId}
                    </td>

                    {/* Amount Paid */}
                    <td className="py-3 px-4 sm:px-5 font-mono text-[#f5f7fa] whitespace-nowrap">
                      {item.amountInr > 0 ? `₹${item.amountInr.toFixed(2)}` : '—'}
                    </td>

                    {/* USDT Credited */}
                    <td className="py-3 px-4 sm:px-5 font-mono font-semibold text-[#10b981] whitespace-nowrap">
                      {item.usdtCredited}
                    </td>

                    {/* Payment Mode */}
                    <td className="py-3 px-4 sm:px-5 text-slate-200 whitespace-nowrap">
                      {item.paymentMode}
                    </td>

                    {/* Status */}
                    <td className="py-3 px-4 sm:px-5 whitespace-nowrap">
                      <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-[11px] font-medium text-slate-200">
                        <span className={`w-1.5 h-1.5 rounded-full ${statusDotColor}`} />
                        {item.status}
                      </span>
                    </td>

                    {/* Date */}
                    <td className="py-3 px-4 sm:px-5 font-mono text-slate-300 whitespace-nowrap text-[11px]">
                      {item.date}
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
