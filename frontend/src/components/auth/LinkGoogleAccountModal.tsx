import { useState, useEffect } from 'react'
import { X, Lock, Eye, EyeOff, ShieldCheck, AlertCircle } from 'lucide-react'
import toast from 'react-hot-toast'
import { authApi } from '../../api/auth'
import { useAuthStore } from '../../store/authStore'
import { useModalFocus } from '../../hooks/useModalFocus'

export interface LinkGoogleAccountModalProps {
  isOpen: boolean
  onClose: () => void
  email: string
  linkToken: string
  onSuccess: () => void
}

/**
 * Modal dialog for linking an existing password-based TradeDrift account
 * with a Google identity when LINK_REQUIRED is returned by the backend.
 */
export default function LinkGoogleAccountModal({
  isOpen,
  onClose,
  email,
  linkToken,
  onSuccess,
}: LinkGoogleAccountModalProps) {
  const { setTokens, setUser } = useAuthStore()
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const modalRef = useModalFocus<HTMLDivElement>({ isOpen, onClose })

  useEffect(() => {
    if (isOpen) {
      setPassword('')
      setShowPassword(false)
      setError('')
      setLoading(false)
    }
  }, [isOpen])

  if (!isOpen) return null

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!password) {
      setError('Please enter your TradeDrift password.')
      return
    }

    setError('')
    setLoading(true)

    try {
      const { data } = await authApi.linkGoogle({
        email,
        password,
        linkToken,
      })

      setTokens(data.accessToken, data.refreshToken)
      if (data.user) setUser(data.user)
      toast.success('Google account linked successfully! Welcome back.')
      onSuccess()
    } catch (err: unknown) {
      const errRes = (err as { response?: { status?: number; data?: { message?: string } } })?.response
      const status = errRes?.status
      const msg = errRes?.data?.message || 'Failed to link account. Please verify your password.'

      if (status === 409 || msg.toLowerCase().includes('already linked')) {
        toast.success('Account is already linked! Please sign in with Google directly.')
        onClose()
      } else {
        setError(msg)
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/75 backdrop-blur-xs"
      onClick={(e) => {
        if (e.target === e.currentTarget && !loading) {
          onClose()
        }
      }}
    >
      <div
        ref={modalRef}
        className="w-full max-w-md bg-[#111318] border border-[#1e2530] rounded-xl shadow-2xl overflow-hidden"
        role="dialog"
        aria-modal="true"
        aria-labelledby="link-google-modal-title"
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-[#1e2530]">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 flex items-center justify-center">
              <ShieldCheck size={16} />
            </div>
            <div>
              <h2 id="link-google-modal-title" className="text-sm font-semibold text-[#f5f7fa]">
                Link TradeDrift Account
              </h2>
              <p className="text-[11px] text-slate-400">One account, unified balance</p>
            </div>
          </div>
          <button
            type="button"
            disabled={loading}
            onClick={onClose}
            className="p-1 rounded-md text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors cursor-pointer disabled:opacity-40"
            aria-label="Close dialog"
          >
            <X size={15} />
          </button>
        </div>

        {/* Informational Banner */}
        <div className="px-5 pt-4">
          <div className="p-3 bg-sky-500/10 border border-sky-500/20 rounded-lg flex items-start gap-2.5 text-xs text-sky-200">
            <AlertCircle size={15} className="text-sky-400 mt-0.5 flex-shrink-0" />
            <p className="leading-relaxed">
              An account with <strong className="text-white font-semibold">{email}</strong> already exists. Enter your password to securely link your Google account to this identity.
            </p>
          </div>
        </div>

        {/* Body Form */}
        <form onSubmit={handleSubmit} className="p-5 space-y-4">
          {error && (
            <div className="px-3 py-2 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-xs">
              {error}
            </div>
          )}

          <div>
            <label className="block text-xs font-medium text-slate-300 mb-1.5">
              Account Email
            </label>
            <input
              type="text"
              disabled
              value={email}
              className="w-full px-3 py-2 bg-[#0a0b0e]/50 border border-[#1e2530] rounded-lg text-slate-400 text-xs cursor-not-allowed"
            />
          </div>

          <div>
            <label htmlFor="link-password" className="block text-xs font-medium text-slate-300 mb-1.5">
              TradeDrift Password
            </label>
            <div className="relative">
              <Lock size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
              <input
                id="link-password"
                type={showPassword ? 'text' : 'password'}
                required
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Enter your current password"
                className="w-full pl-9 pr-9 py-2.5 bg-[#0a0b0e] border border-[#1e2530] rounded-lg text-[#f5f7fa] text-xs placeholder:text-slate-500 focus:outline-none focus:border-brand focus:shadow-[0_0_15px_rgba(16,185,129,0.2)] transition-all"
              />
              <button
                type="button"
                aria-label={showPassword ? 'Hide password' : 'Show password'}
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-white transition-colors"
              >
                {showPassword ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
          </div>

          {/* Footer Actions */}
          <div className="flex items-center justify-end gap-2 pt-2 border-t border-[#1e2530]">
            <button
              type="button"
              disabled={loading}
              onClick={onClose}
              className="px-3.5 py-2 rounded-lg border border-[#1e2530] bg-[#0a0b0e] hover:bg-white/5 text-xs font-medium text-slate-300 hover:text-[#f5f7fa] transition-colors disabled:opacity-40"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading || !password}
              className="px-4 py-2 rounded-lg bg-brand hover:bg-brand-dark text-black font-semibold text-xs transition-colors disabled:opacity-50 disabled:cursor-not-allowed glow-green-sm"
            >
              {loading ? 'Linking Account…' : 'Confirm & Link'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
