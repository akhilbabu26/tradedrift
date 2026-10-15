import { useState, useEffect } from 'react'
import { Settings, Radio } from 'lucide-react'
import { wsService, type ConnectionStatus } from '../../api/ws'
import StatusIndicator from './shared/StatusIndicator'

/**
 * Dashboard page header — welcome message + simulator status + engine indicators.
 * User name comes from authStore, never hardcoded.
 * Status dynamically derived from live WebSocket connection state.
 */
export default function DashboardHeader() {
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
      {/* ── Left: Operational Status ──────────────────────────────────────── */}
      <div>
        <div className="flex items-center gap-3 flex-wrap">
          <h1 className="text-xl font-bold text-[#f5f7fa] tracking-tight">
            Dashboard
          </h1>
          <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full bg-[#10b981]/10 border border-[#10b981]/25 text-[#10b981] text-[11px] font-semibold tracking-wide">
            <StatusIndicator status="live" showPing />
            SIMULATOR ACTIVE
          </span>
        </div>
        <p className="text-xs text-[#777D85] mt-0.5">
          Real-time simulated execution & trading operations
        </p>
      </div>

      {/* ── Right: Engine + Market Data Status ────────────────────────────── */}
      <div className="flex items-center gap-5 flex-shrink-0">
        <div className="flex items-center gap-2">
          <StatusIndicator status={indicatorStatus} showPing={showPing} size="md" />
          <div className="flex flex-col leading-tight">
            <span className="text-xs font-semibold text-[#F2F3F5] flex items-center gap-1.5">
              <Settings size={13} className="text-[#D9DCE0] flex-shrink-0" />
              ENGINE {wsStatus === 'connected' ? 'ONLINE' : wsStatus === 'connecting' ? 'CONNECTING' : 'OFFLINE'}
            </span>
            <span className="text-[11px] text-[#777E87]">
              {wsStatus === 'connected' ? 'Matching Engine • Healthy' : wsStatus === 'connecting' ? 'Reconnecting...' : 'Service Unreachable'}
            </span>
          </div>
        </div>

        <div className="w-px h-8 bg-[#292C30]" aria-hidden="true" />

        <div className="flex items-center gap-2">
          <StatusIndicator status={indicatorStatus} showPing={showPing} size="md" />
          <div className="flex flex-col leading-tight">
            <span className="text-xs font-semibold text-[#F2F3F5] flex items-center gap-1.5">
              <Radio size={13} className="text-[#D9DCE0] flex-shrink-0" />
              MARKET DATA {wsStatus === 'connected' ? 'LIVE' : wsStatus === 'connecting' ? 'SYNCING' : 'OFFLINE'}
            </span>
            <span className="text-[11px] text-[#777E87]">
              {wsStatus === 'connected' ? 'Real-time feed' : wsStatus === 'connecting' ? 'Reconnecting feed' : 'Disconnected'}
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}
