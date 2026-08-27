import { useState, useEffect, useCallback, useMemo } from 'react'
import { ChevronDown, Loader2 } from 'lucide-react'
import toast from 'react-hot-toast'
import AppLayout from '../components/layout/AppLayout'
import OrdersKpiCards from '../components/orders/OrdersKpiCards'
import OrdersFilterToolbar from '../components/orders/OrdersFilterToolbar'
import OpenOrdersTable, { type OrderRowItem } from '../components/orders/OpenOrdersTable'
import TriggerOrdersTable, { type TriggerOrderItem } from '../components/orders/TriggerOrdersTable'
import { orderApi, type Order } from '../api/order'

export default function OrdersPage() {
  const [openOrders, setOpenOrders] = useState<OrderRowItem[]>([])
  const [triggerOrders, setTriggerOrders] = useState<TriggerOrderItem[]>([])
  const [filledTodayOrders, setFilledTodayOrders] = useState<Order[]>([])
  const [loading, setLoading] = useState(true)
  const [activeTab, setActiveTab] = useState('open')
  const [selectedMarket, setSelectedMarket] = useState('ALL')
  const [selectedSide, setSelectedSide] = useState('ALL')
  const [selectedType, setSelectedType] = useState('ALL')
  const [rowsPerPage, setRowsPerPage] = useState('10')
  const [currentPage, setCurrentPage] = useState(1)

  // Fetch live backend orders
  const fetchLiveOrders = useCallback(async () => {
    try {
      setLoading(true)
      const [apiOpenOrders, apiFilledOrders] = await Promise.all([
        orderApi.listOrders({ status: 'OPEN' }).catch(() => []),
        orderApi.listOrders({ status: 'FILLED' }).catch(() => []),
      ])

      if (apiOpenOrders) {
        const mapped: OrderRowItem[] = apiOpenOrders.map((o: Order) => {
          const price = parseFloat(String(o.price || '0').replace(/,/g, '')) || 0
          const qty = parseFloat(String(o.quantity || '0').replace(/,/g, '')) || 0
          const filled = parseFloat(String(o.filled_quantity || '0').replace(/,/g, '')) || 0
          const cleanMarketId = (o.market_id || 'BTC-USDT').replace('_', '-')
          const base = cleanMarketId.split('-')[0] || 'BTC'
          const isBtc = base === 'BTC'
          const isEth = base === 'ETH'
          const parsedDate = o.created_at ? new Date(o.created_at) : new Date()
          const safeDateStr = isNaN(parsedDate.getTime())
            ? new Date().toLocaleTimeString()
            : parsedDate.toLocaleString('en-US', {
                month: 'short',
                day: 'numeric',
                year: 'numeric',
                hour: '2-digit',
                minute: '2-digit',
                second: '2-digit',
                hour12: false,
              }) + ' UTC'

          return {
            id: o.id,
            orderId: `ord_${(o.id || '').substring(0, 16)}`,
            marketId: cleanMarketId,
            marketSymbol: `${base}/USDT`,
            iconChar: isBtc ? '₿' : isEth ? 'Ξ' : 'S',
            iconBg: isBtc
              ? 'bg-[#f7931a]/15 border-[#f7931a]/30'
              : isEth
              ? 'bg-[#627eea]/15 border-[#627eea]/30'
              : 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
            iconText: isBtc ? 'text-[#f7931a]' : isEth ? 'text-[#627eea]' : 'text-[#00e5ff]',
            side: (o.side === 'BUY' ? 'BUY' : 'SELL') as 'BUY' | 'SELL',
            type: o.order_type || 'LIMIT',
            price: price.toLocaleString('en-US', { minimumFractionDigits: 2 }),
            priceNum: price,
            filledQty: filled,
            totalQty: qty,
            assetSymbol: base,
            totalUsd: `$${(price * qty).toLocaleString('en-US', { minimumFractionDigits: 2 })}`,
            timeUtc: safeDateStr,
          }
        })
        setOpenOrders(mapped)
      }

      if (apiFilledOrders) {
        setFilledTodayOrders(apiFilledOrders)
      }
    } catch {
      // keep state
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchLiveOrders()
  }, [fetchLiveOrders])

  // Filter open orders
  const filteredOrders = useMemo(() => {
    return openOrders.filter((o) => {
      const matchMarket = selectedMarket === 'ALL' || o.marketId === selectedMarket
      const matchSide = selectedSide === 'ALL' || o.side === selectedSide
      const matchType = selectedType === 'ALL' || o.type === selectedType
      return matchMarket && matchSide && matchType
    })
  }, [openOrders, selectedMarket, selectedSide, selectedType])

  // Handle single order cancellation
  const handleCancelOrder = async (id: string) => {
    try {
      await orderApi.cancelOrder(id)
      setOpenOrders((prev) => prev.filter((o) => o.id !== id))
      toast.success('Order cancelled successfully')
    } catch {
      toast.error('Failed to cancel order')
    }
  }

  // Handle trigger order cancellation
  const handleCancelTrigger = (id: string) => {
    setTriggerOrders((prev) => prev.filter((t) => t.id !== id))
    toast.success('Trigger order cancelled successfully')
  }

  // Handle cancel all orders
  const handleCancelAll = async () => {
    if (openOrders.length === 0) {
      toast.error('No active open orders to cancel')
      return
    }
    try {
      await Promise.allSettled(openOrders.map((o) => orderApi.cancelOrder(o.id)))
      setOpenOrders([])
      toast.success('All open orders cancelled')
    } catch {
      toast.error('Could not cancel all orders')
    }
  }

  // Aggregate KPI metrics
  const totalLockedValue = useMemo(() => {
    return openOrders.reduce((sum, o) => sum + o.priceNum * o.totalQty, 0)
  }, [openOrders])

  const filledTodayVolume = useMemo(() => {
    return filledTodayOrders.reduce((sum, o) => {
      const p = parseFloat(o.price || '0')
      const q = parseFloat(o.filled_quantity || o.quantity || '0')
      return sum + p * q
    }, 0)
  }, [filledTodayOrders])

  return (
    <AppLayout>
      <div className="flex flex-col space-y-6 max-w-[1920px] mx-auto select-none pb-12">
        {/* ── 1. Page Header ── */}
        <div className="flex flex-col">
          <h1 className="text-xl lg:text-2xl font-black text-white tracking-tight font-sans">
            Orders Central
          </h1>
          <p className="text-xs text-slate-400 font-sans mt-0.5">
            Manage active resting orders, conditional triggers, and executions
          </p>
        </div>

        {/* ── 2. Top KPI Cards ── */}
        <OrdersKpiCards
          openOrdersCount={openOrders.length}
          lockedValueUsd={totalLockedValue}
          triggerOrdersCount={triggerOrders.length}
          totalExposureUsd={0}
          filledTodayCount={filledTodayOrders.length}
          filledTodayVolumeUsd={filledTodayVolume}
        />

        {/* ── 3. Filter Toolbar ── */}
        <OrdersFilterToolbar
          selectedMarket={selectedMarket}
          onMarketChange={setSelectedMarket}
          selectedSide={selectedSide}
          onSideChange={setSelectedSide}
          selectedType={selectedType}
          onTypeChange={setSelectedType}
          onCancelAll={handleCancelAll}
        />

        {/* ── 4. Open Orders Tabbed Table ── */}
        {loading ? (
          <div className="py-16 bg-[#0e121b] border border-[#1b2230] rounded-xl flex items-center justify-center text-slate-400">
            <Loader2 className="animate-spin mr-2" size={20} />
            <span>Loading orders...</span>
          </div>
        ) : (
          <OpenOrdersTable
            orders={filteredOrders}
            activeTab={activeTab}
            onTabChange={setActiveTab}
            onCancelOrder={handleCancelOrder}
          />
        )}

        {/* ── 5. Trigger Orders Section ── */}
        <TriggerOrdersTable
          triggerOrders={triggerOrders}
          onCancelTrigger={handleCancelTrigger}
        />

        {/* ── 6. Pagination Footer ── */}
        <div className="p-4 border border-[#1b2230] rounded-xl bg-[#0e121b] flex flex-wrap items-center justify-between gap-3 text-xs text-slate-400 font-sans shadow-xl">
          <div>
            Showing 1 to {filteredOrders.length} of {filteredOrders.length} open orders
          </div>

          <div className="flex items-center gap-4">
            {/* Pagination Controls */}
            <div className="flex items-center gap-1 font-mono text-xs">
              <button
                type="button"
                disabled={currentPage === 1}
                onClick={() => setCurrentPage(1)}
                className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center text-slate-500 disabled:opacity-50"
              >
                ‹
              </button>
              <button
                type="button"
                className="w-7 h-7 rounded-lg bg-[#00e5ff]/15 border border-[#00e5ff]/40 text-[#00e5ff] font-bold flex items-center justify-center shadow-[0_0_8px_rgba(0,229,255,0.15)]"
              >
                1
              </button>
              <button
                type="button"
                disabled={true}
                className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center text-slate-500 disabled:opacity-50"
              >
                ›
              </button>
            </div>

            {/* Rows per page dropdown */}
            <div className="flex items-center gap-2 text-xs">
              <span>Rows per page:</span>
              <div className="relative">
                <select
                  value={rowsPerPage}
                  onChange={(e) => setRowsPerPage(e.target.value)}
                  className="appearance-none bg-[#07090e] border border-[#1b2230] rounded-lg px-3 py-1 pr-7 text-xs text-white font-mono focus:outline-none focus:border-[#00e5ff]/50 cursor-pointer"
                >
                  <option value="10">10</option>
                  <option value="25">25</option>
                  <option value="50">50</option>
                </select>
                <ChevronDown
                  size={12}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none"
                />
              </div>
            </div>
          </div>
        </div>
      </div>
    </AppLayout>
  )
}
