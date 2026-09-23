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
  const [displayName, setDisplayName] = useState(user?.username || 'Akhil Babu')

  useEffect(() => {
    if (isOpen) {
      setDisplayName(user?.username || 'Akhil Babu')
    }
  }, [isOpen, user?.username])

  if (!isOpen) return null

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = displayName.trim()
    if (!trimmed) {
      toast.error('Display name cannot be empty')
      return
    }

    if (user) {
      setUser({ ...user, username: trimmed })
    } else {
      setUser({
        userId: 'usr_0191e4a2-7b3f-7120-9c4d-8e9a2b5c7d1e',
        email: 'akhilbabu.go@gmail.com',
        username: trimmed,
      })
    }
    toast.success('Profile updated successfully')
    onClose()
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/75 backdrop-blur-xs">
      <div
        className="w-full max-w-md bg-[#111318] border border-[#1e2530] rounded-xl shadow-2xl overflow-hidden"
        role="dialog"
        aria-modal="true"
        aria-labelledby="profile-edit-modal-title"
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-[#1e2530]">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 flex items-center justify-center">
              <UserIcon size={14} />
            </div>
            <h2 id="profile-edit-modal-title" className="text-sm font-semibold text-[#f5f7fa]">
              Edit Profile
            </h2>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="p-1 rounded-md text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors cursor-pointer"
            aria-label="Close dialog"
          >
            <X size={15} />
          </button>
        </div>

        {/* Body Form */}
        <form onSubmit={handleSave} className="p-5 space-y-4">
          <div>
            <label htmlFor="profile-display-name" className="block text-xs font-medium text-slate-300 mb-1.5">
              Display Name
            </label>
            <input
              id="profile-display-name"
              type="text"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="e.g. Akhil Babu"
              className="w-full px-3 py-2 bg-[#0a0b0e] border border-[#1e2530] rounded-lg text-xs text-[#f5f7fa] placeholder-slate-600 focus:outline-none focus:border-[#10b981]/50 transition-colors"
              autoFocus
            />
          </div>

          <div>
            <label htmlFor="profile-email" className="block text-xs font-medium text-slate-400 mb-1.5">
              Email Address
            </label>
            <input
              id="profile-email"
              type="email"
              value={user?.email || 'akhilbabu.go@gmail.com'}
              disabled
              readOnly
              className="w-full px-3 py-2 bg-[#0a0b0e]/50 border border-[#1e2530]/60 rounded-lg text-xs text-slate-500 cursor-not-allowed"
            />
            <p className="text-[11px] text-slate-500 mt-1">Email cannot be changed.</p>
          </div>

          {/* Session Notice */}
          <div className="p-3 bg-emerald-500/10 border border-emerald-500/20 rounded-lg flex items-start gap-2.5">
            <AlertCircle size={14} className="text-emerald-400 mt-0.5 flex-shrink-0" />
            <p className="text-[11px] text-emerald-200/90 leading-relaxed">
              Profile changes are synchronized with your active trading session.
            </p>
          </div>

          {/* Actions */}
          <div className="flex items-center justify-end gap-2.5 pt-2 border-t border-[#1e2530]">
            <button
              type="button"
              onClick={onClose}
              className="px-3.5 py-1.5 rounded-lg border border-[#1e2530] text-xs font-medium text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors cursor-pointer"
            >
              Cancel
            </button>
            <button
              type="submit"
              className="px-4 py-1.5 rounded-lg bg-[#10b981] hover:bg-[#059669] text-xs font-semibold text-[#0a0b0e] transition-colors cursor-pointer"
            >
              Save Changes
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
