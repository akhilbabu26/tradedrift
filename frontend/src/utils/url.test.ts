import { describe, it, expect } from 'vitest'
import { getSafeReturnUrl } from './url'

describe('getSafeReturnUrl', () => {
  it('returns valid relative routes', () => {
    expect(getSafeReturnUrl('/trade')).toBe('/trade')
    expect(getSafeReturnUrl('/trade?market=ETH-USDT')).toBe('/trade?market=ETH-USDT')
    expect(getSafeReturnUrl('/markets')).toBe('/markets')
  })

  it('falls back when url is empty or null', () => {
    expect(getSafeReturnUrl(null)).toBe('/trade')
    expect(getSafeReturnUrl('')).toBe('/trade')
    expect(getSafeReturnUrl(undefined, '/dashboard')).toBe('/dashboard')
  })

  it('rejects protocol-relative and external URLs', () => {
    expect(getSafeReturnUrl('//evil.com')).toBe('/trade')
    expect(getSafeReturnUrl('https://evil.com')).toBe('/trade')
    expect(getSafeReturnUrl('http://evil.com')).toBe('/trade')
    expect(getSafeReturnUrl('javascript:alert(1)')).toBe('/trade')
    expect(getSafeReturnUrl('/\\evil.com')).toBe('/trade')
  })
})
