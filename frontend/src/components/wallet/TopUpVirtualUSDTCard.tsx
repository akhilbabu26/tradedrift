import { Zap, Plus, ArrowRight } from 'lucide-react'
import type { DailyUsage } from '../../api/topup'
import { formatPrice } from '../../utils/formatters'
import { RaisedSurface } from '../common/Surface'

interface TopUpVirtualUSDTCardProps {
  dailyUsage: DailyUsage | null
  onTopUpClick: () => void
}

export default function TopUpVirtualUSDTCard({ dailyUsage, onTopUpClick }: TopUpVirtualUSDTCardProps) {
  const hasUsage = dailyUsage != null
  const consumedUsdt = hasUsage ? (dailyUsage.consumedInr ?? 0) * 1000 : null
  const dailyLimitUsdt = hasUsage ? (dailyUsage.dailyLimitInr ?? dailyUsage.limitInr ?? 10) * 1000 : null
  const usagePct = (dailyLimitUsdt != null && dailyLimitUsdt > 0 && consumedUsdt != null)
    ? Math.min(100, Math.round((consumedUsdt / dailyLimitUsdt) * 100))
    : 0

  return (
    <RaisedSurface variant="raised-subtle" className="flex flex-col justify-between h-full">
      {/* Header */}
      <div>
        <h2 className="text-base font-bold text-[#F2F3F5]">
          Top Up Virtual USDT
        </h2>
        <p className="text-[11px] text-[#777E87] mt-0.5">
          Add simulated trading credits using real money
        </p>

        {/* Highlight Card */}
        <div className="mt-3.5 p-3 rounded-xl bg-[#090A0C] border border-[#292C30] flex items-center gap-3 shadow-[inset_0_2px_4px_rgba(0,0,0,0.5)]">
          <div className="w-8 h-8 rounded-lg bg-amber-400/10 border border-amber-400/20 flex items-center justify-center flex-shrink-0">
            <Zap size={16} className="text-amber-400 fill-amber-400" />
          </div>
          <div className="flex flex-col">
            <span className="text-sm font-bold text-[#10b981]">
              1 INR = 1,000 Virtual USDT
            </span>
            <span className="text-[11px] text-[#777E87]">
              Fast, secure and powered by Razorpay
            </span>
          </div>
        </div>
      </div>

      {/* Usage Progress Section */}
      <div className="mt-4 flex flex-col gap-2">
        <div className="flex items-center justify-between text-xs">
          <span className="text-[#A7ADB5]">Today's Top-Up Usage</span>
          <span className="font-mono font-bold text-[#F2F3F5]">
            {hasUsage && consumedUsdt != null && dailyLimitUsdt != null
              ? `${formatPrice(consumedUsdt.toString())} / ${formatPrice(dailyLimitUsdt.toString())} USDT`
              : '—'}
          </span>
        </div>

        {/* Progress Bar with inline % */}
        <div className="flex items-center gap-2">
          <div className="flex-1 h-2 rounded-full bg-[#090A0C] border border-[#202225] overflow-hidden">
            <div
              className="h-full bg-gradient-to-r from-[#10b981] to-[#06b6d4] transition-all duration-300"
              style={{ width: `${hasUsage ? usagePct : 0}%` }}
            />
          </div>
          <span className="font-mono text-xs font-semibold px-2 py-0.5 rounded bg-[#090A0C] text-[#A7ADB5] border border-[#292C30] min-w-[36px] text-center">
            {hasUsage ? `${usagePct}%` : '—'}
          </span>
        </div>

        {/* Limit note & learn more link */}
        <div className="flex items-center justify-between text-[11px] text-[#777E87]">
          <span>
            {hasUsage && dailyLimitUsdt != null
              ? `Daily Limit: ${formatPrice(dailyLimitUsdt.toString())} USDT per day`
              : 'Daily Limit: Unavailable'}
          </span>
          <button
            type="button"
            onClick={onTopUpClick}
            className="text-[#10b981] hover:text-[#34d399] flex items-center gap-0.5 font-medium transition-colors cursor-pointer"
          >
            Learn more <ArrowRight size={10} />
          </button>
        </div>
      </div>

      {/* Action Button */}
      <div className="mt-4">
        <button
          type="button"
          onClick={onTopUpClick}
          className="w-full py-2.5 rounded-lg bg-[#10b981] hover:bg-[#0ea572] active:bg-[#059669] text-[#08090B] font-bold text-sm flex items-center justify-center gap-1.5 transition-all shadow-[0_2px_8px_rgba(16,185,129,0.3)] hover:shadow-[0_4px_12px_rgba(16,185,129,0.4)] cursor-pointer focus:outline-none focus:ring-2 focus:ring-[#10b981]/50"
        >
          <Plus size={16} strokeWidth={2.5} />
          Top Up Virtual USDT
        </button>
      </div>
    </RaisedSurface>
  )
}
