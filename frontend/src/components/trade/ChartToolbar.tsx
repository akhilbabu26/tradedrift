import { useState, useRef, useEffect } from 'react'
import type { Timeframe } from '../../types/trade'
import type { ActiveIndicators } from '../../utils/indicators'
import {
  CandlestickChart,
  BarChart2,
  Maximize2,
  Camera,
  TrendingUp,
  Activity,
  ChevronDown,
  Check,
} from 'lucide-react'

interface ChartToolbarProps {
  timeframe: Timeframe
  onTimeframeChange: (tf: Timeframe) => void
  chartType: 'candlestick' | 'line'
  onChartTypeChange: (type: 'candlestick' | 'line') => void
  isDemoData: boolean
  activeIndicators?: ActiveIndicators
  onToggleIndicator?: (key: keyof ActiveIndicators) => void
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

interface IndicatorOption {
  key: keyof ActiveIndicators
  label: string
  sublabel: string
  color: string
}

const INDICATOR_OPTIONS: IndicatorOption[] = [
  { key: 'ema20', label: 'EMA 20', sublabel: 'Short Trend', color: '#06b6d4' },
  { key: 'ema50', label: 'EMA 50', sublabel: 'Medium Trend', color: '#f59e0b' },
  { key: 'ema200', label: 'EMA 200', sublabel: 'Long Trend', color: '#a855f7' },
  { key: 'bollinger', label: 'Bollinger Bands', sublabel: '(20, 2)', color: '#38bdf8' },
]

export default function ChartToolbar({
  timeframe,
  onTimeframeChange,
  chartType,
  onChartTypeChange,
  isDemoData,
  activeIndicators,
  onToggleIndicator,
  onScreenshot,
  onFullscreen,
}: ChartToolbarProps) {
  const [indicatorsOpen, setIndicatorsOpen] = useState(false)
  const [dropdownCoords, setDropdownCoords] = useState<{ top: number; left: number }>({ top: 0, left: 0 })
  const buttonRef = useRef<HTMLButtonElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)

  const activeCount = activeIndicators
    ? Object.values(activeIndicators).filter(Boolean).length
    : 0

  const updateCoords = () => {
    if (buttonRef.current) {
      const rect = buttonRef.current.getBoundingClientRect()
      setDropdownCoords({
        top: rect.bottom + 4,
        left: rect.left,
      })
    }
  }

