import { useState } from 'react'
import { X, Copy, Check, QrCode, ArrowUpRight, ArrowDownLeft, ShieldCheck, AlertCircle } from 'lucide-react'
import toast from 'react-hot-toast'

interface DepositModalProps {
  isOpen: boolean
  asset: string
  onClose: () => void
}

export function DepositModal({ isOpen, asset, onClose }: DepositModalProps) {
  const [copied, setCopied] = useState(false)
  const depositAddress = `0x71C...48a29_${asset.toLowerCase()}_dep`

  if (!isOpen) return null

  const handleCopy = () => {
    navigator.clipboard.writeText(depositAddress)
    setCopied(true)
    toast.success('Deposit address copied')
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/80 backdrop-blur-sm select-none">
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-2xl w-full max-w-md overflow-hidden shadow-2xl animate-in fade-in zoom-in-95 duration-150">
        {/* Header */}
        <div className="h-14 border-b border-[#1b2230] px-5 flex items-center justify-between bg-[#07090e]/60">
          <div className="flex items-center gap-2 text-white font-bold text-sm">
            <ArrowDownLeft size={16} className="text-[#00e676]" />
            <span>Deposit {asset}</span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-slate-400 hover:text-white p-1 rounded-lg transition-colors"
          >
            <X size={16} />
          </button>
        </div>

        {/* Content */}
        <div className="p-6 space-y-5">
          {/* Network Notice */}
          <div className="flex items-start gap-2.5 p-3 rounded-xl bg-[#00e676]/10 border border-[#00e676]/20 text-xs text-slate-300">
            <ShieldCheck size={16} className="text-[#00e676] flex-shrink-0 mt-0.5" />
            <span>
              Send only <strong className="text-white">{asset}</strong> to this address. Testnet funds arrive instantaneously.
            </span>
          </div>

          {/* QR Code Placeholder Graphic */}
          <div className="flex flex-col items-center justify-center p-6 bg-[#07090e] border border-[#1b2230] rounded-xl">
            <div className="w-32 h-32 bg-white rounded-lg p-2 flex items-center justify-center shadow-inner">
              <QrCode size={112} className="text-black" />
            </div>
            <span className="text-[11px] text-slate-400 mt-2 font-mono">Scan QR to Deposit</span>
          </div>

          {/* Deposit Address Box */}
          <div className="space-y-1.5">
            <label className="text-[11px] text-slate-400 font-medium">Deposit Address (ERC-20 / Native)</label>
            <div className="flex items-center justify-between px-3 py-2.5 rounded-xl bg-[#07090e] border border-[#1b2230]">
              <span className="font-mono text-xs text-white truncate">{depositAddress}</span>
              <button
                type="button"
                onClick={handleCopy}
                className="flex items-center gap-1 text-xs text-[#00e5ff] hover:text-[#00b4d8] ml-2 font-medium"
              >
                {copied ? <Check size={13} className="text-[#00e676]" /> : <Copy size={13} />}
                <span>{copied ? 'Copied' : 'Copy'}</span>
              </button>
            </div>
          </div>
        </div>

        {/* Footer */}
        <div className="p-4 border-t border-[#1b2230] bg-[#07090e]/40 flex justify-end">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 rounded-xl text-xs font-bold bg-[#141a26] text-slate-300 border border-[#1b2230] hover:text-white hover:border-slate-500 transition-colors"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  )
}

interface WithdrawModalProps {
  isOpen: boolean
  asset: string
  availableBalance: number
  onClose: () => void
  onWithdrawSuccess: (asset: string, amount: number) => void
}

export function WithdrawModal({
  isOpen,
  asset,
  availableBalance,
  onClose,
  onWithdrawSuccess,
}: WithdrawModalProps) {
  const [address, setAddress] = useState('')
  const [amount, setAmount] = useState('')
  const [loading, setLoading] = useState(false)

  if (!isOpen) return null

  const handleWithdraw = (e: React.FormEvent) => {
    e.preventDefault()
    const num = parseFloat(amount)
    if (!address.trim()) {
      toast.error('Please enter a destination address')
      return
    }
    if (isNaN(num) || num <= 0) {
      toast.error('Please enter a valid withdrawal amount')
      return
    }
    if (num > availableBalance) {
      toast.error(`Insufficient ${asset} balance`)
      return
    }

    setLoading(true)
    setTimeout(() => {
      setLoading(false)
      onWithdrawSuccess(asset, num)
      toast.success(`Withdrawal of ${num} ${asset} submitted`)
      onClose()
    }, 800)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/80 backdrop-blur-sm select-none">
      <div className="bg-[#0e121b] border border-[#1b2230] rounded-2xl w-full max-w-md overflow-hidden shadow-2xl animate-in fade-in zoom-in-95 duration-150">
        {/* Header */}
        <div className="h-14 border-b border-[#1b2230] px-5 flex items-center justify-between bg-[#07090e]/60">
          <div className="flex items-center gap-2 text-white font-bold text-sm">
            <ArrowUpRight size={16} className="text-[#ff3366]" />
            <span>Withdraw {asset}</span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-slate-400 hover:text-white p-1 rounded-lg transition-colors"
          >
            <X size={16} />
          </button>
        </div>

        {/* Content Form */}
        <form onSubmit={handleWithdraw} className="p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-[11px] text-slate-400 font-medium">Destination Address</label>
            <input
              type="text"
              placeholder={`Enter recipient ${asset} address`}
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              className="w-full px-3 py-2 rounded-xl bg-[#07090e] border border-[#1b2230] text-xs text-white placeholder-slate-500 focus:outline-none focus:border-[#00e5ff]/50 font-mono"
            />
          </div>

          <div className="space-y-1.5">
            <div className="flex items-center justify-between text-[11px]">
              <span className="text-slate-400 font-medium">Amount</span>
              <span className="text-slate-400 font-mono">
                Avail: <strong className="text-white">{availableBalance.toFixed(4)} {asset}</strong>
              </span>
            </div>
            <div className="relative">
              <input
                type="number"
                step="any"
                placeholder="0.00"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                className="w-full px-3 py-2 pr-16 rounded-xl bg-[#07090e] border border-[#1b2230] text-xs text-white placeholder-slate-500 focus:outline-none focus:border-[#00e5ff]/50 font-mono"
              />
              <button
                type="button"
                onClick={() => setAmount(availableBalance.toString())}
                className="absolute right-2 top-1/2 -translate-y-1/2 px-2 py-0.5 rounded text-[10px] font-bold text-[#00e5ff] bg-[#00e5ff]/10 hover:bg-[#00e5ff]/20 transition-colors"
              >
                MAX
              </button>
            </div>
          </div>

          <div className="flex items-center gap-2 p-3 rounded-xl bg-amber-400/10 border border-amber-400/20 text-xs text-amber-300">
            <AlertCircle size={15} className="flex-shrink-0" />
            <span>Network transaction fee: 0.0000 {asset} (Testnet free)</span>
          </div>

          <div className="pt-2 flex justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 rounded-xl text-xs font-bold bg-[#141a26] text-slate-300 border border-[#1b2230] hover:text-white transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading}
              className="px-4 py-2 rounded-xl text-xs font-bold bg-[#ff3366] text-white hover:bg-[#e62e5c] transition-all shadow-[0_0_12px_rgba(255,51,102,0.25)]"
            >
              {loading ? 'Processing...' : `Confirm Withdraw`}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
