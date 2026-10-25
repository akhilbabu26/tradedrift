import { useEffect, useRef } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { X, Lock, LogIn, UserPlus, ArrowRight } from 'lucide-react'

export interface AuthPromptModalProps {
  isOpen: boolean
  onClose: () => void
  title?: string
  description?: string
  targetMarket?: string
}

export default function AuthPromptModal({
  isOpen,
  onClose,
  title = 'Sign In to Start Trading',
  description = 'You are exploring TradeDrift in Guest Mode. An account is required to place simulator orders, manage virtual funds, and access your personal portfolio.',
  targetMarket,
}: AuthPromptModalProps) {
  const navigate = useNavigate()
  const location = useLocation()
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

  // Target return destination preserves the exact market the user was viewing
  const returnDestination = targetMarket
    ? `/trade?market=${encodeURIComponent(targetMarket)}`
    : location.pathname + location.search || '/trade'

  const returnParam = encodeURIComponent(returnDestination)

  const handleSignIn = () => {
    onClose()
    navigate(`/login?returnUrl=${returnParam}`)
  }

  const handleCreateAccount = () => {
    onClose()
    navigate(`/register?returnUrl=${returnParam}`)
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/80 backdrop-blur-md animate-fade-in"
      role="dialog"
      aria-modal="true"
      aria-labelledby="auth-prompt-title"
    >
      <div className="absolute inset-0" onClick={onClose} aria-hidden="true" />

      <div
        ref={modalRef}
        className="relative w-full max-w-md bg-[#0e1014] border border-[#262a33] rounded-2xl p-6 sm:p-7 shadow-[0_24px_50px_rgba(0,0,0,0.85)] z-10 overflow-hidden"
      >
        {/* Glow */}
        <div
          className="absolute -top-20 -left-20 w-48 h-48 rounded-full opacity-20 pointer-events-none"
          style={{ background: 'radial-gradient(circle, #10b981 0%, transparent 70%)' }}
        />

        {/* Close Button */}
        <button
          type="button"
          onClick={onClose}
          aria-label="Close"
          className="absolute top-4 right-4 p-2 rounded-xl bg-white/[0.03] border border-white/[0.08] text-slate-400 hover:text-white hover:bg-white/10 transition-colors cursor-pointer"
        >
          <X size={16} />
        </button>

        {/* Icon & Title */}
        <div className="flex flex-col items-center text-center mb-6">
          <div className="w-14 h-14 rounded-2xl bg-[#10b981]/10 border border-[#10b981]/25 flex items-center justify-center mb-4 text-[#10b981] shadow-[0_0_20px_rgba(16,185,129,0.15)]">
            <Lock size={26} />
          </div>

          <h3 id="auth-prompt-title" className="text-lg font-bold text-white tracking-tight">
            {title}
          </h3>

          <p className="text-xs text-slate-400 mt-2 leading-relaxed">
            {description}
          </p>

          <div className="mt-3 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-[#10b981]/10 border border-[#10b981]/20 text-[#10b981] text-[11px] font-semibold">
            <span>Free 10,000 USDT Virtual Balance</span>
          </div>
        </div>

        {/* Action Buttons */}
        <div className="space-y-2.5">
          <button
            type="button"
            onClick={handleSignIn}
            className="w-full flex items-center justify-center gap-2 py-3 px-4 rounded-xl bg-[#10b981] hover:bg-[#059669] text-black font-bold text-sm transition-all shadow-[0_0_15px_rgba(16,185,129,0.25)] cursor-pointer"
          >
            <LogIn size={16} />
            <span>Sign In to Your Account</span>
            <ArrowRight size={14} />
          </button>

          <button
            type="button"
            onClick={handleCreateAccount}
            className="w-full flex items-center justify-center gap-2 py-2.5 px-4 rounded-xl bg-white/5 hover:bg-white/10 border border-white/10 text-white font-semibold text-xs transition-colors cursor-pointer"
          >
            <UserPlus size={15} className="text-slate-400" />
            <span>Create New Account</span>
          </button>

          <button
            type="button"
            onClick={onClose}
            className="w-full py-2 text-center text-xs text-slate-500 hover:text-slate-300 transition-colors cursor-pointer"
          >
            Continue Exploring Markets
          </button>
        </div>
      </div>
    </div>
  )
}
