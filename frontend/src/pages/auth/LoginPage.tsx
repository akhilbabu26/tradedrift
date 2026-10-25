import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { Eye, EyeOff, Mail, Lock, ShieldCheck, Coins, BarChart3, PieChart, ChevronRight } from 'lucide-react'
import toast from 'react-hot-toast'
import AuthCard from '../../components/AuthCard'
import { useAuthStore } from '../../store/authStore'
import { authApi } from '../../api/auth'
import GoogleAuthButton from '../../components/auth/GoogleAuthButton'
import LinkGoogleAccountModal from '../../components/auth/LinkGoogleAccountModal'
import { TradeDriftSymbol } from '../../components/brand/TradeDriftLogo'
import { getSafeReturnUrl } from '../../utils/url'

export default function LoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const returnUrl = searchParams.get('returnUrl')
  const targetDestination = getSafeReturnUrl(returnUrl, '/dashboard')
  const { setTokens, setUser } = useAuthStore()

  const [identifier, setIdentifier] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  // Google OAuth link flow states
  const [linkModalOpen, setLinkModalOpen] = useState(false)
  const [pendingLinkEmail, setPendingLinkEmail] = useState('')
  const [pendingLinkToken, setPendingLinkToken] = useState('')
  const [googleLoading, setGoogleLoading] = useState(false)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const { data } = await authApi.login({ identifier, password })
      setTokens(data.accessToken, data.refreshToken)
      if (data.user) setUser(data.user)
      navigate(targetDestination)
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Invalid email or password'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  const handleGoogleSuccess = async (idToken: string) => {
    setError('')
    setGoogleLoading(true)
    try {
      const { data } = await authApi.googleLogin(idToken)
      if (data.status === 'SUCCESS') {
        setTokens(data.accessToken, data.refreshToken)
        if (data.user) setUser(data.user)
        toast.success('Signed in with Google successfully!')
        navigate(targetDestination)
      } else if (data.status === 'LINK_REQUIRED') {
        setPendingLinkEmail(data.email)
        setPendingLinkToken(data.linkToken)
        setLinkModalOpen(true)
      }
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Google sign-in failed'
      setError(msg)
    } finally {
      setGoogleLoading(false)
    }
  }

  return (
    <AuthCard
      hideLogo={true}
      cardClassName="w-full md:w-[70vw] md:max-w-[1080px] !p-0 bg-[rgba(14,18,21,0.78)] backdrop-blur-md rounded-[24px] border border-[#252D32]/70 shadow-[0_20px_50px_rgba(0,0,0,0.85),0_0_40px_rgba(0,201,149,0.06)] overflow-hidden"
    >
      <div className="flex flex-col md:flex-row w-full items-stretch">
        {/* Left Branding Panel (~42% width) */}
        <div className="w-full md:w-[42%] flex-shrink-0 p-6 sm:p-7 lg:p-8 flex flex-col justify-between relative bg-[rgba(16,22,25,0.68)] md:border-r border-[#252D32]/70 overflow-hidden">
          {/* Decorative Candlestick Trend Illustration (Right side of left panel) */}
          <svg
            className="absolute right-0 top-1/2 -translate-y-1/2 w-44 h-72 pointer-events-none opacity-[0.14] select-none"
            viewBox="0 0 160 280"
            fill="none"
            aria-hidden="true"
          >
            {/* Candle 1 */}
            <line x1="20" y1="210" x2="20" y2="260" stroke="#00C995" strokeWidth="1.5" />
            <rect x="15" y="220" width="10" height="28" rx="1.5" fill="#00C995" />

            {/* Candle 2 */}
            <line x1="48" y1="170" x2="48" y2="235" stroke="#00C995" strokeWidth="1.5" />
            <rect x="43" y="182" width="10" height="38" rx="1.5" fill="#00C995" />

            {/* Candle 3 */}
            <line x1="76" y1="130" x2="76" y2="205" stroke="#00C995" strokeWidth="1.5" />
            <rect x="71" y="145" width="10" height="42" rx="1.5" fill="#00C995" />

            {/* Candle 4 */}
            <line x1="104" y1="90" x2="104" y2="165" stroke="#00C995" strokeWidth="1.5" />
            <rect x="99" y="105" width="10" height="45" rx="1.5" fill="#00C995" />

            {/* Candle 5 */}
            <line x1="132" y1="50" x2="132" y2="125" stroke="#00C995" strokeWidth="1.5" />
            <rect x="127" y="65" width="10" height="45" rx="1.5" fill="#00C995" />

            {/* Candle 6 */}
            <line x1="156" y1="20" x2="156" y2="90" stroke="#00C995" strokeWidth="1.5" />
            <rect x="151" y="32" width="10" height="40" rx="1.5" fill="#00C995" />
          </svg>

          {/* Decorative Teal Dotted-Wave Pattern (Bottom right corner) */}
          <svg
            className="absolute -right-4 -bottom-6 w-56 h-40 pointer-events-none opacity-20 select-none"
            viewBox="0 0 240 180"
            fill="none"
            aria-hidden="true"
          >
            <defs>
              <linearGradient id="wave-gradient-compact" x1="0" y1="0" x2="1" y2="1">
                <stop offset="0%" stopColor="#00C995" stopOpacity="0.8" />
                <stop offset="100%" stopColor="#00E5A3" stopOpacity="0.1" />
              </linearGradient>
            </defs>
            {/* Curved dotted particle wave rows */}
            {[0, 10, 20, 30, 40, 50, 60].map((offsetY, i) => (
              <g key={i} opacity={1 - i * 0.12}>
                {[0, 15, 30, 45, 60, 75, 90, 105, 120, 135, 150, 165, 180, 195, 210, 225].map((x, j) => {
                  const y =
                    70 +
                    offsetY +
                    Math.sin((x / 240) * Math.PI * 2.2 + i * 0.4) * 22 -
                    Math.cos((x / 240) * Math.PI * 1.5) * 14
                  return (
                    <circle
                      key={j}
                      cx={x}
                      cy={y}
                      r={j % 3 === 0 ? 1.5 : 1.1}
                      fill="url(#wave-gradient-compact)"
                    />
                  )
                })}
              </g>
            ))}
          </svg>

          <div className="relative z-10">
            {/* Top Branding Lockup */}
            <Link to="/" className="group inline-flex items-center gap-3 mb-4" aria-label="TradeDrift home">
              <div className="w-11 h-11 rounded-xl bg-[#141D22] border border-[#23313A] flex items-center justify-center flex-shrink-0 shadow-inner group-hover:border-[#00C995]/50 transition-colors">
                <TradeDriftSymbol size={26} />
              </div>
              <div className="flex flex-col">
                <span className="text-xl font-bold tracking-tight text-white leading-tight">
                  TradeDrift
                </span>
                <span className="text-[10px] font-semibold text-[#00C995] tracking-[0.14em] uppercase mt-0.5">
                  MARKETS · EXECUTION · INTELLIGENCE
                </span>
              </div>
            </Link>

            {/* Exact supporting text (No welcome heading) */}
            <p className="text-xs sm:text-sm text-[#94A3B8] leading-relaxed max-w-[360px]">
              Sign in to your TradeDrift simulation account. Practice real exchange mechanics with zero financial risk.
            </p>

            {/* Three concise benefit rows with compact vertical spacing */}
            <div className="space-y-2.5 sm:space-y-3 my-4 sm:my-5">
              {/* Row 1: Capital */}
              <div className="flex items-center gap-3">
                <div className="w-9 h-9 sm:w-10 sm:h-10 rounded-xl bg-[#121A1E]/90 border border-[#1F2E36]/90 flex items-center justify-center flex-shrink-0 text-[#00C995] shadow-sm">
                  <Coins size={18} className="stroke-[1.75]" />
                </div>
                <div className="flex flex-col">
                  <span className="text-xs sm:text-sm font-semibold text-white">
                    10,000 USDT Free Simulator Capital
                  </span>
                  <span className="text-[11px] sm:text-xs text-[#94A3B8] mt-0.5">
                    Start trading instantly
                  </span>
                </div>
              </div>

              {/* Row 2: Real Order Matching Engine */}
              <div className="flex items-center gap-3">
                <div className="w-9 h-9 sm:w-10 sm:h-10 rounded-xl bg-[#121A1E]/90 border border-[#1F2E36]/90 flex items-center justify-center flex-shrink-0 text-[#00C995] shadow-sm">
                  <BarChart3 size={18} className="stroke-[1.75]" />
                </div>
                <div className="flex flex-col">
                  <span className="text-xs sm:text-sm font-semibold text-white">
                    Real Order Matching Engine
                  </span>
                  <span className="text-[11px] sm:text-xs text-[#94A3B8] mt-0.5">
                    Experience true market mechanics
                  </span>
                </div>
              </div>

              {/* Row 3: Live Portfolio & PnL Tracking */}
              <div className="flex items-center gap-3">
                <div className="w-9 h-9 sm:w-10 sm:h-10 rounded-xl bg-[#121A1E]/90 border border-[#1F2E36]/90 flex items-center justify-center flex-shrink-0 text-[#00C995] shadow-sm">
                  <PieChart size={18} className="stroke-[1.75]" />
                </div>
                <div className="flex flex-col">
                  <span className="text-xs sm:text-sm font-semibold text-white">
                    Live Portfolio & PnL Tracking
                  </span>
                  <span className="text-[11px] sm:text-xs text-[#94A3B8] mt-0.5">
                    Track your performance in real time
                  </span>
                </div>
              </div>
            </div>
          </div>

          {/* Bottom Security Note */}
          <div className="pt-3 border-t border-[#252D32]/70 flex items-center gap-2 text-xs text-[#94A3B8] relative z-10">
            <ShieldCheck size={15} className="text-[#00C995] flex-shrink-0" />
            <span>Secured with JWT + OTP Verification</span>
          </div>
        </div>

        {/* Right Authentication Panel (~58% width) */}
        <div className="w-full md:w-[58%] flex-1 p-6 sm:p-7 lg:p-8 flex flex-col justify-center bg-[rgba(14,18,21,0.72)]">
          {/* Header */}
          <div className="flex items-start justify-between gap-4 mb-4 sm:mb-5">
            <div>
              <h1 className="text-xl sm:text-2xl font-bold text-white tracking-tight">
                Sign in
              </h1>
              <p className="text-xs sm:text-sm text-[#94A3B8] mt-0.5">
                Enter your credentials to access your TradeDrift account
              </p>
            </div>
            <div className="text-xs sm:text-sm text-[#94A3B8] text-right flex-shrink-0 pt-0.5">
              <span>Need an account?</span>{' '}
              <Link
                to={returnUrl ? `/register?returnUrl=${encodeURIComponent(targetDestination)}` : '/register'}
                className="font-semibold text-[#00C995] hover:text-[#00e6aa] transition-colors ml-1 inline-flex items-center gap-0.5"
              >
                Register
                <ChevronRight size={13} className="inline stroke-[2.5]" />
              </Link>
            </div>
          </div>

          {/* Form */}
          <form onSubmit={handleSubmit} className="space-y-3 sm:space-y-3.5">
            {/* Error Banner */}
            {error && (
              <div className="px-3.5 py-2 rounded-xl bg-red-500/10 border border-red-500/30 text-red-400 text-xs leading-relaxed">
                {error}
              </div>
            )}

            {/* Email or Username */}
            <div>
              <label
                htmlFor="email"
                className="block text-[11px] font-semibold tracking-wider uppercase text-[#94A3B8] mb-1.5"
              >
                EMAIL OR USERNAME
              </label>
              <div className="relative">
                <Mail
                  size={16}
                  className="absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none"
                />
                <input
                  id="email"
                  type="text"
                  required
                  autoComplete="username"
                  value={identifier}
                  onChange={(e) => setIdentifier(e.target.value)}
                  placeholder="email or username"
                  className="w-full h-11 sm:h-12 pl-10 pr-4 bg-[#090D10]/95 border border-[#252D32]/80 rounded-xl text-white placeholder-slate-600 text-sm focus:outline-none focus:border-[#00C995] focus:ring-1 focus:ring-[#00C995] transition-all duration-200"
                />
              </div>
            </div>

            {/* Password */}
            <div>
              <div className="flex items-center justify-between mb-1.5">
                <label
                  htmlFor="password"
                  className="text-[11px] font-semibold tracking-wider uppercase text-[#94A3B8]"
                >
                  PASSWORD
                </label>
                <Link
                  to="/forgot-password"
                  className="text-xs font-medium text-[#00C995] hover:text-[#00e6aa] transition-colors"
                >
                  Forgot password?
                </Link>
              </div>
              <div className="relative">
                <Lock
                  size={16}
                  className="absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none"
                />
                <input
                  id="password"
                  type={showPassword ? 'text' : 'password'}
                  required
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="••••••••"
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

            {/* Primary Submit Button */}
            <button
              type="submit"
              disabled={loading}
              className="w-full h-11 sm:h-12 bg-[#00C995] hover:bg-[#00b585] disabled:opacity-50 disabled:cursor-not-allowed text-[#050B0B] font-bold rounded-xl text-sm sm:text-base transition-all duration-200 shadow-[0_4px_20px_rgba(0,201,149,0.25)] flex items-center justify-center cursor-pointer active:scale-[0.99]"
            >
              {loading ? 'Signing in…' : 'Sign In'}
            </button>
          </form>

          {/* Divider */}
          <div className="my-3 sm:my-3.5 relative">
            <div className="absolute inset-0 flex items-center">
              <div className="w-full border-t border-[#252D32]/60" />
            </div>
            <div className="relative flex justify-center">
              <span className="px-3 bg-[rgba(14,18,21,0.92)] text-[#64748B] text-[10px] sm:text-[11px] uppercase tracking-wider font-semibold rounded">
                Or continue with
              </span>
            </div>
          </div>

          {/* Google Sign-in Section */}
          <div>
            <GoogleAuthButton
              onSuccess={handleGoogleSuccess}
              onError={(msg) => setError(msg)}
              disabled={loading || googleLoading}
            />
          </div>
        </div>
      </div>

      <LinkGoogleAccountModal
        isOpen={linkModalOpen}
        onClose={() => setLinkModalOpen(false)}
        email={pendingLinkEmail}
        linkToken={pendingLinkToken}
        onSuccess={() => {
          setLinkModalOpen(false)
          navigate(targetDestination)
        }}
      />
    </AuthCard>
  )
}
