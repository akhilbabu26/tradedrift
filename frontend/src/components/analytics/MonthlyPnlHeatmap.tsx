import { useState } from 'react'
import { Info, ChevronLeft, ChevronRight } from 'lucide-react'

interface HeatmapDay {
  dayNum: number
  pnlStr: string
  pnlNum: number
  tier: 'deep-green' | 'soft-green' | 'soft-red' | 'deep-red' | 'no-data'
  isCurrentMonth: boolean
  isToday?: boolean
}

const AUGUST_DAYS: HeatmapDay[] = [
  // Previous month trailing days
  { dayNum: 27, pnlStr: '', pnlNum: 0, tier: 'no-data', isCurrentMonth: false },
  { dayNum: 28, pnlStr: '', pnlNum: 0, tier: 'no-data', isCurrentMonth: false },
  { dayNum: 29, pnlStr: '', pnlNum: 0, tier: 'no-data', isCurrentMonth: false },
  { dayNum: 30, pnlStr: '', pnlNum: 0, tier: 'no-data', isCurrentMonth: false },
  { dayNum: 31, pnlStr: '', pnlNum: 0, tier: 'no-data', isCurrentMonth: false },

  // August days
  { dayNum: 1, pnlStr: '+1,250.50', pnlNum: 1250.5, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 2, pnlStr: '+980.30', pnlNum: 980.3, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 3, pnlStr: '+1,820.40', pnlNum: 1820.4, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 4, pnlStr: '-450.20', pnlNum: -450.2, tier: 'soft-red', isCurrentMonth: true },
  { dayNum: 5, pnlStr: '+2,150.75', pnlNum: 2150.75, tier: 'deep-green', isCurrentMonth: true },
  { dayNum: 6, pnlStr: '+980.60', pnlNum: 980.6, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 7, pnlStr: '-320.10', pnlNum: -320.1, tier: 'soft-red', isCurrentMonth: true },
  { dayNum: 8, pnlStr: '+1,430.80', pnlNum: 1430.8, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 9, pnlStr: '+2,210.90', pnlNum: 2210.9, tier: 'deep-green', isCurrentMonth: true },
  { dayNum: 10, pnlStr: '+1,100.20', pnlNum: 1100.2, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 11, pnlStr: '+540.30', pnlNum: 540.3, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 12, pnlStr: '-780.50', pnlNum: -780.5, tier: 'deep-red', isCurrentMonth: true },
  { dayNum: 13, pnlStr: '+1,650.40', pnlNum: 1650.4, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 14, pnlStr: '+2,340.60', pnlNum: 2340.6, tier: 'deep-green', isCurrentMonth: true },
  { dayNum: 15, pnlStr: '-620.30', pnlNum: -620.3, tier: 'deep-red', isCurrentMonth: true },
  { dayNum: 16, pnlStr: '+1,820.25', pnlNum: 1820.25, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 17, pnlStr: '+2,450.80', pnlNum: 2450.8, tier: 'deep-green', isCurrentMonth: true },
  { dayNum: 18, pnlStr: '+980.40', pnlNum: 980.4, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 19, pnlStr: '+1,120.30', pnlNum: 1120.3, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 20, pnlStr: '-410.20', pnlNum: -410.2, tier: 'soft-red', isCurrentMonth: true },
  { dayNum: 21, pnlStr: '+2,030.70', pnlNum: 2030.7, tier: 'deep-green', isCurrentMonth: true },
  { dayNum: 22, pnlStr: '+1,540.20', pnlNum: 1540.2, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 23, pnlStr: '+2,610.50', pnlNum: 2610.5, tier: 'deep-green', isCurrentMonth: true },
  { dayNum: 24, pnlStr: '+1,330.60', pnlNum: 1330.6, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 25, pnlStr: '+980.20', pnlNum: 980.2, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 26, pnlStr: '+1,760.90', pnlNum: 1760.9, tier: 'soft-green', isCurrentMonth: true },
  { dayNum: 27, pnlStr: '+2,320.50', pnlNum: 2320.5, tier: 'deep-green', isCurrentMonth: true, isToday: true },

  // Remaining unreached days
  { dayNum: 28, pnlStr: '', pnlNum: 0, tier: 'no-data', isCurrentMonth: true },
  { dayNum: 29, pnlStr: '', pnlNum: 0, tier: 'no-data', isCurrentMonth: true },
  { dayNum: 30, pnlStr: '', pnlNum: 0, tier: 'no-data', isCurrentMonth: true },
]

