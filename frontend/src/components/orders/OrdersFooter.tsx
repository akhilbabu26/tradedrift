import StatusIndicator from '../dashboard/shared/StatusIndicator'

/**
 * Orders & History page footer.
 * Matches TradeFooter, MarketsFooter, and WalletFooter visual pattern.
 */
export default function OrdersFooter() {
  return (
    <footer className="border-t border-[#1e2530] px-4 py-4 mt-6 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 flex-shrink-0 select-none">
      {/* Logo + Tagline */}
      <div>
        <p className="text-sm font-bold text-[#f5f7fa] flex items-center gap-2">
          <span className="w-5 h-5 rounded bg-[#10b981] flex items-center justify-center text-[#0a0b0e] font-black text-[10px]">
            TD
          </span>
          TradeDrift
        </p>
        <p className="text-xs text-slate-400 mt-0.5">
          A production-grade cryptocurrency exchange simulator.
        </p>
      </div>

      {/* Status + Version */}
      <div className="flex items-center gap-4 text-xs text-slate-400">
        <span className="flex items-center gap-1.5">
          <StatusIndicator status="live" showPing />
          <span className="text-slate-300 font-medium">Matching Engine Online</span>
        </span>
        <span className="px-2 py-0.5 rounded border border-[#1e2530] font-mono text-slate-300">
          v1.0.0
        </span>
        <span className="text-slate-400 font-medium">Build Better Traders</span>
      </div>
    </footer>
  )
}
