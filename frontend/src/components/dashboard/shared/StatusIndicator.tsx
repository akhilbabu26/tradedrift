interface StatusIndicatorProps {
  status: 'online' | 'connecting' | 'offline' | 'live'
  label?: string
  showPing?: boolean
  size?: 'sm' | 'md'
}

/**
 * Green/amber/red animated dot + optional label.
 * Used in MainNavbar (LIVE indicator), DashboardHeader, SystemStatus, MarketsStatsStrip, MarketsFooter.
 */
export default function StatusIndicator({
  status,
  label,
  showPing = false,
  size = 'sm',
}: StatusIndicatorProps) {
  const dotSize = size === 'sm' ? 'w-1.5 h-1.5' : 'w-2 h-2'

  const dotColor =
    status === 'online' || status === 'live'
      ? 'bg-[#10b981]'
      : status === 'connecting'
      ? 'bg-amber-400'
      : 'bg-[#ef4444]'

  const pingColor =
    status === 'online' || status === 'live'
      ? 'bg-[#10b981]'
      : status === 'connecting'
      ? 'bg-amber-400'
      : 'bg-[#ef4444]'

  return (
    <span className="flex items-center gap-1.5">
      <span className={`relative flex ${dotSize}`}>
        {showPing && (status === 'online' || status === 'live') && (
          <span
            className={`animate-ping absolute inline-flex h-full w-full rounded-full ${pingColor} opacity-75`}
          />
        )}
        <span className={`relative inline-flex rounded-full ${dotSize} ${dotColor}`} />
      </span>
      {label && <span className="text-xs font-medium text-[#f5f7fa]">{label}</span>}
    </span>
  )
}
