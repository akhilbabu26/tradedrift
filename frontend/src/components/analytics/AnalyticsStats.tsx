import { Info, TrendingUp } from 'lucide-react'
import type { KpiCardItem } from '../../types/analytics'

interface Props {
  kpis: KpiCardItem[]
}

/**
 * Mini SVG sparkline for PnL cards
 */
function Sparkline() {
  return (
    <svg width="64" height="32" viewBox="0 0 64 32" fill="none" className="flex-shrink-0">
      <path
        d="M2 24C12 22 18 10 26 14C34 18 42 6 52 10C56 12 60 4 62 3"
        stroke="#10b981"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
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
 * 1. Realized PnL (+1,450.20 USDT, +12.4%)
 * 2. Unrealized PnL (+3,120.00 USDT, +8.7%)
 * 3. Total Trades (Fills) (42, +27.3%)
 * 4. Total Volume Traded (28,400.00 USDT, +19.6%)
 */
export default function AnalyticsStats({ kpis }: Props) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 select-none">
      {kpis.map((item) => (
        <div
          key={item.id}
          className="bg-[#111318] border border-[#1e2530] rounded-xl p-4 sm:p-5 flex flex-col justify-between transition-colors hover:border-[#2a3442]"
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
              {item.visualType === 'sparkline' ? <Sparkline /> : <MiniBars />}
            </div>
          </div>

          {/* Value row */}
          <div className="mt-3">
            <p className="text-[22px] font-bold font-mono tracking-tight text-[#f5f7fa] flex items-baseline gap-1.5">
              <span className={item.id.includes('pnl') ? 'text-[#10b981]' : 'text-[#f5f7fa]'}>
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
          <div className="mt-2.5 flex items-center gap-1 text-[11px] font-medium text-[#10b981]">
            <TrendingUp className="w-3 h-3 flex-shrink-0" />
            <span>{item.trendValue}</span>
            <span className="text-[#64748b] font-normal">{item.trendPeriod}</span>
          </div>
        </div>
      ))}
    </div>
  )
}
