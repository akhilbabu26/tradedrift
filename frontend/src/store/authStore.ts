import { create } from 'zustand'
import { type User } from '../api/auth'

interface AuthState {
  user: User | null
  accessToken: string | null
  refreshToken: string | null
  isAuthenticated: boolean

  setTokens: (accessToken: string, refreshToken: string) => void
  setUser: (user: User) => void
  logout: () => void
  hydrate: () => void
}

// Synchronously read stored session so first render has correct auth state
const getStoredSession = () => {
  if (typeof window === 'undefined') {
    return { accessToken: null, refreshToken: null, user: null, isAuthenticated: false }
  }
  try {
    const accessToken = localStorage.getItem('access_token')
    const refreshToken = localStorage.getItem('refresh_token')
    const userStr = localStorage.getItem('user')
    let user: User | null = null
    if (userStr) {
      user = JSON.parse(userStr)
    }
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
    localStorage.setItem('access_token', accessToken)
    localStorage.setItem('refresh_token', refreshToken)
    set({ accessToken, refreshToken, isAuthenticated: true })
  },

  setUser: (user) => {
    localStorage.setItem('user', JSON.stringify(user))
    set({ user })
  },

  logout: () => {
    localStorage.removeItem('access_token')
    localStorage.removeItem('refresh_token')
    localStorage.removeItem('user')
    set({ user: null, accessToken: null, refreshToken: null, isAuthenticated: false })
  },

  // Manual rehydrate helper
  hydrate: () => {
    const session = getStoredSession()
    if (session.isAuthenticated) {
      set(session)
    }
  },
}))
