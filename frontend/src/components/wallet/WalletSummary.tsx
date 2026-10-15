import { Info } from 'lucide-react'
import { formatPrice } from '../../utils/formatters'
import type { WalletSummaryMetrics } from '../../hooks/useWalletData'
import { RaisedSurface } from '../common/Surface'

interface WalletSummaryProps {
  summary: WalletSummaryMetrics
  loading?: boolean
}

export default function WalletSummary({ summary, loading = false }: WalletSummaryProps) {
  if (loading) {
    return (
      <RaisedSurface variant="raised-subtle" className="animate-pulse flex flex-col justify-between h-full min-h-[220px]">
        <div className="h-4 bg-[#202225] rounded w-1/3 mb-4" />
        <div className="h-8 bg-[#202225] rounded w-2/3 mb-6" />
        <div className="space-y-3">
          <div className="h-4 bg-[#202225] rounded w-full" />
          <div className="h-4 bg-[#202225] rounded w-full" />
        </div>
      </RaisedSurface>
    )
  }

  const { totalWalletValue, availableValue, lockedValue, availablePct, lockedPct } = summary

  return (
    <RaisedSurface variant="raised-subtle" className="flex flex-col justify-between h-full">
      {/* Header */}
      <div>
        <div className="flex items-center gap-1.5">
          <h2 className="text-base font-bold text-[#F2F3F5]">
            Total Wallet Value
          </h2>
          <Info size={14} className="text-[#777E87] hover:text-[#A7ADB5] cursor-help transition-colors" />
        </div>
        <p className="text-[11px] text-[#777E87] mt-0.5">
          Current wallet value converted to USDT
        </p>

        {/* Big Value */}
        <div className="mt-3 flex items-baseline gap-1.5">
          <span className="text-3xl font-black font-mono text-[#F2F3F5] tracking-tight">
            {totalWalletValue === '—' ? '—' : `$${formatPrice(totalWalletValue)}`}
          </span>
          {totalWalletValue !== '—' && (
            <span className="text-sm font-semibold text-[#A7ADB5]">
              USDT
            </span>
          )}
        </div>
      </div>

      {/* Breakdown Rows */}
      <div className="mt-5 flex flex-col gap-2.5">
        {/* Available */}
        <div className="flex items-center justify-between text-xs">
          <div className="flex items-center gap-2 text-[#A7ADB5]">
            <span className="w-2.5 h-2.5 rounded-full bg-[#10b981] flex-shrink-0" />
            <span>Available to Trade</span>
          </div>
          <div className="flex items-center gap-3">
            <span className="font-mono font-bold text-sm text-[#F2F3F5]">
              {availableValue === '—' ? '—' : `$${formatPrice(availableValue)}`}
            </span>
            <span className="font-mono text-xs font-semibold px-2 py-0.5 rounded bg-[#090A0C] text-[#A7ADB5] border border-[#292C30] min-w-[40px] text-center">
              {availableValue === '—' ? '—' : `${availablePct}%`}
            </span>
          </div>
        </div>

        {/* Locked */}
        <div className="flex items-center justify-between text-xs">
          <div className="flex items-center gap-2 text-[#A7ADB5]">
            <span className="w-2.5 h-2.5 rounded-full bg-[#3b82f6] flex-shrink-0" />
            <span>Locked in Open Orders</span>
          </div>
          <div className="flex items-center gap-3">
            <span className="font-mono font-bold text-sm text-[#F2F3F5]">
              {lockedValue === '—' ? '—' : `$${formatPrice(lockedValue)}`}
            </span>
            <span className="font-mono text-xs font-semibold px-2 py-0.5 rounded bg-[#090A0C] text-[#A7ADB5] border border-[#292C30] min-w-[40px] text-center">
              {lockedValue === '—' ? '—' : `${lockedPct}%`}
            </span>
          </div>
        </div>

        {/* Allocation Bar */}
        <div className="mt-2">
          <div className="h-2.5 w-full rounded-full bg-[#090A0C] border border-[#202225] overflow-hidden flex">
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
    </RaisedSurface>
  )
}
