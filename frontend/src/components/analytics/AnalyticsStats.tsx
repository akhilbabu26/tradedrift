import { Info, TrendingUp, TrendingDown } from 'lucide-react'
import { RaisedSurface } from '../common/Surface'
import Sparkline from '../dashboard/shared/Sparkline'
import type { KpiCardItem } from '../../types/analytics'

interface Props {
  kpis: KpiCardItem[]
}

/**
 * Mini SVG histogram bars for Volume / Trades cards
 */
function MiniBars() {
  return (
    <svg width="40" height="28" viewBox="0 0 40 28" fill="none" className="flex-shrink-0">
      <rect x="2" y="16" width="4" height="12" rx="1" fill="#334155" />
      <rect x="9" y="10" width="4" height="18" rx="1" fill="#334155" />
      <rect x="16" y="14" width="4" height="14" rx="1" fill="#475569" />
      <rect x="23" y="6" width="4" height="22" rx="1" fill="#475569" />
      <rect x="30" y="2" width="4" height="26" rx="1" fill="#64748b" />
    </svg>
  )
}

/**
 * 4 KPI Cards in one horizontal row at desktop (1440px):
 * 1. Net PnL (-$1.54 or +$1,450.20)
 * 2. Traded Volume ($3,858.77)
 * 3. Win Rate (0.0% or 65.0%)
 * 4. Active Holdings (1)
 */
export default function AnalyticsStats({ kpis }: Props) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 select-none">
      {kpis.map((item) => {
        const isPnl = item.id.includes('pnl')
        const valueColor = isPnl
          ? item.isPositive
            ? 'text-[#10b981]'
            : 'text-[#ef4444]'
          : 'text-[#f5f7fa]'

        const trendColor = item.isPositive ? 'text-[#10b981]' : 'text-[#ef4444]'

        return (
          <RaisedSurface
            key={item.id}
            variant="raised-subtle"
            className="rounded-xl p-4 sm:p-5 flex flex-col justify-between transition-colors hover:border-white/10"
          >
            {/* Top header row: Label + Info icon + Visual */}
            <div className="flex items-center justify-between gap-2">
              <div className="flex items-center gap-1.5">
                <span className="text-xs font-medium text-[#94a3b8]">
                  {item.label}
                </span>
                <Info className="w-3 h-3 text-[#475569]" />
              </div>

              <div>
                {item.visualType === 'sparkline' ? (
                  <Sparkline
                    data={item.sparklinePoints}
                    positive={item.isPositive}
                    width={64}
                    height={28}
                    ariaLabel={`${item.label} trend`}
                  />
                ) : (
                  <MiniBars />
                )}
              </div>
            </div>

            {/* Value row */}
            <div className="mt-3">
              <p className="text-[22px] font-bold font-mono tracking-tight text-[#f5f7fa] flex items-baseline gap-1.5">
                <span className={valueColor}>
                  {item.value}
                </span>
                {item.unit && (
                  <span className="text-xs font-normal text-[#94a3b8] font-sans">
                    {item.unit}
                  </span>
                )}
              </p>
            </div>

            {/* Trend footer */}
            <div className={`mt-2.5 flex items-center gap-1 text-[11px] font-medium ${trendColor}`}>
              {item.isPositive ? (
                <TrendingUp className="w-3 h-3 flex-shrink-0" />
              ) : (
                <TrendingDown className="w-3 h-3 flex-shrink-0" />
              )}
              <span>{item.trendValue}</span>
              <span className="text-[#64748b] font-normal">{item.trendPeriod}</span>
            </div>
          </RaisedSurface>
        )
      })}
    </div>
  )
}

