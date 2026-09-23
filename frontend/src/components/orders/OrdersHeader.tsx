import type { ConnectionStatus } from '../../api/ws'

interface OrdersHeaderProps {
  wsStatus?: ConnectionStatus
}

export default function OrdersHeader({ wsStatus = 'connected' }: OrdersHeaderProps) {
  const isConnected = wsStatus === 'connected'

  return (
    <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 select-none">
      {/* Title & Subtitle */}
      <div>
        <h1 className="text-3xl font-black text-[#f5f7fa] tracking-tight font-sans" style={{ color: '#f5f7fa' }}>
          Orders &amp; History
        </h1>
        <p className="text-sm text-slate-400 mt-1 font-medium">
          Track every order and execution in your trading journey.
        </p>
      </div>

      {/* Right-side Live Status Badge */}
      <div className="flex items-center gap-3">
        <div className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full bg-[#10b981]/10 border border-[#10b981]/30 text-[#10b981] text-xs font-bold tracking-wide">
          <span
            className={`w-2 h-2 rounded-full ${
              isConnected ? 'bg-[#10b981] animate-pulse' : 'bg-amber-400'
            }`}
          />
          <span>{isConnected ? 'Live' : 'Connecting'}</span>
        </div>
        <span className="text-xs text-slate-400 font-medium hidden md:inline">
          Orders synced in real time
        </span>
      </div>
    </div>
  )
}
