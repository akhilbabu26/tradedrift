import DashboardHeader   from '../../components/dashboard/DashboardHeader'
import PortfolioOverview from '../../components/dashboard/portfolio/PortfolioOverview'
import MarketPulse       from '../../components/dashboard/market/MarketPulse'
import LiveOperations    from '../../components/dashboard/operations/LiveOperations'
import SystemStatus      from '../../components/dashboard/system/SystemStatus'
import RecentFills       from '../../components/dashboard/fills/RecentFills'
import { DASHBOARD_MOCK } from '../../data/dashboardMock'

/**
 * Dashboard page — the authenticated home screen of TradeDrift.
 *
 * Layout is provided by MainLayout (App.tsx) via <Outlet />.
 * This component renders only Dashboard-specific content — no layout wrapper.
 *
 * Data source: dashboardMock (replace with API/WebSocket hooks when backend ready)
 */
export default function DashboardPage() {
  const data = DASHBOARD_MOCK

  return (
    <div className="flex-1 overflow-y-auto">
    <div className="max-w-[1600px] mx-auto px-4 lg:px-6 py-5">

      {/* Welcome + Status Header */}
      <DashboardHeader />

      {/* Full-width Portfolio Overview */}
      <PortfolioOverview
        portfolioValue={data.portfolioValue}
        portfolioChangeUsdt={data.portfolioChangeUsdt}
        portfolioChangePercent={data.portfolioChangePercent}
        simulatorBalance={data.simulatorBalance}
        availablePercent={data.availablePercent}
        inOrdersPercent={data.inOrdersPercent}
        availableUsdt={data.availableUsdt}
        inOrdersUsdt={data.inOrdersUsdt}
        holdings={data.holdings}
        chartData={data.chartData}
        allTimeProfit={data.allTimeProfit}
        winRate={data.winRate}
        totalTrades={data.totalTrades}
      />

      {/* Lower two-column grid */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-4">
        {/* Left: Market Pulse */}
        <MarketPulse rows={data.marketRows} />

        {/* Right: Live Operations */}
        <LiveOperations orders={data.activeOrders} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-6">
        {/* Left: System Status */}
        <SystemStatus services={data.services} />

        {/* Right: Recent Fills */}
        <RecentFills fills={data.recentFills} />
      </div>

      {/* Dashboard Footer */}
      <footer className="border-t border-[#1e2530] pt-4 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3">
        <div>
          <p className="text-sm font-bold text-[#f5f7fa] flex items-center gap-2">
            <span className="w-5 h-5 rounded bg-[#10b981] flex items-center justify-center text-[#0a0b0e] font-black text-[10px]">TD</span>
            TradeDrift
          </p>
          <p className="text-xs text-slate-500 mt-0.5">
            A production-grade cryptocurrency exchange simulator.
          </p>
        </div>
        <div className="flex items-center gap-4 text-xs text-slate-500">
          <a href="#" className="hover:text-[#f5f7fa] transition-colors">Docs</a>
          <a href="#" className="hover:text-[#f5f7fa] transition-colors">Support</a>
          <span className="px-2 py-0.5 rounded border border-[#1e2530] font-mono">v1.0.0</span>
          <span className="text-slate-600">Build Better Traders</span>
        </div>
      </footer>
    </div>
    </div>
  )
}
