import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { clearSessionAndRedirect, resetSessionCleanupLock } from './client'
import client from './client'
import { useAuthStore } from '../store/authStore'
import { wsService } from './ws'
import { useNotificationStore } from '../store/notificationStore'
import { useWalletStore } from '../store/walletStore'

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

describe('Client Session Cleanup & 401 Lifecycle Regression Tests', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetSessionCleanupLock()
    mockLocalStorage.clear()
    storageMap.set('access_token', 'expired-token')
    storageMap.set('refresh_token', 'bad-refresh-token')
    storageMap.set('user', JSON.stringify({ userId: 'u123', email: 'u123@test.com' }))

    useAuthStore.setState({
      user: { userId: 'u123', email: 'u123@test.com', username: 'u123' },
      accessToken: 'expired-token',
      refreshToken: 'bad-refresh-token',
      isAuthenticated: true,
    })
  })

  afterEach(() => {
    wsService.disconnect()
  })

  it('clears in-memory auth store, storage, and private WebSocket streams on failed session cleanup', async () => {
    const disconnectSpy = vi.spyOn(wsService, 'disconnect')
    const clearPrivateSpy = vi.spyOn(wsService, 'clearPrivateSubscriptions')
    const walletResetSpy = vi.spyOn(useWalletStore.getState(), 'reset')

    useNotificationStore.setState({
      notifications: [{
        id: 'n1',
        title: 'Confidential Alert',
        description: 'private',
        category: 'trading',
        iconType: 'buy',
        isRead: false,
        timestamp: 'Just now',
      }],
      unreadCount: 1,
    })

    await clearSessionAndRedirect()

    // 1. Storage purged
    expect(localStorage.getItem('access_token')).toBeNull()
    expect(localStorage.getItem('refresh_token')).toBeNull()
    expect(localStorage.getItem('user')).toBeNull()

    // 2. Auth store reset
    const authState = useAuthStore.getState()
    expect(authState.isAuthenticated).toBe(false)
    expect(authState.accessToken).toBeNull()
    expect(authState.refreshToken).toBeNull()
    expect(authState.user).toBeNull()

    // 3. Private WebSocket subscriptions purged & connection closed
    expect(clearPrivateSpy).toHaveBeenCalled()
    expect(disconnectSpy).toHaveBeenCalled()

    // 4. Notification & Wallet stores sanitized
    expect(useNotificationStore.getState().notifications).toEqual([])
    expect(useNotificationStore.getState().unreadCount).toBe(0)
    expect(walletResetSpy).toHaveBeenCalled()
  })

  it('guards against concurrent duplicate logout runs', async () => {
    let logoutCalls = 0
    const originalLogout = useAuthStore.getState().logout
    useAuthStore.setState({
      logout: () => {
        logoutCalls++
        originalLogout()
      },
    })

    // Call concurrently
    await Promise.all([
      clearSessionAndRedirect(),
      clearSessionAndRedirect(),
      clearSessionAndRedirect(),
    ])

    // Should only execute logout once due to concurrency lock
    expect(logoutCalls).toBe(1)
  })

  it('rejects public auth endpoint 401 without wiping session or attempting refresh', async () => {
    let logoutCalled = false
    useAuthStore.setState({
      logout: () => {
        logoutCalled = true
      },
    })

    // Simulated 401 error on login
    const loginError = {
      config: { url: '/api/v1/auth/login' },
      response: { status: 401, data: { message: 'Invalid credentials' } },
    }

    // Pass through client response interceptor rejection handler
    const responseInterceptor = (client.interceptors.response as any).handlers[0]
    await expect(responseInterceptor.rejected(loginError)).rejects.toEqual(loginError)

    // User session remains untouched
    expect(logoutCalled).toBe(false)
    expect(localStorage.getItem('access_token')).toBe('expired-token')
  })

  it('immediately triggers session cleanup and rejects when refresh endpoint itself returns 401', async () => {
    let logoutCalled = false
    useAuthStore.setState({
      logout: () => {
        logoutCalled = true
      },
    })

    const refreshError = {
      config: { url: '/api/v1/auth/refresh' },
      response: { status: 401, data: { message: 'Refresh token expired' } },
    }

    const responseInterceptor = (client.interceptors.response as any).handlers[0]
    await expect(responseInterceptor.rejected(refreshError)).rejects.toEqual(refreshError)

    // Session cleanup triggered
    expect(logoutCalled).toBe(true)
  })

  it('cleans up session when protected request gets 401 but no refresh token exists', async () => {
    storageMap.delete('refresh_token')
    let logoutCalled = false
    useAuthStore.setState({
      logout: () => {
        logoutCalled = true
      },
    })

    const apiError = {
      config: { url: '/api/v1/wallets' },
      response: { status: 401, data: { message: 'Unauthorized' } },
    }

    const responseInterceptor = (client.interceptors.response as any).handlers[0]
    await expect(responseInterceptor.rejected(apiError)).rejects.toEqual(apiError)

    expect(logoutCalled).toBe(true)
  })

  it('prevents infinite loops by wiping session if a retried request (_retry: true) still returns 401', async () => {
    let logoutCalled = false
    useAuthStore.setState({
      logout: () => {
        logoutCalled = true
      },
    })

    const retriedError = {
      config: { url: '/api/v1/wallets', _retry: true },
      response: { status: 401, data: { message: 'Token still invalid' } },
    }

    const responseInterceptor = (client.interceptors.response as any).handlers[0]
    await expect(responseInterceptor.rejected(retriedError)).rejects.toEqual(retriedError)

    expect(logoutCalled).toBe(true)
  })
})

