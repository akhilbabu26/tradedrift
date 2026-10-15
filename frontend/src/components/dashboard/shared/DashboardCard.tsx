import type { ReactNode } from 'react'
import { RaisedSurface } from '../../common/Surface'

interface DashboardCardProps {
  children: ReactNode
  className?: string
  noPadding?: boolean
}

/**
 * Shared card wrapper for all dashboard lower-grid cards.
 * Utilizes the shared 3D Surface system (raised-subtle variant)
 * for depth and consistency with the approved sidebar.
 */
export default function DashboardCard({
  children,
  className = '',
  noPadding = false,
}: DashboardCardProps) {
  return (
    <RaisedSurface
      variant="raised-subtle"
      noPadding={noPadding}
      className={className}
    >
      {children}
    </RaisedSurface>
  )
}
