import { Info } from 'lucide-react'
import { formatUSDT } from '../../../utils/formatters'

interface SimulatorBalanceProps {
  balance: string
  availablePercent: number
  inOrdersPercent: number
  availableUsdt: string
  inOrdersUsdt: string
}

/**
 * Simulator Balance section — second column of Portfolio Overview summary row.
 * Shows balance, allocation bar, and breakdown amounts.
 */
export default function SimulatorBalance({
  balance,
  availablePercent,
  inOrdersPercent,
  availableUsdt,
  inOrdersUsdt,
}: SimulatorBalanceProps) {
  return (
    <div className="flex flex-col gap-2 min-w-0">
      <p className="text-[11px] font-medium text-slate-400 uppercase tracking-wider flex items-center gap-1">
        Simulator Balance
        <Info size={11} className="text-slate-500 cursor-help" aria-label="Virtual simulator balance" />
      </p>
      <p className="text-2xl font-bold text-[#f5f7fa] tracking-tight leading-none font-mono">
        {formatUSDT(balance)}
      </p>

      {/* Allocation bar */}
      <div className="space-y-1.5">
        <div className="flex justify-between text-[11px]">
          <span className="text-[#A7ADB5]">
            Available: <span className="text-[#F2F3F5] font-semibold">{availablePercent}%</span>
          </span>
          <span className="text-[#A7ADB5]">
            In Orders: <span className="text-[#F2F3F5] font-semibold">{inOrdersPercent}%</span>
          </span>
        </div>
        <div className="flex h-1.5 rounded-full overflow-hidden bg-[#090A0C] border border-[#292C30] shadow-[inset_0_1px_2px_rgba(0,0,0,0.8)]">
          <div
            className="bg-[#10B981] transition-all duration-500"
            style={{ width: `${availablePercent}%` }}
            role="progressbar"
            aria-valuenow={availablePercent}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label="Available balance"
          />
          <div
            className="bg-[#3B82F6] transition-all duration-500"
            style={{ width: `${inOrdersPercent}%` }}
            role="progressbar"
            aria-valuenow={inOrdersPercent}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label="In orders"
          />
        </div>
        <div className="flex justify-between text-[11px] font-mono">
          <span className="text-[#10B981]">{formatUSDT(availableUsdt)}</span>
          <span className="text-[#3B82F6]">{formatUSDT(inOrdersUsdt)}</span>
        </div>
      </div>
    </div>
  )
}
