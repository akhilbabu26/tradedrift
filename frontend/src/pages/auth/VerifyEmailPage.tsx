import { useState, useRef, useEffect } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { Mail, RefreshCw, ShieldCheck } from 'lucide-react'
import AuthCard from '../../components/AuthCard'
import { useAuthStore } from '../../store/authStore'
import { authApi } from '../../api/auth'

export default function VerifyEmailPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const email = searchParams.get('email') || ''
  const { setTokens, setUser } = useAuthStore()

  const [otp, setOtp]               = useState<string[]>(Array(6).fill(''))
  const [loading, setLoading]       = useState(false)
  const [resending, setResending]   = useState(false)
  const [error, setError]           = useState('')
  const [success, setSuccess]       = useState('')
  const inputRefs = useRef<(HTMLInputElement | null)[]>([])

  // Auto-focus first input on mount
  useEffect(() => { inputRefs.current[0]?.focus() }, [])

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
      navigate('/dashboard')
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
    if (!email) return
    setResending(true)
    setError('')
    try {
      await authApi.resendVerification({ email })
      setSuccess('A new code has been sent to your email.')
      setTimeout(() => setSuccess(''), 4000)
    } catch {
      setError('Failed to resend code. Please try again.')
    } finally {
      setResending(false)
    }
  }

  return (
    <AuthCard>
      {/* Icon */}
      <div className="flex justify-center mb-6">
        <div className="w-16 h-16 rounded-2xl bg-brand/10 border border-brand/20 flex items-center justify-center">
          <Mail size={28} className="text-brand" />
        </div>
      </div>

      {/* Heading */}
      <div className="text-center mb-7">
        <h1 className="text-2xl font-black text-white mb-1">Check your email</h1>
        <p className="text-slate-400 text-sm">
          We sent a 6-digit code to{' '}
          <span className="text-brand font-semibold">{email || 'your email'}</span>
        </p>
      </div>

      {/* Form */}
      <form onSubmit={handleSubmit} className="space-y-6">
        {error && (
          <div className="px-4 py-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-sm">
            {error}
          </div>
        )}
        {success && (
          <div className="px-4 py-3 rounded-lg bg-brand/10 border border-brand/30 text-brand text-sm">
            {success}
          </div>
        )}

        {/* OTP boxes */}
        <div>
          <label className="block text-[11px] font-semibold tracking-widest uppercase text-slate-400 mb-3 text-center">
            Verification Code
          </label>
          <div className="flex justify-center gap-3" onPaste={handlePaste}>
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
                className="w-12 h-14 text-center text-xl font-bold bg-[#0a0b0e]/80 border border-[#1f2229] rounded-xl text-white focus:outline-none focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)] transition-all duration-200 caret-brand"
              />
            ))}
          </div>
        </div>

        {/* Verify button */}
        <button
          type="submit"
          disabled={loading || otp.join('').length < 6}
          className="w-full py-3 px-4 bg-brand hover:bg-brand-dark disabled:opacity-50 disabled:cursor-not-allowed text-black font-bold rounded-lg text-sm transition-all duration-200 glow-green-sm hover:glow-green"
        >
          {loading ? 'Verifying…' : 'Verify Email'}
        </button>
      </form>

      {/* Footer */}
      <div className="mt-7 text-center space-y-3">
        <p className="text-sm text-slate-400">
          Didn't receive the code?{' '}
          <button
            onClick={handleResend}
            disabled={resending}
            className="font-semibold text-brand hover:text-brand-light transition-colors inline-flex items-center gap-1 disabled:opacity-50"
          >
            <RefreshCw size={13} className={resending ? 'animate-spin' : ''} />
            Resend
          </button>
        </p>
        <p className="text-sm text-slate-400">
          <Link to="/login" className="text-slate-500 hover:text-slate-300 transition-colors">
            ← Back to Login
          </Link>
        </p>
        <div className="flex items-center justify-center gap-1.5 text-xs text-slate-600">
          <ShieldCheck size={12} />
          <span>Code expires in 10 minutes</span>
        </div>
      </div>
    </AuthCard>
  )
}
