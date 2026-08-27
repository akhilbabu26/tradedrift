import { useState } from 'react'
import { ChevronDown } from 'lucide-react'

interface MarketDepthChartProps {
  baseAsset: string
  lastPrice: string
}

export default function MarketDepthChart({ baseAsset, lastPrice }: MarketDepthChartProps) {
  const [grouping, setGrouping] = useState('100')
  const currentPriceNum = parseFloat(lastPrice || '96450.00') || 96450.00

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col p-3 select-none shadow-xl mt-4">
      {/* Header: Title + Grouping + Legend */}
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2">
          <span className="text-xs font-bold text-white font-sans">Market Depth ({baseAsset})</span>
          {/* Grouping */}
          <button
            type="button"
            onClick={() => setGrouping(grouping === '100' ? '50' : '100')}
            className="flex items-center gap-1 px-1.5 py-0.5 rounded bg-[#07090e] border border-[#1b2230] text-[9px] font-mono text-slate-300 hover:text-white"
          >
            <span>{grouping}</span>
            <ChevronDown size={10} className="text-slate-500" />
          </button>
        </div>

        {/* Legend */}
        <div className="flex items-center gap-2.5 text-[10px] font-mono">
          <span className="flex items-center gap-1 text-[#00e676]">
            <span className="w-1.5 h-1.5 rounded-sm bg-[#00e676]" /> Bids
          </span>
          <span className="flex items-center gap-1 text-[#ff3366]">
            <span className="w-1.5 h-1.5 rounded-sm bg-[#ff3366]" /> Asks
          </span>
        </div>
      </div>

      {/* Stepped Cumulative SVG Depth Visual */}
      <div className="w-full h-28 relative">
        <svg className="w-full h-full" viewBox="0 0 300 100" preserveAspectRatio="none">
          <defs>
            <linearGradient id="bidsGradient" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#00e676" stopOpacity="0.25" />
              <stop offset="100%" stopColor="#00e676" stopOpacity="0.02" />
            </linearGradient>
            <linearGradient id="asksGradient" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#ff3366" stopOpacity="0.25" />
              <stop offset="100%" stopColor="#ff3366" stopOpacity="0.02" />
            </linearGradient>
          </defs>

          {/* Left Y-Axis Gridlines */}
          <line x1="0" x2="300" y1="20" y2="20" stroke="#1b2230" strokeDasharray="2,2" strokeWidth="0.8" />
          <line x1="0" x2="300" y1="50" y2="50" stroke="#1b2230" strokeDasharray="2,2" strokeWidth="0.8" />
          <line x1="0" x2="300" y1="80" y2="80" stroke="#1b2230" strokeDasharray="2,2" strokeWidth="0.8" />

          {/* Stepped Bids (Left - Green) */}
          <path
            d="M10,25 L35,25 L35,40 L70,40 L70,60 L105,60 L105,78 L140,78 L150,95 L150,100 L10,100 Z"
            fill="url(#bidsGradient)"
          />
          <path
            d="M10,25 L35,25 L35,40 L70,40 L70,60 L105,60 L105,78 L140,78 L150,95"
            fill="none"
            stroke="#00e676"
            strokeWidth="1.5"
          />

          {/* Stepped Asks (Right - Red) */}
          <path
            d="M150,95 L160,78 L195,78 L195,60 L230,60 L230,40 L265,40 L265,20 L290,20 L290,100 L150,100 Z"
            fill="url(#asksGradient)"
          />
          <path
            d="M150,95 L160,78 L195,78 L195,60 L230,60 L230,40 L265,40 L265,20 L290,20"
            fill="none"
            stroke="#ff3366"
            strokeWidth="1.5"
          />
        </svg>

        {/* Left Y-Axis Labels */}
        <div className="absolute left-1 top-0 bottom-0 flex flex-col justify-between text-[8px] font-mono text-slate-500 pointer-events-none">
          <span>3.0K</span>
          <span>2.0K</span>
          <span>1.0K</span>
          <span>0</span>
        </div>
      </div>

      {/* Bottom X-Axis Price Labels */}
      <div className="flex justify-between pt-1 border-t border-[#1b2230]/60 text-[9px] font-mono text-slate-500">
        <span>{(currentPriceNum - 450).toFixed(0)}</span>
        <span>{(currentPriceNum - 200).toFixed(0)}</span>
        <span className="text-white font-bold">{currentPriceNum.toFixed(0)}</span>
        <span>{(currentPriceNum + 200).toFixed(0)}</span>
        <span>{(currentPriceNum + 450).toFixed(0)}</span>
      </div>
    </div>
  )
}
