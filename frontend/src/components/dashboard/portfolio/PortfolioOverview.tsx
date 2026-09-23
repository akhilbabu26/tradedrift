import { useState } from 'react'
import { Eye, ArrowRight } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { DashboardData, PeriodTab } from '../../../types/dashboard'
import PortfolioSummary  from './PortfolioSummary'
import SimulatorBalance  from './SimulatorBalance'
import MyCoins           from './MyCoins'
import PortfolioChart    from './PortfolioChart'
import PortfolioMetrics  from './PortfolioMetrics'

type PortfolioOverviewProps = Pick<
  DashboardData,
  | 'portfolioValue'
  | 'portfolioChangeUsdt'
  | 'portfolioChangePercent'
  | 'simulatorBalance'
  | 'availablePercent'
  | 'inOrdersPercent'
  | 'availableUsdt'
  | 'inOrdersUsdt'
  | 'holdings'
  | 'chartData'
  | 'allTimeProfit'
  | 'winRate'
  | 'totalTrades'
>

const PERIOD_TABS: PeriodTab[] = ['7D', '30D', '90D', '1Y']

/**
 * Full-width Portfolio Overview card.
 * ONE card — NOT split into three separate cards.
 *
 * Layout:
 *   Header row: title | period tabs | "View Portfolio →"
 *   Summary row: [PortfolioSummary | divider | SimulatorBalance | divider | MyCoins]
 *   Chart: PortfolioChart (full width, ResizeObserver-responsive)
 *   Metrics: PortfolioMetrics (3 columns)
 */
export default function PortfolioOverview({
  portfolioValue,
  portfolioChangeUsdt,
  portfolioChangePercent,
  simulatorBalance,
  availablePercent,
  inOrdersPercent,
  availableUsdt,
  inOrdersUsdt,
  holdings,
  chartData,
  allTimeProfit,
  winRate,
  totalTrades,
}: PortfolioOverviewProps) {
  const [activePeriod, setActivePeriod] = useState<PeriodTab>('7D')

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-lg p-5 mb-4">
      {/* ── Header row ───────────────────────────────────────────────────── */}
      <div className="flex items-center justify-between mb-5 flex-wrap gap-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold text-[#f5f7fa]">Portfolio Overview</h2>
          <Eye size={14} className="text-slate-500" aria-label="Toggle visibility" />
        </div>
        <div className="flex items-center gap-2">
          {/* Period tabs */}
          <div className="flex items-center gap-0.5 bg-[#0a0b0e] rounded-md border border-[#1e2530] p-0.5" role="tablist" aria-label="Portfolio period">
            {PERIOD_TABS.map((tab) => (
              <button
                key={tab}
                role="tab"
                aria-selected={activePeriod === tab}
                onClick={() => setActivePeriod(tab)}
                className={`px-3 py-1 rounded text-xs font-medium transition-colors ${
                  activePeriod === tab
                    ? 'bg-[#10b981] text-[#0a0b0e]'
                    : 'text-slate-400 hover:text-[#f5f7fa]'
                }`}
              >
                {tab}
              </button>
            ))}
          </div>
          <Link
            to="/portfolio"
            className="flex items-center gap-0.5 text-xs text-slate-400 hover:text-[#10b981] transition-colors"
          >
            View Portfolio <ArrowRight size={12} />
          </Link>
        </div>
      </div>

      {/* ── Three-column summary row ──────────────────────────────────────── */}
      <div className="flex flex-col lg:flex-row gap-5 mb-5">
        {/* Column 1: Total value */}
        <div className="lg:w-56 flex-shrink-0">
          <PortfolioSummary
            portfolioValue={portfolioValue}
            changeUsdt={portfolioChangeUsdt}
            changePercent={portfolioChangePercent}
          />
        </div>

        <div className="hidden lg:block w-px bg-[#1e2530]" aria-hidden="true" />

        {/* Column 2: Simulator Balance */}
        <div className="lg:w-56 flex-shrink-0">
          <SimulatorBalance
            balance={simulatorBalance}
            availablePercent={availablePercent}
            inOrdersPercent={inOrdersPercent}
            availableUsdt={availableUsdt}
            inOrdersUsdt={inOrdersUsdt}
          />
        </div>

        <div className="hidden lg:block w-px bg-[#1e2530]" aria-hidden="true" />

        {/* Column 3: My Coins */}
        <div className="flex-1 min-w-0">
          <MyCoins holdings={holdings} />
        </div>
      </div>

      {/* ── Portfolio Chart ───────────────────────────────────────────────── */}
      <PortfolioChart data={chartData} latestValue={portfolioValue} />

      {/* ── Portfolio Metrics ─────────────────────────────────────────────── */}
      <PortfolioMetrics
        allTimeProfit={allTimeProfit}
        winRate={winRate}
        totalTrades={totalTrades}
      />
    </div>
  )
}
