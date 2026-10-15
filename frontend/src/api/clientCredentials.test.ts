import { describe, it, expect, beforeEach, vi } from 'vitest'
import client from './client'

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

describe('Secure Cookie-Based Authentication Architecture (SEC-01)', () => {
  beforeEach(() => {
    mockLocalStorage.clear()
    vi.clearAllMocks()
  })

  it('configures withCredentials: true on the default Axios client instance', () => {
    expect(client.defaults.withCredentials).toBe(true)
  })

  it('attaches Bearer token from localStorage for backward compatibility when available', async () => {
    localStorage.setItem('access_token', 'mock_jwt_token_xyz')

    // Inspect request interceptor behavior
    const config = await (client.interceptors.request as any).handlers[0].fulfilled({
      headers: {},
    })

    expect(config.headers.Authorization).toBe('Bearer mock_jwt_token_xyz')
  })

  it('does not attach Authorization header if localStorage token is absent, relying on HttpOnly session cookie', async () => {
    localStorage.removeItem('access_token')

    const config = await (client.interceptors.request as any).handlers[0].fulfilled({
      headers: {},
    })

    expect(config.headers.Authorization).toBeUndefined()
  })
})
