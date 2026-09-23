import { useState } from 'react'
import { User as UserIcon, Edit2, Copy, Check, Database } from 'lucide-react'
import toast from 'react-hot-toast'
import { useAuthStore } from '../../store/authStore'
import EditProfileModal from './EditProfileModal'

export default function ProfileAccountCard() {
  const { user } = useAuthStore()
  const [isEditOpen, setIsEditOpen] = useState(false)
  const [copied, setCopied] = useState(false)

  const displayName = user?.username || 'Akhil Babu'
  const email = user?.email || 'akhilbabu.go@gmail.com'
  const fullUserId = user?.userId || 'usr_0191e4a2-7b3f-7120-9c4d-8e9a2b5c7d1e'

  const initials = displayName
    .split(' ')
    .filter(Boolean)
    .map((n) => n[0])
    .join('')
    .substring(0, 2)
    .toUpperCase() || 'AB'

  const displayUserId = fullUserId.length > 28
    ? `${fullUserId.substring(0, 26)}...`
    : fullUserId

  const handleCopyId = async () => {
    try {
      await navigator.clipboard.writeText(fullUserId)
      setCopied(true)
      toast.success('User ID copied to clipboard')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('Failed to copy ID')
    }
  }

  return (
    <>
      <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 sm:p-6 mb-5 transition-colors">
        {/* Card Header */}
        <div className="flex items-start justify-between gap-4 mb-5">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-sky-500/10 border border-sky-500/20 text-sky-400 flex items-center justify-center flex-shrink-0">
              <UserIcon size={16} />
            </div>
            <div>
              <h2 className="text-sm font-semibold text-[#f5f7fa] tracking-tight">Profile & Account</h2>
              <p className="text-xs text-slate-400">Your account information and identity.</p>
            </div>
          </div>

          <button
            type="button"
            onClick={() => setIsEditOpen(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-[#1e2530] bg-[#0a0b0e]/60 hover:bg-white/5 hover:border-slate-600 text-xs font-medium text-slate-300 hover:text-[#f5f7fa] transition-colors cursor-pointer"
          >
            <Edit2 size={12} className="text-slate-400" />
            Edit Profile
          </button>
        </div>

        {/* User Identity Row */}
        <div className="flex items-center gap-4 mb-6">
          <div className="w-14 h-14 rounded-full bg-[#1e293b] border border-[#334155] flex items-center justify-center text-[#94a3b8] font-bold text-lg flex-shrink-0 shadow-inner">
            {initials}
          </div>
          <div>
            <div className="flex items-center gap-2.5 flex-wrap">
              <span className="text-base font-bold text-[#f5f7fa] tracking-tight">{displayName}</span>
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30">
                <span className="w-1.5 h-1.5 rounded-full bg-[#10b981]" />
                Verified
              </span>
            </div>
            <p className="text-xs text-slate-400 mt-0.5">{email}</p>
          </div>
        </div>

        {/* Bottom 2-Column Info Grid */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 pt-5 border-t border-[#1e2530]">
          {/* User ID */}
          <div>
            <label className="block text-xs text-slate-400 mb-1.5">User ID</label>
            <div className="flex items-center justify-between gap-2 px-3 py-2 bg-[#0a0b0e] border border-[#1e2530] rounded-lg">
              <span className="text-xs font-mono text-slate-300 truncate" title={fullUserId}>
                {displayUserId}
              </span>
              <button
                type="button"
                onClick={handleCopyId}
                className="flex items-center gap-1 px-2 py-1 rounded bg-[#111318] border border-[#1e2530] hover:border-slate-600 text-[11px] font-medium text-slate-300 hover:text-white transition-colors flex-shrink-0"
                aria-label="Copy User ID"
              >
                {copied ? <Check size={11} className="text-[#10b981]" /> : <Copy size={11} />}
                <span>{copied ? 'Copied' : 'Copy'}</span>
              </button>
            </div>
          </div>

          {/* Account Type */}
          <div>
            <label className="block text-xs text-slate-400 mb-1.5">Account Type</label>
            <div className="flex items-center gap-3 px-3 py-2 bg-[#0a0b0e] border border-[#1e2530] rounded-lg">
              <div className="w-7 h-7 rounded-md bg-sky-500/10 border border-sky-500/20 text-sky-400 flex items-center justify-center flex-shrink-0">
                <Database size={13} />
              </div>
              <div className="leading-tight">
                <p className="text-xs font-semibold text-[#f5f7fa]">Simulated Spot Account</p>
                <p className="text-[11px] text-slate-400">Practice trading with virtual funds</p>
              </div>
            </div>
          </div>
        </div>
      </div>

      <EditProfileModal isOpen={isEditOpen} onClose={() => setIsEditOpen(false)} />
    </>
  )
}
