export default function SettingsHeader() {
  return (
    <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-3 mb-6">
      <div>
        <h1 className="text-2xl font-bold text-[#f5f7fa] tracking-tight">Settings</h1>
        <p className="text-xs text-slate-400 mt-1">
          Manage your account credentials, security, and active sessions.
        </p>
      </div>

      <div className="hidden sm:block text-right">
        <p className="text-xs italic text-slate-400 leading-relaxed">
          &ldquo;Same discipline here,<br />
          for a safer tomorrow.&rdquo;
        </p>
      </div>
    </div>
  )
}
