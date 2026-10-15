import type { ReactNode } from 'react'

interface SectionHeaderProps {
  title: ReactNode
  action?: ReactNode
  className?: string
}

/**
 * Consistent title + optional action-link row used across dashboard cards.
 */
export default function SectionHeader({ title, action, className = '' }: SectionHeaderProps) {
  return (
    <div className={`flex items-center justify-between mb-3 ${className}`}>
      <span className="text-sm font-semibold text-[#f5f7fa]">{title}</span>
      {action && (
        <span className="text-xs text-[#777E87] hover:text-[#F2F3F5] cursor-pointer transition-colors">
          {action}
        </span>
      )}
    </div>
  )
}
