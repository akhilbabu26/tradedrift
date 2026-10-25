/**
 * src/utils/url.ts
 *
 * URL validation helpers to prevent Open Redirect vulnerabilities.
 */

/**
 * Validates and sanitizes a return URL redirect target.
 * Guarantees the destination is a safe relative path within the TradeDrift application.
 *
 * Requirements:
 * 1. Must start with '/'
 * 2. Must NOT start with '//' (protocol-relative external redirect)
 * 3. Must NOT start with '/\\' (Windows path traversal trick)
 * 4. Must NOT contain dangerous URI schemes ('javascript:', 'data:', etc.)
 */
export function getSafeReturnUrl(url?: string | null, fallback = '/trade'): string {
  if (!url) return fallback
  const trimmed = url.trim()

  if (
    trimmed.startsWith('/') &&
    !trimmed.startsWith('//') &&
    !trimmed.startsWith('/\\') &&
    !trimmed.includes('\\') &&
    !trimmed.toLowerCase().includes('javascript:') &&
    !trimmed.toLowerCase().includes('data:')
  ) {
    return trimmed
  }

  return fallback
}
