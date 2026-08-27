import { useState } from 'react'
import { Copy, Check, ChevronDown } from 'lucide-react'
import toast from 'react-hot-toast'

export interface ExecutionFillItem {
  id: string
  executionId: string
  marketId: string
  marketSymbol: string
  iconChar: string
  iconBg: string
  iconText: string
  side: 'BUY' | 'SELL'
  role: 'MAKER' | 'TAKER'
  executedPrice: string
  filledQuantity: string
  totalValueUsd: string
  feeUsdt: string
  realizedPnl: string
  isPnlPositive: boolean
  timestampUtc: string
}

interface ExecutionFillsTableProps {
  executions: ExecutionFillItem[]
}

export default function ExecutionFillsTable({ executions }: ExecutionFillsTableProps) {
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const [currentPage, setCurrentPage] = useState(1)
  const [rowsPerPage, setRowsPerPage] = useState('10')

  const copyExecutionId = (id: string, text: string) => {
    navigator.clipboard.writeText(text)
    setCopiedId(id)
    toast.success('Execution ID copied to clipboard')
    setTimeout(() => setCopiedId((prev) => (prev === id ? null : prev)), 2000)
  }

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden shadow-xl select-none">
      {/* ── Section Header ── */}
      <div className="p-4 border-b border-[#1b2230] flex items-center gap-3 bg-[#07090e]/40">
        <h2 className="text-sm font-bold text-white font-sans tracking-tight">
          Execution Fills
        </h2>
        <span className="text-xs text-slate-400 font-sans">
          All executed trades and fills
        </span>
      </div>

      {/* ── Dense Execution Fills Table ── */}
      <div className="overflow-x-auto custom-scrollbar">
        <table className="w-full text-xs font-mono text-left border-collapse">
          <thead>
            <tr className="border-b border-[#1b2230] bg-[#07090e]/60 text-slate-400 text-[11px] font-sans font-medium">
              <th className="py-3 px-4">Execution ID</th>
              <th className="py-3 px-4">Market</th>
              <th className="py-3 px-4 text-center">Side</th>
              <th className="py-3 px-4 text-center">Role</th>
              <th className="py-3 px-4 text-right">Executed Price</th>
              <th className="py-3 px-4 text-right">Filled Quantity</th>
              <th className="py-3 px-4 text-right">Total Value (USD)</th>
              <th className="py-3 px-4 text-right">Fee (USDT)</th>
              <th className="py-3 px-4 text-right">Realized PnL (USD)</th>
              <th className="py-3 px-4">Timestamp (UTC)</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1b2230]/60">
            {executions.map((e) => {
              const isBuy = e.side === 'BUY'
              const isMaker = e.role === 'MAKER'

              return (
                <tr key={e.id} className="hover:bg-[#141a26]/60 transition-colors group">
                  {/* Execution ID with Copy */}
                  <td className="py-3.5 px-4 font-mono">
                    <div className="flex items-center gap-1.5">
                      <span className="text-[#00e5ff] hover:underline cursor-pointer">
                        {e.executionId}
                      </span>
                      <button
                        type="button"
                        onClick={() => copyExecutionId(e.id, e.executionId)}
                        aria-label="Copy Execution ID"
                        className="text-slate-500 hover:text-slate-300 p-0.5 rounded transition-colors"
                      >
                        {copiedId === e.id ? (
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
                        className={`w-6 h-6 rounded-full flex items-center justify-center font-bold text-[10px] flex-shrink-0 border ${e.iconBg} ${e.iconText}`}
                      >
                        {e.iconChar}
                      </div>
                      <span className="font-bold text-white text-xs tracking-tight">
                        {e.marketSymbol}
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
                      {e.side}
                    </span>
                  </td>

                  {/* Role (MAKER / TAKER) */}
                  <td className="py-3.5 px-4 text-center font-sans">
                    <span
                      className={`inline-block px-2 py-0.5 rounded text-[10px] font-bold ${
                        isMaker
                          ? 'bg-[#00e5ff]/10 text-[#00e5ff] border border-[#00e5ff]/20'
                          : 'bg-amber-400/10 text-amber-400 border border-amber-400/20'
                      }`}
                    >
                      {e.role}
                    </span>
                  </td>

                  {/* Executed Price */}
                  <td className="py-3.5 px-4 text-right font-bold text-white">
                    {e.executedPrice}
                  </td>

                  {/* Filled Quantity */}
                  <td className="py-3.5 px-4 text-right font-semibold text-slate-200">
                    {e.filledQuantity}
                  </td>

                  {/* Total Value (USD) */}
                  <td className="py-3.5 px-4 text-right font-semibold text-white">
                    {e.totalValueUsd}
                  </td>

                  {/* Fee (USDT) */}
                  <td className="py-3.5 px-4 text-right text-slate-300">
                    {e.feeUsdt}
                  </td>

                  {/* Realized PnL (USD) */}
                  <td
                    className={`py-3.5 px-4 text-right font-bold ${
                      e.isPnlPositive ? 'text-[#00e676]' : 'text-[#ff3366]'
                    }`}
                  >
                    {e.realizedPnl}
                  </td>

                  {/* Timestamp (UTC) with millisecond precision */}
                  <td className="py-3.5 px-4 text-slate-400 text-[11px]">
                    {e.timestampUtc}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {/* ── Pagination Footer ── */}
      <div className="p-3 border-t border-[#1b2230] flex flex-wrap items-center justify-between gap-3 text-xs text-slate-400 font-sans bg-[#07090e]/40">
        <div>Showing 1 to {executions.length} of 250+ executions</div>

        <div className="flex items-center gap-4">
          {/* Page numbers */}
          <div className="flex items-center gap-1 font-mono text-xs">
            <button
              type="button"
              onClick={() => setCurrentPage(1)}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              «
            </button>
            <button
              type="button"
              onClick={() => setCurrentPage(Math.max(1, currentPage - 1))}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              ‹
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#00e5ff]/15 border border-[#00e5ff]/40 text-[#00e5ff] font-bold flex items-center justify-center shadow-[0_0_8px_rgba(0,229,255,0.15)]"
            >
              1
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              2
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              3
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              4
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              5
            </button>
            <span className="px-1 text-slate-600">...</span>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              25
            </button>
            <button
              type="button"
              onClick={() => setCurrentPage(currentPage + 1)}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              ›
            </button>
            <button
              type="button"
              onClick={() => setCurrentPage(25)}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              »
            </button>
          </div>

          {/* Rows per page */}
          <div className="flex items-center gap-2 text-xs">
            <span>Rows per page:</span>
            <div className="relative">
              <select
                value={rowsPerPage}
                onChange={(e) => setRowsPerPage(e.target.value)}
                className="appearance-none bg-[#07090e] border border-[#1b2230] rounded-lg px-2.5 py-1 pr-6 text-xs text-white font-mono focus:outline-none focus:border-[#00e5ff]/50 cursor-pointer"
              >
                <option value="10">10</option>
                <option value="25">25</option>
                <option value="50">50</option>
                <option value="100">100</option>
              </select>
              <ChevronDown size={12} className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
