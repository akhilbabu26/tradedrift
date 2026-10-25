import { useState, useRef, useEffect } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { Lock, Eye, EyeOff, ShieldCheck, CheckCircle, AlertCircle, ArrowLeft } from 'lucide-react'
import AuthCard from '../../components/AuthCard'
import { authApi } from '../../api/auth'
import { TradeDriftSymbol } from '../../components/brand/TradeDriftLogo'

export default function ResetPasswordPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const emailParam = searchParams.get('email') || ''

  const [otp, setOtp]                   = useState<string[]>(Array(6).fill(''))
  const [newPassword, setNewPassword]   = useState('')
  const [confirm, setConfirm]           = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [showConfirm, setShowConfirm]   = useState(false)
  const [loading, setLoading]           = useState(false)
  const [error, setError]               = useState('')
  const [done, setDone]                 = useState(false)
  const inputRefs = useRef<(HTMLInputElement | null)[]>([])

  useEffect(() => {
    if (emailParam) {
      inputRefs.current[0]?.focus()
    }
  }, [emailParam])

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
    if (!emailParam) {
      setError('Missing email parameter. Please request a new reset code.')
      return
    }
    const code = otp.join('')
    if (code.length < 6)         { setError('Enter the full 6-digit code'); return }
    if (newPassword !== confirm)  { setError('Passwords do not match'); return }
    if (newPassword.length < 8)   { setError('Password must be at least 8 characters'); return }

    setError('')
    setLoading(true)
    try {
      await authApi.resetPassword({ email: emailParam, code, newPassword })
      setDone(true)
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Failed to reset password. Try again.'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  if (done) {
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
          <div className="w-14 h-14 rounded-2xl bg-[#00C995]/10 border border-[#00C995]/30 flex items-center justify-center text-[#00C995]">
            <CheckCircle size={28} />
          </div>
        </div>
        <div className="text-center mb-6">
          <h1 className="text-xl sm:text-2xl font-bold text-white mb-2">Password reset!</h1>
          <p className="text-xs sm:text-sm text-[#94A3B8] leading-relaxed">
            Your password has been updated successfully. You can now sign in with your new password.
          </p>
        </div>
        <button
          onClick={() => navigate('/login')}
          className="w-full h-11 sm:h-12 bg-[#00C995] hover:bg-[#00b585] text-[#050B0B] font-bold rounded-xl text-sm sm:text-base transition-all duration-200 shadow-[0_4px_20px_rgba(0,201,149,0.25)] flex items-center justify-center cursor-pointer active:scale-[0.99]"
        >
          Sign In Now →
        </button>
      </AuthCard>
    )
  }

  if (!emailParam) {
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
            No email address was provided in the reset link. Please start over from the forgot-password page.
          </p>
        </div>
        <Link
          to="/forgot-password"
          className="w-full h-11 sm:h-12 bg-[#00C995] hover:bg-[#00b585] text-[#050B0B] font-bold rounded-xl text-sm sm:text-base transition-all duration-200 shadow-[0_4px_20px_rgba(0,201,149,0.25)] flex items-center justify-center"
        >
          Request Reset Code →
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
          Reset password
        </h1>
        <p className="text-xs sm:text-sm text-[#94A3B8] text-center mt-1">
          Enter the code sent to <span className="text-[#00C995] font-semibold">{emailParam}</span>
        </p>
      </div>

      <form onSubmit={handleSubmit} className="space-y-4">
        {error && (
          <div className="px-3.5 py-2 rounded-xl bg-red-500/10 border border-red-500/30 text-red-400 text-xs leading-relaxed">
            {error}
          </div>
        )}

        {/* 6-digit OTP Inputs */}
        <div>
          <label className="block text-[11px] font-semibold tracking-wider uppercase text-[#94A3B8] mb-2 text-center">
            6-DIGIT RESET CODE
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

        {/* New Password */}
        <div>
          <label htmlFor="newPassword" className="block text-[11px] font-semibold tracking-wider uppercase text-[#94A3B8] mb-1.5">
            NEW PASSWORD
          </label>
          <div className="relative">
            <Lock size={16} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
            <input
              id="newPassword"
              type={showPassword ? 'text' : 'password'}
              required
              autoComplete="new-password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder="Min. 8 characters"
              className="w-full h-11 sm:h-12 pl-10 pr-10 bg-[#090D10]/95 border border-[#252D32]/80 rounded-xl text-white placeholder-slate-600 text-sm focus:outline-none focus:border-[#00C995] focus:ring-1 focus:ring-[#00C995] transition-all duration-200"
            />
            <button
              type="button"
              aria-label={showPassword ? 'Hide password' : 'Show password'}
              onClick={() => setShowPassword(!showPassword)}
              className="absolute right-3.5 top-1/2 -translate-y-1/2 text-slate-500 hover:text-white transition-colors cursor-pointer"
            >
              {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
            </button>
          </div>
        </div>

        {/* Confirm Password */}
        <div>
          <label htmlFor="confirmPassword" className="block text-[11px] font-semibold tracking-wider uppercase text-[#94A3B8] mb-1.5">
            CONFIRM NEW PASSWORD
          </label>
          <div className="relative">
            <Lock size={16} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
            <input
              id="confirmPassword"
              type={showConfirm ? 'text' : 'password'}
              required
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              placeholder="Re-enter new password"
              className={`w-full h-11 sm:h-12 pl-10 pr-10 bg-[#090D10]/95 border rounded-xl text-white placeholder-slate-600 text-sm focus:outline-none transition-all duration-200 ${
                confirm && confirm !== newPassword
                  ? 'border-red-500/60 focus:border-red-500'
                  : 'border-[#252D32]/80 focus:border-[#00C995] focus:ring-1 focus:ring-[#00C995]'
              }`}
            />
            <button
              type="button"
              aria-label={showConfirm ? 'Hide password' : 'Show password'}
              onClick={() => setShowConfirm(!showConfirm)}
              className="absolute right-3.5 top-1/2 -translate-y-1/2 text-slate-500 hover:text-white transition-colors cursor-pointer"
            >
              {showConfirm ? <EyeOff size={16} /> : <Eye size={16} />}
            </button>
          </div>
          {confirm && confirm !== newPassword && (
            <p className="text-xs text-red-400 mt-1">Passwords do not match</p>
          )}
        </div>

        <button
          type="submit"
          disabled={loading}
          className="w-full h-11 sm:h-12 bg-[#00C995] hover:bg-[#00b585] disabled:opacity-50 disabled:cursor-not-allowed text-[#050B0B] font-bold rounded-xl text-sm sm:text-base transition-all duration-200 shadow-[0_4px_20px_rgba(0,201,149,0.25)] flex items-center justify-center cursor-pointer active:scale-[0.99] mt-2"
        >
          {loading ? 'Updating password…' : 'Set New Password'}
        </button>
      </form>

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
          <span>Sessions on other devices will be invalidated</span>
        </div>
      </div>
    </AuthCard>
  )
}
