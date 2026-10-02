import type { Timeframe } from '../../types/trade'
import { CandlestickChart, BarChart2, Maximize2, Camera, TrendingUp, Activity } from 'lucide-react'

interface ChartToolbarProps {
  timeframe: Timeframe
  onTimeframeChange: (tf: Timeframe) => void
  chartType: 'candlestick' | 'line'
  onChartTypeChange: (type: 'candlestick' | 'line') => void
  isDemoData: boolean
  onScreenshot?: () => void
  onFullscreen?: () => void
}

const TIMEFRAMES: { key: Timeframe; label: string }[] = [
  { key: '1m',  label: '1m'  },
  { key: '5m',  label: '5m'  },
  { key: '15m', label: '15m' },
  { key: '1h',  label: '1H'  },
  { key: '4h',  label: '4H'  },
  { key: '1d',  label: '1D'  },
]

export default function ChartToolbar({
  timeframe,
  onTimeframeChange,
  chartType,
  onChartTypeChange,
  isDemoData,
  onScreenshot,
  onFullscreen,
}: ChartToolbarProps) {
  return (
    <div className="flex items-center gap-0 px-3 py-1.5 border-b border-[#1e2530] bg-[#111318] flex-shrink-0">
      {/* Timeframe buttons */}
      <div className="flex items-center gap-0 mr-3" role="group" aria-label="Chart timeframe">
        {TIMEFRAMES.map(({ key, label }) => {
          const active = timeframe === key
          return (
            <button
              key={key}
              type="button"
              onClick={() => onTimeframeChange(key)}
              aria-pressed={active}
              className={`px-2.5 py-1 text-xs font-medium rounded transition-colors focus:outline-none focus-visible:ring-1 focus-visible:ring-[#10b981] ${
                active
                  ? 'bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/25'
                  : 'text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 border border-transparent'
              }`}
            >
              {label}
            </button>
          )
        })}
      </div>

      {/* Divider */}
      <div className="w-px h-4 bg-[#1e2530] mr-3" />

      {/* Chart type toggle */}
      <div className="flex items-center gap-0.5 mr-3" role="group" aria-label="Chart type">
        <button
          type="button"
          onClick={() => onChartTypeChange('candlestick')}
          aria-pressed={chartType === 'candlestick'}
          aria-label="Candlestick chart"
          title="Candlestick"
          className={`p-1.5 rounded transition-colors border ${
            chartType === 'candlestick'
              ? 'text-[#10b981] bg-[#10b981]/10 border-[#10b981]/20'
              : 'text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 border-transparent'
          }`}
        >
          <CandlestickChart size={14} />
        </button>
        <button
          type="button"
          onClick={() => onChartTypeChange('line')}
          aria-pressed={chartType === 'line'}
          aria-label="Line chart"
          title="Line"
          className={`p-1.5 rounded transition-colors border ${
            chartType === 'line'
              ? 'text-[#10b981] bg-[#10b981]/10 border-[#10b981]/20'
              : 'text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 border-transparent'
          }`}
        >
          <TrendingUp size={14} />
        </button>
      </div>

      {/* Divider */}
      <div className="w-px h-4 bg-[#1e2530] mr-3" />

      {/* Indicators */}
      <button
        type="button"
        className="flex items-center gap-1.5 px-2 py-1 rounded text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 text-xs font-medium transition-colors border border-transparent mr-3"
        aria-label="Toggle indicators"
      >
        <Activity size={13} />
        Indicators
      </button>

      {/* Demo badge */}
      {isDemoData && (
        <span className="text-[10px] font-semibold text-amber-400 bg-amber-500/10 border border-amber-500/20 px-1.5 py-0.5 rounded uppercase tracking-wider mr-3">
          DEMO
        </span>
      )}

      {/* Spacer */}
      <div className="flex-1" />

      {/* Volume indicator label */}
      <div className="flex items-center gap-1 mr-3">
        <BarChart2 size={12} className="text-slate-600" />
        <span className="text-[10px] text-slate-600 font-mono">Volume</span>
      </div>

      {/* Screenshot */}
      <button
        type="button"
        onClick={onScreenshot}
        aria-label="Save chart as image"
        title="Screenshot"
        className="p-1.5 rounded text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors border border-transparent"
      >
        <Camera size={13} />
      </button>

      {/* Fullscreen */}
      <button
        type="button"
        onClick={onFullscreen}
        aria-label="Toggle fullscreen chart"
        title="Fullscreen"
        className="p-1.5 rounded text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors border border-transparent"
      >
        <Maximize2 size={13} />
      </button>
    </div>
  )
}
