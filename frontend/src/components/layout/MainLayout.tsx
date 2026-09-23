import { Outlet } from 'react-router-dom'
import MainNavbar from './MainNavbar'

/**
 * Shared authenticated application shell.
 *
 * Renders MainNavbar at the top and the matched child route below via <Outlet />.
 * Used by App.tsx as the layout for all protected routes:
 *
 *   /dashboard, /trade, /markets, /portfolio, /orders, /analytics
 *
 * This is NOT dashboard-specific. The Dashboard is one consumer of this layout.
 */
export default function MainLayout() {
  return (
    <div className="min-h-screen w-full flex flex-col bg-[#0a0b0e] text-[#f5f7fa] font-sans antialiased">
      {/* Shared application navbar */}
      <MainNavbar />

      {/* Page body — child route renders here via <Outlet />.
          Each page is responsible for its own overflow/scroll behaviour:
          - TradePage: overflow-hidden, fills viewport
          - DashboardPage / MarketsPage: overflow-y-auto on their own content */}
      <main className="flex-1 flex flex-col overflow-hidden min-h-0">
        <Outlet />
      </main>
    </div>
  )
}
