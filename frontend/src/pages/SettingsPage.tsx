import { useState } from 'react'
import {
  User, Lock, SlidersHorizontal, Bell, Save,
  Eye, EyeOff, Monitor, ShieldOff, AlertTriangle,
} from 'lucide-react'
import toast from 'react-hot-toast'
import Sidebar from '../components/dashboard/Sidebar'
import { useAuthStore } from '../store/authStore'
import { authApi } from '../api/auth'

// ── Toggle switch ──────────────────────────────────────────────────────────────
function Toggle({ on, onChange }: { on: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      onClick={() => onChange(!on)}
      className={`relative w-11 h-6 rounded-full transition-colors duration-200 ${
        on ? 'bg-[#10b981]' : 'bg-[#1e2025] border border-[#1f2229]'
      }`}
    >
      <span
        className={`absolute top-1 left-1 w-4 h-4 bg-white rounded-full shadow transition-transform duration-200 ${
          on ? 'translate-x-5' : 'translate-x-0'
        }`}
      />
    </button>
  )
}

// ── Password field ─────────────────────────────────────────────────────────────
function PasswordField({
  label, value, onChange,
}: { label: string; value: string; onChange: (v: string) => void }) {
  const [show, setShow] = useState(false)
  return (
    <div className="flex flex-col gap-1.5">
      <label className="text-[11px] text-slate-400 uppercase tracking-wider font-semibold">{label}</label>
      <div className="relative">
        <input
          type={show ? 'text' : 'password'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="w-full bg-[#1e2025] border border-[#1f2229] rounded-lg px-4 py-2.5 text-white text-sm font-mono focus:outline-none focus:border-[#10b981] pr-10 transition-colors"
        />
        <button
          type="button"
          onClick={() => setShow(!show)}
          className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-white transition-colors"
        >
          {show ? <EyeOff size={14} /> : <Eye size={14} />}
        </button>
      </div>
    </div>
  )
}

// ── TABS ───────────────────────────────────────────────────────────────────────
type Tab = 'profile' | 'security' | 'preferences' | 'notifications'

const TABS: { id: Tab; label: string; icon: React.ElementType }[] = [
  { id: 'profile',       label: 'Profile',       icon: User               },
  { id: 'security',      label: 'Security',       icon: Lock               },
  { id: 'preferences',   label: 'Preferences',    icon: SlidersHorizontal  },
  { id: 'notifications', label: 'Notifications',  icon: Bell               },
]

// ── Main page ──────────────────────────────────────────────────────────────────
export default function SettingsPage() {
  const user = useAuthStore((s) => s.user)

  const [activeTab, setActiveTab] = useState<Tab>('profile')
  const [hasChanges, setHasChanges] = useState(false)

  // Security form
  const [oldPw,     setOldPw]     = useState('')
  const [newPw,     setNewPw]     = useState('')
  const [confirmPw, setConfirmPw] = useState('')
  const [pwLoading, setPwLoading] = useState(false)

  // Preferences toggles
  const [compactMode,   setCompactMode]   = useState(false)
  const [showBalances,  setShowBalances]  = useState(true)
  const [soundEffects,  setSoundEffects]  = useState(false)
  const [autoRefresh,   setAutoRefresh]   = useState(true)

  // Notification toggles
  const [emailSummaries,     setEmailSummaries]     = useState(false)
  const [priceAlerts,        setPriceAlerts]        = useState(true)
  const [tradeConfirmations, setTradeConfirmations] = useState(true)
  const [systemUpdates,      setSystemUpdates]      = useState(true)

  const initials = (user?.username ?? 'TD').slice(0, 2).toUpperCase()

  const markChanged = (setter: (v: any) => void) => (val: any) => {
    setter(val)
    setHasChanges(true)
  }

  const handleSaveChanges = () => {
    setHasChanges(false)
    toast.success('Changes saved successfully')
  }

  // ── Change Password ────────────────────────────────────────────────────────
  const handleChangePassword = async () => {
    if (!oldPw || !newPw || !confirmPw) { toast.error('Fill in all fields'); return }
    if (newPw !== confirmPw)             { toast.error('Passwords do not match'); return }
    if (newPw.length < 8)               { toast.error('Password must be at least 8 characters'); return }
    setPwLoading(true)
    try {
      await authApi.changePassword({ oldPassword: oldPw, newPassword: newPw })
      toast.success('Password updated successfully')
      setOldPw(''); setNewPw(''); setConfirmPw('')
    } catch {
      toast.error('Incorrect current password')
    } finally {
      setPwLoading(false)
    }
  }

  // ── Logout All ────────────────────────────────────────────────────────────
  const handleLogoutAll = async () => {
    try {
      await authApi.logoutAll()
      toast.success('All other sessions logged out')
    } catch {
      toast.error('Failed to logout all sessions')
    }
  }

  return (
    <div className="bg-[#0a0b0e] text-white h-screen w-screen overflow-hidden flex font-sans text-sm select-none">
      <Sidebar />

      <div className="flex-1 flex flex-col min-w-0">

        {/* ── Header ── */}
        <header className="h-16 bg-[#111318]/80 backdrop-blur-md border-b border-[#1f2229] flex items-center justify-between px-6 flex-shrink-0">
          <div className="flex flex-col">
            <span className="font-bold text-white text-base">Settings</span>
            <span className="text-[11px] text-slate-400">Manage your account &amp; preferences</span>
          </div>
          <button
            onClick={handleSaveChanges}
            disabled={!hasChanges}
            className={`flex items-center gap-2 text-xs px-4 py-2 rounded-lg transition-all duration-200 font-bold ${
              hasChanges
                ? 'bg-[#10b981] hover:bg-[#0e9f6e] text-black shadow-[0_0_12px_rgba(16,185,129,0.35)] cursor-pointer opacity-100'
                : 'bg-[#1e2025] text-slate-500 border border-[#1f2229] opacity-60 cursor-not-allowed'
            }`}
          >
            <Save size={14} />
            Save Changes
          </button>
        </header>

        {/* ── Main ── */}
        <main className="flex-1 overflow-y-auto p-5">
          <div className="max-w-5xl mx-auto flex gap-5">

            {/* Left tab list */}
            <aside className="w-48 shrink-0">
              <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-2 flex flex-col gap-1">
                {TABS.map(({ id, label, icon: Icon }) => (
                  <button
                    key={id}
                    onClick={() => setActiveTab(id)}
                    className={`flex items-center gap-2.5 px-3 py-2.5 rounded-lg text-[13px] font-medium transition-all w-full text-left ${
                      activeTab === id
                        ? 'bg-[#10b981] text-black'
                        : 'text-slate-400 hover:text-white hover:bg-[#1e2025]'
                    }`}
                  >
                    <Icon size={16} />
                    {label}
                  </button>
                ))}
              </div>
            </aside>

            {/* Right content */}
            <div className="flex-1 min-w-0 flex flex-col gap-4">

              {/* ── PROFILE ── */}
              {activeTab === 'profile' && (
                <>
                  <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-6">
                    <h2 className="font-semibold text-white text-base mb-5">User Profile</h2>

                    {/* Avatar + info */}
                    <div className="flex items-center gap-5 mb-6">
                      <div className="w-16 h-16 rounded-full bg-[#10b981]/10 border border-[#10b981]/40 text-[#10b981] flex items-center justify-center text-2xl font-black">
                        {initials}
                      </div>
                      <div>
                        <h3 className="font-bold text-white text-lg">{user?.username ?? '—'}</h3>
                        <p className="text-slate-400 text-[13px] mb-2">{user?.email ?? '—'}</p>
                        <span className="inline-block px-2 py-0.5 rounded text-[10px] font-semibold bg-[#10b981]/10 text-[#10b981] border border-[#10b981]/20">
                          Account: Simulation
                        </span>
                      </div>
                    </div>

                    {/* Read-only fields */}
                    <div className="grid grid-cols-2 gap-4">
                      {[
                        { label: 'Username',     value: user?.username ?? '—' },
                        { label: 'Email Address', value: user?.email    ?? '—' },
                        { label: 'Member Since',  value: new Date().toLocaleDateString('en-US', { month: 'long', year: 'numeric' }) },
                        { label: 'Account Type',  value: 'Paper Trading'       },
                      ].map(({ label, value }) => (
                        <div key={label} className="flex flex-col gap-1.5">
                          <label className="text-[11px] text-slate-400 uppercase tracking-wider font-semibold">{label}</label>
                          <input
                            readOnly
                            value={value}
                            className="w-full bg-[#1e2025] border border-[#1f2229] rounded-lg px-4 py-2.5 text-slate-300 text-sm font-mono focus:outline-none cursor-not-allowed opacity-70"
                          />
                        </div>
                      ))}
                    </div>
                  </div>

                  {/* Danger zone */}
                  <div className="bg-[#111318] border border-red-500/20 rounded-xl p-6">
                    <div className="flex items-center gap-2 mb-2">
                      <AlertTriangle size={16} className="text-red-400" />
                      <h2 className="font-semibold text-red-400 text-base">Danger Zone</h2>
                    </div>
                    <p className="text-slate-400 text-[13px] mb-4">
                      Permanently delete your account and all simulation data. This action cannot be undone.
                    </p>
                    <button className="px-5 py-2 rounded-lg border border-red-500/50 text-red-400 hover:bg-red-500/10 text-sm font-medium transition-colors">
                      Delete Account
                    </button>
                  </div>
                </>
              )}

              {/* ── SECURITY ── */}
              {activeTab === 'security' && (
                <>
                  {/* Change password */}
                  <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-6">
                    <h2 className="font-semibold text-white text-base mb-5">Change Password</h2>
                    <div className="flex flex-col gap-4 max-w-md">
                      <PasswordField label="Current Password"     value={oldPw}     onChange={setOldPw}     />
                      <PasswordField label="New Password"         value={newPw}     onChange={setNewPw}     />
                      <PasswordField label="Confirm New Password" value={confirmPw} onChange={setConfirmPw} />
                      <button
                        onClick={handleChangePassword}
                        disabled={pwLoading}
                        className="mt-1 px-5 py-2 w-max bg-[#10b981] hover:bg-[#0e9f6e] disabled:opacity-50 text-black font-bold text-sm rounded-lg transition-colors shadow-[0_0_12px_rgba(16,185,129,0.25)]"
                      >
                        {pwLoading ? 'Updating…' : 'Update Password'}
                      </button>
                    </div>
                  </div>

                  {/* 2FA */}
                  <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5 flex items-center justify-between">
                    <div>
                      <div className="flex items-center gap-2 mb-1">
                        <h2 className="font-semibold text-white text-base">Two-Factor Authentication</h2>
                        <span className="px-2 py-0.5 rounded text-[10px] uppercase font-semibold bg-[#1e2025] text-slate-500 border border-[#1f2229]">
                          Coming Soon
                        </span>
                      </div>
                      <p className="text-slate-400 text-[13px]">Add an extra layer of security to your account.</p>
                    </div>
                    <div className="opacity-40 cursor-not-allowed">
                      <div className="relative w-11 h-6 rounded-full bg-[#1e2025] border border-[#1f2229]">
                        <span className="absolute top-0.5 left-1 w-4 h-4 bg-slate-500 rounded-full" />
                      </div>
                    </div>
                  </div>

                  {/* Active sessions */}
                  <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5">
                    <div className="flex items-center justify-between mb-4">
                      <h2 className="font-semibold text-white text-base">Active Sessions</h2>
                      <button
                        onClick={handleLogoutAll}
                        className="flex items-center gap-1.5 text-[12px] text-red-400 hover:text-red-300 transition-colors"
                      >
                        <ShieldOff size={13} />
                        Logout All Devices
                      </button>
                    </div>
                    <div className="flex items-center justify-between p-3.5 bg-[#1e2025] border border-[#1f2229] rounded-lg">
                      <div className="flex items-center gap-3">
                        <Monitor size={18} className="text-slate-400" />
                        <div>
                          <p className="text-white text-[13px] font-medium">Chrome on Windows</p>
                          <p className="text-slate-500 text-[11px] font-mono mt-0.5">Current Session</p>
                        </div>
                      </div>
                      <span className="text-[11px] text-[#10b981] font-semibold font-mono">● Active Now</span>
                    </div>
                  </div>
                </>
              )}

              {/* ── PREFERENCES ── */}
              {activeTab === 'preferences' && (
                <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-6">
                  <h2 className="font-semibold text-white text-base mb-5">Display &amp; Interface</h2>
                  <div className="flex flex-col divide-y divide-[#1f2229]/40">
                    {[
                      { label: 'Compact Mode',    desc: 'Reduce padding in data tables for higher information density.',          on: compactMode,  set: markChanged(setCompactMode)  },
                      { label: 'Show Balances',   desc: 'Display portfolio value and available funds in top bar.',                on: showBalances, set: markChanged(setShowBalances)  },
                      { label: 'Sound Effects',   desc: 'Play audio cues for filled orders and price alerts.',                   on: soundEffects, set: markChanged(setSoundEffects)  },
                      { label: 'Auto-refresh Data',desc: 'Keep order book and recent trades updating in real-time.', border: true, on: autoRefresh,  set: markChanged(setAutoRefresh)   },
                    ].map(({ label, desc, on, set }) => (
                      <div key={label} className="flex items-center gap-4 py-4">
                        <div className="flex-1">
                          <p className="text-white font-medium text-[13px]">{label}</p>
                          <p className="text-slate-400 text-[12px] mt-0.5">{desc}</p>
                        </div>
                        <div className="shrink-0">
                          <Toggle on={on} onChange={set} />
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* ── NOTIFICATIONS ── */}
              {activeTab === 'notifications' && (
                <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-6">
                  <h2 className="font-semibold text-white text-base mb-5">Notification Preferences</h2>
                  <div className="flex flex-col divide-y divide-[#1f2229]/40">
                    {[
                      { label: 'Email Summaries',      desc: 'Receive daily simulation performance reports.',                       on: emailSummaries,     set: markChanged(setEmailSummaries)     },
                      { label: 'Price Alerts',          desc: 'In-app notifications when your price targets are hit.',              on: priceAlerts,        set: markChanged(setPriceAlerts)        },
                      { label: 'Trade Confirmations',   desc: 'Toast notifications when orders are filled or rejected.',            on: tradeConfirmations, set: markChanged(setTradeConfirmations) },
                      { label: 'System Updates',        desc: 'Important simulator maintenance and feature announcements.',         on: systemUpdates,      set: markChanged(setSystemUpdates)      },
                    ].map(({ label, desc, on, set }) => (
                      <div key={label} className="flex items-center gap-4 py-4">
                        <div className="flex-1">
                          <p className="text-white font-medium text-[13px]">{label}</p>
                          <p className="text-slate-400 text-[12px] mt-0.5">{desc}</p>
                        </div>
                        <div className="shrink-0">
                          <Toggle on={on} onChange={set} />
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}

            </div>
          </div>
        </main>
      </div>
    </div>
  )
}
