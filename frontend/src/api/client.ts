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

// 2. Handle 401: refresh → retry; on failure clear session
client.interceptors.response.use(
  (res) => res,
  async (error) => {
    const original = error.config
    if (error.response?.status === 401 && !original?._retry) {
      const refresh = localStorage.getItem('refresh_token')
      if (!refresh) {
        return Promise.reject(error)
      }

      original._retry = true
      try {
        const { data } = await axios.post(`${API_BASE_URL}/api/v1/auth/refresh`, {
          refreshToken: refresh,
        })
        localStorage.setItem('access_token', data.accessToken)
        localStorage.setItem('refresh_token', data.refreshToken)
        original.headers.Authorization = `Bearer ${data.accessToken}`
        return client(original)
      } catch {
        localStorage.removeItem('access_token')
        localStorage.removeItem('refresh_token')
        localStorage.removeItem('user')
        window.location.href = '/login'
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
