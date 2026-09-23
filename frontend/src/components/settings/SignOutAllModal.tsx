import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { AlertTriangle, X } from 'lucide-react'
import toast from 'react-hot-toast'
import { authApi } from '../../api/auth'
import { useAuthStore } from '../../store/authStore'

interface SignOutAllModalProps {
  isOpen: boolean
  onClose: () => void
}

export default function SignOutAllModal({ isOpen, onClose }: SignOutAllModalProps) {
  const navigate = useNavigate()
  const logout = useAuthStore((s) => s.logout)
  const [loading, setLoading] = useState(false)

  if (!isOpen) return null

  const handleConfirm = async () => {
    setLoading(true)
    try {
      // Call backend API first
      await authApi.logoutAll()
      toast.success('Signed out of all sessions')
      // Only clear local state and redirect after API succeeds
      logout()
      navigate('/login')
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Failed to sign out of all sessions. Please try again.'
      toast.error(msg)
      // Keep session active on failure
      onClose()
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-xs">
      <div
        className="w-full max-w-md bg-[#111318] border border-[#1e2530] rounded-xl shadow-2xl overflow-hidden"
        role="dialog"
        aria-modal="true"
        aria-labelledby="signout-all-title"
      >
        <div className="flex items-center justify-between px-5 py-4 border-b border-[#1e2530]">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-lg bg-[#ef4444]/10 border border-[#ef4444]/20 text-[#ef4444] flex items-center justify-center">
              <AlertTriangle size={14} />
            </div>
            <h2 id="signout-all-title" className="text-sm font-semibold text-[#f5f7fa]">
              Sign Out All Sessions?
            </h2>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="p-1 rounded-md text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors"
            aria-label="Close dialog"
          >
            <X size={15} />
          </button>
        </div>

        <div className="p-5">
          <p className="text-xs text-slate-300 leading-relaxed">
            This will revoke all active refresh tokens and sign you out from all devices, including this one.
            You will need to sign in again to continue.
          </p>

          <div className="flex items-center justify-end gap-2.5 pt-5 mt-4 border-t border-[#1e2530]">
            <button
              type="button"
              disabled={loading}
              onClick={onClose}
              className="px-3 py-1.5 rounded-lg border border-[#1e2530] text-xs font-medium text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={loading}
              onClick={handleConfirm}
              className="px-4 py-1.5 rounded-lg bg-[#ef4444] hover:bg-[#dc2626] disabled:opacity-50 text-xs font-semibold text-white transition-colors"
            >
              {loading ? 'Signing out...' : 'Sign Out All Sessions'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
