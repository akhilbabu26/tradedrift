import { useState, useRef, useEffect } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { RefreshCw, ShieldCheck, AlertCircle, ArrowLeft } from 'lucide-react'
import AuthCard from '../../components/AuthCard'
import { useAuthStore } from '../../store/authStore'
import { authApi } from '../../api/auth'
import { TradeDriftSymbol } from '../../components/brand/TradeDriftLogo'
import { getSafeReturnUrl } from '../../utils/url'

export default function VerifyEmailPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const email = searchParams.get('email') || ''
  const returnUrl = searchParams.get('returnUrl')
  const targetDestination = getSafeReturnUrl(returnUrl, '/dashboard')
  const { setTokens, setUser } = useAuthStore()

  const [otp, setOtp]               = useState<string[]>(Array(6).fill(''))
  const [loading, setLoading]       = useState(false)
  const [resending, setResending]   = useState(false)
  const [cooldown, setCooldown]     = useState(60)
  const [error, setError]           = useState('')
  const [success, setSuccess]       = useState('')
  const inputRefs = useRef<(HTMLInputElement | null)[]>([])

  // Auto-focus first input on mount if email is present
  useEffect(() => {
    if (email) {
      inputRefs.current[0]?.focus()
    }
  }, [email])

  // 60-second countdown timer for resending verification code
  useEffect(() => {
    if (cooldown <= 0) return
    const timer = setInterval(() => {
      setCooldown((prev) => prev - 1)
    }, 1000)
    return () => clearInterval(timer)
  }, [cooldown])

  const handleOtpChange = (index: number, value: string) => {
    if (!/^\d?$/.test(value)) return
    const next = [...otp]
    next[index] = value
    setOtp(next)
    if (value && index < 5) inputRefs.current[index + 1]?.focus()
  }

  const handleKeyDown = (index: number, e: React.KeyboardEvent) => {
    if (e.key === 'Backspace' && !otp[index] && index > 0) {
      inputRefs.current[index - 1]?.focus()
    }
  }

  const handlePaste = (e: React.ClipboardEvent) => {
    const text = e.clipboardData.getData('text').replace(/\D/g, '').slice(0, 6)
    if (text.length === 6) {
      setOtp(text.split(''))
      inputRefs.current[5]?.focus()
    }
    e.preventDefault()
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!email) {
      setError('Missing email address. Please register or log in first.')
      return
    }
    const code = otp.join('')
    if (code.length < 6) { setError('Please enter the full 6-digit code'); return }
    setError('')
    setLoading(true)
    try {
      const { data } = await authApi.verifyEmail({ email, code })
      if (data?.accessToken) {
        setTokens(data.accessToken, data.refreshToken)
        if (data.user) setUser(data.user)
      }
      navigate(targetDestination)
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Invalid or expired code'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  const handleResend = async () => {
    if (!email || cooldown > 0 || resending) return
    setResending(true)
    setError('')
    try {
      await authApi.resendVerification({ email })
      setSuccess('A new code has been sent to your email.')
      setCooldown(60)
      setTimeout(() => setSuccess(''), 4000)
    } catch {
      setError('Failed to resend code. Please try again.')
    } finally {
      setResending(false)
    }
  }

  if (!email) {
    return (
      <AuthCard
        hideLogo={true}
        cardClassName="w-full max-w-[460px] p-6 sm:p-8 bg-[rgba(14,18,21,0.82)] backdrop-blur-md rounded-[24px] border border-[#252D32]/70 shadow-[0_20px_50px_rgba(0,0,0,0.85),0_0_40px_rgba(0,201,149,0.06)]"
      >
        <div className="flex justify-center mb-5">
          <Link to="/" className="group inline-flex items-center gap-3" aria-label="TradeDrift home">
            <div className="w-10 h-10 rounded-xl bg-[#141D22] border border-[#23313A] flex items-center justify-center shadow-inner group-hover:border-[#00C995]/50 transition-colors">
              <TradeDriftSymbol size={24} />
            </div>
            <div className="flex flex-col text-left">
              <span className="text-lg font-bold tracking-tight text-white leading-tight">TradeDrift</span>
              <span className="text-[9px] font-semibold text-[#00C995] tracking-[0.14em] uppercase">MARKETS · EXECUTION · INTELLIGENCE</span>
            </div>
          </Link>
        </div>

        <div className="flex justify-center mb-4">
          <div className="w-14 h-14 rounded-2xl bg-amber-500/10 border border-amber-500/30 flex items-center justify-center text-amber-400">
            <AlertCircle size={28} />
          </div>
        </div>
        <div className="text-center mb-6">
          <h1 className="text-xl sm:text-2xl font-bold text-white mb-2">Missing Email</h1>
          <p className="text-xs sm:text-sm text-[#94A3B8] leading-relaxed">
            No email was provided to verify. Please register an account or sign in to continue.
          </p>
        </div>
        <Link
          to="/register"
          className="w-full h-11 sm:h-12 bg-[#00C995] hover:bg-[#00b585] text-[#050B0B] font-bold rounded-xl text-sm sm:text-base transition-all duration-200 shadow-[0_4px_20px_rgba(0,201,149,0.25)] flex items-center justify-center"
        >
          Register an Account →
        </Link>
      </AuthCard>
    )
  }

  return (
    <AuthCard
      hideLogo={true}
      cardClassName="w-full max-w-[480px] p-6 sm:p-8 bg-[rgba(14,18,21,0.82)] backdrop-blur-md rounded-[24px] border border-[#252D32]/70 shadow-[0_20px_50px_rgba(0,0,0,0.85),0_0_40px_rgba(0,201,149,0.06)]"
    >
      {/* Top Branding Lockup */}
      <div className="flex flex-col items-center mb-5">
        <Link to="/" className="group inline-flex items-center gap-3 mb-3" aria-label="TradeDrift home">
          <div className="w-11 h-11 rounded-xl bg-[#141D22] border border-[#23313A] flex items-center justify-center shadow-inner group-hover:border-[#00C995]/50 transition-colors">
            <TradeDriftSymbol size={26} />
          </div>
          <div className="flex flex-col text-left">
            <span className="text-xl font-bold tracking-tight text-white leading-tight">TradeDrift</span>
            <span className="text-[10px] font-semibold text-[#00C995] tracking-[0.14em] uppercase mt-0.5">MARKETS · EXECUTION · INTELLIGENCE</span>
          </div>
        </Link>
        <h1 className="text-xl sm:text-2xl font-bold text-white tracking-tight mt-1 text-center">
          Verify your email
        </h1>
        <p className="text-xs sm:text-sm text-[#94A3B8] text-center mt-1">
          We sent a 6-digit code to <span className="text-[#00C995] font-semibold">{email}</span>
        </p>
      </div>

      <form onSubmit={handleSubmit} className="space-y-4">
        {error && (
          <div className="px-3.5 py-2 rounded-xl bg-red-500/10 border border-red-500/30 text-red-400 text-xs leading-relaxed">
            {error}
          </div>
        )}

        {success && (
          <div className="px-3.5 py-2 rounded-xl bg-[#00C995]/10 border border-[#00C995]/30 text-[#00C995] text-xs leading-relaxed">
            {success}
          </div>
        )}

        {/* 6-digit OTP Inputs */}
        <div>
          <label className="block text-[11px] font-semibold tracking-wider uppercase text-[#94A3B8] mb-2 text-center">
            ENTER 6-DIGIT CODE
          </label>
          <div className="flex justify-between gap-2" onPaste={handlePaste}>
            {otp.map((digit, i) => (
              <input
                key={i}
                ref={(el) => { inputRefs.current[i] = el }}
                type="text"
                inputMode="numeric"
                maxLength={1}
                value={digit}
                onChange={(e) => handleOtpChange(i, e.target.value)}
                onKeyDown={(e) => handleKeyDown(i, e)}
                aria-label={`Digit ${i + 1}`}
                className="w-11 sm:w-12 h-12 bg-[#090D10]/95 border border-[#252D32]/80 rounded-xl text-center text-lg font-bold text-white focus:outline-none focus:border-[#00C995] focus:ring-1 focus:ring-[#00C995] transition-all"
              />
            ))}
          </div>
        </div>

        <button
          type="submit"
          disabled={loading}
          className="w-full h-11 sm:h-12 bg-[#00C995] hover:bg-[#00b585] disabled:opacity-50 disabled:cursor-not-allowed text-[#050B0B] font-bold rounded-xl text-sm sm:text-base transition-all duration-200 shadow-[0_4px_20px_rgba(0,201,149,0.25)] flex items-center justify-center cursor-pointer active:scale-[0.99] mt-2"
        >
          {loading ? 'Verifying…' : 'Verify Email →'}
        </button>
      </form>

      {/* Resend Action */}
      <div className="mt-5 text-center">
        <button
          type="button"
          onClick={handleResend}
          disabled={cooldown > 0 || resending}
          className="inline-flex items-center gap-1.5 text-xs text-[#94A3B8] hover:text-white disabled:text-slate-600 transition-colors cursor-pointer disabled:cursor-not-allowed"
        >
          <RefreshCw size={13} className={resending ? 'animate-spin' : ''} />
          {resending
            ? 'Sending…'
            : cooldown > 0
            ? `Resend code in ${cooldown}s`
            : 'Resend verification code'}
        </button>
      </div>

      {/* Footer */}
      <div className="mt-6 pt-4 border-t border-[#252D32]/60 text-center space-y-2">
        <Link
          to="/login"
          className="inline-flex items-center gap-1.5 text-xs sm:text-sm font-semibold text-[#00C995] hover:text-[#00e6aa] transition-colors"
        >
          <ArrowLeft size={14} />
          Back to Sign In
        </Link>
        <div className="flex items-center justify-center gap-1.5 text-[11px] text-[#94A3B8]">
          <ShieldCheck size={13} className="text-[#00C995]" />
          <span>Verification link and code valid for 24 hours</span>
        </div>
      </div>
    </AuthCard>
  )
}
