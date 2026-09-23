import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Lock, Eye, EyeOff, ShieldCheck, CheckCircle2 } from 'lucide-react'
import toast from 'react-hot-toast'
import { authApi } from '../../api/auth'
import { useAuthStore } from '../../store/authStore'

export default function SecurityPasswordCard() {
  const navigate = useNavigate()
  const logout = useAuthStore((s) => s.logout)

  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')

  const [showCurrent, setShowCurrent] = useState(false)
  const [showNew, setShowNew] = useState(false)
  const [showConfirm, setShowConfirm] = useState(false)

  const [loading, setLoading] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')

  // Real-time password requirement checks
  const hasMinLength = newPassword.length >= 8
  const hasLetter = /[a-zA-Z]/.test(newPassword)
  const hasNumber = /[0-9]/.test(newPassword)
  const isPasswordValid = hasMinLength && hasLetter && hasNumber

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErrorMessage('')

    if (!currentPassword) {
      setErrorMessage('Please enter your current password.')
      toast.error('Current password is required')
      return
    }

    if (!isPasswordValid) {
      setErrorMessage('New password does not meet all security requirements.')
      toast.error('Password does not meet requirements')
      return
    }

    if (newPassword !== confirmPassword) {
      setErrorMessage('New password and confirmation do not match.')
      toast.error('Passwords do not match')
      return
    }

    if (currentPassword === newPassword) {
      setErrorMessage('New password must be different from current password.')
      toast.error('New password must be different')
      return
    }

    setLoading(true)
    try {
      await authApi.changePassword({
        oldPassword: currentPassword,
        newPassword,
      })

      toast.success('Password updated successfully. Please log in again.')
      // Clear local session since backend invalidates existing refresh tokens
      logout()
      navigate('/login')
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Failed to update password. Please check your current password.'
      setErrorMessage(msg)
      toast.error(msg)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 sm:p-6 mb-5 transition-colors">
      {/* Card Header */}
      <div className="flex items-center gap-3 mb-6">
        <div className="w-8 h-8 rounded-lg bg-[#10b981]/10 border border-[#10b981]/20 text-[#10b981] flex items-center justify-center flex-shrink-0">
          <Lock size={16} />
        </div>
        <div>
          <h2 className="text-sm font-semibold text-[#f5f7fa] tracking-tight">Security & Password</h2>
          <p className="text-xs text-slate-400">Update your password to keep your account secure.</p>
        </div>
      </div>

      {/* Main 2-Column Layout */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
        {/* Form Left Side (~7 cols) */}
        <form onSubmit={handleSubmit} className="lg:col-span-7 space-y-3.5">
          {/* Current Password */}
          <div>
            <label htmlFor="current-password" className="block text-xs text-slate-300 mb-1">
              Current Password
            </label>
            <div className="relative">
              <input
                id="current-password"
                type={showCurrent ? 'text' : 'password'}
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
                placeholder="••••••••••••"
                className="w-full pl-3 pr-9 py-2 bg-[#0a0b0e] border border-[#1e2530] rounded-lg text-xs text-[#f5f7fa] placeholder-slate-600 focus:outline-none focus:border-[#10b981]/50 transition-colors"
                autoComplete="current-password"
              />
              <button
                type="button"
                onClick={() => setShowCurrent((s) => !s)}
                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300 transition-colors"
                aria-label={showCurrent ? 'Hide current password' : 'Show current password'}
              >
                {showCurrent ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
          </div>

          {/* New Password */}
          <div>
            <label htmlFor="new-password" className="block text-xs text-slate-300 mb-1">
              New Password
            </label>
            <div className="relative">
              <input
                id="new-password"
                type={showNew ? 'text' : 'password'}
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                placeholder="••••••••••••"
                className="w-full pl-3 pr-9 py-2 bg-[#0a0b0e] border border-[#1e2530] rounded-lg text-xs text-[#f5f7fa] placeholder-slate-600 focus:outline-none focus:border-[#10b981]/50 transition-colors"
                autoComplete="new-password"
              />
              <button
                type="button"
                onClick={() => setShowNew((s) => !s)}
                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300 transition-colors"
                aria-label={showNew ? 'Hide new password' : 'Show new password'}
              >
                {showNew ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
          </div>

          {/* Confirm New Password */}
          <div>
            <label htmlFor="confirm-password" className="block text-xs text-slate-300 mb-1">
              Confirm New Password
            </label>
            <div className="relative">
              <input
                id="confirm-password"
                type={showConfirm ? 'text' : 'password'}
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                placeholder="••••••••••••"
                className="w-full pl-3 pr-9 py-2 bg-[#0a0b0e] border border-[#1e2530] rounded-lg text-xs text-[#f5f7fa] placeholder-slate-600 focus:outline-none focus:border-[#10b981]/50 transition-colors"
                autoComplete="new-password"
              />
              <button
                type="button"
                onClick={() => setShowConfirm((s) => !s)}
                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300 transition-colors"
                aria-label={showConfirm ? 'Hide confirm password' : 'Show confirm password'}
              >
                {showConfirm ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
          </div>

          {errorMessage && (
            <p className="text-xs text-[#ef4444] pt-0.5">{errorMessage}</p>
          )}

          {/* Action Button & Note */}
          <div className="pt-2 flex flex-col sm:flex-row sm:items-center gap-3">
            <button
              type="submit"
              disabled={loading}
              className="px-4 py-2 bg-[#10b981] hover:bg-[#059669] disabled:opacity-50 text-xs font-semibold text-[#0a0b0e] rounded-lg transition-colors cursor-pointer flex-shrink-0"
            >
              {loading ? 'Updating...' : 'Update Password'}
            </button>
            <span className="text-[11px] text-slate-400 leading-tight">
              Changing your password will sign you out of existing sessions.
            </span>
          </div>
        </form>

        {/* Requirements Box Right Side (~5 cols) */}
        <div className="lg:col-span-5 bg-[#0a0b0e] border border-[#10b981]/20 rounded-xl p-4 sm:p-5">
          <div className="flex items-center gap-2 mb-3.5">
            <ShieldCheck size={16} className="text-[#10b981]" />
            <h3 className="text-xs font-bold text-[#10b981] tracking-tight">Password Requirements</h3>
          </div>

          <ul className="space-y-2.5 text-xs">
            <li className="flex items-center gap-2.5">
              <CheckCircle2
                size={14}
                className={hasMinLength ? 'text-[#10b981]' : 'text-slate-600'}
              />
              <span className={hasMinLength ? 'text-[#f5f7fa]' : 'text-slate-400'}>
                Minimum 8 characters
              </span>
            </li>
            <li className="flex items-center gap-2.5">
              <CheckCircle2
                size={14}
                className={hasLetter ? 'text-[#10b981]' : 'text-slate-600'}
              />
              <span className={hasLetter ? 'text-[#f5f7fa]' : 'text-slate-400'}>
                At least one letter (a–z, A–Z)
              </span>
            </li>
            <li className="flex items-center gap-2.5">
              <CheckCircle2
                size={14}
                className={hasNumber ? 'text-[#10b981]' : 'text-slate-600'}
              />
              <span className={hasNumber ? 'text-[#f5f7fa]' : 'text-slate-400'}>
                At least one number (0–9)
              </span>
            </li>
          </ul>
        </div>
      </div>
    </div>
  )
}
