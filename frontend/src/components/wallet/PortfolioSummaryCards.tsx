import { useState } from 'react'
import { Eye, EyeOff, Wallet, Lock, Info } from 'lucide-react'

interface PortfolioSummaryCardsProps {
  totalEquityUsd: number
  totalBtcEquiv: number
  availableUsd: number
  availableBtcEquiv: number
  reservedUsd: number
  reservedBtcEquiv: number
  pnlPercent: number
  pnlUsd: number
}

export default function PortfolioSummaryCards({
  totalEquityUsd,
  totalBtcEquiv,
  availableUsd,
  availableBtcEquiv,
  reservedUsd,
  reservedBtcEquiv,
  pnlPercent,
  pnlUsd,
}: PortfolioSummaryCardsProps) {
  const [showValues, setShowValues] = useState(true)

  const fmtUsd = (n: number) =>
    showValues
      ? `$${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
      : '••••••••'

  const fmtBtc = (n: number) =>
    showValues ? `≈ ${n.toFixed(8)} BTC` : '≈ •••••••• BTC'

  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
      {/* ── CARD 1: Total Estimated Equity ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e5ff]/30 transition-all">
        {/* Card Header */}
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium">
            <span>Total Estimated Equity</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer transition-colors" />
          </div>
          <button
            type="button"
            onClick={() => setShowValues(!showValues)}
            aria-label="Toggle value visibility"
            className="text-slate-500 hover:text-slate-300 transition-colors p-1 rounded-lg hover:bg-[#141a26]"
          >
            {showValues ? <Eye size={15} /> : <EyeOff size={15} />}
          </button>
        </div>

        {/* Values + Cyan Sparkline */}
        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-2xl lg:text-3xl font-black font-mono text-white tracking-tight flex items-baseline gap-1.5">
              <span>{fmtUsd(totalEquityUsd)}</span>
              {showValues && <span className="text-xs font-semibold text-slate-400 font-sans">USD</span>}
            </div>
            <div className="text-xs font-mono text-slate-400 mt-1">
              {fmtBtc(totalBtcEquiv)}
            </div>
          </div>

          {/* Cyan Smooth Sparkline Graphic */}
          <div className="w-28 h-12 flex-shrink-0">
            <svg className="w-full h-full" viewBox="0 0 100 40" preserveAspectRatio="none">
              <defs>
                <linearGradient id="equitySparkGradient" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#00e5ff" stopOpacity="0.35" />
                  <stop offset="100%" stopColor="#00e5ff" stopOpacity="0.0" />
                </linearGradient>
              </defs>
              <path
                d="M0,32 Q15,28 30,30 T60,18 T80,12 T100,4 L100,40 L0,40 Z"
                fill="url(#equitySparkGradient)"
              />
              <path
                d="M0,32 Q15,28 30,30 T60,18 T80,12 T100,4"
                fill="none"
                stroke="#00e5ff"
                strokeWidth="2.2"
                strokeLinecap="round"
              />
            </svg>
          </div>
        </div>

        {/* 24h PnL Badge */}
        <div className="flex items-center gap-2 mt-4 pt-3 border-t border-[#1b2230]/60 z-10 text-xs font-mono">
          <span className="flex items-center gap-1 text-[#00e676] bg-[#00e676]/10 border border-[#00e676]/20 px-2 py-0.5 rounded-md font-bold text-[11px]">
            ↑ {pnlPercent.toFixed(2)}%
          </span>
          <span className="text-[#00e676] font-semibold">
            +${pnlUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
          </span>
          <span className="text-slate-400 text-[11px] font-sans">(24h PnL)</span>
        </div>
      </div>

      {/* ── CARD 2: Available Funds ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e5ff]/30 transition-all">
        {/* Card Header */}
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium">
            <span>Available Funds</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer transition-colors" />
          </div>
          <div className="w-8 h-8 rounded-xl bg-[#00e5ff]/10 border border-[#00e5ff]/20 text-[#00e5ff] flex items-center justify-center shadow-[0_0_12px_rgba(0,229,255,0.15)]">
            <Wallet size={16} />
          </div>
        </div>

        {/* Values */}
        <div className="mt-3 z-10">
          <div className="text-2xl lg:text-3xl font-black font-mono text-white tracking-tight flex items-baseline gap-1.5">
            <span>{fmtUsd(availableUsd)}</span>
            {showValues && <span className="text-xs font-semibold text-slate-400 font-sans">USD</span>}
          </div>
          <div className="text-xs font-mono text-slate-400 mt-1">
            {fmtBtc(availableBtcEquiv)}
          </div>
        </div>

        {/* Bottom indicator */}
        <div className="flex items-center justify-between mt-4 pt-3 border-t border-[#1b2230]/60 z-10 text-xs text-slate-400 font-sans">
          <span>Ready for instant spot execution</span>
          <span className="text-[#00e5ff] font-mono font-medium">
            {totalEquityUsd > 0 ? `${((availableUsd / totalEquityUsd) * 100).toFixed(1)}%` : '100%'}
          </span>
        </div>
      </div>

      {/* ── CARD 3: In-Order Reserved ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-amber-500/30 transition-all">
        {/* Card Header */}
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium">
            <span>In-Order Reserved</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer transition-colors" />
          </div>
          <div className="w-8 h-8 rounded-xl bg-amber-400/10 border border-amber-400/20 text-amber-400 flex items-center justify-center shadow-[0_0_12px_rgba(251,191,36,0.15)]">
            <Lock size={16} />
          </div>
        </div>

        {/* Values */}
        <div className="mt-3 z-10">
          <div className="text-2xl lg:text-3xl font-black font-mono text-white tracking-tight flex items-baseline gap-1.5">
            <span>{fmtUsd(reservedUsd)}</span>
            {showValues && <span className="text-xs font-semibold text-slate-400 font-sans">USD</span>}
          </div>
          <div className="text-xs font-mono text-slate-400 mt-1">
            {fmtBtc(reservedBtcEquiv)}
          </div>
        </div>

        {/* Bottom indicator */}
        <div className="flex items-center justify-between mt-4 pt-3 border-t border-[#1b2230]/60 z-10 text-xs text-slate-400 font-sans">
          <span>Locked in resting limit orders</span>
          <span className="text-amber-400 font-mono font-medium">
            {totalEquityUsd > 0 ? `${((reservedUsd / totalEquityUsd) * 100).toFixed(1)}%` : '0.0%'}
          </span>
        </div>
      </div>
    </div>
  )
}
