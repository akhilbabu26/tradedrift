import type { AssetPerformanceItem } from '../../types/analytics'

interface Props {
  assets: AssetPerformanceItem[]
}

/**
 * Portfolio Performance by Asset — left ~60% panel
 * Displays Unrealized PnL based on current holdings across BTC, ETH, SOL, USDT.
 */
export default function PortfolioPerformanceByAsset({ assets }: Props) {
  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl overflow-hidden flex flex-col">
      {/* Header */}
      <div className="px-5 py-4 border-b border-[#1e2530]">
        <h2 className="text-sm font-semibold text-[#f5f7fa]">
          Portfolio Performance by Asset
        </h2>
        <p className="text-xs text-[#94a3b8] mt-0.5">
          Unrealized PnL based on current holdings
        </p>
      </div>

      {/* Table */}
      <div className="overflow-x-auto flex-1">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-[#1e2530] text-[#64748b]">
              <th className="text-left py-3.5 px-5 font-medium">Asset</th>
              <th className="text-left py-3.5 px-4 font-medium">Holdings</th>
              <th className="text-left py-3.5 px-4 font-medium">Avg. Entry Price</th>
              <th className="text-left py-3.5 px-4 font-medium">Current Price</th>
              <th className="text-left py-3.5 px-4 font-medium">Unrealized PnL</th>
              <th className="text-left py-3.5 px-5 font-medium w-36">Allocation</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#181c24]">
            {assets.map((item) => {
              const isUsdt = item.asset === 'USDT'
              const pnlColorClass = isUsdt
                ? 'text-[#94a3b8]'
                : item.unrealizedPnl.startsWith('-')
                  ? 'text-[#ef4444]'
                  : 'text-[#10b981]'

              return (
                <tr
                  key={item.asset}
                  className="hover:bg-[#151921] transition-colors group"
                >
                  {/* Asset */}
                  <td className="py-4 px-5">
                    <div className="flex items-center gap-2.5">
                      <div
                        className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-bold border ${item.meta.iconBg} ${item.meta.iconBorder} ${item.meta.iconColor} flex-shrink-0`}
                      >
                        {item.meta.symbol}
                      </div>
                      <span className="text-xs font-semibold text-[#f5f7fa]">
                        {item.asset}
                      </span>
                    </div>
                  </td>

                  {/* Holdings */}
                  <td className="py-4 px-4 font-mono text-[#cbd5e1]">
                    {item.holdings}
                  </td>

                  {/* Avg Entry Price */}
                  <td className="py-4 px-4 font-mono text-[#cbd5e1]">
                    {item.avgEntryPrice}
                  </td>

                  {/* Current Price */}
                  <td className="py-4 px-4 font-mono text-[#cbd5e1]">
                    {item.currentPrice}
                  </td>

                  {/* Unrealized PnL (stacked) */}
                  <td className="py-4 px-4">
                    <div className="flex flex-col">
                      <span className={`font-mono font-medium text-xs ${pnlColorClass}`}>
                        {item.unrealizedPnl}
                      </span>
                      <span className={`font-mono text-[10px] ${pnlColorClass}`}>
                        {item.unrealizedPnlPct}
                      </span>
                    </div>
                  </td>

                  {/* Allocation (bar + percentage) */}
                  <td className="py-4 px-5">
                    <div className="flex items-center gap-3">
                      <div className="w-16 h-1.5 bg-[#1e2530] rounded-full overflow-hidden flex-shrink-0">
                        <div
                          className="h-full rounded-full transition-all duration-300"
                          style={{
                            width: `${Math.min(item.allocationPct, 100)}%`,
                            backgroundColor: item.barColor,
                          }}
                        />
                      </div>
                      <span className="font-mono text-xs text-[#cbd5e1] font-medium">
                        {item.allocationPct}%
                      </span>
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
