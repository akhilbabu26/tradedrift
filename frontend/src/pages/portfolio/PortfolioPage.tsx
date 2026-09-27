import { usePortfolioData } from '../../hooks/usePortfolioData'
import PortfolioHeader from '../../components/portfolio/PortfolioHeader'
import PortfolioMetrics from '../../components/portfolio/PortfolioMetrics'
import PortfolioPerformance from '../../components/portfolio/PortfolioPerformance'
import AssetAllocation from '../../components/portfolio/AssetAllocation'
import HoldingsSection from '../../components/portfolio/HoldingsSection'
import PortfolioInsights from '../../components/portfolio/PortfolioInsights'
import PortfolioFooter from '../../components/portfolio/PortfolioFooter'

export default function PortfolioPage() {
  const {
    loading,
    isDemoData,
    metrics,
    holdings,
    allocation,
    insights,
    performanceData,
    timeframe,
    setTimeframe,
    showBtcBenchmark,
    setShowBtcBenchmark,
  } = usePortfolioData()

  return (
    <div className="flex-1 overflow-y-auto min-h-0 bg-[#0a0b0e] text-[#f5f7fa] flex flex-col justify-between">
      <div className="max-w-[1600px] w-full mx-auto px-4 lg:px-6 py-5 flex flex-col gap-4">

        {isDemoData && (
          <div className="bg-[#111318] border border-amber-500/30 rounded-lg px-4 py-2 flex items-center gap-2 text-xs text-amber-400">
            <span className="w-1.5 h-1.5 rounded-full bg-amber-400 animate-pulse flex-shrink-0" />
            Viewing demo portfolio data. Connect to the TradeDrift API backend for your live portfolio.
          </div>
        )}

        <PortfolioHeader />

        <PortfolioMetrics metrics={metrics} loading={loading} />

        <div className="grid grid-cols-1 lg:grid-cols-[1.6fr_1fr] gap-4 items-stretch">
          <PortfolioPerformance
            data={performanceData}
            timeframe={timeframe}
            onTimeframeChange={setTimeframe}
            showBtcBenchmark={showBtcBenchmark}
            onToggleBtcBenchmark={setShowBtcBenchmark}
          />
          <AssetAllocation
            allocation={allocation}
            totalValue={metrics.totalValue}
          />
        </div>

        <HoldingsSection holdings={holdings} loading={loading} />

        <PortfolioInsights insights={insights} />

      </div>

      <PortfolioFooter />
    </div>
  )
}