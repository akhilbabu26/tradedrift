import { Zap, Plus, ArrowRight } from 'lucide-react'
import type { DailyUsage } from '../../api/topup'
import { formatPrice } from '../../utils/formatters'

interface TopUpVirtualUSDTCardProps {
  dailyUsage: DailyUsage
  onTopUpClick: () => void
}

export default function TopUpVirtualUSDTCard({ dailyUsage, onTopUpClick }: TopUpVirtualUSDTCardProps) {
  const consumedUsdt = dailyUsage.consumedInr * 1000
  const dailyLimitUsdt = dailyUsage.dailyLimitInr * 1000
  const usagePct = dailyLimitUsdt > 0
    ? Math.min(100, Math.round((consumedUsdt / dailyLimitUsdt) * 100))
    : 0

  return (
    <div className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 flex flex-col justify-between shadow-sm">
      {/* Header */}
      <div>
        <h2 className="text-base font-bold text-[#f5f7fa]" style={{ color: '#f5f7fa' }}>
          Top Up Virtual USDT
        </h2>
        <p className="text-xs text-slate-300 mt-0.5">
          Add simulated trading credits using real money
        </p>

        {/* Highlight Card */}
        <div className="mt-3.5 p-3 rounded-lg bg-[#10b981]/5 border border-[#10b981]/20 flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-amber-400/10 border border-amber-400/20 flex items-center justify-center flex-shrink-0">
            <Zap size={16} className="text-amber-400 fill-amber-400" />
          </div>
          <div className="flex flex-col">
            <span className="text-sm font-bold text-[#10b981]">
              1 INR = 1,000 Virtual USDT
            </span>
            <span className="text-[11px] text-slate-300">
              Fast, secure and powered by Razorpay
            </span>
          </div>
        </div>
      </div>

      {/* Usage Progress Section */}
      <div className="mt-4 flex flex-col gap-2">
        <div className="flex items-center justify-between text-xs">
          <span className="text-slate-300">Today's Top-Up Usage</span>
          <span className="font-mono font-bold text-[#f5f7fa]">
            {formatPrice(consumedUsdt.toString())} / {formatPrice(dailyLimitUsdt.toString())} USDT
          </span>
        </div>

        {/* Progress Bar with inline % */}
        <div className="flex items-center gap-2">
          <div className="flex-1 h-2 rounded-full bg-[#1e2530] overflow-hidden">
            <div
              className="h-full bg-gradient-to-r from-[#10b981] to-[#06b6d4] transition-all duration-300"
              style={{ width: `${usagePct}%` }}
            />
          </div>
          <span className="font-mono text-xs font-semibold text-slate-200 min-w-[32px] text-right">
            {usagePct}%
          </span>
        </div>

        {/* Limit note & learn more link */}
        <div className="flex items-center justify-between text-[11px] text-slate-400">
          <span>Daily Limit: {formatPrice(dailyLimitUsdt.toString())} USDT per day</span>
          <button
            type="button"
            onClick={onTopUpClick}
            className="text-[#10b981] hover:underline flex items-center gap-0.5 font-medium cursor-pointer"
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
          className="w-full py-2.5 rounded-lg bg-[#10b981] hover:bg-[#0ea572] text-[#0a0b0e] font-bold text-sm flex items-center justify-center gap-1.5 transition-all shadow-md shadow-[#10b981]/15 cursor-pointer"
        >
          <Plus size={16} strokeWidth={2.5} />
          Top Up Virtual USDT
        </button>
      </div>
    </div>
  )
}
