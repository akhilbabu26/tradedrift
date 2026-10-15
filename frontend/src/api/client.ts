/**
 * src/api/client.ts
 *
 * Centralized Axios instance with:
 *   - JWT Bearer token injection on every request
 *   - 401 → token refresh → retry (once)
 *   - In-flight GET deduplication: identical concurrent GET requests share one
 *     underlying HTTP call. This prevents the 429 burst caused by multiple
 *     hooks (useDashboardData + useMarkets + useWalletData) all fetching the
 *     same ticker/balance endpoints simultaneously on page load.
 *
 * NOTE: Only safe GET requests are deduplicated. POST/PUT/DELETE are never shared.
 */

import axios from 'axios'

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080'

const client = axios.create({
  baseURL: API_BASE_URL,
  headers: { 'Content-Type': 'application/json' },
  withCredentials: true,
})

// ── In-flight GET deduplication ──────────────────────────────────────────────
// Map of "<method>:<url>:<serialized-params>" → in-flight Promise
const inFlight = new Map<string, Promise<unknown>>()

function dedupeKey(url: string, params?: unknown): string {
  return `GET:${url}:${params ? JSON.stringify(params) : ''}`
}

// ── Request interceptors ──────────────────────────────────────────────────────

// 1. Attach Bearer token
client.interceptors.request.use((config) => {
  const token = localStorage.getItem('access_token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

// ── Response interceptors ─────────────────────────────────────────────────────

// Singleton in-flight refresh promise.
// When multiple concurrent requests all receive a 401 (e.g. on page load with
// an expired access token), only the FIRST one fires the actual token rotation.
// All subsequent 401 interceptors await the same promise, so the refresh token
// is presented to the auth service exactly once. Without this, each concurrent
// caller presents the same (already-rotated) refresh token, which the auth
// service correctly treats as a hijack attempt and revokes all sessions.
let refreshPromise: Promise<{ accessToken: string; refreshToken: string }> | null = null
let isLoggingOut = false

type SessionExpiredHandler = () => void
let sessionExpiredHandler: SessionExpiredHandler | null = null

type TokenRefreshHandler = (accessToken: string, refreshToken: string) => void
let tokenRefreshHandler: TokenRefreshHandler | null = null

export function setSessionExpiredHandler(handler: SessionExpiredHandler | null) {
  sessionExpiredHandler = handler
}

export function setTokenRefreshHandler(handler: TokenRefreshHandler | null) {
  tokenRefreshHandler = handler
}

/**
 * Safely invalidates the session and cleans up user-specific state (in-memory auth store,
 * private WebSocket channels, notification/wallet stores) without circular imports or redirect loops.
 */
export async function clearSessionAndRedirect() {
  if (isLoggingOut) return
  isLoggingOut = true

  try {
    if (sessionExpiredHandler) {
      try {
        sessionExpiredHandler()
      } catch (err) {
        console.error('Session expired handler failed:', err)
      }
    }
    if (typeof localStorage !== 'undefined') {
      localStorage.removeItem('access_token')
      localStorage.removeItem('refresh_token')
      localStorage.removeItem('user')
    }
  } finally {
    if (typeof window !== 'undefined' && window.location && window.location.pathname !== '/login') {
      window.location.href = '/login'
    }
    setTimeout(() => {
      isLoggingOut = false
    }, 500)
  }
}

export function resetSessionCleanupLock() {
  isLoggingOut = false
}

// 2. Handle 401: refresh → retry; on failure clear session
client.interceptors.response.use(
  (res) => res,
  async (error) => {
    const original = error.config
    if (!original) {
      return Promise.reject(error)
    }

    // Do NOT intercept or attempt token refresh on public auth endpoints (login, register, etc.)
    const isPublicAuthEndpoint = Boolean(
      original.url &&
        (original.url.includes('/api/v1/auth/login') ||
          original.url.includes('/api/v1/auth/register') ||
          original.url.includes('/api/v1/auth/forgot-password') ||
          original.url.includes('/api/v1/auth/reset-password') ||
          original.url.includes('/api/v1/auth/verify'))
    )

    if (error.response?.status === 401) {
      if (isPublicAuthEndpoint) {
        return Promise.reject(error)
      }

      // If the request was the refresh call itself, or it was already retried once and still got 401,
      // invalidate session immediately to prevent infinite loops.
      if (original.url?.includes('/api/v1/auth/refresh') || original._retry) {
        await clearSessionAndRedirect()
        return Promise.reject(error)
      }

      const refresh = typeof localStorage !== 'undefined' ? localStorage.getItem('refresh_token') : null
      if (!refresh) {
        await clearSessionAndRedirect()
        return Promise.reject(error)
      }

      original._retry = true
      try {
        // Only the first concurrent 401 creates the refresh promise.
        // All others share it, so the refresh token is rotated exactly once.
        if (!refreshPromise) {
          refreshPromise = axios
            .post(
              `${API_BASE_URL}/api/v1/auth/refresh`,
              { refreshToken: refresh },
              { withCredentials: true }
            )
            .then((r) => r.data as { accessToken: string; refreshToken: string })
            .finally(() => {
              refreshPromise = null
            })
        }
        const data = await refreshPromise
        if (typeof localStorage !== 'undefined') {
          localStorage.setItem('access_token', data.accessToken)
          localStorage.setItem('refresh_token', data.refreshToken)
        }
        if (tokenRefreshHandler) {
          tokenRefreshHandler(data.accessToken, data.refreshToken)
        }

        original.headers.Authorization = `Bearer ${data.accessToken}`
        return client(original)
      } catch (refreshErr) {
        await clearSessionAndRedirect()
        return Promise.reject(refreshErr)
      }
    }
    return Promise.reject(error)
  }
)

// ── Deduplication wrapper ─────────────────────────────────────────────────────
// Wraps client.get() to return a shared promise for concurrent identical calls.
// Automatically cleans up after the request settles.

// Keep a reference to the original get before overriding it
const originalGet = client.get

client.get = (function dedupedGet(url: string, config?: Record<string, unknown>) {
  // If request has an explicit AbortSignal, do not share in-flight promise
  if (config?.signal) {
    return originalGet.call(client, url, config)
  }

  const key = dedupeKey(url, config?.params)

  const existing = inFlight.get(key)
  if (existing) {
    return existing
  }

  const request = originalGet.call(client, url, config).finally(() => {
    inFlight.delete(key)
  })

  inFlight.set(key, request as Promise<unknown>)
  return request
}) as typeof originalGet

export default client
