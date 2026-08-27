import { Calendar, Info } from 'lucide-react'
import AppLayout from '../components/layout/AppLayout'
import AnalyticsMetricCards from '../components/analytics/AnalyticsMetricCards'
import EquityCurveChart from '../components/analytics/EquityCurveChart'
import MonthlyPnlHeatmap from '../components/analytics/MonthlyPnlHeatmap'
import AssetAllocationChart from '../components/analytics/AssetAllocationChart'

export default function AnalyticsPage() {
  return (
    <AppLayout>
      <div className="flex flex-col space-y-6 max-w-[1920px] mx-auto select-none pb-12">
        {/* ── 1. Page Header: Title & Subtitle + Date Selector ── */}
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex flex-col">
            <h1 className="text-xl lg:text-2xl font-black text-white tracking-tight font-sans">
              Analytics &amp; PnL Performance
            </h1>
            <p className="text-xs text-slate-400 font-sans mt-0.5">
              Deep performance insights, advanced analytics, and trading intelligence
            </p>
          </div>

          {/* Date Selector */}
          <button
            type="button"
            className="flex items-center gap-2 px-3.5 py-1.5 rounded-lg bg-[#0e121b] border border-[#1b2230] text-xs font-mono text-slate-300 hover:text-white hover:border-slate-500 transition-colors shadow-lg"
          >
            <Calendar size={13} className="text-slate-400" />
            <span>Aug 01, 2026 – Aug 27, 2026</span>
          </button>
        </div>

        {/* ── 2. Top Metrics Bar (5 KPI Cards) ── */}
        <AnalyticsMetricCards />

        {/* ── 3. Main Cumulative Equity Curve Panel ── */}
        <EquityCurveChart />

        {/* ── 4 & 5. Monthly PnL Heatmap (Left 6 Cols) & Asset Allocation (Right 6 Cols) ── */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 items-stretch">
          <MonthlyPnlHeatmap />
          <AssetAllocationChart />
        </div>

        {/* ── Footer Note ── */}
        <div className="flex items-center gap-1.5 text-[11px] text-slate-500 font-sans pt-2">
          <Info size={13} className="flex-shrink-0" />
          <span>All values are estimated in USD · Data updates every 30 seconds via WebSocket</span>
        </div>
      </div>
    </AppLayout>
  )
}
