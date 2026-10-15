import { useState, useEffect, useCallback, Suspense } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import AppSidebar from './AppSidebar'
import AppHeader from './AppHeader'
import PageLoadingFallback from '../common/PageLoadingFallback'
import { useAuthStore } from '../../store/authStore'
import { useNotificationStore } from '../../store/notificationStore'
import { useWalletStore } from '../../store/walletStore'
import { useMarketStore } from '../../store/marketStore'

/**
 * Shared authenticated application layout.
 *
 * Architecture:
 * - Persistent left 3D sidebar (AppSidebar) with glossy raised active navigation.
 * - Slim top header (AppHeader) with global search, LIVE status, notifications, and profile.
 * - Body container rendering matched child route via <Outlet />.
 * - Global WebSocket listeners for notifications, balances, and market overview.
 */
export default function MainLayout() {
  const { pathname } = useLocation()
  const [mobileOpen, setMobileOpen] = useState(false)

  const handleOpenMobile = useCallback(() => setMobileOpen(true), [])
  const handleCloseMobile = useCallback(() => setMobileOpen(false), [])

  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)
  const userId = useAuthStore((s) => s.user?.userId)
  const loadNotifications = useNotificationStore((s) => s.loadNotifications)
  const subscribeNotifications = useNotificationStore((s) => s.subscribeToWs)
  const loadBalances = useWalletStore((s) => s.loadBalances)
  const subscribeBalances = useWalletStore((s) => s.subscribeToBalanceUpdates)
  const loadMarkets = useMarketStore((s) => s.loadMarketsAndOverview)

  // Close mobile drawer on route navigation
  useEffect(() => {
    setMobileOpen(false)
  }, [pathname])

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

    // Focus reconciliation for unread badge and wallet balances
    const onFocus = () => {
      useNotificationStore.getState().fetchUnreadCount()
      useWalletStore.getState().loadBalances()
    }
    window.addEventListener('focus', onFocus)

    return () => {
      unsubNotifs()
      unsubBalances()
      window.removeEventListener('focus', onFocus)
    }
  }, [isAuthenticated, userId, loadNotifications, subscribeNotifications, loadBalances, subscribeBalances])

  return (
    <div className="h-screen w-full flex bg-[#08090B] text-[#F2F3F5] font-sans antialiased overflow-hidden select-none">
      {/* ── Globally Shared 3D Navigation Sidebar ──────────────────────── */}
      <AppSidebar
        mobileOpen={mobileOpen}
        onCloseMobile={handleCloseMobile}
      />

      {/* ── Main Viewport Content (Header + Page Outlet) ────────────────── */}
      <div className="flex-1 flex flex-col min-w-0 h-screen overflow-hidden">
        {/* Slim Top Utility Header */}
        <AppHeader onOpenMobile={handleOpenMobile} />

        {/* Route Page Container (fills exactly, child page handles its own overflow) */}
        <main className="flex-1 flex flex-col overflow-hidden min-h-0 bg-[#08090B] select-auto">
          <Suspense fallback={<PageLoadingFallback />}>
            <Outlet />
          </Suspense>
        </main>
      </div>
    </div>
  )
}

/** Named alias for architecture alignment */
export { MainLayout as AppLayout }
