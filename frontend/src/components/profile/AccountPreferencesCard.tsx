import { Sliders, Moon, Database, DollarSign, Globe } from 'lucide-react'
import type { AccountPreferenceItem } from '../../types/profile'

interface AccountPreferencesCardProps {
  preferences: AccountPreferenceItem[]
}

export default function AccountPreferencesCard({ preferences }: AccountPreferencesCardProps) {
  const getIcon = (label: string) => {
    switch (label) {
      case 'Theme':
        return <Moon size={14} className="text-emerald-400" />
      case 'Account Type':
        return <Database size={14} className="text-sky-400" />
      case 'Base Currency':
        return <DollarSign size={14} className="text-amber-400" />
      case 'Language':
        return <Globe size={14} className="text-indigo-400" />
      default:
        return <Sliders size={14} className="text-slate-400" />
    }
  }

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 flex flex-col justify-between transition-colors">
      {/* Header */}
      <div className="flex items-center gap-2 mb-4">
        <Sliders size={15} className="text-emerald-400" />
        <div>
          <h2 className="text-sm font-semibold text-[#f5f7fa] tracking-tight">Account Preferences</h2>
          <p className="text-xs text-slate-400 mt-0.5">Terminal runtime and interface preferences.</p>
        </div>
      </div>

      {/* Preferences Rows */}
      <div className="divide-y divide-[#1e2530] border border-[#1e2530] rounded-lg bg-[#0a0b0e] overflow-hidden">
        {preferences.map((item) => (
          <div
            key={item.label}
            className="flex items-center justify-between px-3.5 py-3 hover:bg-white/[0.02] transition-colors"
          >
            <div className="flex items-center gap-2.5">
              <div className="w-6 h-6 rounded bg-[#111318] border border-[#1e2530] flex items-center justify-center flex-shrink-0">
                {getIcon(item.label)}
              </div>
              <span className="text-xs font-medium text-slate-300">{item.label}</span>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-xs font-mono text-[#f5f7fa] font-medium bg-[#111318] border border-[#1e2530] px-2.5 py-1 rounded">
                {item.value}
              </span>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
