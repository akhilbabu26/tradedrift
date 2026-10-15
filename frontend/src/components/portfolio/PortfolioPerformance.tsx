import PerformanceChart from './PerformanceChart'
import type { PerformanceDataPoint, TimeframeOption } from '../../types/portfolio'
import { RaisedSurface } from '../common/Surface'

const TIMEFRAMES: TimeframeOption[] = ['24H', '7D', '30D', '90D', '1Y']

interface Props {
  data: PerformanceDataPoint[]
  timeframe: TimeframeOption
  onTimeframeChange: (tf: TimeframeOption) => void
  showBtcBenchmark: boolean
  onToggleBtcBenchmark: (v: boolean) => void
  isLoading?: boolean
  error?: string | null
  onRetry?: () => void
}

/**
 * Portfolio Performance card — wraps the chart with timeframe switcher matching Dashboard visual fidelity.
 */
export default function PortfolioPerformance({
  data,
  timeframe,
  onTimeframeChange,
  showBtcBenchmark,
  onToggleBtcBenchmark,
  isLoading = false,
  error = null,
  onRetry,
}: Props) {
  return (
    <RaisedSurface variant="raised" noPadding className="flex flex-col overflow-hidden h-full">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-5 pt-5 pb-4 border-b border-[#202225] flex-shrink-0">
        <div>
          <h2 className="text-sm font-semibold text-[#F2F3F5]">
            Portfolio Performance
          </h2>
          <p className="text-[11px] font-medium text-[#777E87] mt-0.5 uppercase tracking-wider">
            Portfolio Value (USDT)
          </p>
        </div>

        {/* Controls */}
        <div className="flex items-center gap-3 flex-wrap">
          {/* Timeframe pills matching Dashboard */}
          <div
            className="flex items-center gap-1 bg-[#090A0C] rounded-xl border border-[#292C30] p-1 shadow-[inset_0_2px_4px_rgba(0,0,0,0.5)]"
            role="tablist"
            aria-label="Portfolio period"
          >
            {TIMEFRAMES.map((tf) => {
              const isActive = timeframe === tf || (tf === '1Y' && timeframe === 'ALL')
              return (
                <button
                  key={tf}
                  role="tab"
                  aria-selected={isActive}
                  onClick={() => onTimeframeChange(tf)}
                  className={`px-3 py-1 rounded-lg text-xs font-semibold transition-all duration-150 ${
                    isActive
                      ? 'bg-gradient-to-b from-[#FFFFFF] to-[#D9DCE0] text-[#090B0D] shadow-[0_2px_8px_rgba(0,0,0,0.4)]'
                      : 'text-[#777E87] hover:text-[#F2F3F5]'
                  }`}
                >
                  {tf}
                </button>
              )
            })}
          </div>

          {/* BTC benchmark toggle */}
          <label className="flex items-center gap-2 cursor-pointer select-none group">
            <div
              onClick={() => onToggleBtcBenchmark(!showBtcBenchmark)}
              className={`relative w-8 h-4 rounded-full transition-colors cursor-pointer ${
                showBtcBenchmark ? 'bg-[#10B981]' : 'bg-[#292C30]'
              }`}
            >
              <span
                className={`absolute top-0.5 left-0.5 w-3 h-3 rounded-full bg-white transition-transform ${
                  showBtcBenchmark ? 'translate-x-4' : 'translate-x-0'
                }`}
              />
            </div>
            <span className="text-[11px] text-[#777E87] group-hover:text-[#F2F3F5] transition-colors whitespace-nowrap">
              Compare with BTC
            </span>
          </label>
        </div>
      </div>

      {/* Chart area — responsive height aligned with header padding */}
      <div className="flex-1 w-full min-h-[220px] px-5 pt-2 pb-4 relative flex flex-col justify-end">
        <PerformanceChart
          data={data}
          showBtcBenchmark={showBtcBenchmark}
          isLoading={isLoading}
          error={error}
          onRetry={onRetry}
        />
      </div>
    </RaisedSurface>
  )
}
