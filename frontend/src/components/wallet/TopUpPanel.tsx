import { useState, useId } from 'react'
import { Zap, ArrowRight, Lock } from 'lucide-react'
import type { DailyUsage } from '../../api/topup'
import { formatPrice } from '../../utils/formatters'
import { RaisedSurface } from '../common/Surface'

interface TopUpPanelProps {
  dailyUsage: DailyUsage | null
  onPay: (inrAmount: number) => Promise<void>
  submitting?: boolean
}

export default function TopUpPanel({ dailyUsage, onPay, submitting = false }: TopUpPanelProps) {
  const [inrAmount, setInrAmount] = useState<number>(5)
  const sliderId = useId()

  const virtualUsdt = inrAmount * 1000
  const hasUsage = dailyUsage != null
  const consumedUsdt = hasUsage ? dailyUsage.consumedInr * 1000 : null
  const dailyLimitUsdt = hasUsage ? dailyUsage.dailyLimitInr * 1000 : null
  const usagePct = (dailyLimitUsdt != null && dailyLimitUsdt > 0 && consumedUsdt != null)
    ? Math.min(100, Math.round((consumedUsdt / dailyLimitUsdt) * 100))
    : 0

  const remainingInr = hasUsage
    ? (dailyUsage.remainingInr !== undefined
        ? dailyUsage.remainingInr
        : Math.max(0, (dailyUsage.dailyLimitInr ?? 10) - (dailyUsage.consumedInr ?? 0) - (dailyUsage.reservedInr ?? 0)))
    : null
  const isDailyLimitReached = remainingInr != null && remainingInr <= 0
  const exceedsRemaining = remainingInr != null && inrAmount > remainingInr
  const isActionDisabled = submitting || !hasUsage || isDailyLimitReached || exceedsRemaining

  const handleSliderChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setInrAmount(parseInt(e.target.value, 10))
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    onPay(inrAmount)
  }

  return (
    <RaisedSurface
      id="top-up-panel"
      variant="raised-subtle"
      className="flex flex-col h-full"
    >
      <form onSubmit={handleSubmit} className="flex-1 flex flex-col justify-between gap-4">
        {/* Top Controls Cluster */}
        <div className="flex flex-col gap-3.5">
          {/* Header */}
          <div>
            <h2 className="text-base font-bold text-[#F2F3F5]">
              Top Up Virtual USDT
            </h2>
            <p className="text-[11px] text-[#777E87] mt-0.5">
              Get more simulated trading credits
            </p>

            {/* Rate Banner */}
            <div className="mt-2.5 p-2 rounded-xl bg-[#090A0C] border border-[#292C30] flex items-center gap-2 shadow-[inset_0_1px_3px_rgba(0,0,0,0.5)]">
              <Zap size={15} className="text-amber-400 fill-amber-400 flex-shrink-0" />
              <span className="text-xs font-bold text-[#10b981]">
                1 INR = 1,000 Virtual USDT
              </span>
            </div>
          </div>

          {/* Amount Selector */}
          <div className="flex flex-col gap-1.5 pt-0.5">
            <div className="flex items-center justify-between text-xs">
              <label htmlFor={sliderId} className="font-semibold text-[#F2F3F5]">
                Select Amount (INR)
              </label>
              <span className="text-[11px] text-[#777E87]">
                Choose between ₹1 and ₹10
              </span>
            </div>

            {/* Slider */}
            <div className="flex flex-col gap-1 px-1 py-1">
              <input
                id={sliderId}
                type="range"
                min={1}
                max={10}
                step={1}
                value={inrAmount}
                onChange={handleSliderChange}
                className="w-full h-2 bg-[#090A0C] border border-[#202225] rounded-lg appearance-none cursor-pointer accent-[#10b981] focus:outline-none"
              />
              {/* Steps Mark 1..10 */}
              <div className="flex items-center justify-between text-[10px] font-mono text-[#777E87] px-0.5 select-none font-medium">
                {[1, 2, 3, 4, 5, 6, 7, 8, 9, 10].map((step) => (
                  <span
                    key={step}
                    className={`transition-colors ${
                      step === inrAmount ? 'text-[#10b981] font-bold scale-110' : ''
                    }`}
                  >
                    {step}
                  </span>
                ))}
              </div>
            </div>
          </div>

          {/* Selected Amount -> USDT Preview Box */}
          <div className="rounded-xl border border-[#292C30] bg-[#090A0C] p-3 flex items-center justify-between shadow-[inset_0_2px_4px_rgba(0,0,0,0.5)]">
            <div className="flex flex-col">
              <span className="text-[11px] uppercase tracking-wider text-[#777E87] font-medium">
                Selected Amount
              </span>
              <span className="text-xl font-bold font-mono text-[#F2F3F5] mt-0.5">
                ₹ {inrAmount}
              </span>
            </div>

            <div className="w-8 h-8 rounded-full bg-[#16181C] border border-[#292C30] flex items-center justify-center flex-shrink-0">
              <ArrowRight size={14} className="text-[#A7ADB5]" />
            </div>

            <div className="flex flex-col text-right">
              <span className="text-[11px] uppercase tracking-wider text-[#777E87] font-medium">
                You will receive
              </span>
              <span className="text-xl font-black font-mono text-[#10b981] mt-0.5">
                {formatPrice(virtualUsdt.toString())} USDT
              </span>
            </div>
          </div>

          {/* Daily Limit Bar */}
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between text-[11px]">
              <span className="text-[#A7ADB5]">
                {hasUsage && consumedUsdt != null && dailyLimitUsdt != null
                  ? `Daily Limit: ${formatPrice(consumedUsdt.toString())} / ${formatPrice(dailyLimitUsdt.toString())} USDT used today`
                  : 'Daily Limit: Usage unavailable'}
              </span>
              <span className="font-mono font-semibold text-[#F2F3F5]">
                {hasUsage ? `${usagePct}%` : '—'}
              </span>
            </div>
            <div className="h-1.5 rounded-full bg-[#090A0C] border border-[#202225] overflow-hidden">
              <div
                className="h-full bg-[#06b6d4] transition-all duration-300"
                style={{ width: `${hasUsage ? usagePct : 0}%` }}
              />
            </div>
          </div>
        </div>

        {/* Bottom Actions Cluster */}
        <div className="flex flex-col gap-3 pt-2">
          {/* Pay Action Button */}
          <button
            type="submit"
            disabled={isActionDisabled}
            className="w-full py-2.5 rounded-lg bg-[#10b981] hover:bg-[#0ea572] active:bg-[#059669] disabled:opacity-50 disabled:cursor-not-allowed text-[#08090B] font-extrabold text-sm transition-all shadow-[0_2px_8px_rgba(16,185,129,0.3)] hover:shadow-[0_4px_12px_rgba(16,185,129,0.4)] flex items-center justify-center gap-2 cursor-pointer focus:outline-none focus:ring-2 focus:ring-[#10b981]/50"
          >
            {submitting ? (
              <>
                <span className="w-4 h-4 border-2 border-[#08090B] border-t-transparent rounded-full animate-spin" />
                Processing...
              </>
            ) : !hasUsage ? (
              'Daily Limit Unavailable'
            ) : isDailyLimitReached ? (
              'Daily Limit Reached (₹10 / 10,000 USDT max per day)'
            ) : exceedsRemaining ? (
              `Exceeds Limit (Only ₹${remainingInr} remaining today)`
            ) : (
              `Pay ₹${inrAmount} & Get ${formatPrice(virtualUsdt.toString())} USDT`
            )}
          </button>

          {/* Trust Badges */}
          <div className="pt-2 border-t border-[#202225] flex flex-col items-center gap-2 select-none">
            <div className="flex items-center gap-1.5 text-[11px] text-[#A7ADB5]">
              <Lock size={12} className="text-[#777E87]" />
              <span>Secure payment via Razorpay</span>
            </div>

            <div className="flex items-center gap-1.5 text-[10px] text-[#A7ADB5] flex-wrap justify-center">
              <span className="px-1.5 py-0.5 rounded bg-[#090A0C] border border-[#292C30] font-medium">
                UPI
              </span>
              <span className="px-1.5 py-0.5 rounded bg-[#090A0C] border border-[#292C30] font-medium">
                VISA
              </span>
              <span className="px-1.5 py-0.5 rounded bg-[#090A0C] border border-[#292C30] font-medium">
                Mastercard
              </span>
              <span className="px-1.5 py-0.5 rounded bg-[#090A0C] border border-[#292C30] font-medium">
                RuPay
              </span>
              <span className="text-[10px] text-[#777E87] font-medium">
                and more...
              </span>
            </div>
          </div>
        </div>
      </form>
    </RaisedSurface>
  )
}