  useEffect(() => {
    if (indicatorsOpen) {
      updateCoords()
      window.addEventListener('resize', updateCoords)
      window.addEventListener('scroll', updateCoords, true)
    }
    return () => {
      window.removeEventListener('resize', updateCoords)
      window.removeEventListener('scroll', updateCoords, true)
    }
  }, [indicatorsOpen])

  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      const target = event.target as Node
      if (
        dropdownRef.current &&
        !dropdownRef.current.contains(target) &&
        buttonRef.current &&
        !buttonRef.current.contains(target)
      ) {
        setIndicatorsOpen(false)
      }
    }
    if (indicatorsOpen) {
      document.addEventListener('mousedown', handleClickOutside)
    }
    return () => {
      document.removeEventListener('mousedown', handleClickOutside)
    }
  }, [indicatorsOpen])

  const handleWheel = (e: React.WheelEvent<HTMLDivElement>) => {
    if (e.deltaY !== 0) {
      e.currentTarget.scrollLeft += e.deltaY
    }
  }

  return (
    <div
      onWheel={handleWheel}
      className="flex items-center gap-0 px-2.5 min-h-[38px] h-[38px] border-b border-[#1e2530] bg-[#111318] flex-shrink-0 w-full min-w-0 trade-scrollbar-x select-none"
    >
      {/* Timeframe buttons */}
      <div className="flex items-center gap-0.5 mr-2 flex-shrink-0" role="group" aria-label="Chart timeframe">
        {TIMEFRAMES.map(({ key, label }) => {
          const active = timeframe === key
          return (
            <button
              key={key}
              type="button"
              onClick={() => onTimeframeChange(key)}
              aria-pressed={active}
              className={`px-2 py-1 text-[11px] font-medium rounded transition-colors whitespace-nowrap focus:outline-none focus-visible:ring-1 focus-visible:ring-[#10b981] ${
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
      <div className="w-px h-3.5 bg-[#1e2530] mr-2 flex-shrink-0" />

      {/* Chart type toggle */}
      <div className="flex items-center gap-0.5 mr-2 flex-shrink-0" role="group" aria-label="Chart type">
        <button
          type="button"
          onClick={() => onChartTypeChange('candlestick')}
          aria-pressed={chartType === 'candlestick'}
          aria-label="Candlestick chart"
          title="Candlestick"
          className={`p-1 rounded transition-colors border flex-shrink-0 ${
            chartType === 'candlestick'
              ? 'text-[#10b981] bg-[#10b981]/10 border-[#10b981]/20'
              : 'text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 border-transparent'
          }`}
        >
          <CandlestickChart size={13} />
        </button>
        <button
          type="button"
          onClick={() => onChartTypeChange('line')}
          aria-pressed={chartType === 'line'}
          aria-label="Line chart"
          title="Line"
          className={`p-1 rounded transition-colors border flex-shrink-0 ${
            chartType === 'line'
              ? 'text-[#10b981] bg-[#10b981]/10 border-[#10b981]/20'
              : 'text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 border-transparent'
          }`}
        >
          <TrendingUp size={13} />
        </button>
      </div>

      {/* Divider */}
      <div className="w-px h-3.5 bg-[#1e2530] mr-2 flex-shrink-0" />

      {/* Indicators Dropdown */}
      <div className="relative flex-shrink-0 mr-2">
        <button
          ref={buttonRef}
          type="button"
          onClick={() => {
            updateCoords()
            setIndicatorsOpen((prev) => !prev)
          }}
          className={`flex items-center gap-1.5 px-2 py-1 rounded text-xs font-medium transition-colors border whitespace-nowrap ${
            indicatorsOpen || activeCount > 0
              ? 'bg-[#10b981]/10 text-[#10b981] border-[#10b981]/30'
              : 'text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 border-transparent'
          }`}
          aria-label="Toggle indicators menu"
          aria-expanded={indicatorsOpen}
        >
          <Activity size={13} />
          <span>Indicators</span>
          {activeCount > 0 && (
            <span className="w-4 h-4 rounded-full bg-[#10b981] text-[#0a0b0e] text-[10px] font-bold flex items-center justify-center">
              {activeCount}
            </span>
          )}
          <ChevronDown
            size={12}
            className={`transition-transform duration-150 ${indicatorsOpen ? 'rotate-180 text-[#10b981]' : 'text-slate-500'}`}
          />
        </button>

        {indicatorsOpen && (
          <div
            ref={dropdownRef}
            style={{ top: `${dropdownCoords.top}px`, left: `${dropdownCoords.left}px` }}
            className="fixed w-60 bg-[#161B22] border border-[#292C30] rounded-lg shadow-2xl p-1.5 z-[9999] animate-in fade-in zoom-in-95 duration-100"
            role="menu"
            aria-label="Indicators menu"
          >
            <div className="px-2 py-1.5 border-b border-[#292C30] mb-1 flex items-center justify-between">
              <span className="text-[11px] font-semibold text-slate-300 uppercase tracking-wider">
                Overlay Indicators
              </span>
              <span className="text-[10px] text-slate-500 font-mono">Category A</span>
            </div>

            <div className="space-y-0.5">
              {INDICATOR_OPTIONS.map(({ key, label, sublabel, color }) => {
                const isActive = activeIndicators ? !!activeIndicators[key] : false
                return (
                  <button
                    key={key}
                    type="button"
                    onClick={() => {
                      onToggleIndicator?.(key)
                    }}
                    role="menuitemcheckbox"
                    aria-checked={isActive}
                    className={`w-full flex items-center justify-between px-2 py-1.5 rounded-md text-xs transition-colors text-left ${
                      isActive
                        ? 'bg-[#10b981]/10 text-[#f5f7fa]'
                        : 'text-slate-300 hover:bg-white/5 hover:text-white'
                    }`}
                  >
                    <div className="flex items-center gap-2">
                      <span
                        className="w-2.5 h-2.5 rounded-full flex-shrink-0 shadow-sm"
                        style={{ backgroundColor: color }}
                      />
                      <div className="flex flex-col">
                        <span className="font-medium text-[11px] text-[#f5f7fa]">{label}</span>
                        <span className="text-[10px] text-slate-400 font-mono">{sublabel}</span>
                      </div>
                    </div>
                    <div
                      className={`w-4 h-4 rounded border flex items-center justify-center transition-colors ${
                        isActive
                          ? 'bg-[#10b981] border-[#10b981] text-[#0a0b0e]'
                          : 'border-slate-600 bg-transparent'
                      }`}
                    >
                      {isActive && <Check size={11} strokeWidth={3} />}
                    </div>
                  </button>
                )
              })}
            </div>
          </div>
        )}
      </div>

      {/* Demo badge */}
      {isDemoData && (
        <span className="text-[10px] font-semibold text-amber-400 bg-amber-500/10 border border-amber-500/20 px-1.5 py-0.5 rounded uppercase tracking-wider mr-3 flex-shrink-0 whitespace-nowrap">
          DEMO
        </span>
      )}

      {/* Spacer */}
      <div className="flex-1 min-w-[8px]" />

      {/* Volume indicator label */}
      <div className="flex items-center gap-1 mr-3 flex-shrink-0 whitespace-nowrap">
        <BarChart2 size={12} className="text-slate-600" />
        <span className="text-[10px] text-slate-600 font-mono">Volume</span>
      </div>

      {/* Screenshot */}
      <button
        type="button"
        onClick={onScreenshot}
        aria-label="Save chart as image"
        title="Screenshot"
        className="p-1.5 rounded text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors border border-transparent flex-shrink-0"
      >
        <Camera size={13} />
      </button>

      {/* Fullscreen */}
      <button
        type="button"
        onClick={onFullscreen}
        aria-label="Toggle fullscreen chart"
        title="Fullscreen"
        className="p-1.5 rounded text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors border border-transparent flex-shrink-0"
      >
        <Maximize2 size={13} />
      </button>
    </div>
  )
}
