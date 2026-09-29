import { ArrowRight } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { RecentFill } from '../../../types/dashboard'
import { formatPrice, formatQuantity, formatRelativeTime } from '../../../utils/formatters'
import DashboardCard from '../shared/DashboardCard'
import SectionHeader from '../shared/SectionHeader'

interface RecentFillsProps {
  fills: RecentFill[]
}

/**
 * Recent Fills card — last 4 trade executions with Buy/Sell color badges.
 */
export default function RecentFills({ fills }: RecentFillsProps) {
  return (
    <DashboardCard>
      {/* Header */}
      <SectionHeader
        title="Recent Fills"
        action={
          <Link to="/orders" className="flex items-center gap-0.5 text-xs text-slate-400 hover:text-[#10b981] transition-colors">
            View All <ArrowRight size={11} />
          </Link>
        }
      />

      {fills.length === 0 ? (
        <div className="py-8 text-center flex flex-col items-center justify-center">
          <p className="text-xs text-slate-400 mb-1">No recent fills</p>
          <p className="text-[11px] text-slate-500">Executed orders will appear here in real time.</p>
        </div>
      ) : (
        /* Table */
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="text-slate-500 text-[10px] uppercase tracking-wider border-b border-[#1e2530]">
                <th className="text-left pb-2 font-medium">Type</th>
                <th className="text-left pb-2 font-medium">Pair</th>
                <th className="text-right pb-2 font-medium">Price</th>
                <th className="text-right pb-2 font-medium">Amount</th>
                <th className="text-right pb-2 font-medium">Time</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#1e2530]/40">
              {fills.map((fill) => {
                const isBuy = fill.type === 'Buy'
                return (
                  <tr key={fill.id} className="hover:bg-white/[0.02] transition-colors">
                    {/* Type badge */}
                    <td className="py-2.5 pr-2">
                      <span
                        className={`inline-block px-2 py-0.5 rounded text-[10px] font-bold ${
                          isBuy
                            ? 'bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30'
                            : 'bg-[#ef4444]/15 text-[#ef4444] border border-[#ef4444]/30'
                        }`}
                      >
                        {fill.type}
                      </span>
                    </td>
                    {/* Pair */}
                    <td className="py-2.5 text-slate-300 font-medium">{fill.pair}</td>
                    {/* Price */}
                    <td className="py-2.5 text-right font-mono text-slate-200">
                      {formatPrice(fill.price)}
                    </td>
                    {/* Amount */}
                    <td className="py-2.5 text-right font-mono text-slate-400">
                      {formatQuantity(fill.amount)}
                    </td>
                    {/* Time */}
                    <td className="py-2.5 text-right text-slate-500">
                      {formatRelativeTime(fill.timeIso)}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </DashboardCard>
  )
}
