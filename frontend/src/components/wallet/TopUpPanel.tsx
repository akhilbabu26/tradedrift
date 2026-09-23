import { useState, useId } from 'react'
import { Zap, ArrowRight, Lock } from 'lucide-react'
import type { DailyUsage } from '../../api/topup'
import { formatPrice } from '../../utils/formatters'

interface TopUpPanelProps {
  dailyUsage: DailyUsage
  onPay: (inrAmount: number) => Promise<void>
  submitting?: boolean
}

export default function TopUpPanel({ dailyUsage, onPay, submitting = false }: TopUpPanelProps) {
  const [inrAmount, setInrAmount] = useState<number>(5)
  const sliderId = useId()

  const virtualUsdt = inrAmount * 1000
  const consumedUsdt = dailyUsage.consumedInr * 1000
  const dailyLimitUsdt = dailyUsage.dailyLimitInr * 1000
  const usagePct = dailyLimitUsdt > 0
    ? Math.min(100, Math.round((consumedUsdt / dailyLimitUsdt) * 100))
    : 0

  const handleSliderChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setInrAmount(parseInt(e.target.value, 10))
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    onPay(inrAmount)
  }

  return (
    <div
      id="top-up-panel"
      className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 flex flex-col justify-between shadow-sm"
    >
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        {/* Header */}
        <div>
          <h2 className="text-base font-bold text-[#f5f7fa]" style={{ color: '#f5f7fa' }}>
            Top Up Virtual USDT
          </h2>
          <p className="text-xs text-slate-300 mt-0.5">
            Get more simulated trading credits
          </p>

          {/* Rate Banner */}
          <div className="mt-3 p-2.5 rounded-lg bg-[#10b981]/5 border border-[#10b981]/20 flex items-center gap-2">
            <Zap size={15} className="text-amber-400 fill-amber-400 flex-shrink-0" />
            <span className="text-xs font-bold text-[#10b981]">
              1 INR = 1,000 Virtual USDT
            </span>
          </div>
        </div>

        {/* Amount Selector */}
        <div className="flex flex-col gap-2 pt-1">
          <div className="flex items-center justify-between text-xs">
            <label htmlFor={sliderId} className="font-semibold text-[#f5f7fa]">
              Select Amount (INR)
            </label>
            <span className="text-[11px] text-slate-400">
              Choose between ₹1 and ₹10
            </span>
          </div>

          {/* Slider */}
          <div className="flex flex-col gap-1.5 px-1 py-2">
            <input
              id={sliderId}
              type="range"
              min={1}
              max={10}
              step={1}
              value={inrAmount}
              onChange={handleSliderChange}
              className="w-full h-2 bg-[#1e2530] rounded-lg appearance-none cursor-pointer accent-[#10b981] focus:outline-none"
            />
            {/* Steps Mark 1..10 */}
            <div className="flex items-center justify-between text-[10px] font-mono text-slate-400 px-0.5 select-none font-medium">
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
        <div className="rounded-xl border border-[#1e2530] bg-[#0a0b0e] p-3.5 flex items-center justify-between">
          <div className="flex flex-col">
            <span className="text-[11px] uppercase tracking-wider text-slate-400 font-medium">
              Selected Amount
            </span>
            <span className="text-xl font-bold font-mono text-[#f5f7fa] mt-0.5">
              ₹ {inrAmount}
            </span>
          </div>

          <div className="w-8 h-8 rounded-full bg-white/[0.03] border border-white/10 flex items-center justify-center flex-shrink-0">
            <ArrowRight size={14} className="text-slate-300" />
          </div>

          <div className="flex flex-col text-right">
            <span className="text-[11px] uppercase tracking-wider text-slate-400 font-medium">
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
            <span className="text-slate-300">
              Daily Limit: {formatPrice(consumedUsdt.toString())} / {formatPrice(dailyLimitUsdt.toString())} USDT used today
            </span>
            <span className="font-mono font-semibold text-slate-200">
              {usagePct}%
            </span>
          </div>
          <div className="h-1.5 rounded-full bg-[#1e2530] overflow-hidden">
            <div
              className="h-full bg-[#06b6d4] transition-all duration-300"
              style={{ width: `${usagePct}%` }}
            />
          </div>
        </div>

        {/* Pay Action Button */}
        <button
          type="submit"
          disabled={submitting}
          className="w-full py-3 rounded-lg bg-[#10b981] hover:bg-[#0ea572] disabled:opacity-50 text-[#0a0b0e] font-extrabold text-sm transition-all shadow-lg shadow-[#10b981]/20 flex items-center justify-center gap-2 cursor-pointer"
        >
          {submitting ? (
            <>
              <span className="w-4 h-4 border-2 border-[#0a0b0e] border-t-transparent rounded-full animate-spin" />
              Processing...
            </>
          ) : (
            `Pay ₹${inrAmount} & Get ${formatPrice(virtualUsdt.toString())} USDT`
          )}
        </button>

        {/* Trust Badges */}
        <div className="pt-2 border-t border-[#1e2530]/60 flex flex-col items-center gap-2 select-none">
          <div className="flex items-center gap-1.5 text-[11px] text-slate-300">
            <Lock size={12} className="text-slate-400" />
            <span>Secure payment via Razorpay</span>
          </div>

          <div className="flex items-center gap-1.5 text-[10px] text-slate-200 flex-wrap justify-center">
            <span className="px-1.5 py-0.5 rounded bg-white/[0.06] border border-white/15 font-medium">
              UPI
            </span>
            <span className="px-1.5 py-0.5 rounded bg-white/[0.06] border border-white/15 font-medium">
              VISA
            </span>
            <span className="px-1.5 py-0.5 rounded bg-white/[0.06] border border-white/15 font-medium">
              Mastercard
            </span>
            <span className="px-1.5 py-0.5 rounded bg-white/[0.06] border border-white/15 font-medium">
              RuPay
            </span>
            <span className="text-[10px] text-slate-400 font-medium">
              and more...
            </span>
          </div>
        </div>
      </form>
    </div>
  )
}
