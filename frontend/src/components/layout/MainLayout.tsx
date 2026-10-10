import { useEffect } from 'react'
import { Outlet } from 'react-router-dom'
import MainNavbar from './MainNavbar'
import { useAuthStore } from '../../store/authStore'
import { useNotificationStore } from '../../store/notificationStore'
import { useWalletStore } from '../../store/walletStore'
import { useMarketStore } from '../../store/marketStore'

/**
 * Shared authenticated application shell.
 *
 * Renders MainNavbar at the top and the matched child route below via <Outlet />.
 * Used by App.tsx as the layout for all protected routes:
 *
 *   /dashboard, /trade, /markets, /portfolio, /orders, /analytics
 *
 * Phase 2: Hosts global WebSocket listeners for user notifications, live balances,
 * and market tickers so pages remain continuously synchronized across navigations.
 */
export default function MainLayout() {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)
  const userId = useAuthStore((s) => s.user?.userId)
  const loadNotifications = useNotificationStore((s) => s.loadNotifications)
  const subscribeNotifications = useNotificationStore((s) => s.subscribeToWs)
  const loadBalances = useWalletStore((s) => s.loadBalances)
  const subscribeBalances = useWalletStore((s) => s.subscribeToBalanceUpdates)
  const loadMarkets = useMarketStore((s) => s.loadMarketsAndOverview)

  // 1. Initial shared market data fetch
  useEffect(() => {
    loadMarkets()
  }, [loadMarkets])

  // 2. Global notification & wallet balance streams upon user authentication
  useEffect(() => {
    if (!isAuthenticated) return

    // Load initial authoritative snapshots
    loadNotifications(true)
    loadBalances()

    // Start persistent subscriptions
    const unsubNotifs = subscribeNotifications()
    const unsubBalances = subscribeBalances()

    return () => {
      unsubNotifs()
      unsubBalances()
    }
  }, [isAuthenticated, userId, loadNotifications, subscribeNotifications, loadBalances, subscribeBalances])

  return (
    <div className="h-screen w-full flex flex-col bg-[#0a0b0e] text-[#f5f7fa] font-sans antialiased overflow-hidden">
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

