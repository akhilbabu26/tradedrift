import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Mail, ArrowLeft, ShieldCheck } from 'lucide-react'
import AuthCard from '../../components/AuthCard'
import { authApi } from '../../api/auth'

export default function ForgotPasswordPage() {
  const navigate = useNavigate()
  const [email, setEmail]     = useState('')
  const [loading, setLoading] = useState(false)
  const [sent, setSent]       = useState(false)
  const [error, setError]     = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await authApi.forgotPassword({ email })
      setSent(true)
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Failed to send reset email. Please try again.'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  if (sent) {
    return (
      <AuthCard>
        {/* Success state */}
        <div className="flex justify-center mb-6">
          <div className="w-16 h-16 rounded-2xl bg-brand/10 border border-brand/20 flex items-center justify-center">
            <Mail size={28} className="text-brand" />
          </div>
        </div>
        <div className="text-center mb-8">
          <h1 className="text-2xl font-black text-white mb-2">Check your inbox</h1>
          <p className="text-slate-400 text-sm leading-relaxed">
            We sent a password reset code to{' '}
            <span className="text-brand font-semibold">{email}</span>.
            <br />Enter the code on the next page.
          </p>
        </div>
        <button
          onClick={() => navigate(`/reset-password?email=${encodeURIComponent(email)}`)}
          className="w-full py-3 px-4 bg-brand hover:bg-brand-dark text-black font-bold rounded-lg text-sm transition-all duration-200 glow-green-sm hover:glow-green"
        >
          Enter Reset Code →
        </button>
        <div className="mt-5 text-center">
          <button
            onClick={() => setSent(false)}
            className="text-sm text-slate-500 hover:text-slate-300 transition-colors"
          >
            ← Use a different email
          </button>
        </div>
      </AuthCard>
    )
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
        <h1 className="text-2xl font-black text-white mb-1">Forgot your password?</h1>
        <p className="text-slate-400 text-sm leading-relaxed">
          No worries. Enter your email and we'll send you a reset code.
        </p>
      </div>

      {/* Form */}
      <form onSubmit={handleSubmit} className="space-y-5">
        {error && (
          <div className="px-4 py-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-sm">
            {error}
          </div>
        )}

        {/* Email */}
        <div>
          <label htmlFor="email" className="block text-[11px] font-semibold tracking-widest uppercase text-slate-400 mb-1.5">
            Email Address
          </label>
          <div className="relative">
            <Mail size={15} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              id="email"
              type="email"
              required
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="trader@example.com"
              className="w-full pl-10 pr-4 py-3 bg-[#0a0b0e]/80 border border-[#1f2229] rounded-lg text-white placeholder-slate-600 text-sm focus:outline-none focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)] transition-all duration-200"
            />
          </div>
        </div>

        <button
          type="submit"
          disabled={loading}
          className="w-full py-3 px-4 bg-brand hover:bg-brand-dark disabled:opacity-50 disabled:cursor-not-allowed text-black font-bold rounded-lg text-sm transition-all duration-200 glow-green-sm hover:glow-green"
        >
          {loading ? 'Sending code…' : 'Send Reset Code'}
        </button>
      </form>

      {/* Footer */}
      <div className="mt-7 text-center space-y-3">
        <Link
          to="/login"
          className="inline-flex items-center gap-1.5 text-sm text-slate-400 hover:text-white transition-colors"
        >
          <ArrowLeft size={14} />
          Back to Login
        </Link>
        <div className="flex items-center justify-center gap-1.5 text-xs text-slate-600">
          <ShieldCheck size={12} />
          <span>Reset code expires in 15 minutes</span>
        </div>
      </div>
    </AuthCard>
  )
}
