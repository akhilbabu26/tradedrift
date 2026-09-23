import type { ReactNode } from 'react'

interface DashboardCardProps {
  children: ReactNode
  className?: string
  noPadding?: boolean
}

/**
 * Shared card wrapper for all dashboard lower-grid cards.
 * bg-[#111318] + 1px border + rounded-lg + consistent padding.
 */
export default function DashboardCard({
  children,
  className = '',
  noPadding = false,
}: DashboardCardProps) {
  return (
    <div
      className={`
        bg-[#111318] border border-[#1e2530] rounded-lg
        ${noPadding ? '' : 'p-4'}
        ${className}
      `}
    >
      {children}
    </div>
  )
}
