import { Navigate, useLocation } from 'react-router-dom'
import { useAuthStore } from '../store/authStore'
import type { ReactNode } from 'react'

interface ProtectedRouteProps {
  children: ReactNode
}

/**
 * Route guard — redirects unauthenticated users to /login preserving the return destination.
 * Reads from the existing authStore (useAuthStore) — no duplicate auth logic.
 */
export default function ProtectedRoute({ children }: ProtectedRouteProps) {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)
  const location = useLocation()

  if (!isAuthenticated) {
    const returnDestination = encodeURIComponent(location.pathname + location.search)
    return <Navigate to={`/login?returnUrl=${returnDestination}`} replace />
  }

  return <>{children}</>
}

