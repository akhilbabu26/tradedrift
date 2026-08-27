import { Coins, Info } from 'lucide-react'

interface HistoryKpiCardsProps {
  totalVolumeUsd?: number
  btcEquiv?: number
  realizedPnlUsd?: number
  pnlChangePercent?: number
  feesPaidUsdt?: number
  feeTier?: string
  feeRate?: string
}

export default function HistoryKpiCards({
  totalVolumeUsd = 1842500.0,
  btcEquiv = 19.285734,
  realizedPnlUsd = 18450.25,
  pnlChangePercent = 14.2,
  feesPaidUsdt = 420.5,
  feeTier = 'VIP 2',
  feeRate = '0.02%',
}: HistoryKpiCardsProps) {
  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-4 select-none">
      {/* ── CARD 1: 30-Day Total Volume ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e5ff]/30 transition-all">
        {/* Header */}
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium font-sans">
            <span>30-Day Total Volume</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer transition-colors" />
          </div>
        </div>

        {/* Value + Sparkline */}
        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-2xl lg:text-3xl font-black font-mono text-white tracking-tight flex items-baseline gap-1.5">
              <span>${totalVolumeUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}</span>
              <span className="text-xs font-semibold text-slate-400 font-sans">USD</span>
            </div>
            <div className="text-xs font-mono text-slate-400 mt-1">
              ≈ {btcEquiv.toFixed(6)} BTC
            </div>
          </div>

          {/* Cyan Smooth Sparkline Graphic */}
          <div className="w-28 h-12 flex-shrink-0">
            <svg className="w-full h-full" viewBox="0 0 100 40" preserveAspectRatio="none">
              <defs>
                <linearGradient id="volumeSparkGradient" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#00e5ff" stopOpacity="0.35" />
                  <stop offset="100%" stopColor="#00e5ff" stopOpacity="0.0" />
                </linearGradient>
              </defs>
              <path
                d="M0,32 Q20,28 35,22 T60,20 T80,12 T100,6 L100,40 L0,40 Z"
                fill="url(#volumeSparkGradient)"
              />
              <path
                d="M0,32 Q20,28 35,22 T60,20 T80,12 T100,6"
                fill="none"
                stroke="#00e5ff"
                strokeWidth="2.2"
                strokeLinecap="round"
              />
            </svg>
          </div>
        </div>
      </div>

      {/* ── CARD 2: Realized PnL (30D) ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e676]/30 transition-all">
        {/* Header */}
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium font-sans">
            <span>Realized PnL (30D)</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer transition-colors" />
          </div>
        </div>

        {/* Value + Sparkline */}
        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-2xl lg:text-3xl font-black font-mono text-[#00e676] tracking-tight flex items-baseline gap-1.5">
              <span>+${realizedPnlUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}</span>
              <span className="text-xs font-semibold text-slate-400 font-sans">USD</span>
            </div>
            <div className="flex items-center gap-1.5 mt-1.5">
              <span className="text-[#00e676] font-bold text-xs font-mono">
                ↑ +{pnlChangePercent.toFixed(1)}%
              </span>
              <span className="text-slate-400 text-xs font-sans">vs Previous 30 Days</span>
            </div>
          </div>

          {/* Green Smooth Sparkline Graphic */}
          <div className="w-28 h-12 flex-shrink-0">
            <svg className="w-full h-full" viewBox="0 0 100 40" preserveAspectRatio="none">
              <defs>
                <linearGradient id="pnlSparkGradient" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#00e676" stopOpacity="0.35" />
                  <stop offset="100%" stopColor="#00e676" stopOpacity="0.0" />
                </linearGradient>
              </defs>
              <path
                d="M0,34 Q20,30 40,24 T65,18 T85,10 T100,4 L100,40 L0,40 Z"
                fill="url(#pnlSparkGradient)"
              />
              <path
                d="M0,34 Q20,30 40,24 T65,18 T85,10 T100,4"
                fill="none"
                stroke="#00e676"
                strokeWidth="2.2"
                strokeLinecap="round"
              />
            </svg>
          </div>
        </div>
      </div>

      {/* ── CARD 3: Total Trading Fees Paid ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e5ff]/30 transition-all">
        {/* Header */}
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium font-sans">
            <span>Total Trading Fees Paid</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer transition-colors" />
          </div>
        </div>

        {/* Value + Fee Tier Badge + Coin Graphic */}
        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-2xl lg:text-3xl font-black font-mono text-white tracking-tight flex items-baseline gap-1.5">
              <span>${feesPaidUsdt.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}</span>
              <span className="text-xs font-semibold text-slate-400 font-sans">USDT</span>
            </div>
            <div className="flex items-center gap-2 mt-1.5 text-xs font-sans">
              <span className="text-slate-400">Maker / Taker Fee Tier</span>
              <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-[#00e5ff]/15 text-[#00e5ff] border border-[#00e5ff]/30 font-mono">
                {feeTier}
              </span>
              <span className="text-slate-300 font-mono">{feeRate}</span>
            </div>
          </div>

          {/* Coin Stack Icon Graphic */}
          <div className="w-11 h-11 rounded-full bg-[#00e5ff]/10 border border-[#00e5ff]/30 text-[#00e5ff] flex items-center justify-center shadow-[0_0_15px_rgba(0,229,255,0.15)] flex-shrink-0">
            <Coins size={22} />
          </div>
        </div>
      </div>
    </div>
  )
}
