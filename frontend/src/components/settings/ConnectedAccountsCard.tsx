import { useState, useEffect } from 'react'
import { Link2, CheckCircle2 } from 'lucide-react'
import toast from 'react-hot-toast'
import { authApi } from '../../api/auth'
import { RaisedSurface } from '../common/Surface'
import GoogleAuthButton from '../auth/GoogleAuthButton'

const STORAGE_KEY_GOOGLE_LINKED = 'tradedrift_google_linked'

export default function ConnectedAccountsCard() {
  const [isLinked, setIsLinked] = useState<boolean>(() => {
    try {
      return localStorage.getItem(STORAGE_KEY_GOOGLE_LINKED) === 'true'
    } catch {
      return false
    }
  })
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    try {
      if (isLinked) {
        localStorage.setItem(STORAGE_KEY_GOOGLE_LINKED, 'true')
      }
    } catch {
      // ignore
    }
  }, [isLinked])

  const handleConnectSuccess = async (idToken: string) => {
    setLoading(true)
    try {
      const { data } = await authApi.connectGoogle(idToken)
      if (data.success) {
        setIsLinked(true)
        toast.success(`Connected Google account (${data.email || 'Verified'}) successfully!`)
      }
    } catch (err: unknown) {
      const errRes = (err as { response?: { status?: number; data?: { message?: string } } })?.response
      const status = errRes?.status
      const msg = errRes?.data?.message || 'Failed to connect Google account'

      if (status === 409) {
        if (msg.toLowerCase().includes('already linked to your account')) {
          setIsLinked(true)
          toast.success('Google account is already connected to your profile.')
        } else {
          toast.error(msg || 'This Google account is already linked to another TradeDrift user.')
        }
      } else {
        toast.error(msg)
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <RaisedSurface variant="raised-subtle" className="rounded-xl p-5 sm:p-6 mb-5 transition-colors">
      {/* Card Header */}
      <div className="flex items-center gap-3 mb-5">
        <div className="w-8 h-8 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 flex items-center justify-center flex-shrink-0">
          <Link2 size={16} />
        </div>
        <div>
          <h2 className="text-sm font-semibold text-[#f5f7fa] tracking-tight">Connected Accounts</h2>
          <p className="text-xs text-slate-400">Manage third-party authentication methods linked to your TradeDrift identity.</p>
        </div>
      </div>

      {/* Accounts List */}
      <div className="space-y-3 pt-1">
        {/* Google Account Row */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 p-4 rounded-lg bg-[#0a0b0e] border border-[#1e2530]">
          <div className="flex items-center gap-3.5">
            <div className="w-10 h-10 rounded-lg bg-[#111318] border border-[#1e2530] flex items-center justify-center flex-shrink-0">
              <svg className="h-5 w-5" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
                <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4" />
                <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853" />
                <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05" />
                <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335" />
              </svg>
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h3 className="text-xs font-semibold text-[#f5f7fa]">Google</h3>
                {isLinked && (
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-medium bg-[#10b981]/15 text-[#10b981] border border-[#10b981]/30">
                    <CheckCircle2 size={10} />
                    Connected
                  </span>
                )}
              </div>
              <p className="text-[11px] text-slate-400 mt-0.5">
                {isLinked
                  ? 'Your Google account is linked. You can sign in using Google anytime.'
                  : 'Link your Google account for faster, one-click sign-in to this portfolio.'}
              </p>
            </div>
          </div>

          <div className="flex-shrink-0 sm:self-center">
            {isLinked ? (
              <div className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 text-xs font-medium">
                <CheckCircle2 size={13} />
                <span>Linked</span>
              </div>
            ) : (
              <div className="w-full sm:w-auto min-w-[160px]">
                <GoogleAuthButton
                  onSuccess={handleConnectSuccess}
                  onError={(err) => toast.error(err || 'Failed to authenticate with Google')}
                  text="continue_with"
                  disabled={loading}
                />
              </div>
            )}
          </div>
        </div>
      </div>
    </RaisedSurface>
  )
}
