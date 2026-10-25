import { useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { X, ArrowRight, Eye, ShieldCheck, Sparkles, UserPlus, LogIn } from 'lucide-react'
import { TradeDriftSymbol } from '../brand/TradeDriftLogo'

export interface LaunchSimulatorModalProps {
  isOpen: boolean
  onClose: () => void
  destinationMarket?: string
}

export default function LaunchSimulatorModal({
  isOpen,
  onClose,
  destinationMarket = 'BTC-USDT',
}: LaunchSimulatorModalProps) {
  const navigate = useNavigate()
  const modalRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!isOpen) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, onClose])

  if (!isOpen) return null

  const targetTradeUrl = `/trade?market=${encodeURIComponent(destinationMarket)}`

  const handleGuestExplore = () => {
    onClose()
    navigate(targetTradeUrl)
  }

  const handleCreateAccount = () => {
    onClose()
    navigate(`/register?returnUrl=${encodeURIComponent(targetTradeUrl)}`)
  }

  const handleSignIn = () => {
    onClose()
    navigate(`/login?returnUrl=${encodeURIComponent(targetTradeUrl)}`)
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/75 backdrop-blur-md animate-fade-in"
      role="dialog"
      aria-modal="true"
      aria-labelledby="launch-simulator-title"
    >
      {/* Backdrop click */}
      <div className="absolute inset-0" onClick={onClose} aria-hidden="true" />

      {/* Modal Card */}
      <div
        ref={modalRef}
        className="relative w-full max-w-lg bg-[#0e1014] border border-[#242830] rounded-2xl p-6 sm:p-7 shadow-[0_24px_50px_rgba(0,0,0,0.8),0_0_40px_rgba(16,185,129,0.06)] overflow-hidden z-10"
      >
        {/* Subtle decorative radial glow */}
        <div
          className="absolute -top-24 -right-24 w-60 h-60 rounded-full opacity-25 pointer-events-none"
          style={{ background: 'radial-gradient(circle, #10b981 0%, transparent 70%)' }}
        />

        {/* Close button */}
        <button
          type="button"
          onClick={onClose}
          aria-label="Close dialog"
          className="absolute top-4 right-4 p-2 rounded-xl bg-white/[0.03] border border-white/[0.08] text-slate-400 hover:text-white hover:bg-white/10 transition-colors cursor-pointer"
        >
          <X size={16} />
        </button>

        {/* Header */}
        <div className="flex items-center gap-3 mb-5">
          <div className="p-2.5 rounded-xl bg-[#10b981]/10 border border-[#10b981]/25 flex-shrink-0">
            <TradeDriftSymbol size={28} />
          </div>
          <div>
            <h2 id="launch-simulator-title" className="text-xl font-bold text-white tracking-tight">
              Launch TradeDrift Simulator
            </h2>
            <p className="text-xs text-slate-400 mt-0.5">
              Choose how you would like to explore the trading environment
            </p>
          </div>
        </div>

        {/* Options Grid */}
        <div className="space-y-3.5 my-6">
          {/* Option A: Create Account / Sign In */}
          <div className="relative group p-4 sm:p-5 rounded-xl bg-gradient-to-b from-[#15191f] to-[#101217] border border-[#10b981]/30 hover:border-[#10b981]/60 transition-all shadow-[0_4px_20px_rgba(0,0,0,0.4)]">
            <div className="flex items-start justify-between gap-3 mb-2">
              <div className="flex items-center gap-2">
                <span className="p-1.5 rounded-lg bg-[#10b981]/15 text-[#10b981]">
                  <Sparkles size={16} />
                </span>
                <span className="text-sm font-bold text-white">Option A: Full Trading Access</span>
              </div>
              <span className="px-2 py-0.5 rounded-full bg-[#10b981]/15 text-[#10b981] text-[10px] font-semibold border border-[#10b981]/30">
                10,000 USDT Included
              </span>
            </div>

            <p className="text-xs text-slate-300 leading-relaxed mb-4">
              Create an account or sign in to execute simulator orders, manage virtual funds, and track your portfolio.
            </p>

            <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2.5">
              <button
                type="button"
                onClick={handleCreateAccount}
                className="flex-1 flex items-center justify-center gap-2 py-2.5 px-4 bg-[#10b981] hover:bg-[#059669] text-black font-bold text-xs rounded-lg transition-all shadow-[0_0_15px_rgba(16,185,129,0.3)] cursor-pointer"
              >
                <UserPlus size={14} />
                Create Account
                <ArrowRight size={13} />
              </button>
              <button
                type="button"
                onClick={handleSignIn}
                className="flex items-center justify-center gap-1.5 py-2.5 px-4 bg-white/5 hover:bg-white/10 border border-white/10 text-white font-medium text-xs rounded-lg transition-all cursor-pointer"
              >
                <LogIn size={14} className="text-slate-400" />
                Sign In
              </button>
            </div>
          </div>

          {/* Option B: Explore as Guest */}
          <div className="relative group p-4 sm:p-5 rounded-xl bg-[#121418] border border-[#272b33] hover:border-slate-500/50 transition-all">
            <div className="flex items-start justify-between gap-3 mb-2">
              <div className="flex items-center gap-2">
                <span className="p-1.5 rounded-lg bg-white/5 text-slate-300">
                  <Eye size={16} />
                </span>
                <span className="text-sm font-bold text-white">Option B: Explore as Guest</span>
              </div>
              <span className="px-2 py-0.5 rounded-full bg-white/5 text-slate-400 text-[10px] font-medium border border-white/10">
                Read-Only
              </span>
            </div>

            <p className="text-xs text-slate-400 leading-relaxed mb-4">
              Explore live order books, real-time candlestick charts, and tickers freely without creating an account.
            </p>

            <button
              type="button"
              onClick={handleGuestExplore}
              className="w-full flex items-center justify-center gap-2 py-2.5 px-4 bg-[#1b1f26] hover:bg-[#232832] border border-[#303744] hover:border-slate-400 text-slate-200 hover:text-white font-semibold text-xs rounded-lg transition-all cursor-pointer"
            >
              Explore as Guest
              <ArrowRight size={13} />
            </button>
          </div>
        </div>

        {/* Security / Info footer note */}
        <div className="flex items-center justify-center gap-2 pt-1 text-[11px] text-slate-500">
          <ShieldCheck size={13} className="text-[#10b981]" />
          <span>No credit card or download required · Completely risk-free</span>
        </div>
      </div>
    </div>
  )
}
