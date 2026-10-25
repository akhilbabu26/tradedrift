import { create } from 'zustand'
import { type User } from '../api/auth'
import { wsService } from '../api/ws'
import { useNotificationStore } from './notificationStore'
import { useWalletStore } from './walletStore'
import { setSessionExpiredHandler, setTokenRefreshHandler } from '../api/client'
import { clearAllPrivateCaches } from '../utils/cacheRegistry'

interface AuthState {
  user: User | null
  accessToken: string | null
  refreshToken: string | null
  isAuthenticated: boolean

  setTokens: (accessToken: string, refreshToken: string) => void
  setUser: (user: User | Record<string, any> | null) => void
  logout: () => void
  hydrate: () => void
}

const safeStorage = {
  getItem: (key: string): string | null => {
    if (typeof localStorage === 'undefined') return null
    try {
      return localStorage.getItem(key)
    } catch {
      return null
    }
  },
  setItem: (key: string, value: string): void => {
    if (typeof localStorage === 'undefined') return
    try {
      localStorage.setItem(key, value)
    } catch {
      // ignore
    }
  },
  removeItem: (key: string): void => {
    if (typeof localStorage === 'undefined') return
    try {
      localStorage.removeItem(key)
    } catch {
      // ignore
    }
  },
}

/** Extracts user_id or sub from JWT token if available */
export function extractUserIdFromJwt(token: string | null): string | null {
  if (!token) return null
  try {
    const parts = token.split('.')
    if (parts.length !== 3) return null
    const base64 = parts[1].replace(/-/g, '+').replace(/_/g, '/')
    const jsonPayload = decodeURIComponent(
      atob(base64)
        .split('')
        .map((c) => '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2))
        .join('')
    )
    const decoded = JSON.parse(jsonPayload)
    const id = decoded.user_id || decoded.sub || decoded.userId
    return id ? String(id).trim() : null
  } catch {
    return null
  }
}

/** Normalizes user representation across userId, user_id, and id */
export function normalizeUser(raw: any, tokenFallback?: string | null): User | null {
  if (!raw && !tokenFallback) return null

  const resolvedId =
    raw?.userId ||
    raw?.user_id ||
    raw?.id ||
    (tokenFallback ? extractUserIdFromJwt(tokenFallback) : null) ||
    ''

  const email = raw?.email || ''
  const username = raw?.username || ''

  if (!resolvedId && !email && !username) {
    return null
  }

  return {
    userId: String(resolvedId).trim(),
    email: String(email).trim(),
    username: String(username).trim(),
  }
}

// Synchronously read stored session so first render has correct auth state
const getStoredSession = () => {
  try {
    const accessToken = safeStorage.getItem('access_token')
    const refreshToken = safeStorage.getItem('refresh_token')
    const userStr = safeStorage.getItem('user')
    let rawUser: any = null
    if (userStr) {
      try {
        rawUser = JSON.parse(userStr)
      } catch {
        rawUser = null
      }
    }
    const user = normalizeUser(rawUser, accessToken)
    const isAuthenticated = Boolean(accessToken && refreshToken)
    return { accessToken, refreshToken, user, isAuthenticated }
  } catch {
    return { accessToken: null, refreshToken: null, user: null, isAuthenticated: false }
  }
}

const initialSession = getStoredSession()

export const useAuthStore = create<AuthState>((set) => ({
  user: initialSession.user,
  accessToken: initialSession.accessToken,
  refreshToken: initialSession.refreshToken,
  isAuthenticated: initialSession.isAuthenticated,

  setTokens: (accessToken, refreshToken) => {
    safeStorage.setItem('access_token', accessToken)
    safeStorage.setItem('refresh_token', refreshToken)
    set((state) => {
      const currentUser = state.user
      const user = normalizeUser(currentUser, accessToken)
      if (user) {
        safeStorage.setItem('user', JSON.stringify(user))
      }
      return { accessToken, refreshToken, user: user || currentUser, isAuthenticated: true }
    })
    // Reconnect WebSocket immediately with the current authenticated token
    wsService.reconnect()
  },

  setUser: (rawUser) => {
    if (!rawUser) {
      safeStorage.removeItem('user')
      set({ user: null })
      return
    }
    const token = safeStorage.getItem('access_token')
    const user = normalizeUser(rawUser, token)
    if (user) {
      safeStorage.setItem('user', JSON.stringify(user))
    } else {
      safeStorage.removeItem('user')
    }
    set({ user })
  },

  logout: () => {
    safeStorage.removeItem('access_token')
    safeStorage.removeItem('refresh_token')
    safeStorage.removeItem('user')
    set({ user: null, accessToken: null, refreshToken: null, isAuthenticated: false })

    // Disconnect WebSocket and purge private user-scoped subscriptions
    wsService.clearPrivateSubscriptions()
    wsService.disconnect()

    // Clear notification and wallet private state
    useNotificationStore.setState({
      notifications: [],
      unreadCount: 0,
      activeTab: 'all',
      loading: false,
      error: null,
      isDemoData: false,
      hasMore: false,
      nextCursorTime: null,
      nextCursorId: null,
    })
    useWalletStore.getState().reset()

    // Purge in-memory private SWR caches
    clearAllPrivateCaches()
  },

  // Manual rehydrate helper
  hydrate: () => {
    const session = getStoredSession()
    if (session.isAuthenticated) {
      set(session)
    }
  },
}))

// Wire API client session lifecycle hooks
setSessionExpiredHandler(() => {
  useAuthStore.getState().logout()
})

setTokenRefreshHandler((accessToken, refreshToken) => {
  useAuthStore.getState().setTokens(accessToken, refreshToken)
})
