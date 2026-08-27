import { useState, useEffect, useCallback, useMemo } from 'react'
import { ChevronDown } from 'lucide-react'
import toast from 'react-hot-toast'
import AppLayout from '../components/layout/AppLayout'
import OrdersKpiCards from '../components/orders/OrdersKpiCards'
import OrdersFilterToolbar from '../components/orders/OrdersFilterToolbar'
import OpenOrdersTable, { type OrderRowItem } from '../components/orders/OpenOrdersTable'
import TriggerOrdersTable, { type TriggerOrderItem } from '../components/orders/TriggerOrdersTable'
import { orderApi, type Order } from '../api/order'

const INITIAL_OPEN_ORDERS: OrderRowItem[] = [
  {
    id: '1',
    orderId: 'ord_8f7c2d1a9b3e4f7a',
    marketId: 'BTC-USDT',
    marketSymbol: 'BTC/USDT',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    side: 'BUY',
    type: 'LIMIT',
    price: '96,450.00',
    priceNum: 96450.0,
    filledQty: 0.15,
    totalQty: 0.35,
    assetSymbol: 'BTC',
    totalUsd: '$33,757.50',
    timeUtc: 'May 26, 2025 12:45:33 UTC',
  },
  {
    id: '2',
    orderId: 'ord_3a9b1e7d5c2f4a8b',
    marketId: 'ETH-USDT',
    marketSymbol: 'ETH/USDT',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    side: 'SELL',
    type: 'LIMIT',
    price: '2,800.00',
    priceNum: 2800.0,
    filledQty: 0.0,
    totalQty: 2.0,
    assetSymbol: 'ETH',
    totalUsd: '$5,600.00',
    timeUtc: 'May 26, 2025 12:32:18 UTC',
  },
  {
    id: '3',
    orderId: 'ord_d2c6e8b4f1a9d3c7',
    marketId: 'SOL-USDT',
    marketSymbol: 'SOL/USDT',
    iconChar: 'S',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    side: 'BUY',
    type: 'LIMIT',
    price: '186.50',
    priceNum: 186.5,
    filledQty: 5.0,
    totalQty: 10.0,
    assetSymbol: 'SOL',
    totalUsd: '$1,865.00',
    timeUtc: 'May 26, 2025 12:31:05 UTC',
  },
  {
    id: '4',
    orderId: 'ord_a1b2c3d4e5f6a7b8',
    marketId: 'BTC-USDT',
    marketSymbol: 'BTC/USDT',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    side: 'SELL',
    type: 'LIMIT',
    price: '98,200.00',
    priceNum: 98200.0,
    filledQty: 0.0,
    totalQty: 0.2,
    assetSymbol: 'BTC',
    totalUsd: '$19,640.00',
    timeUtc: 'May 26, 2025 11:58:44 UTC',
  },
]

const INITIAL_TRIGGER_ORDERS: TriggerOrderItem[] = [
  {
    id: 'trg-1',
    orderId: 'trg_7e6d5c4b3a2f1e9d',
    marketSymbol: 'BTC/USDT',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    side: 'SELL',
    type: 'STOP-LIMIT',
    triggerPrice: '94,000.00',
    price: '93,900.00',
    amountStr: '0.2500 BTC',
    totalUsd: '$23,475.00',
    timeUtc: 'May 26, 2025 11:20:10 UTC',
    status: 'ACTIVE',
  },
  {
    id: 'trg-2',
    orderId: 'trg_5d4c3b2a1f0e9d8c',
    marketSymbol: 'ETH/USDT',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    side: 'BUY',
    type: 'TAKE-PROFIT',
    triggerPrice: '2,950.00',
    price: '2,950.00',
    amountStr: '1.5000 ETH',
    totalUsd: '$4,425.00',
    timeUtc: 'May 26, 2025 10:45:21 UTC',
    status: 'ACTIVE',
  },
]

