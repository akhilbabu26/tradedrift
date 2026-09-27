import { useCallback } from 'react'
import { useOrdersPageData } from '../../hooks/useOrdersPageData'
import OrdersHeader from '../../components/orders/OrdersHeader'
import OrdersStats from '../../components/orders/OrdersStats'
import OpenOrdersSection from '../../components/orders/OpenOrdersSection'
import OrderHistorySection from '../../components/orders/OrderHistorySection'
import TradeFillsSection from '../../components/orders/TradeFillsSection'
import OrdersFooter from '../../components/orders/OrdersFooter'

export default function OrdersPage() {
  const {
    openOrders,
    orderHistory,
    tradeFills,
    kpis,
    loading,
    isDemoData,
    cancellingId,
    wsStatus,
    handleCancelOrder,
  } = useOrdersPageData()

  // Smooth scroll handler for the top navigation tabs
  const handleScrollToSection = useCallback((sectionId: string) => {
    const el = document.getElementById(sectionId)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' })
      el.classList.add('ring-1', 'ring-[#10b981]/50')
      setTimeout(() => {
        el.classList.remove('ring-1', 'ring-[#10b981]/50')
      }, 1200)
    }
  }, [])

  return (
    <div className="flex-1 overflow-y-auto min-h-0 bg-[#0a0b0e] text-[#f5f7fa] flex flex-col justify-between">
      <div className="max-w-[1600px] w-full mx-auto px-4 lg:px-6 py-6 flex flex-col gap-6">
        {/* Subtle Demo State Notice when running in dev without backend */}
        {isDemoData && (
          <div className="bg-[#111318] border border-amber-500/30 rounded-lg px-4 py-2.5 flex items-center justify-between text-xs text-amber-400 select-none">
            <span className="flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-amber-400 animate-pulse" />
              Viewing paper trading order history. Connect to the TradeDrift API backend for live account persistence.
            </span>
          </div>
        )}

        {/* 1. Page Header */}
        <OrdersHeader wsStatus={wsStatus} />

        {/* 2. 4 KPI Metric Cards */}
        <OrdersStats kpis={kpis} loading={loading} />

        {/* 3. Open Orders Section */}
        <OpenOrdersSection
          orders={openOrders}
          cancellingId={cancellingId}
          onCancel={handleCancelOrder}
          onScrollToSection={handleScrollToSection}
        />

        {/* 4. Order History Section */}
        <OrderHistorySection history={orderHistory} />

        {/* 5. Trade Fills Section */}
        <TradeFillsSection fills={tradeFills} />
      </div>

      <OrdersFooter />
    </div>
  )
}
