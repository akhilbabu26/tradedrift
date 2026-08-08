import { useState, useRef, useEffect } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { Lock, Eye, EyeOff, ShieldCheck, CheckCircle } from 'lucide-react'
import AuthCard from '../../components/AuthCard'
import { authApi } from '../../api/auth'

export default function ResetPasswordPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const emailParam = searchParams.get('email') || ''

  const [otp, setOtp]                   = useState<string[]>(Array(6).fill(''))
  const [newPassword, setNewPassword]   = useState('')
  const [confirm, setConfirm]           = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [loading, setLoading]           = useState(false)
  const [error, setError]               = useState('')
  const [done, setDone]                 = useState(false)
  const inputRefs = useRef<(HTMLInputElement | null)[]>([])

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
      <AuthCard>
        <div className="flex justify-center mb-6">
          <div className="w-16 h-16 rounded-2xl bg-brand/10 border border-brand/20 flex items-center justify-center">
            <CheckCircle size={28} className="text-brand" />
          </div>
        </div>
        <div className="text-center mb-8">
          <h1 className="text-2xl font-black text-white mb-2">Password reset!</h1>
          <p className="text-slate-400 text-sm">Your password has been updated successfully. You can now sign in with your new password.</p>
        </div>
        <button
          onClick={() => navigate('/login')}
          className="w-full py-3 px-4 bg-brand hover:bg-brand-dark text-black font-bold rounded-lg text-sm transition-all duration-200 glow-green-sm hover:glow-green"
        >
          Sign In Now →
        </button>
      </AuthCard>
    )
  }

  return (
    <AuthCard>
      {/* Heading */}
      <div className="text-center mb-7">
        <h1 className="text-2xl font-black text-white mb-1">Create new password</h1>
        <p className="text-slate-400 text-sm">
          Enter the code sent to{' '}
          <span className="text-brand font-semibold">{emailParam || 'your email'}</span>
        </p>
      </div>

      <form onSubmit={handleSubmit} className="space-y-5">
        {error && (
          <div className="px-4 py-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-sm">
            {error}
          </div>
        )}

        {/* OTP boxes */}
        <div>
          <label className="block text-[11px] font-semibold tracking-widest uppercase text-slate-400 mb-3 text-center">
            Reset Code
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

        {/* New password */}
        <div>
          <label htmlFor="newPassword" className="block text-[11px] font-semibold tracking-widest uppercase text-slate-400 mb-1.5">
            New Password
          </label>
          <div className="relative">
            <Lock size={15} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              id="newPassword"
              type={showPassword ? 'text' : 'password'}
              required
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder="Min. 8 characters"
              className="w-full pl-10 pr-10 py-3 bg-[#0a0b0e]/80 border border-[#1f2229] rounded-lg text-white placeholder-slate-600 text-sm focus:outline-none focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)] transition-all duration-200"
            />
            <button type="button" onClick={() => setShowPassword(!showPassword)}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-white transition-colors">
              {showPassword ? <EyeOff size={15} /> : <Eye size={15} />}
            </button>
          </div>
        </div>

        {/* Confirm password */}
        <div>
          <label htmlFor="confirm" className="block text-[11px] font-semibold tracking-widest uppercase text-slate-400 mb-1.5">
            Confirm Password
          </label>
          <div className="relative">
            <Lock size={15} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              id="confirm"
              type={showPassword ? 'text' : 'password'}
              required
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              placeholder="Re-enter new password"
              className={`w-full pl-10 pr-4 py-3 bg-[#0a0b0e]/80 border rounded-lg text-white placeholder-slate-600 text-sm focus:outline-none transition-all duration-200 ${
                confirm && confirm !== newPassword
                  ? 'border-red-500/60 focus:border-red-500'
                  : 'border-[#1f2229] focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)]'
              }`}
            />
          </div>
          {confirm && confirm !== newPassword && (
            <p className="text-xs text-red-400 mt-1">Passwords do not match</p>
          )}
        </div>

        <button
          type="submit"
          disabled={loading}
          className="w-full py-3 px-4 bg-brand hover:bg-brand-dark disabled:opacity-50 disabled:cursor-not-allowed text-black font-bold rounded-lg text-sm transition-all duration-200 glow-green-sm hover:glow-green"
        >
          {loading ? 'Resetting…' : 'Reset Password'}
        </button>
      </form>

      {/* Footer */}
      <div className="mt-7 text-center space-y-3">
        <Link to="/forgot-password" className="text-sm text-slate-500 hover:text-slate-300 transition-colors">
          ← Request a new code
        </Link>
        <div className="flex items-center justify-center gap-1.5 text-xs text-slate-600">
          <ShieldCheck size={12} />
          <span>Secured with end-to-end encryption</span>
        </div>
      </div>
    </AuthCard>
  )
}
