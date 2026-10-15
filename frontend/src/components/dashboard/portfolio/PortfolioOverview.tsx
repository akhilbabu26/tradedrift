import { useState } from 'react'
import { Eye } from 'lucide-react'
import type { DashboardData, PeriodTab } from '../../../types/dashboard'
import { RaisedSurface, SurfaceDivider } from '../../common/Surface'
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
> & {
  activePeriod?: PeriodTab
  onPeriodChange?: (period: PeriodTab) => void
  isChartLoading?: boolean
  chartError?: string | null
  onRetryChart?: () => void
}

const PERIOD_TABS: PeriodTab[] = ['24H', '7D', '30D', '90D', '1Y']

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
  activePeriod: controlledPeriod,
  onPeriodChange,
  isChartLoading,
  chartError,
  onRetryChart,
}: PortfolioOverviewProps) {
  const [localPeriod, setLocalPeriod] = useState<PeriodTab>('7D')
  const activePeriod = controlledPeriod ?? localPeriod

  const handlePeriodClick = (tab: PeriodTab) => {
    setLocalPeriod(tab)
    onPeriodChange?.(tab)
  }


  return (
    <RaisedSurface variant="raised" className="mb-4">
      {/* ── Header row ───────────────────────────────────────────────────── */}
      <div className="flex items-center justify-between mb-5 flex-wrap gap-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold text-[#F2F3F5]">Portfolio Overview</h2>
          <Eye size={14} className="text-[#777E87]" aria-label="Toggle visibility" />
        </div>
        <div className="flex items-center gap-2">
          {/* Period tabs */}
          <div className="flex items-center gap-1 bg-[#090A0C] rounded-xl border border-[#292C30] p-1 shadow-[inset_0_2px_4px_rgba(0,0,0,0.5)]" role="tablist" aria-label="Portfolio period">
            {PERIOD_TABS.map((tab) => {
              const isActive = activePeriod === tab
              return (
                <button
                  key={tab}
                  role="tab"
                  aria-selected={isActive}
                  onClick={() => handlePeriodClick(tab)}
                  className={`px-3 py-1 rounded-lg text-xs font-semibold transition-all duration-150 ${
                    isActive
                      ? 'bg-gradient-to-b from-[#FFFFFF] to-[#D9DCE0] text-[#090B0D] shadow-[0_2px_8px_rgba(0,0,0,0.4)]'
                      : 'text-[#777E87] hover:text-[#F2F3F5]'
                  }`}
                >
                  {tab}
                </button>
              )
            })}
          </div>
        </div>
      </div>

      {/* ── Three-column summary row ──────────────────────────────────────── */}
      <div className="flex flex-col lg:flex-row gap-5 mb-5 items-stretch">
        {/* Column 1: Total value */}
        <div className="lg:w-56 flex-shrink-0">
          <PortfolioSummary
            portfolioValue={portfolioValue}
            changeUsdt={portfolioChangeUsdt}
            changePercent={portfolioChangePercent}
          />
        </div>

        <SurfaceDivider orientation="vertical" className="hidden lg:block my-1" />

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

        <SurfaceDivider orientation="vertical" className="hidden lg:block my-1" />

        {/* Column 3: My Coins */}
        <div className="flex-1 min-w-0">
          <MyCoins holdings={holdings} />
        </div>
      </div>

      {/* ── Portfolio Chart ───────────────────────────────────────────────── */}
      <PortfolioChart
        data={chartData}
        latestValue={portfolioValue}
        isLoading={isChartLoading}
        error={chartError}
        onRetry={onRetryChart}
      />

      {/* ── Portfolio Metrics ─────────────────────────────────────────────── */}
      <PortfolioMetrics
        allTimeProfit={allTimeProfit}
        winRate={winRate}
        totalTrades={totalTrades}
      />
    </RaisedSurface>
  )
}
