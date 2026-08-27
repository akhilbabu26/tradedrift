import { useState } from 'react'
import { Droplet, ShieldCheck, Check, Loader2 } from 'lucide-react'

interface FaucetItem {
  asset: string
  symbol: string
  amountStr: string
  label: string
  iconChar: string
  iconBg: string
  iconText: string
  numericAmount: number
}

const FAUCET_ASSETS: FaucetItem[] = [
  {
    asset: 'USDT',
    symbol: 'USDT',
    amountStr: '+10,000 USDT',
    label: 'Claim 10,000 USDT',
    iconChar: '₮',
    iconBg: 'bg-[#00e676]/15 border-[#00e676]/30',
    iconText: 'text-[#00e676]',
    numericAmount: 10000,
  },
  {
    asset: 'BTC',
    symbol: 'BTC',
    amountStr: '+1.0 BTC',
    label: 'Claim 1.0 BTC',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    numericAmount: 1.0,
  },
  {
    asset: 'ETH',
    symbol: 'ETH',
    amountStr: '+10.0 ETH',
    label: 'Claim 10.0 ETH',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    numericAmount: 10.0,
  },
  {
    asset: 'SOL',
    symbol: 'SOL',
    amountStr: '+50.0 SOL',
    label: 'Claim 50.0 SOL',
    iconChar: 'S',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    numericAmount: 50.0,
  },
]

interface TestnetFaucetCardProps {
  onClaim: (asset: string, amount: number) => Promise<void>
}

export default function TestnetFaucetCard({ onClaim }: TestnetFaucetCardProps) {
  const [loadingAsset, setLoadingAsset] = useState<string | null>(null)
  const [claimedAsset, setClaimedAsset] = useState<string | null>(null)

  const handleClaim = async (item: FaucetItem) => {
    if (loadingAsset) return
    setLoadingAsset(item.asset)
    try {
      await onClaim(item.asset, item.numericAmount)
      setClaimedAsset(item.asset)
      setTimeout(() => {
        setClaimedAsset((prev) => (prev === item.asset ? null : prev))
      }, 2500)
    } finally {
      setLoadingAsset(null)
    }
  }

  return (
    <div className="bg-[#0e121b] border border-[#00e676]/30 rounded-xl p-5 flex flex-col justify-between shadow-[0_0_25px_rgba(0,230,118,0.08)] relative overflow-hidden select-none">
      {/* Subtle top-right ambient emerald glow */}
      <div className="absolute top-0 right-0 w-36 h-36 bg-[#00e676]/10 blur-3xl pointer-events-none rounded-full" />

      {/* ── Top Header: Title & Description ── */}
      <div>
        <div className="flex items-center gap-2.5 mb-2">
          <div className="w-8 h-8 rounded-xl bg-[#00e676]/15 border border-[#00e676]/30 text-[#00e676] flex items-center justify-center shadow-[0_0_12px_rgba(0,230,118,0.2)]">
            <Droplet size={16} />
          </div>
          <h2 className="text-sm font-bold text-white font-sans tracking-tight">
            1-Click Testnet Faucet
          </h2>
        </div>

        <p className="text-xs text-slate-400 font-sans leading-relaxed mb-4">
          Instant testnet assets for demo trading. <br className="hidden sm:block" />
          No verification. 1-click. Instant.
        </p>

        {/* ── 4 Faucet Action Rows ── */}
        <div className="space-y-2.5">
          {FAUCET_ASSETS.map((item) => {
            const isLoading = loadingAsset === item.asset
            const isSuccess = claimedAsset === item.asset

            return (
              <div
                key={item.asset}
                className="flex items-center justify-between p-2.5 rounded-xl bg-[#07090e] border border-[#1b2230] hover:border-[#00e676]/40 transition-all group"
              >
                {/* Left: Crypto Icon + Amount + Label */}
                <div className="flex items-center gap-3">
                  <div
                    className={`w-8 h-8 rounded-full flex items-center justify-center font-bold text-xs flex-shrink-0 border ${item.iconBg} ${item.iconText}`}
                  >
                    {item.iconChar}
                  </div>
                  <div className="flex flex-col">
                    <span className="font-bold text-white font-mono text-xs tracking-tight">
                      {item.amountStr}
                    </span>
                    <span className="text-[10px] text-slate-400 font-sans">
                      {item.label}
                    </span>
                  </div>
                </div>

                {/* Right: Claim Now Button */}
                <button
                  type="button"
                  disabled={isLoading}
                  onClick={() => handleClaim(item)}
                  className={`px-3 py-1.5 rounded-lg text-xs font-semibold font-sans transition-all flex items-center gap-1.5 ${
                    isSuccess
                      ? 'bg-[#00e676] text-black shadow-[0_0_12px_rgba(0,230,118,0.4)]'
                      : 'bg-[#00e676]/10 text-[#00e676] border border-[#00e676]/30 hover:bg-[#00e676]/20 shadow-[0_0_10px_rgba(0,230,118,0.15)] active:scale-95'
                  }`}
                >
                  {isLoading ? (
                    <>
                      <Loader2 size={12} className="animate-spin" />
                      <span>Minting...</span>
                    </>
                  ) : isSuccess ? (
                    <>
                      <Check size={12} className="stroke-[3]" />
                      <span>Claimed!</span>
                    </>
                  ) : (
                    <span>Claim Now</span>
                  )}
                </button>
              </div>
            )
          })}
        </div>
      </div>

      {/* ── Footer Notice ── */}
      <div className="mt-4 pt-3 border-t border-[#1b2230]/60 flex items-center gap-1.5 text-[11px] text-slate-400 font-sans">
        <ShieldCheck size={13} className="text-[#00e676] flex-shrink-0" />
        <span>Testnet assets have no real-world value.</span>
      </div>
    </div>
  )
}
