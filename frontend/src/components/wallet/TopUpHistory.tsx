import { ChevronDown } from 'lucide-react'
import type { TopUpHistoryItem } from '../../api/topup'
import { RaisedSurface } from '../common/Surface'

interface TopUpHistoryProps {
  history: TopUpHistoryItem[]
}

export default function TopUpHistory({ history }: TopUpHistoryProps) {
  return (
    <RaisedSurface variant="raised-subtle" noPadding className="overflow-hidden flex flex-col">
      {/* Header */}
      <div className="p-4 sm:p-5 border-b border-[#202225]">
        <h2 className="text-base font-bold text-[#F2F3F5]">
          Top-Up History
        </h2>
        <p className="text-[11px] text-[#777E87] mt-0.5">
          Record of real INR to virtual USDT transactions
        </p>
      </div>

      {/* Table */}
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead>
            <tr className="border-b border-[#202225] text-[#777E87] uppercase tracking-wider text-[11px] font-semibold bg-[#111316]/40">
              <th className="py-3 px-4 sm:px-5">
                <span className="flex items-center gap-1">
                  Order ID <ChevronDown size={12} className="text-[#777E87]" />
                </span>
              </th>
              <th className="py-3 px-4 sm:px-5">Amount Paid (INR)</th>
              <th className="py-3 px-4 sm:px-5">USDT Credited</th>
              <th className="py-3 px-4 sm:px-5">Payment Mode</th>
              <th className="py-3 px-4 sm:px-5">Status</th>
              <th className="py-3 px-4 sm:px-5">Date</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#202225]/60">
            {history.length === 0 ? (
              <tr>
                <td colSpan={6} className="py-8 text-center text-[#777E87]">
                  No top-up transactions yet
                </td>
              </tr>
            ) : (
              history.map((item) => {
                const isCredited = item.status === 'Credited'
                const isCompleted = item.status === 'Completed'
                const isFailed = item.status === 'Failed'
                const statusDotColor = isCompleted
                  ? 'bg-[#10B981]'
                  : isCredited
                  ? 'bg-[#14B8A6]'
                  : isFailed
                  ? 'bg-[#EF4444]'
                  : 'bg-amber-400'
                const statusTextColor = isCompleted
                  ? 'text-[#10B981]'
                  : isCredited
                  ? 'text-[#14B8A6]'
                  : isFailed
                  ? 'text-[#EF4444]'
                  : 'text-amber-400'

                return (
                  <tr
                    key={item.id}
                    className="hover:bg-white/[0.02] transition-colors"
                  >
                    {/* Order ID */}
                    <td className="py-3 px-4 sm:px-5 font-mono font-medium text-[#A7ADB5] whitespace-nowrap">
                      {item.orderId}
                    </td>

                    {/* Amount Paid */}
                    <td className="py-3 px-4 sm:px-5 font-mono text-[#F2F3F5] whitespace-nowrap">
                      {item.amountInr > 0 ? `₹${item.amountInr.toFixed(2)}` : '—'}
                    </td>

                    {/* USDT Credited */}
                    <td className="py-3 px-4 sm:px-5 font-mono font-semibold text-[#10B981] whitespace-nowrap">
                      {item.usdtCredited}
                    </td>

                    {/* Payment Mode */}
                    <td className="py-3 px-4 sm:px-5 text-[#A7ADB5] whitespace-nowrap">
                      {item.paymentMode}
                    </td>

                    {/* Status */}
                    <td className="py-3 px-4 sm:px-5 whitespace-nowrap">
                      <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-[11px] font-medium text-[#A7ADB5]">
                        <span className={`w-1.5 h-1.5 rounded-full ${statusDotColor}`} />
                        <span className={statusTextColor}>{item.status}</span>
                      </span>
                    </td>

                    {/* Date */}
                    <td className="py-3 px-4 sm:px-5 font-mono text-[#777E87] whitespace-nowrap text-[11px]">
                      {item.date}
                    </td>
                  </tr>
                )
              })
            )}
          </tbody>
        </table>
      </div>
    </RaisedSurface>
  )
}
