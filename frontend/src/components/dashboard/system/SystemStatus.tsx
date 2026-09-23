import { ArrowRight, RefreshCw } from 'lucide-react'
import type { ServiceStatus } from '../../../types/dashboard'
import DashboardCard from '../shared/DashboardCard'
import StatusIndicator from '../shared/StatusIndicator'

interface SystemStatusProps {
  services: ServiceStatus[]
}

/**
 * System Status card — 2-column service grid with latency, overall health,
 * and last-checked timestamp.
 */
export default function SystemStatus({ services }: SystemStatusProps) {
  const allOnline = services.every((s) => s.status === 'Online')

  return (
    <DashboardCard>
      {/* Header */}
      <div className="flex items-center justify-between mb-3">
        <span className="text-sm font-semibold text-[#f5f7fa]">System Status</span>
        <ArrowRight size={13} className="text-slate-500" />
      </div>

      {/* 2-column service grid */}
      <div className="grid grid-cols-2 gap-x-4 gap-y-2 mb-3">
        {services.map((service) => {
          const indicatorStatus =
            service.status === 'Online' ? 'online'
            : service.status === 'Degraded' ? 'connecting'
            : 'offline'
          return (
            <div key={service.name} className="flex items-center gap-2">
              <StatusIndicator status={indicatorStatus} showPing={service.status === 'Online'} />
              <div className="flex flex-col min-w-0">
                <span className="text-xs text-slate-300 truncate">{service.name}</span>
                <div className="flex items-center gap-1">
                  <span className="text-[11px] font-semibold text-[#10b981]">{service.status}</span>
                  <span className="text-[11px] text-slate-500 font-mono">{service.latencyMs}ms</span>
                </div>
              </div>
            </div>
          )
        })}
      </div>

      {/* Divider */}
      <div className="border-t border-[#1e2530] pt-3 flex items-center justify-between">
        {/* Overall health */}
        <div className="flex items-center gap-2">
          <StatusIndicator status={allOnline ? 'online' : 'offline'} showPing={allOnline} />
          <div className="flex flex-col">
            <span className="text-[11px] text-slate-500 uppercase tracking-wider">All Systems</span>
            <span className={`text-xs font-semibold ${allOnline ? 'text-[#10b981]' : 'text-[#ef4444]'}`}>
              {allOnline ? 'Healthy' : 'Degraded'}
            </span>
          </div>
        </div>

        {/* Last checked */}
        <div className="flex items-center gap-1 text-[11px] text-slate-500">
          <RefreshCw size={10} />
          <span>Last checked: just now</span>
        </div>
      </div>
    </DashboardCard>
  )
}
