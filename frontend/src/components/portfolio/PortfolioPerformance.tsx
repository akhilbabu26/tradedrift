import PerformanceChart from './PerformanceChart'
import type { PerformanceDataPoint, TimeframeOption } from '../../types/portfolio'

const TIMEFRAMES: TimeframeOption[] = ['24H', '7D', '30D', '90D', 'ALL']

interface Props {
  data: PerformanceDataPoint[]
  timeframe: TimeframeOption
  onTimeframeChange: (tf: TimeframeOption) => void
  showBtcBenchmark: boolean
  onToggleBtcBenchmark: (v: boolean) => void
}

/**
 * Portfolio Performance card — wraps the chart with time tabs, legend, and BTC benchmark toggle.
 * All text uses TradeDrift light tokens. No dark inherited classes.
 */
export default function PortfolioPerformance({
  data,
  timeframe,
  onTimeframeChange,
  showBtcBenchmark,
  onToggleBtcBenchmark,
}: Props) {
  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl flex flex-col overflow-hidden">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-5 pt-5 pb-4 border-b border-[#1e2530] flex-shrink-0">
        <div>
          <h2 className="text-sm font-semibold text-[#f5f7fa]">
            Portfolio Performance
          </h2>
          {/* Legend */}
          <div className="flex items-center gap-4 mt-1">
            <div className="flex items-center gap-1.5">
              <span className="w-6 h-0.5 bg-[#10b981] rounded-full inline-block" />
              <span className="text-[11px] text-[#94a3b8]">Portfolio Value</span>
            </div>
            <div className="flex items-center gap-1.5">
              <span
                style={{ display: 'inline-block', width: 24, borderTop: '1px dashed #64748b', verticalAlign: 'middle' }}
              />
              <span className="text-[11px] text-[#64748b]">BTC (Benchmark)</span>
            </div>
          </div>
        </div>

        {/* Controls */}
        <div className="flex items-center gap-3 flex-wrap">
          {/* Timeframe pills */}
          <div className="flex items-center gap-1 bg-[#0a0b0e] border border-[#1e2530] rounded-lg p-1">
            {TIMEFRAMES.map((tf) => (
              <button
                key={tf}
                onClick={() => onTimeframeChange(tf)}
                className={`px-2.5 py-1 rounded-md text-[11px] font-medium transition-all ${
                  timeframe === tf
                    ? 'bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30'
                    : 'text-[#64748b] hover:text-[#94a3b8]'
                }`}
              >
                {tf}
              </button>
            ))}
          </div>

          {/* BTC benchmark toggle */}
          <label className="flex items-center gap-2 cursor-pointer select-none group">
            <div
              onClick={() => onToggleBtcBenchmark(!showBtcBenchmark)}
              className={`relative w-8 h-4 rounded-full transition-colors cursor-pointer ${
                showBtcBenchmark ? 'bg-[#10b981]' : 'bg-[#1e2530]'
              }`}
            >
              <span
                className={`absolute top-0.5 left-0.5 w-3 h-3 rounded-full bg-white transition-transform ${
                  showBtcBenchmark ? 'translate-x-4' : 'translate-x-0'
                }`}
              />
            </div>
            <span className="text-[11px] text-[#64748b] group-hover:text-[#94a3b8] transition-colors whitespace-nowrap">
              Compare with BTC
            </span>
          </label>
        </div>
      </div>

      {/* Chart area — fixed height matching reference */}
      <div className="px-4 pb-4 pt-2" style={{ height: '240px' }}>
        <PerformanceChart data={data} showBtcBenchmark={showBtcBenchmark} />
      </div>
    </div>
  )
}
