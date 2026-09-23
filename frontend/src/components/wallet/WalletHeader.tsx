import { Info } from 'lucide-react'

export default function WalletHeader() {
  return (
    <div className="flex flex-col md:flex-row md:items-center md:justify-between gap-4 select-none">
      {/* Page Title & Subtitle */}
      <div>
        <h1 className="text-3xl font-black text-[#f5f7fa] tracking-tight font-sans" style={{ color: '#f5f7fa' }}>
          Wallet
        </h1>
        <p className="text-sm text-slate-300 mt-1">
          Manage your simulated trading funds. Top up with real money and keep trading.
        </p>
      </div>

      {/* Right-side Informational Disclaimer */}
      <div className="flex items-center gap-3 px-4 py-3 rounded-xl bg-[#111318] border border-[#1e2530] shadow-sm max-w-md">
        <div className="w-7 h-7 rounded-full bg-sky-500/10 border border-sky-500/20 flex items-center justify-center flex-shrink-0">
          <Info size={15} className="text-sky-400" />
        </div>
        <p className="text-xs text-slate-300 leading-snug">
          Top-ups purchase virtual trading credits for the simulator. They have no cash withdrawal value.
        </p>
      </div>
    </div>
  )
}
