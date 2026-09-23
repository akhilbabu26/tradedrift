import { useState } from 'react'
import AnalyticsHeader from '../../components/analytics/AnalyticsHeader'
import AnalyticsStats from '../../components/analytics/AnalyticsStats'
import PortfolioPerformanceByAsset from '../../components/analytics/PortfolioPerformanceByAsset'
import TradingActivity from '../../components/analytics/TradingActivity'
import RecentExecutionFills from '../../components/analytics/RecentExecutionFills'
import AnalyticsFooter from '../../components/analytics/AnalyticsFooter'
import type { DateRangeOption } from '../../types/analytics'
import {
  ANALYTICS_KPIS,
  ASSET_PERFORMANCE_LIST,
  TRADING_ACTIVITY_BY_SIDE,
  TRADING_ACTIVITY_BY_ASSET,
  TRADING_ACTIVITY_BY_MONTH,
  TRADING_ACTIVITY_SUMMARY,
  RECENT_EXECUTION_FILLS,
} from '../../data/analyticsMock'

/**
 * AnalyticsPage — TradeDrift Analytics & Journal
 *
 * Matches the reference screenshot layout at 1440px / 1536px / 1366px / 1920px:
 * - AnalyticsHeader (Title, subtitle, quote, date selector)
 * - AnalyticsStats (4 KPI cards in one horizontal desktop row)
 * - Side-by-side panels starting at same vertical position:
 *     Left: PortfolioPerformanceByAsset (~60%)
 *     Right: TradingActivity (~40%)
 * - Full-width RecentExecutionFills
 * - Terminal Footer
 */
export default function AnalyticsPage() {
  const [dateRange, setDateRange] = useState<DateRangeOption>('Last 30 Days')

  return (
    <div className="flex-1 overflow-y-auto min-h-0 bg-[#0a0b0e] text-[#f5f7fa]">
      <div className="max-w-[1600px] mx-auto px-4 lg:px-6 py-5 flex flex-col gap-4">
        {/* Page Header */}
        <AnalyticsHeader
          selectedRange={dateRange}
          onRangeChange={setDateRange}
        />

        {/* 4 KPI Cards in a single desktop row */}
        <AnalyticsStats kpis={ANALYTICS_KPIS} />

        {/* Side-by-side: Portfolio Performance (~60%) + Trading Activity (~40%) */}
        <div className="grid grid-cols-1 lg:grid-cols-[1.55fr_1fr] gap-4 items-stretch">
          <PortfolioPerformanceByAsset assets={ASSET_PERFORMANCE_LIST} />
          <TradingActivity
            bySide={TRADING_ACTIVITY_BY_SIDE}
            byAsset={TRADING_ACTIVITY_BY_ASSET}
            byMonth={TRADING_ACTIVITY_BY_MONTH}
            currentPortfolioValue={TRADING_ACTIVITY_SUMMARY.currentPortfolioValue}
            activeAssetsCount={TRADING_ACTIVITY_SUMMARY.activeAssetsCount}
          />
        </div>

        {/* Recent Execution Fills (Full-width) */}
        <RecentExecutionFills fills={RECENT_EXECUTION_FILLS} />

        {/* Footer */}
        <AnalyticsFooter />
      </div>
    </div>
  )
}
