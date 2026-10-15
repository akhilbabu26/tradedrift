import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { CheckCircle2, AlertCircle, XCircle, Radio } from 'lucide-react'
import { wsService, type ConnectionStatus } from '../../api/ws'
import { useMarketStore } from '../../store/marketStore'
import { TradeDriftSymbol } from '../brand/TradeDriftLogo'
import { RaisedSurface } from '../common/Surface'

type ServiceHealth = 'operational' | 'degraded' | 'offline'

interface GlobalFooterProps {
  /** Optional compact mode for space-constrained terminals */
  compact?: boolean
}

export default function GlobalFooter({ compact = false }: GlobalFooterProps) {
  const [wsStatus, setWsStatus] = useState<ConnectionStatus>(() => wsService.getStatus())
  const marketError = useMarketStore((s) => s.error)
  const markets = useMarketStore((s) => s.markets)

  useEffect(() => {
    const unsub = wsService.onStatus((_connected, status) => {
      setWsStatus(status)
    })
    return () => {
      unsub()
    }
  }, [])

  // Authoritative health states derived from real connection & market store
  const isWsLive = wsStatus === 'connected'
  const isWsDegraded = wsStatus === 'connecting' || wsStatus === 'reconnecting'

  const wsHealth: ServiceHealth = isWsLive ? 'operational' : isWsDegraded ? 'degraded' : 'offline'

  const marketHealth: ServiceHealth = marketError
    ? 'degraded'
    : isWsLive || markets.length > 0
    ? 'operational'
    : isWsDegraded
    ? 'degraded'
    : 'offline'

  const matchingHealth: ServiceHealth = isWsLive ? 'operational' : isWsDegraded ? 'degraded' : 'offline'

  // Overall system status
  const allOperational = wsHealth === 'operational' && marketHealth === 'operational' && matchingHealth === 'operational'
  const isDegraded = !allOperational && (wsHealth === 'degraded' || marketHealth === 'degraded' || isWsDegraded)

  const overallStatusLabel = allOperational
    ? 'Systems Operational'
    : isDegraded
    ? 'Degraded Performance'
    : 'Systems Offline'

  const overallPillClasses = allOperational
    ? 'bg-[#10b981]/10 border-[#10b981]/30 text-[#10b981]'
    : isDegraded
    ? 'bg-amber-400/10 border-amber-400/30 text-amber-400'
    : 'bg-[#ef4444]/10 border-[#ef4444]/30 text-[#ef4444]'

  const currentYear = new Date().getFullYear()

  const renderStatusIcon = (health: ServiceHealth) => {
    switch (health) {
      case 'operational':
        return <CheckCircle2 size={14} className="text-[#10b981] flex-shrink-0" />
      case 'degraded':
        return <AlertCircle size={14} className="text-amber-400 flex-shrink-0" />
      case 'offline':
        return <XCircle size={14} className="text-[#ef4444] flex-shrink-0" />
    }
  }

  return (
    <footer
      className={`w-full bg-[#0a0b0e] select-none text-[#f5f7fa] ${
        compact ? 'px-4 py-4' : 'px-4 sm:px-6 lg:px-8 py-8'
      }`}
      aria-label="TradeDrift Footer"
    >
      <RaisedSurface variant="raised-subtle" className="max-w-[1600px] mx-auto rounded-xl p-6 sm:p-8 lg:p-10">
        {/* ── A. Brand Section ──────────────────────────────────────────────── */}
        <div className="flex items-start gap-4">
          <RaisedSurface
            variant="raised"
            className="w-10 h-10 rounded-lg flex items-center justify-center flex-shrink-0 shadow-sm p-1"
            aria-hidden="true"
          >
            <TradeDriftSymbol size={32} />
          </RaisedSurface>
          <div className="flex flex-col">
            <h2 className="text-lg font-bold text-white tracking-tight leading-tight">TradeDrift</h2>
            <p className="text-xs font-semibold text-[#10b981] tracking-[0.14em] uppercase mt-0.5">
              MARKETS · EXECUTION · INTELLIGENCE
            </p>
            <p className="text-xs sm:text-sm text-slate-400 mt-1 max-w-3xl leading-relaxed">
              Explore markets. Execute trades. Understand the engine.
            </p>
          </div>
        </div>

        {/* Horizontal Divider */}
        <div className="border-b border-[#1e2530] my-7" />

        {/* ── B. Information Sections (2-column layout on desktop) ───────────── */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-x-12 lg:gap-x-24 gap-y-9">
          {/* ── Upper Left: Trading ── */}
          <div>
            <h3 className="text-sm font-bold text-white tracking-tight mb-3.5">Trading</h3>
            <ul className="space-y-2.5">
              <li>
                <Link to="/markets" className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors">
                  Markets
                </Link>
              </li>
              <li>
                <Link to="/trade" className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors">
                  Trading Terminal
                </Link>
              </li>
              <li>
                <Link to="/portfolio" className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors">
                  Portfolio
                </Link>
              </li>
              <li>
                <Link to="/orders" className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors">
                  Order History
                </Link>
              </li>
            </ul>
          </div>

          {/* ── Upper Right: Developer Resources ── */}
          <div>
            <h3 className="text-sm font-bold text-white tracking-tight mb-3.5">Developer Resources</h3>
            <ul className="space-y-2.5">
              <li>
                <a
                  href="https://github.com/akhilbabu26/tradedrift#system-architecture"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors"
                >
                  Architecture
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/akhilbabu26/tradedrift/tree/main/docs"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors"
                >
                  API Documentation
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/akhilbabu26/tradedrift"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors"
                >
                  GitHub Repository
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/akhilbabu26/tradedrift/releases"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors"
                >
                  Changelog
                </a>
              </li>
            </ul>
          </div>

          {/* ── Lower Left: Platform ── */}
          <div>
            <h3 className="text-sm font-bold text-white tracking-tight mb-3.5">Platform</h3>
            <ul className="space-y-2.5">
              <li>
                <Link to="/#how" className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors">
                  How It Works
                </Link>
              </li>
              <li>
                <a
                  href="https://github.com/akhilbabu26/tradedrift#readme"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors"
                >
                  Trading Guide
                </a>
              </li>
              <li>
                <Link to="/" className="text-xs sm:text-sm text-slate-400 hover:text-slate-200 transition-colors">
                  About TradeDrift
                </Link>
              </li>
            </ul>
          </div>

          {/* ── Lower Right: System Status ── */}
          <div>
            <h3 className="text-sm font-bold text-white tracking-tight mb-3.5">System Status</h3>
            <ul className="space-y-2.5">
              <li className="flex items-center gap-2 text-xs sm:text-sm text-slate-300">
                {renderStatusIcon(matchingHealth)}
                <span>Matching Engine</span>
              </li>
              <li className="flex items-center gap-2 text-xs sm:text-sm text-slate-300">
                {renderStatusIcon(marketHealth)}
                <span>Market Data</span>
              </li>
              <li className="flex items-center gap-2 text-xs sm:text-sm text-slate-300">
                {renderStatusIcon(wsHealth)}
                <span>WebSocket</span>
              </li>
            </ul>
            <p className="text-[11px] text-slate-500 mt-2.5 font-normal">Illustrative status preview</p>
          </div>
        </div>

        {/* Horizontal Divider */}
        <div className="border-b border-[#1e2530] my-7" />

        {/* ── D. Bottom Bar ─────────────────────────────────────────────────── */}
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 py-1">
          {/* Left: Copyright */}
          <p className="text-xs text-slate-500">
            {`© ${currentYear} TradeDrift · Educational simulator`}
          </p>

          {/* Right: Status Pill & Version */}
          <div className="flex items-center gap-3">
            <div
              className={`inline-flex items-center gap-1.5 px-3 py-1 rounded-full border text-xs font-medium ${overallPillClasses}`}
              role="status"
              aria-live="polite"
            >
              <Radio size={12} className="flex-shrink-0" />
              <span>{overallStatusLabel}</span>
            </div>
            <span className="text-xs font-mono text-slate-500">v1.0.0</span>
          </div>
        </div>
      </RaisedSurface>
    </footer>
  )
}
