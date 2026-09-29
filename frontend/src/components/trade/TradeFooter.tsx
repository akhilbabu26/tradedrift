import { useState, useEffect } from 'react'
import StatusIndicator from '../dashboard/shared/StatusIndicator'
import { wsService, type ConnectionStatus } from '../../api/ws'

/**
 * Trade page footer.
 * Shows dynamic live status connected to the WebSocket stream.
 */
export default function TradeFooter() {
  const [wsStatus, setWsStatus] = useState<ConnectionStatus>(() => wsService.getStatus())

  useEffect(() => {
    const unsub = wsService.onStatus((_connected, status) => {
      setWsStatus(status)
    })
    return () => {
      unsub()
    }
  }, [])

  const indicatorStatus = wsStatus === 'connected' ? 'live' : wsStatus === 'connecting' || wsStatus === 'reconnecting' ? 'connecting' : 'offline'
  const isLive = indicatorStatus === 'live'
  const statusLabel = isLive ? 'Matching Engine Online' : wsStatus === 'connecting' || wsStatus === 'reconnecting' ? 'Connecting to Matching Engine...' : 'Matching Engine Offline'

  return (
    <footer className="border-t border-[#1e2530] px-4 py-3 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 flex-shrink-0">
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
          <StatusIndicator status={indicatorStatus} showPing={isLive} />
          <span className="text-slate-400 font-medium">{statusLabel}</span>
        </span>
        <span className="px-2 py-0.5 rounded border border-[#1e2530] font-mono">
          v1.0.0
        </span>
        <span className="text-slate-600">Build Better Traders</span>
      </div>
    </footer>
  )
}
