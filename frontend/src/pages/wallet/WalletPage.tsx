import { useRef, useCallback } from 'react'
import { useWalletData } from '../../hooks/useWalletData'
import WalletHeader from '../../components/wallet/WalletHeader'
import WalletSummary from '../../components/wallet/WalletSummary'
import TopUpVirtualUSDTCard from '../../components/wallet/TopUpVirtualUSDTCard'
import WhyTopUp from '../../components/wallet/WhyTopUp'
import WalletAssets from '../../components/wallet/WalletAssets'
import TopUpPanel from '../../components/wallet/TopUpPanel'
import TopUpHistory from '../../components/wallet/TopUpHistory'
import WalletFooter from '../../components/wallet/WalletFooter'

export default function WalletPage() {
  const {
    enrichedAssets,
    summary,
    dailyUsage,
    topUpHistory,
    loading,
    isDemoData,
    submittingTopUp,
    initiateTopUp,
  } = useWalletData()

  const topUpPanelRef = useRef<HTMLDivElement>(null)

  const handleScrollToTopUp = useCallback(() => {
    if (topUpPanelRef.current) {
      topUpPanelRef.current.scrollIntoView({ behavior: 'smooth', block: 'start' })
      // Highlight briefly
      topUpPanelRef.current.classList.add('ring-2', 'ring-[#10b981]')
      setTimeout(() => {
        topUpPanelRef.current?.classList.remove('ring-2', 'ring-[#10b981]')
      }, 1500)
    }
  }, [])

  return (
    <div className="flex-1 overflow-y-auto min-h-0 bg-[#0a0b0e] text-[#f5f7fa] flex flex-col justify-between">
      <div className="max-w-[1600px] w-full mx-auto px-4 lg:px-6 py-6 flex flex-col gap-6">
        {/* Demo Data Banner when offline/fallback */}
        {isDemoData && (
          <div className="bg-[#111318] border border-amber-500/30 rounded-lg px-4 py-2.5 flex items-center justify-between text-xs text-amber-400">
            <span className="flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-amber-400 animate-pulse" />
              Viewing paper trading demo balances. Connect to the TradeDrift API backend for live account persistence.
            </span>
          </div>
        )}

        {/* 1. Page Header */}
        <WalletHeader />

        {/* 2. Top Metric / Action Cards (3 columns on lg+) */}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
          <WalletSummary summary={summary} loading={loading} />
          <TopUpVirtualUSDTCard
            dailyUsage={dailyUsage}
            onTopUpClick={handleScrollToTopUp}
          />
          <WhyTopUp />
        </div>

        {/* 3. Main 2-Column Section (Assets Table + Top-Up Panel) */}
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start lg:items-stretch">
          {/* Assets Table (approx 65% / 8 cols) */}
          <div className="lg:col-span-8 flex flex-col">
            <WalletAssets
              assets={enrichedAssets}
              loading={loading}
              onTopUpClick={handleScrollToTopUp}
            />
          </div>

          {/* Top-Up Panel (approx 35% / 4 cols) */}
          <div className="lg:col-span-4 flex flex-col" ref={topUpPanelRef}>
            <div id="top-up-panel-wrapper" className="transition-all duration-300 rounded-xl h-full flex flex-col">
              <TopUpPanel
                dailyUsage={dailyUsage}
                onPay={initiateTopUp}
                submitting={submittingTopUp}
              />
            </div>
          </div>
        </div>

        {/* 4. Top-Up Transaction Ledger */}
        <TopUpHistory history={topUpHistory} />
      </div>

      <WalletFooter />
    </div>
  )
}
