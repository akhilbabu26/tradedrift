import { User as UserIcon } from 'lucide-react'

export default function ProfileHeader() {
  return (
    <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-3 mb-6">
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-xl bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 flex items-center justify-center flex-shrink-0 mt-0.5">
          <UserIcon size={20} />
        </div>
        <div>
          <h1 className="text-2xl font-bold text-[#f5f7fa] tracking-tight">Profile</h1>
          <p className="text-xs text-slate-400 mt-1">
            Manage your identity and view your trading profile.
          </p>
        </div>
      </div>

      <div className="hidden sm:block text-right">
        <p className="text-xs italic text-slate-400 leading-relaxed">
          &ldquo;Trade with discipline,<br />
          grow with consistency.&rdquo;
        </p>
      </div>
    </div>
  )
}
