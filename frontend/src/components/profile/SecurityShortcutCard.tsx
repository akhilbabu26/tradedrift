import { useNavigate } from 'react-router-dom'
import { Shield, Key, MailCheck, Laptop, ChevronRight } from 'lucide-react'
import type { SecurityShortcutInfo } from '../../types/profile'

interface SecurityShortcutCardProps {
  security: SecurityShortcutInfo
}

export default function SecurityShortcutCard({ security }: SecurityShortcutCardProps) {
  const navigate = useNavigate()

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 flex flex-col justify-between transition-colors">
      <div>
        {/* Header */}
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-2">
            <Shield size={16} className="text-emerald-400" />
            <div>
              <h2 className="text-sm font-semibold text-[#f5f7fa] tracking-tight">Security</h2>
              <p className="text-xs text-slate-400 mt-0.5">Authentication and active session overview.</p>
            </div>
          </div>
        </div>

        {/* Rows */}
        <div className="space-y-2.5 mb-4">
          {/* Password */}
          <div className="flex items-center justify-between p-2.5 bg-[#0a0b0e] border border-[#1e2530] rounded-lg">
            <div className="flex items-center gap-2">
              <Key size={13} className="text-slate-400" />
              <span className="text-xs font-medium text-slate-300">Password</span>
            </div>
            <span className="text-xs text-slate-400 font-mono">{security.passwordStatus}</span>
          </div>

          {/* Email Verification */}
          <div className="flex items-center justify-between p-2.5 bg-[#0a0b0e] border border-[#1e2530] rounded-lg">
            <div className="flex items-center gap-2">
              <MailCheck size={13} className="text-emerald-400" />
              <span className="text-xs font-medium text-slate-300">Email Verification</span>
            </div>
            <span className="inline-flex items-center gap-1 text-xs text-emerald-400 font-medium">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
              {security.emailVerification}
            </span>
          </div>

          {/* Active Sessions */}
          <div className="flex items-center justify-between p-2.5 bg-[#0a0b0e] border border-[#1e2530] rounded-lg">
            <div className="flex items-center gap-2">
              <Laptop size={13} className="text-sky-400" />
              <span className="text-xs font-medium text-slate-300">Active Sessions</span>
            </div>
            <span className="text-xs text-slate-400 font-mono">{security.activeSessions}</span>
          </div>
        </div>
      </div>

      {/* Manage Security Button */}
      <button
        type="button"
        onClick={() => navigate('/settings')}
        className="w-full flex items-center justify-center gap-2 py-2 px-3 rounded-lg border border-[#1e2530] bg-[#0a0b0e] hover:bg-white/5 hover:border-slate-600 text-xs font-medium text-slate-200 hover:text-white transition-colors cursor-pointer mt-1"
      >
        <span>Manage Security</span>
        <ChevronRight size={13} className="text-slate-400" />
      </button>
    </div>
  )
}
