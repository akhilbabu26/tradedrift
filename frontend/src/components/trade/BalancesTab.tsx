import type { Balance } from '../../types/trade'
import { formatPrice, formatQuantity } from '../../utils/formatters'

interface BalancesTabProps {
  balances: Balance[]
}

/** Balance table for the Balances tab in OpenOrdersPanel */
export default function BalancesTab({ balances }: BalancesTabProps) {
  if (balances.length === 0) {
    return (
      <div className="flex items-center justify-center py-8">
        <p className="text-xs text-slate-500">No balances found</p>
      </div>
    )
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-[#1e2530]">
            {['Asset', 'Available', 'Reserved', 'Total'].map((col) => (
              <th key={col} className="px-3 py-2 text-left text-[10px] font-medium text-slate-600 uppercase tracking-wider">
                {col}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-[#1e2530]/50">
          {balances.map((b) => {
            const avail    = parseFloat(b.availableBalance || '0')
            const reserved = parseFloat(b.reservedBalance  || '0')
            const total    = avail + reserved
            const needsDec = b.asset === 'BTC' || b.asset === 'ETH' || b.asset === 'SOL'
            const fmt = (v: number) => needsDec ? formatQuantity(v.toString(), 6) : formatPrice(v.toString())
            return (
              <tr key={b.asset} className="hover:bg-white/[0.015] transition-colors">
                <td className="px-3 py-2 font-semibold text-[#f5f7fa]">{b.asset}</td>
                <td className="px-3 py-2 font-mono text-[#10b981]">{fmt(avail)}</td>
                <td className="px-3 py-2 font-mono text-slate-400">{fmt(reserved)}</td>
                <td className="px-3 py-2 font-mono text-slate-200">{fmt(total)}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
