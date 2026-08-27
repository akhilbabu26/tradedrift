import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { User, Mail, Lock, Eye, EyeOff, ShieldCheck } from 'lucide-react'
import AuthCard from '../../components/AuthCard'
import { authApi } from '../../api/auth'

export default function RegisterPage() {
  const navigate = useNavigate()

  const [username, setUsername]         = useState('')
  const [email, setEmail]               = useState('')
  const [password, setPassword]         = useState('')
  const [confirm, setConfirm]           = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [loading, setLoading]           = useState(false)
  const [error, setError]               = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    if (password !== confirm) {
      setError('Passwords do not match')
      return
    }
    if (password.length < 8) {
      setError('Password must be at least 8 characters')
      return
    }
    setLoading(true)
    try {
      await authApi.register({ email, username, password })
      navigate(`/verify?email=${encodeURIComponent(email)}`)
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Registration failed. Please try again.'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  return (
    <AuthCard>
      {/* Heading */}
      <div className="text-center mb-7">
        <h1 className="text-2xl font-black text-white mb-1">Create your account</h1>
        <p className="text-slate-400 text-sm">Start trading with 10,000 USDT — zero risk</p>
      </div>

      {/* Form */}
      <form onSubmit={handleSubmit} className="space-y-4">
        {error && (
          <div className="px-4 py-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-sm">
            {error}
          </div>
        )}

        {/* Username */}
        <div>
          <label htmlFor="username" className="block text-[11px] font-semibold tracking-widest uppercase text-slate-400 mb-1.5">
            Username
          </label>
          <div className="relative">
            <User size={15} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              id="username"
              type="text"
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="trader_one"
              className="w-full pl-10 pr-4 py-3 bg-[#0a0b0e]/80 border border-[#1f2229] rounded-lg text-white placeholder-slate-600 text-sm focus:outline-none focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)] transition-all duration-200"
            />
          </div>
        </div>

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

        {/* Password */}
        <div>
          <label htmlFor="password" className="block text-[11px] font-semibold tracking-widest uppercase text-slate-400 mb-1.5">
            Password
          </label>
          <div className="relative">
            <Lock size={15} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              id="password"
              type={showPassword ? 'text' : 'password'}
              required
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Min. 8 characters"
              className="w-full pl-10 pr-10 py-3 bg-[#0a0b0e]/80 border border-[#1f2229] rounded-lg text-white placeholder-slate-600 text-sm focus:outline-none focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)] transition-all duration-200"
            />
            <button
              type="button"
              aria-label={showPassword ? 'Hide password' : 'Show password'}
              onClick={() => setShowPassword(!showPassword)}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-white transition-colors"
            >
              {showPassword ? <EyeOff size={15} /> : <Eye size={15} />}
            </button>
          </div>
        </div>

        {/* Confirm Password */}
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
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              placeholder="Re-enter password"
              className={`w-full pl-10 pr-4 py-3 bg-[#0a0b0e]/80 border rounded-lg text-white placeholder-slate-600 text-sm focus:outline-none transition-all duration-200 ${
                confirm && confirm !== password
                  ? 'border-red-500/60 focus:border-red-500'
                  : 'border-[#1f2229] focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)]'
              }`}
            />
          </div>
          {confirm && confirm !== password && (
            <p className="text-xs text-red-400 mt-1">Passwords do not match</p>
          )}
        </div>

        {/* Submit */}
        <button
          type="submit"
          disabled={loading}
          className="w-full py-3 px-4 mt-2 bg-brand hover:bg-brand-dark disabled:opacity-50 disabled:cursor-not-allowed text-black font-bold rounded-lg text-sm transition-all duration-200 glow-green-sm hover:glow-green"
        >
          {loading ? 'Creating account…' : 'Create Account'}
        </button>
      </form>

      {/* Feature pills */}
      <div className="mt-7 pt-5 border-t border-[#1f2229]">
        <div className="flex flex-wrap justify-center gap-2">
          {[
            { label: '10,000 USDT Free', pulse: true },
            { label: 'Real Order Book',  pulse: false },
            { label: 'Live PnL',         pulse: false },
          ].map(({ label, pulse }) => (
            <div key={label} className="bg-[#0a0b0e]/50 border border-[#1f2229] rounded-full px-3 py-1.5 flex items-center gap-2">
              <span className={`w-1.5 h-1.5 rounded-full bg-brand ${pulse ? 'animate-pulse' : ''}`} />
              <span className="text-xs text-slate-200">{label}</span>
            </div>
          ))}
        </div>
      </div>

      {/* Footer */}
      <div className="mt-6 text-center space-y-3">
        <p className="text-sm text-slate-400">
          Already have an account?{' '}
          <Link to="/login" className="font-semibold text-brand hover:text-brand-light transition-colors">
            Sign In →
          </Link>
        </p>
        <div className="flex items-center justify-center gap-1.5 text-xs text-slate-600">
          <ShieldCheck size={12} />
          <span>Email verification required after registration</span>
        </div>
      </div>
    </AuthCard>
  )
}
