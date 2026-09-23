import { Link } from 'react-router-dom'
import { ArrowRight, ChevronDown, Copy, Check } from 'lucide-react'
import { useState } from 'react'
import toast from 'react-hot-toast'
import type { ExecutionFillItem } from '../../types/analytics'

interface Props {
  fills: ExecutionFillItem[]
}

/**
 * Recent Execution Fills — full width table below the upper panels
 * Features formatted columns, green BUY and red SELL badges, and one-click Trade ID copy.
 */
export default function RecentExecutionFills({ fills }: Props) {
  const [copiedId, setCopiedId] = useState<string | null>(null)

  const handleCopy = (tradeId: string) => {
    navigator.clipboard.writeText(tradeId)
    setCopiedId(tradeId)
    toast.success(`Copied ${tradeId}`)
    setTimeout(() => setCopiedId(null), 2000)
  }

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl overflow-hidden flex flex-col">
      {/* Header */}
      <div className="px-5 py-4 border-b border-[#1e2530] flex items-center justify-between">
        <div>
          <h2 className="text-sm font-semibold text-[#f5f7fa]">
            Recent Execution Fills
          </h2>
          <p className="text-xs text-[#94a3b8] mt-0.5">
            Your latest trade executions from the matching engine
          </p>
        </div>

        <Link
          to="/orders"
          className="text-xs text-[#10b981] hover:text-[#10b981]/80 transition-colors flex items-center gap-1 font-medium select-none"
        >
          <span>View all executions</span>
          <ArrowRight className="w-3.5 h-3.5" />
        </Link>
      </div>

      {/* Table */}
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-[#1e2530] text-[#64748b]">
              <th className="text-left py-3.5 px-5 font-medium">
                <div className="flex items-center gap-1 cursor-pointer hover:text-[#94a3b8]">
                  <span>Time</span>
                  <ChevronDown className="w-3 h-3" />
                </div>
              </th>
              <th className="text-left py-3.5 px-4 font-medium">Trade ID</th>
              <th className="text-left py-3.5 px-4 font-medium">Pair</th>
              <th className="text-left py-3.5 px-4 font-medium">Side</th>
              <th className="text-left py-3.5 px-4 font-medium">Price (USDT)</th>
              <th className="text-left py-3.5 px-4 font-medium">Filled Amount</th>
              <th className="text-left py-3.5 px-4 font-medium">Total Value (USDT)</th>
              <th className="text-right py-3.5 px-5 font-medium w-12"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#181c24]">
            {fills.map((fill) => {
              const isBuy = fill.side === 'BUY'
              const isCopied = copiedId === fill.tradeId

              return (
                <tr
                  key={fill.id}
                  className="hover:bg-[#151921] transition-colors group"
                >
                  {/* Time */}
                  <td className="py-3.5 px-5 font-mono text-[#94a3b8] whitespace-nowrap">
                    {fill.time}
                  </td>

                  {/* Trade ID */}
                  <td className="py-3.5 px-4 font-mono text-[#cbd5e1] whitespace-nowrap">
                    {fill.tradeId}
                  </td>

                  {/* Pair */}
                  <td className="py-3.5 px-4 font-medium text-[#f5f7fa] whitespace-nowrap">
                    {fill.pair}
                  </td>

                  {/* Side badge */}
                  <td className="py-3.5 px-4 whitespace-nowrap">
                    <span
                      className={`inline-flex items-center px-2 py-0.5 rounded text-[10px] font-bold tracking-wider ${
                        isBuy
                          ? 'bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30'
                          : 'bg-[#ef4444]/15 text-[#ef4444] border border-[#ef4444]/30'
                      }`}
                    >
                      {fill.side}
                    </span>
                  </td>

                  {/* Price */}
                  <td className="py-3.5 px-4 font-mono text-[#cbd5e1] whitespace-nowrap">
                    {fill.price}
                  </td>

                  {/* Filled Amount */}
                  <td className="py-3.5 px-4 font-mono text-[#cbd5e1] whitespace-nowrap">
                    {fill.filledAmount}
                  </td>

                  {/* Total Value */}
                  <td className="py-3.5 px-4 font-mono text-[#cbd5e1] whitespace-nowrap">
                    {fill.totalValue}
                  </td>

                  {/* Action: Copy icon */}
                  <td className="py-3.5 px-5 text-right whitespace-nowrap">
                    <button
                      type="button"
                      onClick={() => handleCopy(fill.tradeId)}
                      className="p-1 text-[#475569] hover:text-[#94a3b8] hover:bg-[#1e2530] rounded transition-colors"
                      title="Copy Trade ID"
                      aria-label={`Copy trade ID ${fill.tradeId}`}
                    >
                      {isCopied ? (
                        <Check className="w-3.5 h-3.5 text-[#10b981]" />
                      ) : (
                        <Copy className="w-3.5 h-3.5" />
                      )}
                    </button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
