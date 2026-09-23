import { Info } from 'lucide-react'
import { formatPrice } from '../../utils/formatters'
import type { WalletSummaryMetrics } from '../../hooks/useWalletData'

interface WalletSummaryProps {
  summary: WalletSummaryMetrics
  loading?: boolean
}

export default function WalletSummary({ summary, loading = false }: WalletSummaryProps) {
  if (loading) {
    return (
      <div className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 animate-pulse flex flex-col justify-between h-full min-h-[220px]">
        <div className="h-4 bg-[#1e2530] rounded w-1/3 mb-4" />
        <div className="h-8 bg-[#1e2530] rounded w-2/3 mb-6" />
        <div className="space-y-3">
          <div className="h-4 bg-[#1e2530] rounded w-full" />
          <div className="h-4 bg-[#1e2530] rounded w-full" />
        </div>
      </div>
    )
  }

  const { totalWalletValue, availableValue, lockedValue, availablePct, lockedPct } = summary

  return (
    <div className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 flex flex-col justify-between shadow-sm">
      {/* Header */}
      <div>
        <div className="flex items-center gap-1.5">
          <h2 className="text-base font-bold text-[#f5f7fa]" style={{ color: '#f5f7fa' }}>
            Total Wallet Value
          </h2>
          <Info size={14} className="text-slate-400 hover:text-slate-300 cursor-help" />
        </div>
        <p className="text-[11px] text-slate-400 mt-0.5">
          Current wallet value converted to USDT
        </p>

        {/* Big Value */}
        <div className="mt-3 flex items-baseline gap-1.5">
          <span className="text-3xl font-black font-mono text-[#f5f7fa] tracking-tight">
            ${formatPrice(totalWalletValue)}
          </span>
          <span className="text-sm font-semibold text-slate-300">
            USDT
          </span>
        </div>
      </div>

      {/* Breakdown Rows */}
      <div className="mt-5 flex flex-col gap-2.5">
        {/* Available */}
        <div className="flex items-center justify-between text-xs">
          <div className="flex items-center gap-2 text-slate-200">
            <span className="w-2.5 h-2.5 rounded-full bg-[#10b981] flex-shrink-0" />
            <span>Available to Trade</span>
          </div>
          <div className="flex items-center gap-3">
            <span className="font-mono font-bold text-sm text-[#f5f7fa]">
              ${formatPrice(availableValue)}
            </span>
            <span className="font-mono text-xs font-semibold px-2 py-0.5 rounded bg-white/[0.06] text-slate-200 border border-white/15 min-w-[40px] text-center">
              {availablePct}%
            </span>
          </div>
        </div>

        {/* Locked */}
        <div className="flex items-center justify-between text-xs">
          <div className="flex items-center gap-2 text-slate-200">
            <span className="w-2.5 h-2.5 rounded-full bg-[#3b82f6] flex-shrink-0" />
            <span>Locked in Open Orders</span>
          </div>
          <div className="flex items-center gap-3">
            <span className="font-mono font-bold text-sm text-[#f5f7fa]">
              ${formatPrice(lockedValue)}
            </span>
            <span className="font-mono text-xs font-semibold px-2 py-0.5 rounded bg-white/[0.06] text-slate-200 border border-white/15 min-w-[40px] text-center">
              {lockedPct}%
            </span>
          </div>
        </div>

        {/* Allocation Bar */}
        <div className="mt-2">
          <div className="h-2.5 w-full rounded-full bg-[#1e2530] overflow-hidden flex">
            <div
              className="h-full bg-[#10b981] transition-all duration-300"
              style={{ width: `${availablePct}%` }}
              title={`Available: ${availablePct}%`}
            />
            <div
              className="h-full bg-[#3b82f6] transition-all duration-300"
              style={{ width: `${lockedPct}%` }}
              title={`Locked: ${lockedPct}%`}
            />
          </div>

          {/* Bar Legends */}
          <div className="flex items-center justify-between text-[11px] font-mono mt-1.5">
            <span className="text-[#10b981] font-semibold">Available {availablePct}%</span>
            <span className="text-[#3b82f6] font-semibold">Locked {lockedPct}%</span>
          </div>
        </div>
      </div>
    </div>
  )
}
