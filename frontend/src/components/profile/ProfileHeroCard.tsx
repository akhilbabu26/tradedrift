import { useState } from 'react'
import { Edit2, Copy, Check, ShieldCheck, Database, Activity, CheckCircle2 } from 'lucide-react'
import toast from 'react-hot-toast'
import { useAuthStore } from '../../store/authStore'
import EditProfileModal from './EditProfileModal'

export default function ProfileHeroCard() {
  const { user } = useAuthStore()
  const [isEditOpen, setIsEditOpen] = useState(false)
  const [copied, setCopied] = useState(false)

  const displayName = user?.username || 'Trader'
  const email = user?.email || 'account@tradedrift.local'
  const fullUserId = user?.userId || 'usr_anonymous'

  const initials = displayName
    .split(' ')
    .filter(Boolean)
    .map((n) => n[0])
    .join('')
    .substring(0, 2)
    .toUpperCase() || 'TD'

  const displayUserId = fullUserId.length > 28
    ? `${fullUserId.substring(0, 24)}...`
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
      <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 sm:p-6 transition-colors">
        {/* Top Hero Section */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-5 pb-6 border-b border-[#1e2530]">
          <div className="flex items-center gap-4 sm:gap-5">
            {/* Large Circular Avatar */}
            <div className="w-16 h-16 sm:w-20 sm:h-20 rounded-full bg-[#1e293b] border-2 border-[#334155] flex items-center justify-center text-[#94a3b8] font-bold text-xl sm:text-2xl flex-shrink-0 shadow-inner select-none">
              {initials}
            </div>

            {/* Identity Details */}
            <div className="min-w-0">
              <div className="flex items-center gap-2.5 flex-wrap">
                <h2 className="text-lg sm:text-xl font-bold text-[#f5f7fa] tracking-tight truncate">
                  {displayName}
                </h2>
                <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30">
                  <span className="w-1.5 h-1.5 rounded-full bg-[#10b981] animate-pulse" />
                  Verified
                </span>
              </div>

              <p className="text-xs text-slate-400 mt-1 truncate">{email}</p>

              {/* Sub-identity pills */}
              <div className="flex items-center gap-2 mt-2.5 flex-wrap">
                <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[11px] font-medium bg-[#0a0b0e] text-slate-300 border border-[#1e2530]">
                  <Activity size={12} className="text-emerald-400" />
                  Simulated Spot Trader
                </span>
                <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md text-[11px] font-medium bg-[#0a0b0e] text-slate-400 border border-[#1e2530]">
                  Member since 2026
                </span>
              </div>
            </div>
          </div>

          {/* Edit Profile Button */}
          <div className="flex sm:self-start">
            <button
              type="button"
              onClick={() => setIsEditOpen(true)}
              className="flex items-center gap-2 px-3.5 py-2 rounded-lg border border-[#1e2530] bg-[#0a0b0e]/80 hover:bg-white/5 hover:border-slate-600 text-xs font-medium text-slate-200 hover:text-white transition-colors cursor-pointer"
            >
              <Edit2 size={13} className="text-slate-400" />
              Edit Profile
            </button>
          </div>
        </div>

        {/* Account Information Section */}
        <div className="pt-5">
          <div className="flex items-center gap-2 mb-4">
            <ShieldCheck size={15} className="text-emerald-400" />
            <h3 className="text-xs font-semibold text-slate-300 uppercase tracking-wider">
              Account Information
            </h3>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3.5">
            {/* User ID */}
            <div className="p-3 bg-[#0a0b0e] border border-[#1e2530] rounded-lg">
              <span className="block text-[11px] font-medium text-slate-400 mb-1">User ID</span>
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-mono text-slate-300 truncate" title={fullUserId}>
                  {displayUserId}
                </span>
                <button
                  type="button"
                  onClick={handleCopyId}
                  className="flex items-center gap-1 px-2 py-1 rounded bg-[#111318] border border-[#1e2530] hover:border-slate-600 text-[11px] font-medium text-slate-300 hover:text-white transition-colors flex-shrink-0 cursor-pointer"
                  title="Copy User ID"
                >
                  {copied ? <Check size={11} className="text-[#10b981]" /> : <Copy size={11} />}
                  <span>{copied ? 'Copied' : 'Copy'}</span>
                </button>
              </div>
            </div>

            {/* Account Type */}
            <div className="p-3 bg-[#0a0b0e] border border-[#1e2530] rounded-lg">
              <span className="block text-[11px] font-medium text-slate-400 mb-1">Account Type</span>
              <div className="flex items-center gap-2 text-xs font-medium text-[#f5f7fa]">
                <Database size={13} className="text-sky-400 flex-shrink-0" />
                <span className="truncate">Simulated Spot Account</span>
              </div>
            </div>

            {/* Account Status */}
            <div className="p-3 bg-[#0a0b0e] border border-[#1e2530] rounded-lg">
              <span className="block text-[11px] font-medium text-slate-400 mb-1">Account Status</span>
              <div className="flex items-center gap-2 text-xs font-medium text-[#10b981]">
                <span className="w-2 h-2 rounded-full bg-[#10b981]" />
                <span>Active</span>
              </div>
            </div>

            {/* Email Status */}
            <div className="p-3 bg-[#0a0b0e] border border-[#1e2530] rounded-lg">
              <span className="block text-[11px] font-medium text-slate-400 mb-1">Email Status</span>
              <div className="flex items-center gap-1.5 text-xs font-medium text-[#10b981]">
                <CheckCircle2 size={13} className="text-[#10b981]" />
                <span>Verified</span>
              </div>
            </div>

            {/* Trading Mode */}
            <div className="p-3 bg-[#0a0b0e] border border-[#1e2530] rounded-lg sm:col-span-2 lg:col-span-2">
              <span className="block text-[11px] font-medium text-slate-400 mb-1">Trading Mode</span>
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium text-[#f5f7fa]">Simulation</span>
                <span className="text-[11px] text-slate-400 font-mono">Zero risk virtual execution</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <EditProfileModal isOpen={isEditOpen} onClose={() => setIsEditOpen(false)} />
    </>
  )
}
