import type { BackendTrade } from '../../api/trades'
import { formatPrice, formatQuantity } from '../../utils/formatters'

interface FillsTabProps {
  trades: BackendTrade[]
  loading: boolean
}

/** Trade Fills table for OpenOrdersPanel */
export default function FillsTab({ trades, loading }: FillsTabProps) {
  if (loading) {
    return (
      <div className="flex items-center justify-center py-8">
        <div className="w-4 h-4 border-2 border-[#10b981] border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  if (trades.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-8 text-center">
        <div className="w-10 h-10 rounded-full bg-[#1e2530] flex items-center justify-center mb-2">
          <span className="text-slate-600 text-base">⚡</span>
        </div>
        <p className="text-xs text-slate-500">No trade fills yet</p>
        <p className="text-[10px] text-slate-600 mt-0.5">Your executed trade fills will appear here</p>
      </div>
    )
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs min-w-[700px]">
        <thead>
          <tr className="border-b border-[#1e2530]">
            {['Time', 'Pair', 'Price', 'Amount', 'Total (USDT)', 'Trade ID'].map((col) => (
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
          {trades.map((trade) => {
            const pair = trade.market_id.replace('-', '/')
            const price = parseFloat(trade.price || '0')
            const qty = parseFloat(trade.quantity || '0')
            const total = price * qty
            return (
              <tr key={trade.id} className="hover:bg-white/[0.015] transition-colors">
                <td className="px-3 py-2 text-slate-500 font-mono whitespace-nowrap">
                  {trade.executed_at
                    ? new Date(trade.executed_at).toLocaleString('en-US', {
                        month: '2-digit', day: '2-digit', year: '2-digit',
                        hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
                      })
                    : '—'}
                </td>
                <td className="px-3 py-2 font-semibold text-[#f5f7fa] whitespace-nowrap">{pair}</td>
                <td className="px-3 py-2 font-mono text-slate-200 whitespace-nowrap">${formatPrice(trade.price)}</td>
                <td className="px-3 py-2 font-mono text-slate-200 whitespace-nowrap">{formatQuantity(trade.quantity, 4)}</td>
                <td className="px-3 py-2 font-mono text-slate-300 whitespace-nowrap">${formatPrice(total.toString())}</td>
                <td className="px-3 py-2 font-mono text-slate-500 text-[10px] whitespace-nowrap">
                  {trade.id.length > 12 ? `${trade.id.substring(0, 10)}...` : trade.id}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