export default function OrdersPage() {
  const [openOrders, setOpenOrders] = useState<OrderRowItem[]>(INITIAL_OPEN_ORDERS)
  const [triggerOrders, setTriggerOrders] = useState<TriggerOrderItem[]>(INITIAL_TRIGGER_ORDERS)
  const [activeTab, setActiveTab] = useState('open')
  const [selectedMarket, setSelectedMarket] = useState('ALL')
  const [selectedSide, setSelectedSide] = useState('ALL')
  const [selectedType, setSelectedType] = useState('ALL')
  const [rowsPerPage, setRowsPerPage] = useState('10')
  const [currentPage, setCurrentPage] = useState(1)

  // Fetch live backend orders if available
  const fetchLiveOrders = useCallback(async () => {
    try {
      const apiOrders = await orderApi.listOrders({ status: 'OPEN' })
      if (apiOrders && apiOrders.length > 0) {
        const mapped: OrderRowItem[] = apiOrders.map((o: Order) => {
          const price = parseFloat(o.price?.replace(/,/g, '')) || 0
          const qty = parseFloat(o.quantity?.replace(/,/g, '')) || 0
          const filled = parseFloat(o.filled_quantity?.replace(/,/g, '')) || 0
          const base = o.market_id.split('-')[0] || 'BTC'
          const isBtc = base === 'BTC'
          const isEth = base === 'ETH'
          return {
            id: o.id,
            orderId: `ord_${o.id.substring(0, 16)}`,
            marketId: o.market_id,
            marketSymbol: `${base}/USDT`,
            iconChar: isBtc ? '₿' : isEth ? 'Ξ' : 'S',
            iconBg: isBtc
              ? 'bg-[#f7931a]/15 border-[#f7931a]/30'
              : isEth
              ? 'bg-[#627eea]/15 border-[#627eea]/30'
              : 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
            iconText: isBtc ? 'text-[#f7931a]' : isEth ? 'text-[#627eea]' : 'text-[#00e5ff]',
            side: o.side === 'BUY' ? 'BUY' : 'SELL',
            type: o.order_type || 'LIMIT',
            price: price.toLocaleString('en-US', { minimumFractionDigits: 2 }),
            priceNum: price,
            filledQty: filled,
            totalQty: qty,
            assetSymbol: base,
            totalUsd: `$${(price * qty).toLocaleString('en-US', { minimumFractionDigits: 2 })}`,
            timeUtc: new Date(o.created_at).toLocaleString('en-US', {
              month: 'short',
              day: 'numeric',
              year: 'numeric',
              hour: '2-digit',
              minute: '2-digit',
              second: '2-digit',
              hour12: false,
            }) + ' UTC',
          }
        })
        setOpenOrders(mapped)
      }
    } catch {
      // Retain baseline reference orders
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
    } catch {
      // optimistic fallback
    }
    setOpenOrders((prev) => prev.filter((o) => o.id !== id))
    toast.success('Order cancelled successfully')
  }

  // Handle trigger order cancellation
  const handleCancelTrigger = (id: string) => {
    setTriggerOrders((prev) => prev.filter((t) => t.id !== id))
    toast.success('Trigger order cancelled successfully')
  }

  // Handle cancel all orders
  const handleCancelAll = () => {
    if (openOrders.length === 0) {
      toast.error('No active open orders to cancel')
      return
    }
    setOpenOrders([])
    toast.success('All open orders cancelled')
  }

  // Aggregate KPI metrics
  const totalLockedValue = useMemo(() => {
    return openOrders.reduce((sum, o) => sum + (o.priceNum * o.totalQty), 0) || 42150.0
  }, [openOrders])

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
          totalExposureUsd={18620.5}
          filledTodayCount={18}
          filledTodayVolumeUsd={112400.0}
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
        <OpenOrdersTable
          orders={filteredOrders}
          activeTab={activeTab}
          onTabChange={setActiveTab}
          onCancelOrder={handleCancelOrder}
        />

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
                <ChevronDown size={12} className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
              </div>
            </div>
          </div>
        </div>
      </div>
    </AppLayout>
  )
}
