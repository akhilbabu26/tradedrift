import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Eye, EyeOff, Mail, Lock, ShieldCheck } from 'lucide-react'
import WebGLBackground from '../../components/WebGLBackground'
import { useAuthStore } from '../../store/authStore'
import { authApi } from '../../api/auth'

const FEATURE_PILLS = [
  { label: '10,000 USDT', pulse: true },
  { label: 'Real Matching', pulse: false },
  { label: 'Live Tracking', pulse: false },
]

export default function LoginPage() {
  const navigate = useNavigate()
  const { setTokens, setUser } = useAuthStore()

  const [identifier, setIdentifier]     = useState('')
  const [password, setPassword]         = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [loading, setLoading]           = useState(false)
  const [error, setError]               = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const { data } = await authApi.login({ identifier, password })
      setTokens(data.accessToken, data.refreshToken)
      if (data.user) setUser(data.user)
      navigate('/dashboard')
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Invalid email or password'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="relative min-h-screen w-screen overflow-auto flex items-center justify-center bg-base py-6 px-4">

      {/* WebGL animated background */}
      <div className="fixed inset-0 z-0">
        <WebGLBackground />
      </div>

      {/* Glass login card */}
      <div className="relative z-10 w-full max-w-[500px] bg-[#111318]/80 backdrop-blur-xl rounded-2xl shadow-2xl border border-[#1f2229] flex flex-col p-8 md:p-10">

        {/* Logo + heading */}
        <div className="mb-8 text-center flex flex-col items-center">
          <Link to="/">
            <img src="/logo.png" alt="TradeDrift" className="h-12 w-auto mb-6 object-contain" />
          </Link>
          <h1 className="text-2xl font-black text-white mb-1">
            Welcome back, Trader <span className="inline-block hover:animate-bounce">👋</span>
          </h1>
          <p className="text-slate-400 text-sm">Sign in to your simulation account</p>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="space-y-5">
          {/* Error banner */}
          {error && (
            <div className="px-4 py-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-sm">
              {error}
            </div>
          )}

          {/* Email */}
          <div>
            <label htmlFor="email" className="block text-[11px] font-semibold tracking-widest uppercase text-slate-400 mb-2">
              Email Address
            </label>
            <div className="relative">
              <Mail size={15} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
              <input
                id="email"
                type="email"
                required
                autoComplete="email"
                value={identifier}
                onChange={(e) => setIdentifier(e.target.value)}
                placeholder="email or username"
                className="w-full pl-10 pr-4 py-3 bg-[#0a0b0e]/80 border border-[#1f2229] rounded-lg text-white placeholder-slate-600 text-sm focus:outline-none focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)] transition-all duration-200"
              />
            </div>
          </div>

          {/* Password */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <label htmlFor="password" className="text-[11px] font-semibold tracking-widest uppercase text-slate-400">
                Password
              </label>
              <Link to="/forgot-password" className="text-sm font-medium text-brand hover:text-brand-light transition-colors">
                Forgot password?
              </Link>
            </div>
            <div className="relative">
              <Lock size={15} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
              <input
                id="password"
                type={showPassword ? 'text' : 'password'}
                required
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                className="w-full pl-10 pr-10 py-3 bg-[#0a0b0e]/80 border border-[#1f2229] rounded-lg text-white placeholder-slate-600 text-sm focus:outline-none focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)] transition-all duration-200"
              />
              <button
                type="button"
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-white transition-colors"
              >
                {showPassword ? <EyeOff size={15} /> : <Eye size={15} />}
              </button>
            </div>
          </div>

          {/* Submit */}
          <button
            type="submit"
            disabled={loading}
            className="w-full py-3 px-4 bg-brand hover:bg-brand-dark disabled:opacity-50 disabled:cursor-not-allowed text-black font-bold rounded-lg text-sm transition-all duration-200 glow-green-sm hover:glow-green"
          >
            {loading ? 'Signing in…' : 'Sign In'}
          </button>
        </form>

        {/* Divider */}
        <div className="my-6 relative">
          <div className="absolute inset-0 flex items-center">
            <div className="w-full border-t border-[#1f2229]" />
          </div>
          <div className="relative flex justify-center">
            <span className="px-3 bg-[#111318] text-slate-500 text-[11px] uppercase tracking-widest font-semibold rounded">
              Or continue with
            </span>
          </div>
        </div>

        {/* Google */}
        <button
          type="button"
          className="w-full flex justify-center items-center gap-3 py-3 px-4 border border-[#1f2229] rounded-lg bg-[#0a0b0e]/50 text-sm font-medium text-white hover:bg-[#1f2229] transition-colors duration-200"
        >
          <svg className="h-4 w-4" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
            <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4" />
            <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853" />
            <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05" />
            <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335" />
          </svg>
          Google
        </button>

        {/* Feature pills */}
        <div className="mt-7 pt-6 border-t border-[#1f2229]">
          <p className="text-center text-xs text-slate-400 mb-4 font-medium">
            Practice real exchange mechanics —{' '}
            <span className="text-brand">zero financial risk</span>
          </p>
          <div className="flex flex-wrap justify-center gap-2">
            {FEATURE_PILLS.map(({ label, pulse }) => (
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
            Don't have an account?{' '}
            <Link to="/register" className="font-semibold text-brand hover:text-brand-light transition-colors">
              Register →
            </Link>
          </p>
          <div className="flex items-center justify-center gap-1.5 text-xs text-slate-600">
            <ShieldCheck size={12} />
            <span>Secured with JWT + OTP verification</span>
          </div>
        </div>
      </div>
    </div>
  )
}
