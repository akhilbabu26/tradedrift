import { useState, useEffect } from 'react'
import { Cpu, Radio } from 'lucide-react'
import { useAuthStore } from '../../store/authStore'
import { wsService, type ConnectionStatus } from '../../api/ws'
import StatusIndicator from './shared/StatusIndicator'

/**
 * Dashboard page header — welcome message + simulator status + engine indicators.
 * User name comes from authStore, never hardcoded.
 * Status dynamically derived from live WebSocket connection state.
 */
export default function DashboardHeader() {
  const user = useAuthStore((s) => s.user)
  const firstName = user?.username?.split(' ')[0] || null
  const [wsStatus, setWsStatus] = useState<ConnectionStatus>(() => wsService.getStatus())

  useEffect(() => {
    const unsub = wsService.onStatus((_connected, status) => {
      setWsStatus(status)
    })
    return () => {
      unsub()
    }
  }, [])

  const indicatorStatus =
    wsStatus === 'connected' ? 'live'
    : wsStatus === 'connecting' ? 'connecting'
    : 'offline'

  const showPing = wsStatus === 'connected'

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
          <StatusIndicator status={indicatorStatus} showPing={showPing} size="md" />
          <div className="flex flex-col leading-tight">
            <span className="text-xs font-semibold text-[#f5f7fa] flex items-center gap-1">
              <Cpu size={11} className={showPing ? 'text-[#10b981]' : 'text-slate-400'} />
              ENGINE {wsStatus === 'connected' ? 'ONLINE' : wsStatus === 'connecting' ? 'CONNECTING' : 'OFFLINE'}
            </span>
            <span className="text-[11px] text-slate-500">
              {wsStatus === 'connected' ? 'Matching Engine • Healthy' : wsStatus === 'connecting' ? 'Reconnecting...' : 'Service Unreachable'}
            </span>
          </div>
        </div>

        <div className="w-px h-8 bg-[#1e2530]" aria-hidden="true" />

        <div className="flex items-center gap-2">
          <StatusIndicator status={indicatorStatus} showPing={showPing} size="md" />
          <div className="flex flex-col leading-tight">
            <span className="text-xs font-semibold text-[#f5f7fa] flex items-center gap-1">
              <Radio size={11} className={showPing ? 'text-[#10b981]' : 'text-slate-400'} />
              MARKET DATA {wsStatus === 'connected' ? 'LIVE' : wsStatus === 'connecting' ? 'SYNCING' : 'OFFLINE'}
            </span>
            <span className="text-[11px] text-slate-500">
              {wsStatus === 'connected' ? 'Real-time feed' : wsStatus === 'connecting' ? 'Reconnecting feed' : 'Disconnected'}
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}
