import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useAuthStore } from './authStore'
import { wsService } from '../api/ws'
import { useNotificationStore } from './notificationStore'
import { useWalletStore } from './walletStore'

// Polyfill in-memory localStorage for Node test runner
const storageMap = new Map<string, string>()
const mockLocalStorage = {
  getItem: (key: string) => storageMap.get(key) ?? null,
  setItem: (key: string, value: string) => storageMap.set(key, String(value)),
  removeItem: (key: string) => storageMap.delete(key),
  clear: () => storageMap.clear(),
}
Object.defineProperty(globalThis, 'localStorage', {
  value: mockLocalStorage,
  writable: true,
  configurable: true,
})

describe('Auth WebSocket Session Lifecycle Synchronization (Phase A)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockLocalStorage.clear()
    useAuthStore.setState({
      user: null,
      accessToken: null,
      refreshToken: null,
      isAuthenticated: false,
    })
  })

  afterEach(() => {
    wsService.disconnect()
  })

  it('triggers wsService.reconnect when setTokens is called on login', () => {
    const reconnectSpy = vi.spyOn(wsService, 'reconnect')

    useAuthStore.getState().setTokens('mock-jwt-token-123', 'mock-refresh-token')

    expect(localStorage.getItem('access_token')).toBe('mock-jwt-token-123')
    expect(localStorage.getItem('refresh_token')).toBe('mock-refresh-token')
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(reconnectSpy).toHaveBeenCalledTimes(1)
  })

  it('disconnects wsService, purges private streams, and resets private stores on logout', () => {
    const disconnectSpy = vi.spyOn(wsService, 'disconnect')
    const clearPrivateSpy = vi.spyOn(wsService, 'clearPrivateSubscriptions')

    // Seed mock state in stores
    useNotificationStore.setState({
      notifications: [{
        id: 'notif-1',
        title: 'Secret Trade',
        description: 'Trade details',
        category: 'trading',
        iconType: 'buy',
        isRead: false,
        timestamp: new Date().toISOString(),
      }],
      unreadCount: 1,
    })

    useWalletStore.setState({
      balances: [{ asset: 'USDT', availableBalance: '5000.00', reservedBalance: '0.00' }],
    })

    // Perform logout
    useAuthStore.getState().logout()

    // 1. Storage & auth state cleared
    expect(localStorage.getItem('access_token')).toBeNull()
    expect(localStorage.getItem('refresh_token')).toBeNull()
    expect(useAuthStore.getState().isAuthenticated).toBe(false)
    expect(useAuthStore.getState().user).toBeNull()

    // 2. WebSocket disconnected and private streams purged
    expect(clearPrivateSpy).toHaveBeenCalledTimes(1)
    expect(disconnectSpy).toHaveBeenCalledTimes(1)

    // 3. Notification store private data wiped
    expect(useNotificationStore.getState().notifications).toEqual([])
    expect(useNotificationStore.getState().unreadCount).toBe(0)

    // 4. Wallet store private data wiped
    expect(useWalletStore.getState().balances).toEqual([])
  })

  it('clears only private user:* subscriptions and preserves public market:* subscriptions', () => {
    const dummyHandler = () => {}

    // Subscribe to both public and private channels
    wsService.subscribe('market:ticker:BTC-USDT', dummyHandler)
    wsService.subscribe('market:orderbook:ETH-USDT', dummyHandler)
    wsService.subscribe('user:notifications:00000000-0000-0000-0000-000000000001', dummyHandler)
    wsService.subscribe('user:portfolio:00000000-0000-0000-0000-000000000001', dummyHandler)

    // Clear private subscriptions
    wsService.clearPrivateSubscriptions()

    // Inspect subscribers map
    const subscribers = (wsService as any).subscribers as Map<string, Set<any>>
    expect(subscribers.has('market:ticker:BTC-USDT')).toBe(true)
    expect(subscribers.has('market:orderbook:ETH-USDT')).toBe(true)
    expect(subscribers.has('user:notifications:00000000-0000-0000-0000-000000000001')).toBe(false)
    expect(subscribers.has('user:portfolio:00000000-0000-0000-0000-000000000001')).toBe(false)
  })

  it('safely handles rapid login and logout transitions with epoch isolation', () => {
    const reconnectSpy = vi.spyOn(wsService, 'reconnect')
    const disconnectSpy = vi.spyOn(wsService, 'disconnect')

    // Simulate rapid login followed by immediate logout
    useAuthStore.getState().setTokens('token-1', 'refresh-1')
    useAuthStore.getState().logout()
    useAuthStore.getState().setTokens('token-2', 'refresh-2')

    expect(reconnectSpy).toHaveBeenCalledTimes(2)
    expect(disconnectSpy).toHaveBeenCalledTimes(1)
    expect(useAuthStore.getState().accessToken).toBe('token-2')
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
  })
})
