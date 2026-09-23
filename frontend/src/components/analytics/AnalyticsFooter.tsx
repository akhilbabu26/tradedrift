/**
 * Analytics page footer — matches existing TradeDrift terminal footer pattern
 * seen in the reference screenshot.
 */
export default function AnalyticsFooter() {
  return (
    <footer className="border-t border-[#1e2530] px-1 py-4 mt-2 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 flex-shrink-0 select-none">
      {/* Logo + Tagline */}
      <div>
        <p className="text-sm font-bold text-[#f5f7fa] flex items-center gap-2">
          <span className="w-5 h-5 rounded bg-[#10b981] flex items-center justify-center text-[#0a0b0e] font-black text-[10px]">
            TD
          </span>
          TradeDrift
        </p>
        <p className="text-xs text-[#64748b] mt-0.5">
          A production-grade cryptocurrency exchange simulator.
        </p>
      </div>

      {/* Right side Tagline & Version */}
      <div className="flex items-center gap-4 text-xs">
        <span className="text-[#94a3b8] font-medium">Trade. Learn. Grow.</span>
        <span className="text-[#64748b] font-mono">v1.0.0</span>
      </div>
    </footer>
  )
}
