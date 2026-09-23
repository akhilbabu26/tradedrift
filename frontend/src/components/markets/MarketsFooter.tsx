import StatusIndicator from '../dashboard/shared/StatusIndicator'

/**
 * Markets page footer.
 * Matches the dashboard footer pattern. Can be extracted to MainFooter
 * once other authenticated pages are built and the footer is confirmed shared.
 */
export default function MarketsFooter() {
  return (
    <footer className="border-t border-[#1e2530] pt-4 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3">
      {/* Logo + tagline */}
      <div>
        <p className="text-sm font-bold text-[#f5f7fa] flex items-center gap-2">
          <span className="w-5 h-5 rounded bg-[#10b981] flex items-center justify-center text-[#0a0b0e] font-black text-[10px]">
            TD
          </span>
          TradeDrift
        </p>
        <p className="text-xs text-slate-500 mt-0.5">
          A production-grade cryptocurrency exchange simulator.
        </p>
      </div>

      {/* Status + version */}
      <div className="flex items-center gap-4 text-xs text-slate-500">
        <span className="flex items-center gap-1.5">
          <StatusIndicator status="live" showPing />
          <span className="text-slate-400 font-medium">Matching Engine Online</span>
        </span>
        <span className="px-2 py-0.5 rounded border border-[#1e2530] font-mono">
          v1.0.0
        </span>
        <span className="text-slate-600">Build Better Traders</span>
      </div>
    </footer>
  )
}
