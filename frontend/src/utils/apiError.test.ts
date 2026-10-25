import { describe, it, expect } from 'vitest'
import { AxiosError } from 'axios'
import { extractApiError } from './apiError'

describe('extractApiError', () => {
  it('maps timeout ECONNABORTED error to request timed out message', () => {
    const error = new AxiosError('timeout of 10000ms exceeded', 'ECONNABORTED')
    expect(extractApiError(error)).toBe('Request timed out. Please check your connection and try again.')
  })

  it('maps ERR_NETWORK to network connection unavailable message', () => {
    const error = new AxiosError('Network Error', 'ERR_NETWORK')
    expect(extractApiError(error)).toBe('Network connection unavailable.')
  })

  it('maps 401 status to session expired message', () => {
    const error = new AxiosError('Unauthorized', 'ERR_BAD_REQUEST', undefined, undefined, {
      status: 401,
      statusText: 'Unauthorized',
      headers: {},
      config: {} as any,
      data: { message: 'Token expired' },
    })
    expect(extractApiError(error)).toBe('Session expired. Please log in again.')
  })

  it('maps 429 status to rate limit message', () => {
    const error = new AxiosError('Too Many Requests', 'ERR_BAD_REQUEST', undefined, undefined, {
      status: 429,
      statusText: 'Too Many Requests',
      headers: {},
      config: {} as any,
      data: {},
    })
    expect(extractApiError(error)).toBe('Too many requests. Please try again later.')
  })

  it('extracts server error message when available on 500', () => {
    const error = new AxiosError('Internal Error', 'ERR_BAD_RESPONSE', undefined, undefined, {
      status: 500,
      statusText: 'Internal Error',
      headers: {},
      config: {} as any,
      data: { message: 'Custom database failure' },
    })
    expect(extractApiError(error)).toBe('Custom database failure')
  })
})
