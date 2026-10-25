import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Mail, ArrowLeft, ShieldCheck, CheckCircle } from 'lucide-react'
import AuthCard from '../../components/AuthCard'
import { authApi } from '../../api/auth'
import { TradeDriftSymbol } from '../../components/brand/TradeDriftLogo'

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
      <AuthCard
        hideLogo={true}
        cardClassName="w-full max-w-[460px] p-6 sm:p-8 bg-[rgba(14,18,21,0.82)] backdrop-blur-md rounded-[24px] border border-[#252D32]/70 shadow-[0_20px_50px_rgba(0,0,0,0.85),0_0_40px_rgba(0,201,149,0.06)]"
      >
        {/* Brand Lockup */}
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

        {/* Success Icon */}
        <div className="flex justify-center mb-4">
          <div className="w-14 h-14 rounded-2xl bg-[#00C995]/10 border border-[#00C995]/30 flex items-center justify-center text-[#00C995]">
            <CheckCircle size={28} />
          </div>
        </div>

        <div className="text-center mb-6">
          <h1 className="text-xl sm:text-2xl font-bold text-white mb-2">Check your inbox</h1>
          <p className="text-xs sm:text-sm text-[#94A3B8] leading-relaxed">
            We sent a password reset code to{' '}
            <span className="text-[#00C995] font-semibold">{email}</span>.
            <br />Enter the 6-digit code on the next page to proceed.
          </p>
        </div>

        <button
          onClick={() => navigate(`/reset-password?email=${encodeURIComponent(email)}`)}
          className="w-full h-11 sm:h-12 bg-[#00C995] hover:bg-[#00b585] text-[#050B0B] font-bold rounded-xl text-sm sm:text-base transition-all duration-200 shadow-[0_4px_20px_rgba(0,201,149,0.25)] flex items-center justify-center cursor-pointer active:scale-[0.99]"
        >
          Enter Reset Code →
        </button>

        <div className="mt-4 text-center">
          <button
            onClick={() => setSent(false)}
            className="text-xs sm:text-sm text-[#94A3B8] hover:text-white transition-colors cursor-pointer"
          >
            ← Use a different email
          </button>
        </div>
      </AuthCard>
    )
  }

  return (
    <AuthCard
      hideLogo={true}
      cardClassName="w-full max-w-[460px] p-6 sm:p-8 bg-[rgba(14,18,21,0.82)] backdrop-blur-md rounded-[24px] border border-[#252D32]/70 shadow-[0_20px_50px_rgba(0,0,0,0.85),0_0_40px_rgba(0,201,149,0.06)]"
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
        <h1 className="text-xl sm:text-2xl font-bold text-white tracking-tight mt-2 text-center">
          Forgot your password?
        </h1>
        <p className="text-xs sm:text-sm text-[#94A3B8] text-center mt-1 max-w-[340px]">
          Enter your registered email and we'll send you a 6-digit code to reset your credentials.
        </p>
      </div>

      {/* Form */}
      <form onSubmit={handleSubmit} className="space-y-4">
        {error && (
          <div className="px-3.5 py-2 rounded-xl bg-red-500/10 border border-red-500/30 text-red-400 text-xs leading-relaxed">
            {error}
          </div>
        )}

        {/* Email */}
        <div>
          <label htmlFor="email" className="block text-[11px] font-semibold tracking-wider uppercase text-[#94A3B8] mb-1.5">
            EMAIL ADDRESS
          </label>
          <div className="relative">
            <Mail size={16} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
            <input
              id="email"
              type="email"
              required
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="trader@example.com"
              className="w-full h-11 sm:h-12 pl-10 pr-4 bg-[#090D10]/95 border border-[#252D32]/80 rounded-xl text-white placeholder-slate-600 text-sm focus:outline-none focus:border-[#00C995] focus:ring-1 focus:ring-[#00C995] transition-all duration-200"
            />
          </div>
        </div>

        <button
          type="submit"
          disabled={loading}
          className="w-full h-11 sm:h-12 bg-[#00C995] hover:bg-[#00b585] disabled:opacity-50 disabled:cursor-not-allowed text-[#050B0B] font-bold rounded-xl text-sm sm:text-base transition-all duration-200 shadow-[0_4px_20px_rgba(0,201,149,0.25)] flex items-center justify-center cursor-pointer active:scale-[0.99] mt-2"
        >
          {loading ? 'Sending code…' : 'Send Reset Code'}
        </button>
      </form>

      {/* Footer */}
      <div className="mt-6 pt-4 border-t border-[#252D32]/60 text-center space-y-2.5">
        <div>
          <Link
            to="/login"
            className="inline-flex items-center gap-1.5 text-xs sm:text-sm font-semibold text-[#00C995] hover:text-[#00e6aa] transition-colors"
          >
            <ArrowLeft size={14} />
            Back to Sign In
          </Link>
        </div>
        <div className="flex items-center justify-center gap-1.5 text-[11px] text-[#94A3B8]">
          <ShieldCheck size={13} className="text-[#00C995]" />
          <span>Reset code expires in 15 minutes</span>
        </div>
      </div>
    </AuthCard>
  )
}
