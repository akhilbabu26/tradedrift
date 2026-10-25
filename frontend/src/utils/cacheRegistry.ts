/**
 * src/utils/cacheRegistry.ts
 *
 * Centralized registry for private, user-scoped in-memory SWR caches.
 * Ensures zero cross-user data leakage upon logout or session expiry
 * without circular dependencies between hooks and auth stores.
 */

type CacheClearHandler = () => void

const handlers = new Set<CacheClearHandler>()

export function registerCacheClearHandler(handler: CacheClearHandler): () => void {
  handlers.add(handler)
  return () => {
    handlers.delete(handler)
  }
}

export function clearAllPrivateCaches(): void {
  handlers.forEach((h) => {
    try {
      h()
    } catch (err) {
      console.error('Failed to clear private cache:', err)
    }
  })
}
