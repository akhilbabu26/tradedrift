import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { LogOut } from 'lucide-react'
import { authApi } from '../../api/auth'
import { useAuthStore } from '../../store/authStore'

export default function SignOutCard() {
  const navigate = useNavigate()
  const logout = useAuthStore((s) => s.logout)
  const [loading, setLoading] = useState(false)

  const handleSignOut = async () => {
    setLoading(true)
    try {
      await authApi.logout()
    } catch {
      // Ignore network errors on logout
    } finally {
      logout()
      navigate('/login')
    }
  }

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 sm:p-6 mb-8 transition-colors">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-[#ef4444]/10 border border-[#ef4444]/20 text-[#ef4444] flex items-center justify-center flex-shrink-0">
            <LogOut size={16} />
          </div>
          <div>
            <h2 className="text-sm font-semibold text-[#f5f7fa] tracking-tight">Sign Out</h2>
            <p className="text-xs text-slate-400">End your current session and return to the login page.</p>
          </div>
        </div>

        <button
          type="button"
          onClick={handleSignOut}
          disabled={loading}
          className="px-4 py-1.5 rounded-lg border border-[#ef4444]/40 hover:bg-[#ef4444]/10 disabled:opacity-50 text-xs font-semibold text-[#ef4444] transition-colors cursor-pointer self-start sm:self-auto"
        >
          {loading ? 'Signing Out...' : 'Sign Out'}
        </button>
      </div>
    </div>
  )
}
