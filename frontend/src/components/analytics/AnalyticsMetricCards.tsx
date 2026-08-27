import { Clock, Shield, Info, BarChart2 } from 'lucide-react'

interface AnalyticsMetricCardsProps {
  netProfit?: number
  winRatePct?: number
  winsCount?: number
  lossesCount?: number
  profitFactor?: number
  maxDrawdownPct?: number
  totalTradesCount?: number
}

export default function AnalyticsMetricCards({
  netProfit = 0,
  winRatePct = 0,
  winsCount = 0,
  lossesCount = 0,
  profitFactor = 0,
  maxDrawdownPct = 0,
  totalTradesCount = 0,
}: AnalyticsMetricCardsProps) {
  const isProfit = netProfit >= 0

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4 select-none">
      {/* ── CARD 1: Cumulative Net Profit ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-4 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e676]/30 transition-all">
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium font-sans">
            <div className="w-5 h-5 rounded-md bg-[#00e676]/10 text-[#00e676] flex items-center justify-center text-[10px] font-mono font-bold">
              $
            </div>
            <span>Cumulative Net Profit</span>
            <Info size={12} className="text-slate-500 hover:text-slate-300 cursor-pointer" />
          </div>
        </div>

        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div
              className={`text-xl lg:text-2xl font-black font-mono tracking-tight ${
                isProfit ? 'text-[#00e676]' : 'text-[#ff3366]'
              }`}
            >
              {isProfit ? '+' : '-'}${Math.abs(netProfit).toLocaleString('en-US', { minimumFractionDigits: 2 })}
            </div>
            <div className="flex items-center gap-1 mt-1 text-[11px] font-mono text-slate-400">
              <span className="text-slate-400 font-sans text-[10px]">Realized Account Return</span>
            </div>
          </div>

          {/* Sparkline */}
          <div className="w-20 h-9 flex-shrink-0">
            <svg className="w-full h-full" viewBox="0 0 80 30" preserveAspectRatio="none">
              <path
                d={isProfit ? 'M0,25 Q15,22 30,18 T55,14 T70,8 T80,3' : 'M0,5 Q15,10 30,15 T55,20 T70,24 T80,27'}
                fill="none"
                stroke={isProfit ? '#00e676' : '#ff3366'}
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
          </div>
        </div>
      </div>

      {/* ── CARD 2: Win Rate ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-4 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#a855f7]/30 transition-all">
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium font-sans">
            <div className="w-5 h-5 rounded-md bg-[#a855f7]/10 text-[#a855f7] flex items-center justify-center text-[10px] font-mono font-bold">
              %
            </div>
            <span>Win Rate</span>
            <Info size={12} className="text-slate-500 hover:text-slate-300 cursor-pointer" />
          </div>
        </div>

        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-xl lg:text-2xl font-black font-mono text-white tracking-tight">
              {winRatePct > 0 ? `${winRatePct.toFixed(1)}%` : '--'}
            </div>
            <div className="text-[11px] font-mono text-slate-400 mt-1">
              {winsCount} Wins / {lossesCount} Losses
            </div>
          </div>

          {/* Purple Circular Donut Ring */}
          <div className="w-10 h-10 flex-shrink-0 relative flex items-center justify-center">
            <svg className="w-full h-full -rotate-90" viewBox="0 0 36 36">
              <path
                className="text-[#1b2230]"
                strokeWidth="3.5"
                stroke="currentColor"
                fill="none"
                d="M18 2.0845 a 15.9155 15.9155 0 0 1 0 31.831 a 15.9155 15.9155 0 0 1 0 -31.831"
              />
              <path
                className="text-[#a855f7]"
                strokeDasharray={`${winRatePct}, 100`}
                strokeWidth="3.5"
                strokeLinecap="round"
                stroke="currentColor"
                fill="none"
                d="M18 2.0845 a 15.9155 15.9155 0 0 1 0 31.831 a 15.9155 15.9155 0 0 1 0 -31.831"
              />
            </svg>
          </div>
        </div>
      </div>

      {/* ── CARD 3: Profit Factor ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-4 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e5ff]/30 transition-all">
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium font-sans">
            <div className="w-5 h-5 rounded-md bg-[#00e5ff]/10 text-[#00e5ff] flex items-center justify-center">
              <BarChart2 size={12} />
            </div>
            <span>Profit Factor</span>
            <Info size={12} className="text-slate-500 hover:text-slate-300 cursor-pointer" />
          </div>
        </div>

        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-xl lg:text-2xl font-black font-mono text-white tracking-tight">
              {profitFactor > 0 ? profitFactor.toFixed(2) : '--'}
            </div>
            <div className="text-[11px] font-sans font-medium text-[#00e5ff] mt-1">
              {totalTradesCount > 0 ? 'Active Performance' : 'Awaiting Trades'}
            </div>
          </div>

          {/* Cyan Vertical Equalizer Bars */}
          <div className="flex items-end gap-1 h-8 flex-shrink-0">
            <div className="w-1.5 h-3 bg-[#00e5ff]/40 rounded-full" />
            <div className="w-1.5 h-4.5 bg-[#00e5ff]/60 rounded-full" />
            <div className="w-1.5 h-6 bg-[#00e5ff]/80 rounded-full" />
            <div className="w-1.5 h-7.5 bg-[#00e5ff] rounded-full shadow-[0_0_6px_#00e5ff]" />
          </div>
        </div>
      </div>

      {/* ── CARD 4: Max Drawdown ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-4 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#ff3366]/30 transition-all">
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium font-sans">
            <div className="w-5 h-5 rounded-md bg-[#ff3366]/10 text-[#ff3366] flex items-center justify-center">
              <Shield size={12} />
            </div>
            <span>Max Drawdown</span>
            <Info size={12} className="text-slate-500 hover:text-slate-300 cursor-pointer" />
          </div>
        </div>

        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-xl lg:text-2xl font-black font-mono text-white tracking-tight">
              {maxDrawdownPct !== 0 ? `${maxDrawdownPct.toFixed(2)}%` : '0.00%'}
            </div>
            <div className="text-[11px] font-sans font-medium text-emerald-400 mt-1">
              Protected Risk State
            </div>
          </div>

          {/* Red Downward Sparkline */}
          <div className="w-20 h-9 flex-shrink-0">
            <svg className="w-full h-full" viewBox="0 0 80 30" preserveAspectRatio="none">
              <path
                d="M0,8 Q20,6 40,16 T60,22 T80,26"
                fill="none"
                stroke="#00e676"
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
          </div>
        </div>
      </div>

      {/* ── CARD 5: Total Executions ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-4 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e5ff]/30 transition-all">
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium font-sans">
            <div className="w-5 h-5 rounded-md bg-[#00e5ff]/10 text-[#00e5ff] flex items-center justify-center">
              <Clock size={12} />
            </div>
            <span>Total Executions</span>
            <Info size={12} className="text-slate-500 hover:text-slate-300 cursor-pointer" />
          </div>
        </div>

        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-xl lg:text-2xl font-black font-mono text-white tracking-tight">
              {totalTradesCount}
            </div>
            <div className="text-[11px] font-sans text-slate-400 mt-1">
              Filled Orders
            </div>
          </div>

          <div className="w-9 h-9 rounded-full bg-[#00e5ff]/10 border border-[#00e5ff]/20 text-[#00e5ff] flex items-center justify-center flex-shrink-0">
            <Clock size={16} />
          </div>
        </div>
      </div>
    </div>
  )
}
