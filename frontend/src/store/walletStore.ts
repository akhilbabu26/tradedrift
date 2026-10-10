/**
 * src/store/walletStore.ts
 *
 * Centralized Zustand store for user wallet balances and real-time invalidation.
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * Phase 2 Architectural Guarantees:
 * 1. Single Authoritative Balance Store:
 *    Available and Reserved balances are shared between /trade, /wallet, and /dashboard.
 * 2. Real-Time WS Invalidation:
 *    Subscribes to user-scoped notification and portfolio streams. Fills, cancellations,
 *    and portfolio updates immediately invalidate and refresh balances from the backend.
 * 3. Reference-Counted Subscriptions:
 *    MainLayout and active page components can safely subscribe; the underlying
 *    WebSocket channels remain active as long as at least one subscriber is mounted.
 * 4. Automatic Reconnection Re-fetch:
 *    Refetches authoritative balances on WebSocket reconnect to eliminate silent drift.
 */

import { create } from 'zustand'
import { walletApi, type Balance } from '../api/wallet'
import { wsService, WsChannels } from '../api/ws'
import { useAuthStore } from './authStore'
import { extractApiError } from '../utils/apiError'

export interface WalletState {
  balances: Balance[]
  balancesByAsset: Record<string, Balance>
  loading: boolean
  error: string | null
  lastFetchedAt: number

  loadBalances: () => Promise<void>
  getBalance: (asset: string) => Balance
  subscribeToBalanceUpdates: () => () => void
  invalidateBalances: () => Promise<void>
  reset: () => void
}

let wsBalanceSubCount = 0
let notifUnsub: (() => void) | null = null
let portfolioUnsub: (() => void) | null = null

export const useWalletStore = create<WalletState>((set, get) => ({
  balances: [],
  balancesByAsset: {},
  loading: false,
  error: null,
  lastFetchedAt: 0,

  loadBalances: async () => {
    // Avoid redundant concurrent fetches if loaded in the last 200ms
    const now = Date.now()
    if (get().loading && now - get().lastFetchedAt < 200) {
      return
    }

    set({ loading: true, error: null })
    try {
      const data = await walletApi.getAllBalances()
      const list = data || []
      const byAsset: Record<string, Balance> = {}

      list.forEach((b) => {
        byAsset[b.asset.toUpperCase()] = b
      })

      set({
        balances: list,
        balancesByAsset: byAsset,
        loading: false,
        lastFetchedAt: Date.now(),
        error: null,
      })
    } catch (err) {
      set({
        loading: false,
        error: extractApiError(err),
      })
    }
  },

  getBalance: (asset: string): Balance => {
    const symbol = asset.toUpperCase()
    return (
      get().balancesByAsset[symbol] || {
        asset,
        availableBalance: '0.00',
        reservedBalance: '0.00',
      }
    )
  },

  invalidateBalances: async () => {
    await get().loadBalances()
  },

  subscribeToBalanceUpdates: () => {
    wsBalanceSubCount++

    if (wsBalanceSubCount === 1) {
      const user = useAuthStore.getState().user
      const notifChannel = WsChannels.userNotifications(user?.userId)
      const portfolioChannel = WsChannels.userPortfolio(user?.userId)

      const handleUpdate = () => {
        get().loadBalances()
      }

      notifUnsub = wsService.subscribe(notifChannel, handleUpdate)
      portfolioUnsub = wsService.subscribe(portfolioChannel, handleUpdate)
    }

    return () => {
      wsBalanceSubCount = Math.max(0, wsBalanceSubCount - 1)
      if (wsBalanceSubCount === 0) {
        if (notifUnsub) {
          notifUnsub()
          notifUnsub = null
        }
        if (portfolioUnsub) {
          portfolioUnsub()
          portfolioUnsub = null
        }
      }
    }
  },

  reset: () => {
    set({
      balances: [],
      balancesByAsset: {},
      loading: false,
      error: null,
      lastFetchedAt: 0,
    })
  },
}))

// Auto-resync balances on WebSocket reconnection if logged in
wsService.onReconnect(() => {
  if (useAuthStore.getState().isAuthenticated) {
    useWalletStore.getState().loadBalances()
  }
})
