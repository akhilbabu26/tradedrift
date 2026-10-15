import { Info } from 'lucide-react'
import { RaisedSurface } from '../common/Surface'

export default function WalletHeader() {
  return (
    <div className="flex flex-col md:flex-row md:items-center md:justify-between gap-4 select-none">
      {/* Page Title & Subtitle */}
      <div>
        <h1 className="text-3xl font-black text-[#F2F3F5] tracking-tight font-sans">
          Wallet
        </h1>
        <p className="text-sm text-[#777E87] mt-1">
          Manage your simulated trading funds. Top up with real money and keep trading.
        </p>
      </div>

      {/* Right-side Informational Disclaimer */}
      <RaisedSurface
        variant="raised-subtle"
        className="flex items-center gap-3 !py-2.5 !px-4 max-w-md shadow-sm"
      >
        <div className="w-7 h-7 rounded-lg bg-sky-500/10 border border-sky-500/20 flex items-center justify-center flex-shrink-0">
          <Info size={15} className="text-sky-400" />
        </div>
        <p className="text-xs text-[#A7ADB5] leading-snug">
          Top-ups purchase virtual trading credits for the simulator. They have no cash withdrawal value.
        </p>
      </RaisedSurface>
    </div>
  )
}
