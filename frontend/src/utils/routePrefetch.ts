/**
 * src/utils/routePrefetch.ts
 *
 * Lightweight route prefetching utility.
 * Initiates route chunk prefetching on link hover/focus so cold navigation
 * transitions are instantaneous.
 */

const routeLoaders: Record<string, () => Promise<unknown>> = {
  '/dashboard': () => import('../pages/dashboard/DashboardPage'),
  '/trade': () => import('../pages/trade/TradePage'),
  '/markets': () => import('../pages/markets/MarketsPage'),
  '/portfolio': () => import('../pages/portfolio/PortfolioPage'),
  '/wallet': () => import('../pages/wallet/WalletPage'),
  '/orders': () => import('../pages/orders/OrdersPage'),
  '/analytics': () => import('../pages/analytics/AnalyticsPage'),
  '/settings': () => import('../pages/settings/SettingsPage'),
  '/profile': () => import('../pages/profile/ProfilePage'),
  '/notifications': () => import('../pages/notifications/NotificationsPage'),
  '/login': () => import('../pages/auth/LoginPage'),
  '/register': () => import('../pages/auth/RegisterPage'),
}

const prefetchedRoutes = new Set<string>()

export function prefetchRoute(path: string | undefined): void {
  if (!path) return
  const cleanPath = path.split('?')[0]
  if (prefetchedRoutes.has(cleanPath)) return
  const loader = routeLoaders[cleanPath]
  if (loader) {
    prefetchedRoutes.add(cleanPath)
    loader().catch(() => {
      prefetchedRoutes.delete(cleanPath)
    })
  }
}
