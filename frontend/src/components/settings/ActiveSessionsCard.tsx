import { useState } from 'react'
import { Smartphone, Monitor, RotateCw, LogOut } from 'lucide-react'
import toast from 'react-hot-toast'
import { DEMO_ACTIVE_SESSIONS } from '../../data/settingsMock'
import SignOutAllModal from './SignOutAllModal'

export default function ActiveSessionsCard() {
  const [isSignOutAllOpen, setIsSignOutAllOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [sessions] = useState(DEMO_ACTIVE_SESSIONS)

  const handleRefresh = () => {
    setRefreshing(true)
    setTimeout(() => {
      setRefreshing(false)
      toast.success('Session records refreshed')
    }, 500)
  }

  return (
    <>
      <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 sm:p-6 mb-5 transition-colors">
        {/* Header */}
        <div className="flex items-start justify-between gap-4 mb-5">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-sky-500/10 border border-sky-500/20 text-sky-400 flex items-center justify-center flex-shrink-0">
              <Smartphone size={16} />
            </div>
            <div>
              <h2 className="text-sm font-semibold text-[#f5f7fa] tracking-tight">Active Sessions</h2>
              <p className="text-xs text-slate-400">Devices currently signed in to your account.</p>
            </div>
          </div>

          <button
            type="button"
            onClick={handleRefresh}
            disabled={refreshing}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-[#1e2530] bg-[#0a0b0e]/60 hover:bg-white/5 hover:border-slate-600 text-xs font-medium text-slate-300 hover:text-[#f5f7fa] transition-colors cursor-pointer"
          >
            <RotateCw size={12} className={`text-slate-400 ${refreshing ? 'animate-spin' : ''}`} />
            Refresh
          </button>
        </div>

        {/* Sessions Table Container */}
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead>
              <tr className="border-b border-[#1e2530] text-slate-500 text-[11px] font-medium uppercase tracking-wider">
                <th className="pb-3 pr-4 font-medium">Device</th>
                <th className="pb-3 px-4 font-medium">Location / IP</th>
                <th className="pb-3 px-4 font-medium">Last Active</th>
                <th className="pb-3 pl-4 font-medium">Status</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#1e2530]/60">
              {sessions.map((sess) => (
                <tr key={sess.id} className="hover:bg-white/[0.02] transition-colors">
                  {/* Device */}
                  <td className="py-3.5 pr-4">
                    <div className="flex items-center gap-3">
                      <div className="w-7 h-7 rounded-md bg-[#0a0b0e] border border-[#1e2530] flex items-center justify-center text-slate-400 flex-shrink-0">
                        {sess.device === 'desktop' ? <Monitor size={14} /> : <Smartphone size={14} />}
                      </div>
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-semibold text-[#f5f7fa]">
                          {sess.browser} on {sess.os}
                        </span>
                        {sess.isCurrent && (
                          <span className="px-2 py-0.5 rounded-full text-[10px] font-medium bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30">
                            This Device
                          </span>
                        )}
                      </div>
                    </div>
                  </td>

                  {/* Location & IP */}
                  <td className="py-3.5 px-4">
                    <div className="leading-tight">
                      <p className="text-[#f5f7fa] font-medium">{sess.location}</p>
                      <p className="text-[11px] font-mono text-slate-500 mt-0.5">{sess.ip}</p>
                    </div>
                  </td>

                  {/* Last Active */}
                  <td className="py-3.5 px-4 text-slate-400">
                    {sess.lastActive}
                  </td>

                  {/* Status */}
                  <td className="py-3.5 pl-4">
                    <span className="inline-flex items-center gap-1.5 text-xs font-medium">
                      <span
                        className={`w-1.5 h-1.5 rounded-full ${
                          sess.status === 'active' ? 'bg-[#10b981]' : 'bg-slate-500'
                        }`}
                      />
                      <span
                        className={sess.status === 'active' ? 'text-[#10b981]' : 'text-slate-400'}
                      >
                        {sess.status === 'active' ? 'Active' : 'Inactive'}
                      </span>
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {/* Bottom Danger Action */}
        <div className="pt-4 mt-2 border-t border-[#1e2530] flex flex-col sm:flex-row sm:items-center gap-3">
          <button
            type="button"
            onClick={() => setIsSignOutAllOpen(true)}
            className="flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg border border-[#ef4444]/40 bg-[#ef4444]/10 hover:bg-[#ef4444]/20 text-xs font-semibold text-[#ef4444] transition-colors cursor-pointer flex-shrink-0"
          >
            <LogOut size={13} />
            Sign Out All Sessions
          </button>
          <span className="text-[11px] text-slate-400">
            This will sign you out from all devices, including this one.
          </span>
        </div>
      </div>

      <SignOutAllModal
        isOpen={isSignOutAllOpen}
        onClose={() => setIsSignOutAllOpen(false)}
      />
    </>
  )
}
