import { Cpu, Radio } from 'lucide-react'
import { useAuthStore } from '../../store/authStore'
import StatusIndicator from './shared/StatusIndicator'

/**
 * Dashboard page header — welcome message + simulator status + engine indicators.
 * User name comes from authStore, never hardcoded.
 */
export default function DashboardHeader() {
  const user = useAuthStore((s) => s.user)
  const firstName = user?.username?.split(' ')[0] || null

  return (
    <div className="flex items-start justify-between flex-wrap gap-3 mb-5">
      {/* ── Left: Welcome + Simulator Status ──────────────────────────────── */}
      <div>
        <div className="flex items-center gap-3 flex-wrap">
          <h1 className="text-xl font-bold text-[#f5f7fa] tracking-tight">
            {firstName ? `Welcome back, ${firstName} 👋` : 'Welcome back 👋'}
          </h1>
          <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-[#10b981]/10 border border-[#10b981]/25 text-[#10b981] text-[11px] font-semibold tracking-wide">
            <StatusIndicator status="live" showPing />
            SIMULATOR ACTIVE
          </span>
        </div>
        <p className="text-sm text-slate-400 mt-0.5">
          Ready to trade? The simulator is live and running.
        </p>
      </div>

      {/* ── Right: Engine + Market Data Status ────────────────────────────── */}
      <div className="flex items-center gap-4 flex-shrink-0">
        <div className="flex items-center gap-2">
          <StatusIndicator status="live" showPing size="md" />
          <div className="flex flex-col leading-tight">
            <span className="text-xs font-semibold text-[#f5f7fa] flex items-center gap-1">
              <Cpu size={11} className="text-[#10b981]" />
              ENGINE ONLINE
            </span>
            <span className="text-[11px] text-slate-500">Matching Engine • Healthy</span>
          </div>
        </div>

        <div className="w-px h-8 bg-[#1e2530]" aria-hidden="true" />

        <div className="flex items-center gap-2">
          <StatusIndicator status="live" showPing size="md" />
          <div className="flex flex-col leading-tight">
            <span className="text-xs font-semibold text-[#f5f7fa] flex items-center gap-1">
              <Radio size={11} className="text-[#10b981]" />
              MARKET DATA LIVE
            </span>
            <span className="text-[11px] text-slate-500">Real-time data</span>
          </div>
        </div>
      </div>
    </div>
  )
}