export default function MonthlyPnlHeatmap() {
  const [month] = useState('August 2026')

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 flex flex-col justify-between shadow-xl select-none">
      {/* ── Header: Title & Month Switcher ── */}
      <div className="flex items-center justify-between pb-3 border-b border-[#1b2230]/60">
        <div className="flex items-center gap-1.5 text-sm font-bold text-white font-sans tracking-tight">
          <span>Monthly PnL Heatmap Matrix</span>
          <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer" />
        </div>

        {/* Month Selector */}
        <div className="flex items-center gap-2">
          <button
            type="button"
            className="p-1 rounded-lg bg-[#07090e] border border-[#1b2230] text-slate-400 hover:text-white"
          >
            <ChevronLeft size={13} />
          </button>
          <span className="text-xs font-semibold text-white font-sans">{month}</span>
          <button
            type="button"
            className="p-1 rounded-lg bg-[#07090e] border border-[#1b2230] text-slate-400 hover:text-white"
          >
            <ChevronRight size={13} />
          </button>
        </div>
      </div>

      {/* ── Days of Week Header ── */}
      <div className="grid grid-cols-7 gap-1.5 text-center text-[10px] font-semibold text-slate-400 font-sans mt-3">
        <span>Mon</span>
        <span>Tue</span>
        <span>Wed</span>
        <span>Thu</span>
        <span>Fri</span>
        <span>Sat</span>
        <span>Sun</span>
      </div>

      {/* ── Heatmap Calendar Grid ── */}
      <div className="grid grid-cols-7 gap-1.5 mt-2">
        {AUGUST_DAYS.map((d, idx) => {
          const isDeepGreen = d.tier === 'deep-green'
          const isSoftGreen = d.tier === 'soft-green'
          const isSoftRed = d.tier === 'soft-red'
          const isDeepRed = d.tier === 'deep-red'

          let bgClass = 'bg-[#07090e] border-[#1b2230] text-slate-600'
          if (isDeepGreen) {
            bgClass = 'bg-[#00e676]/20 border-[#00e676]/40 text-[#00e676]'
          } else if (isSoftGreen) {
            bgClass = 'bg-[#00e676]/10 border-[#00e676]/25 text-[#00e676]'
          } else if (isDeepRed) {
            bgClass = 'bg-[#ff3366]/20 border-[#ff3366]/40 text-[#ff3366]'
          } else if (isSoftRed) {
            bgClass = 'bg-[#ff3366]/10 border-[#ff3366]/25 text-[#ff3366]'
          }

          if (d.isToday) {
            bgClass += ' ring-1 ring-[#00e5ff] shadow-[0_0_8px_rgba(0,229,255,0.25)]'
          }

          return (
            <div
              key={idx}
              className={`rounded-lg p-1.5 border min-h-[46px] flex flex-col justify-between transition-all ${bgClass}`}
            >
              <span className={`text-[10px] font-mono font-semibold ${d.isCurrentMonth ? 'text-slate-300' : 'text-slate-600'}`}>
                {d.dayNum}
              </span>
              {d.pnlStr ? (
                <span className="text-[9px] font-mono font-bold truncate leading-tight">
                  {d.pnlStr}
                </span>
              ) : (
                <span className="text-[9px] text-transparent leading-tight">-</span>
              )}
            </div>
          )
        })}
      </div>

      {/* ── Legend at Bottom ── */}
      <div className="flex flex-wrap items-center justify-between gap-2 pt-3 mt-3 border-t border-[#1b2230]/60 text-[10px] font-sans text-slate-400">
        <div className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-sm bg-[#00e676]/30 border border-[#00e676]/50" />
          <span>&gt; +2.0%</span>
        </div>
        <div className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-sm bg-[#00e676]/15 border border-[#00e676]/30" />
          <span>+0.5% to +2.0%</span>
        </div>
        <div className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-sm bg-[#ff3366]/15 border border-[#ff3366]/30" />
          <span>-0.5% to -0.0%</span>
        </div>
        <div className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-sm bg-[#ff3366]/30 border border-[#ff3366]/50" />
          <span>&lt; -0.5%</span>
        </div>
        <div className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-sm bg-[#07090e] border border-[#1b2230]" />
          <span>No Data</span>
        </div>
      </div>
    </div>
  )
}
