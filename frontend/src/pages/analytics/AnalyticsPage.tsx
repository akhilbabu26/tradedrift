import { useState } from 'react'
import AnalyticsHeader from '../../components/analytics/AnalyticsHeader'
import AnalyticsStats from '../../components/analytics/AnalyticsStats'
import PortfolioPerformanceByAsset from '../../components/analytics/PortfolioPerformanceByAsset'
import TradingActivity from '../../components/analytics/TradingActivity'
import RecentExecutionFills from '../../components/analytics/RecentExecutionFills'
import AnalyticsFooter from '../../components/analytics/AnalyticsFooter'
import { useAnalyticsData } from '../../hooks/useAnalyticsData'
import type { DateRangeOption } from '../../types/analytics'

/**
 * AnalyticsPage — TradeDrift Analytics & Journal
 *
 * Connected directly to live backend services via useAnalyticsData.
 * Derives KPIs, asset performance, and execution journal from live trading data.
 */
export default function AnalyticsPage() {
  const [dateRange, setDateRange] = useState<DateRangeOption>('Last 30 Days')
  const {
    kpis,
    assetPerformanceList,
    bySide,
    byAsset,
    byMonth,
    recentFills,
    currentPortfolioValue,
    activeAssetsCount,
  } = useAnalyticsData(dateRange)

  return (
    <div className="flex-1 overflow-y-auto min-h-0 bg-[#0a0b0e] text-[#f5f7fa] flex flex-col justify-between">
      <div className="max-w-[1600px] w-full mx-auto px-4 lg:px-6 py-5 flex flex-col gap-4">
        {/* Page Header */}
        <AnalyticsHeader
          selectedRange={dateRange}
          onRangeChange={setDateRange}
        />

        {/* 4 KPI Cards in a single desktop row */}
        <AnalyticsStats kpis={kpis} />

        {/* Side-by-side: Portfolio Performance (~60%) + Trading Activity (~40%) */}
        <div className="grid grid-cols-1 lg:grid-cols-[1.55fr_1fr] gap-4 items-stretch">
          <PortfolioPerformanceByAsset assets={assetPerformanceList} />
          <TradingActivity
            bySide={bySide}
            byAsset={byAsset}
            byMonth={byMonth}
            currentPortfolioValue={currentPortfolioValue}
            activeAssetsCount={activeAssetsCount}
          />
        </div>

        {/* Recent Execution Fills (Full-width) */}
        <RecentExecutionFills fills={recentFills} />
      </div>

      <AnalyticsFooter />
    </div>
  )
}
