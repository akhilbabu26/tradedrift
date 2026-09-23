import { useState, useEffect } from 'react'
import { X, User as UserIcon, AlertCircle } from 'lucide-react'
import toast from 'react-hot-toast'
import { useAuthStore } from '../../store/authStore'

interface EditProfileModalProps {
  isOpen: boolean
  onClose: () => void
}

export default function EditProfileModal({ isOpen, onClose }: EditProfileModalProps) {
  const { user, setUser } = useAuthStore()
  const [username, setUsername] = useState(user?.username || '')

  useEffect(() => {
    if (isOpen) {
      setUsername(user?.username || '')
    }
  }, [isOpen, user?.username])

  if (!isOpen) return null

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = username.trim()
    if (!trimmed) {
      toast.error('Username cannot be empty')
      return
    }

    if (user) {
      setUser({ ...user, username: trimmed })
      toast.success('Display name updated for local session')
    }
    onClose()
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-xs">
      <div
        className="w-full max-w-md bg-[#111318] border border-[#1e2530] rounded-xl shadow-2xl overflow-hidden"
        role="dialog"
        aria-modal="true"
        aria-labelledby="edit-profile-title"
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-[#1e2530]">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-lg bg-sky-500/10 border border-sky-500/20 text-sky-400 flex items-center justify-center">
              <UserIcon size={14} />
            </div>
            <h2 id="edit-profile-title" className="text-sm font-semibold text-[#f5f7fa]">
              Edit Profile
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

        {/* Body Form */}
        <form onSubmit={handleSave} className="p-5 space-y-4">
          <div>
            <label htmlFor="edit-username" className="block text-xs font-medium text-slate-300 mb-1.5">
              Display Name / Username
            </label>
            <input
              id="edit-username"
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="e.g. Akhil Babu"
              className="w-full px-3 py-2 bg-[#0a0b0e] border border-[#1e2530] rounded-lg text-xs text-[#f5f7fa] placeholder-slate-600 focus:outline-none focus:border-[#10b981]/50 transition-colors"
              autoFocus
            />
          </div>

          <div>
            <label htmlFor="edit-email" className="block text-xs font-medium text-slate-400 mb-1.5">
              Email Address
            </label>
            <input
              id="edit-email"
              type="email"
              value={user?.email || ''}
              disabled
              readOnly
              className="w-full px-3 py-2 bg-[#0a0b0e]/50 border border-[#1e2530]/60 rounded-lg text-xs text-slate-500 cursor-not-allowed"
            />
            <p className="text-[11px] text-slate-500 mt-1">Email cannot be changed.</p>
          </div>

          {/* Local Session Notice */}
          <div className="p-3 bg-amber-500/10 border border-amber-500/20 rounded-lg flex items-start gap-2.5">
            <AlertCircle size={14} className="text-amber-400 mt-0.5 flex-shrink-0" />
            <p className="text-[11px] text-amber-200/90 leading-relaxed">
              <strong>Local session notice:</strong> Server profile persistence is not currently implemented in the API. This update applies to your active browser session.
            </p>
          </div>

          {/* Actions */}
          <div className="flex items-center justify-end gap-2.5 pt-2 border-t border-[#1e2530]">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 rounded-lg border border-[#1e2530] text-xs font-medium text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              className="px-4 py-1.5 rounded-lg bg-[#10b981] hover:bg-[#059669] text-xs font-semibold text-[#0a0b0e] transition-colors"
            >
              Save Changes
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
