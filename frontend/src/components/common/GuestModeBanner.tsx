import { Link, useLocation } from 'react-router-dom'
import { LogIn, UserPlus } from 'lucide-react'

export interface GuestModeBannerProps {
  className?: string
}

export default function GuestModeBanner({ className = '' }: GuestModeBannerProps) {
  const location = useLocation()
  const returnUrl = encodeURIComponent(location.pathname + location.search)

  return (
    <div
      role="banner"
      aria-label="Guest Mode notice"
      className={`w-full bg-gradient-to-r from-[#12161c] via-[#101918] to-[#12161c] border-b border-[#10b981]/25 px-3 sm:px-4 py-2 flex items-center justify-between gap-3 text-xs flex-shrink-0 select-none z-10 ${className}`}
    >
      <div className="flex items-center gap-2.5 min-w-0">
        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-[#10b981]/15 border border-[#10b981]/30 text-[#10b981] font-mono font-bold text-[10px] tracking-wide uppercase flex-shrink-0">
          <span className="w-1.5 h-1.5 rounded-full bg-[#10b981] animate-pulse" />
          GUEST MODE
        </span>
        <span className="text-slate-300 font-medium truncate">
          <span className="hidden sm:inline text-white font-semibold">Guest Mode — </span>
          Explore markets freely. Sign in to start trading.
        </span>
      </div>

      <div className="flex items-center gap-2 flex-shrink-0">
        <Link
          to={`/login?returnUrl=${returnUrl}`}
          className="inline-flex items-center gap-1.5 px-3 py-1 rounded-md bg-white/5 hover:bg-white/10 border border-white/10 text-white font-medium text-xs transition-colors"
        >
          <LogIn size={13} className="text-slate-400" />
          <span>Sign In</span>
        </Link>
        <Link
          to={`/register?returnUrl=${returnUrl}`}
          className="inline-flex items-center gap-1.5 px-3 py-1 rounded-md bg-[#10b981] hover:bg-[#059669] text-black font-bold text-xs transition-colors shadow-[0_0_10px_rgba(16,185,129,0.25)]"
        >
          <UserPlus size={13} />
          <span className="hidden sm:inline">Create Account</span>
          <span className="sm:hidden">Register</span>
        </Link>
      </div>
    </div>
  )
}
