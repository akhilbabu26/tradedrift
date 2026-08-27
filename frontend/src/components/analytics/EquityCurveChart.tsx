import { useState } from 'react'
import { Info, Maximize2, ChevronDown } from 'lucide-react'

const TIME_RANGES = ['7D', '30D', '90D', '6M', '1Y', 'All']

export default function EquityCurveChart() {
  const [activeRange, setActiveRange] = useState('30D')
  const [interval, setInterval] = useState('Daily')
  const [hoveredPoint] = useState<{
    date: string
    equity: string
    benchmark: string
    diff: string
    diffPct: string
    x: number
    y: number
  } | null>({
    date: 'Aug 27, 2026',
    equity: '$48,920.50',
    benchmark: '$31,250.80',
    diff: '+$17,669.70',
    diffPct: '(+56.62%)',
    x: 880,
    y: 120,
  })

  // Exact SVG curve data points matching reference trajectory
  const portfolioPath =
    'M 30,300 Q 80,290 120,270 T 200,240 T 280,225 T 360,200 T 440,165 T 520,180 T 600,140 T 680,110 T 760,130 T 840,90 T 940,65'
  const portfolioAreaPath = `${portfolioPath} L 940,340 L 30,340 Z`
  const benchmarkPath =
    'M 30,305 Q 80,300 120,290 T 200,280 T 280,265 T 360,250 T 440,240 T 520,230 T 600,215 T 680,225 T 760,205 T 840,185 T 940,155'

  const yLabels = ['60,000', '50,000', '40,000', '30,000', '20,000', '10,000', '0', '-10,000']
  const xLabels = [
    'Jul 29',
    'Aug 1',
    'Aug 4',
    'Aug 7',
    'Aug 10',
    'Aug 13',
    'Aug 16',
    'Aug 19',
    'Aug 22',
    'Aug 25',
    'Aug 27',
  ]

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 flex flex-col shadow-xl select-none relative overflow-hidden">
      {/* ── Header: Title & Legend + Controls ── */}
      <div className="flex flex-wrap items-center justify-between gap-4 pb-4 border-b border-[#1b2230]/60">
        {/* Left: Title & Legend */}
        <div className="flex flex-wrap items-center gap-5">
          <div className="flex items-center gap-1.5 text-sm font-bold text-white font-sans tracking-tight">
            <span>Cumulative Equity Curve</span>
            <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer" />
          </div>

          <div className="flex items-center gap-4 text-xs font-sans">
            <div className="flex items-center gap-1.5">
              <span className="w-2 h-2 rounded-full bg-[#00e5ff] shadow-[0_0_6px_#00e5ff]" />
              <span className="text-slate-300">Portfolio Equity</span>
            </div>
            <div className="flex items-center gap-1.5">
              <span className="w-2 h-2 rounded-full bg-[#f7931a]" />
              <span className="text-slate-300">BTC Benchmark</span>
            </div>
          </div>
        </div>

        {/* Right: Time Range & Interval Controls */}
        <div className="flex items-center gap-3">
          {/* Time range buttons */}
          <div className="flex items-center gap-1 bg-[#07090e] border border-[#1b2230] p-0.5 rounded-lg text-xs font-sans">
            {TIME_RANGES.map((r) => {
              const isActive = activeRange === r
              return (
                <button
                  key={r}
                  type="button"
                  onClick={() => setActiveRange(r)}
                  className={`px-2.5 py-1 rounded-md text-xs font-semibold transition-all ${
                    isActive
                      ? 'bg-[#00e5ff]/15 text-[#00e5ff] border border-[#00e5ff]/40 shadow-[0_0_8px_rgba(0,229,255,0.15)]'
                      : 'text-slate-400 hover:text-white'
                  }`}
                >
                  {r}
                </button>
              )
            })}
          </div>

          {/* Interval Dropdown */}
          <div className="relative">
            <select
              value={interval}
              onChange={(e) => setInterval(e.target.value)}
              className="appearance-none bg-[#07090e] border border-[#1b2230] rounded-lg px-2.5 py-1 pr-6 text-xs text-white font-sans focus:outline-none focus:border-[#00e5ff]/50 cursor-pointer"
            >
              <option value="Daily">Daily</option>
              <option value="Weekly">Weekly</option>
              <option value="Monthly">Monthly</option>
            </select>
            <ChevronDown size={12} className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
          </div>

          {/* Expand Fullscreen Button */}
          <button
            type="button"
            aria-label="Expand Chart"
            className="p-1.5 rounded-lg bg-[#07090e] border border-[#1b2230] text-slate-400 hover:text-white hover:border-slate-500 transition-colors"
          >
            <Maximize2 size={13} />
          </button>
        </div>
      </div>

      {/* ── Main Chart Canvas (Interactive SVG) ── */}
      <div className="relative w-full h-80 mt-4">
        <svg className="w-full h-full" viewBox="0 0 980 370" preserveAspectRatio="none">
          <defs>
            {/* Cyan glowing area fill gradient */}
            <linearGradient id="equityAreaGradient" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#00e5ff" stopOpacity="0.25" />
              <stop offset="60%" stopColor="#00e5ff" stopOpacity="0.06" />
              <stop offset="100%" stopColor="#00e5ff" stopOpacity="0.0" />
            </linearGradient>
          </defs>

          {/* Horizontal Grid lines & Y-Axis Labels */}
          {yLabels.map((val, idx) => {
            const y = 30 + idx * 40
            return (
              <g key={val}>
                <line
                  x1="30"
                  y1={y}
                  x2="960"
                  y2={y}
                  stroke="#1b2230"
                  strokeWidth="0.8"
                  strokeDasharray="2 4"
                />
                <text
                  x="20"
                  y={y + 3}
                  fill="#64748b"
                  fontSize="10"
                  fontFamily="monospace"
                  textAnchor="end"
                >
                  {val}
                </text>
              </g>
            )
          })}

          {/* Portfolio Equity Area Fill */}
          <path d={portfolioAreaPath} fill="url(#equityAreaGradient)" />

          {/* BTC Benchmark Line (Orange dashed) */}
          <path
            d={benchmarkPath}
            fill="none"
            stroke="#f7931a"
            strokeWidth="1.8"
            strokeDasharray="4 4"
          />

          {/* Portfolio Equity Line (Cyan solid) */}
          <path
            d={portfolioPath}
            fill="none"
            stroke="#00e5ff"
            strokeWidth="2.4"
            strokeLinecap="round"
            strokeLinejoin="round"
          />

          {/* Endpoint Circles */}
          <circle cx="940" cy="155" r="4" fill="#f7931a" />
          <circle
            cx="940"
            cy="65"
            r="5"
            fill="#00e5ff"
            stroke="#07090e"
            strokeWidth="2"
            className="shadow-[0_0_8px_#00e5ff]"
          />

          {/* Vertical Current Line */}
          <line
            x1="940"
            y1="40"
            x2="940"
            y2="330"
            stroke="#00e5ff"
            strokeWidth="1"
            strokeDasharray="2 2"
            opacity="0.6"
          />
        </svg>

        {/* X-Axis Date Labels */}
        <div className="flex justify-between px-6 pt-2 text-[10px] font-mono text-slate-400 border-t border-[#1b2230]/60">
          {xLabels.map((lbl) => (
            <span key={lbl}>{lbl}</span>
          ))}
        </div>

        {/* Floating Tooltip Box matching reference */}
        {hoveredPoint && (
          <div className="absolute right-8 top-16 bg-[#07090e]/95 border border-[#1b2230] rounded-xl p-3.5 shadow-2xl backdrop-blur-md text-xs font-mono w-56 pointer-events-none select-none z-20">
            <div className="text-[11px] font-bold text-white mb-2 font-sans border-b border-[#1b2230] pb-1">
              {hoveredPoint.date}
            </div>
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-1.5 text-slate-300">
                  <span className="w-2 h-2 rounded-full bg-[#00e5ff]" />
                  <span>Portfolio Equity</span>
                </div>
                <span className="text-white font-bold">{hoveredPoint.equity}</span>
              </div>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-1.5 text-slate-300">
                  <span className="w-2 h-2 rounded-full bg-[#f7931a]" />
                  <span>BTC Benchmark</span>
                </div>
                <span className="text-slate-300 font-semibold">{hoveredPoint.benchmark}</span>
              </div>
              <div className="pt-2 mt-1 border-t border-[#1b2230]/80 flex items-center justify-between">
                <span className="text-slate-400 text-[10px] font-sans">Difference</span>
                <span className="text-[#00e676] font-bold">
                  {hoveredPoint.diff} <span className="text-[10px]">{hoveredPoint.diffPct}</span>
                </span>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
