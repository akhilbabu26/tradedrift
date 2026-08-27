import { FileText, Target, BarChart2, Info } from 'lucide-react'

interface OrdersKpiCardsProps {
  openOrdersCount: number
  lockedValueUsd: number
  triggerOrdersCount: number
  totalExposureUsd: number
  filledTodayCount: number
  filledTodayVolumeUsd: number
}

export default function OrdersKpiCards({
  openOrdersCount = 0,
  lockedValueUsd = 0,
  triggerOrdersCount = 0,
  totalExposureUsd = 0,
  filledTodayCount = 0,
  filledTodayVolumeUsd = 0,
}: OrdersKpiCardsProps) {
  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-4 select-none">
      {/* ── CARD 1: Active Open Orders ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e5ff]/30 transition-all">
        {/* Header */}
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium">
            <span>Active Open Orders</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer transition-colors" />
          </div>
          <div className="w-8 h-8 rounded-xl bg-[#00e5ff]/10 border border-[#00e5ff]/20 text-[#00e5ff] flex items-center justify-center shadow-[0_0_12px_rgba(0,229,255,0.15)]">
            <FileText size={16} />
          </div>
        </div>

        {/* Value + Cyan Sparkline */}
        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-2xl lg:text-3xl font-black font-mono text-white tracking-tight">
              {openOrdersCount} <span className="text-base font-semibold text-slate-400 font-sans">Orders</span>
            </div>
            <div className="text-xs font-mono text-slate-400 mt-1">
              ${lockedValueUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} Locked Value
            </div>
          </div>

          {/* Cyan Smooth Sparkline Graphic */}
          <div className="w-28 h-12 flex-shrink-0">
            <svg className="w-full h-full" viewBox="0 0 100 40" preserveAspectRatio="none">
              <defs>
                <linearGradient id="openOrdersSparkGradient" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#00e5ff" stopOpacity="0.35" />
                  <stop offset="100%" stopColor="#00e5ff" stopOpacity="0.0" />
                </linearGradient>
              </defs>
              <path
                d="M0,32 Q20,28 35,22 T60,20 T80,12 T100,6 L100,40 L0,40 Z"
                fill="url(#openOrdersSparkGradient)"
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

      {/* ── CARD 2: Trigger Orders (Stop-Loss / Take-Profit) ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#a855f7]/30 transition-all">
        {/* Header */}
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium">
            <span>Trigger Orders (Stop-Loss / Take-Profit)</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer transition-colors" />
          </div>
          <div className="w-8 h-8 rounded-xl bg-[#a855f7]/10 border border-[#a855f7]/20 text-[#a855f7] flex items-center justify-center shadow-[0_0_12px_rgba(168,85,247,0.15)]">
            <Target size={16} />
          </div>
        </div>

        {/* Value + Purple Sparkline */}
        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-2xl lg:text-3xl font-black font-mono text-white tracking-tight">
              {triggerOrdersCount} <span className="text-base font-semibold text-slate-400 font-sans">Active</span>
            </div>
            <div className="text-xs font-mono text-slate-400 mt-1">
              ${totalExposureUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} Total Exposure
            </div>
          </div>

          {/* Purple Smooth Sparkline Graphic */}
          <div className="w-28 h-12 flex-shrink-0">
            <svg className="w-full h-full" viewBox="0 0 100 40" preserveAspectRatio="none">
              <defs>
                <linearGradient id="triggerOrdersSparkGradient" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#a855f7" stopOpacity="0.35" />
                  <stop offset="100%" stopColor="#a855f7" stopOpacity="0.0" />
                </linearGradient>
              </defs>
              <path
                d="M0,30 Q25,24 45,28 T70,18 T90,14 T100,8 L100,40 L0,40 Z"
                fill="url(#triggerOrdersSparkGradient)"
              />
              <path
                d="M0,30 Q25,24 45,28 T70,18 T90,14 T100,8"
                fill="none"
                stroke="#a855f7"
                strokeWidth="2.2"
                strokeLinecap="round"
              />
            </svg>
          </div>
        </div>
      </div>

      {/* ── CARD 3: Filled Today ── */}
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#00e676]/30 transition-all">
        {/* Header */}
        <div className="flex items-center justify-between z-10">
          <div className="flex items-center gap-1.5 text-xs text-slate-400 font-medium">
            <span>Filled Today</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer transition-colors" />
          </div>
          <div className="w-8 h-8 rounded-xl bg-[#00e676]/10 border border-[#00e676]/20 text-[#00e676] flex items-center justify-center shadow-[0_0_12px_rgba(0,230,118,0.15)]">
            <BarChart2 size={16} />
          </div>
        </div>

        {/* Value + Emerald Sparkline */}
        <div className="flex items-end justify-between mt-3 z-10">
          <div>
            <div className="text-2xl lg:text-3xl font-black font-mono text-white tracking-tight">
              {filledTodayCount} <span className="text-base font-semibold text-slate-400 font-sans">Orders</span>
            </div>
            <div className="text-xs font-mono text-slate-400 mt-1">
              ${filledTodayVolumeUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} Volume
            </div>
          </div>

          {/* Emerald Smooth Sparkline Graphic */}
          <div className="w-28 h-12 flex-shrink-0">
            <svg className="w-full h-full" viewBox="0 0 100 40" preserveAspectRatio="none">
              <defs>
                <linearGradient id="filledOrdersSparkGradient" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#00e676" stopOpacity="0.35" />
                  <stop offset="100%" stopColor="#00e676" stopOpacity="0.0" />
                </linearGradient>
              </defs>
              <path
                d="M0,34 Q20,30 40,24 T65,18 T85,10 T100,4 L100,40 L0,40 Z"
                fill="url(#filledOrdersSparkGradient)"
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
    </div>
  )
}
