/**
 * src/utils/apiError.ts
 *
 * Centralized Axios error mapper.
 * Maps HTTP status codes to user-facing error strings.
 * No raw Axios errors should bubble to the UI layer.
 */
import axios from 'axios'

export function extractApiError(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const status = err.response?.status
    const serverMsg = err.response?.data?.message
      || err.response?.data?.error
      || ''
    if (status === 401) return 'Session expired. Please log in again.'
    if (status === 403) return 'You do not have permission to access this resource.'
    if (status === 404) return 'Requested resource not found.'
    if (status === 429) return 'Too many requests. Please try again later.'
    if (status && status >= 500) return serverMsg || 'Server error. Please try again.'
    if (err.code === 'ECONNABORTED' || err.code === 'ERR_NETWORK') return 'Network connection unavailable.'
    return serverMsg || err.message || 'An unexpected error occurred.'
  }
  if (err instanceof Error) return err.message
  return 'An unexpected error occurred.'
}