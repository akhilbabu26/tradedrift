import { useEffect, useState, useMemo, useCallback } from 'react'
import toast from 'react-hot-toast'
import AppLayout from '../components/layout/AppLayout'
import TradeMarketHeader from '../components/trading/TradeMarketHeader'
import PriceChart from '../components/trading/PriceChart'
import OrderBook, { type LiveDepthLevel } from '../components/trading/OrderBook'
import RecentTrades, { type LiveTradeItem } from '../components/trading/RecentTrades'
import OrderForm from '../components/trading/OrderForm'
import OrdersPanel from '../components/trading/OrdersPanel'
import MarketDepthChart from '../components/trading/MarketDepthChart'
import { walletApi, type Balance } from '../api/wallet'
import { orderApi, type Order } from '../api/order'
import { marketApi, type Market, type Ticker24h } from '../api/market'
import { wsService } from '../api/ws'

export default function TradePage() {
  // ── State ──────────────────────────────────────────────────────────────────
  const [markets, setMarkets] = useState<Market[]>([])
  const [selectedMarketId, setSelectedMarketId] = useState('BTC-USDT')
  const [ticker, setTicker] = useState<Ticker24h | null>(null)
  const [balances, setBalances] = useState<Balance[]>([])
  const [orders, setOrders] = useState<Order[]>([])
  const [recentTrades, setRecentTrades] = useState<LiveTradeItem[]>([])
  const [loadingOrders, setLoadingOrders] = useState(false)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [wsConnected, setWsConnected] = useState(true)

  // Real-time L2 orderbook state
  const [liveAsks, setLiveAsks] = useState<LiveDepthLevel[]>([])
  const [liveBids, setLiveBids] = useState<LiveDepthLevel[]>([])

  // Order form price
  const [price, setPrice] = useState('96450.00')

  // Current market details
  const currentMarket = useMemo(() => {
    return (
      markets.find((m) => m.id === selectedMarketId) || {
        id: selectedMarketId,
        base_asset: selectedMarketId.split('-')[0] || 'BTC',
        quote_asset: selectedMarketId.split('-')[1] || 'USDT',
        tick_size: '0.01',
        lot_size: '0.0001',
        min_quantity: '0.0001',
        status: 'ACTIVE',
        created_at: '',
        updated_at: '',
      }
    )
  }, [markets, selectedMarketId])

  const baseAsset = currentMarket.base_asset
  const quoteAsset = currentMarket.quote_asset

  // ── REST Fetch ─────────────────────────────────────────────────────────────
  const fetchBalances = useCallback(async () => {
    try {
      const data = await walletApi.getAllBalances()
      setBalances(data)
    } catch {
      // ignore
    }
  }, [])

  const fetchOrders = useCallback(async () => {
    setLoadingOrders(true)
    try {
      const data = await orderApi.listOrders({ market_id: selectedMarketId, limit: 50 })
      setOrders(data)
    } catch {
      // ignore
    } finally {
      setLoadingOrders(false)
    }
  }, [selectedMarketId])

  const fetchMarketData = useCallback(async () => {
    try {
      const [allMarkets, t] = await Promise.all([
        marketApi.getMarkets(),
        marketApi.getTicker(selectedMarketId).catch(() => null),
      ])
      setMarkets(allMarkets)
      if (t) {
        setTicker(t)
        const cleanPrice = String(t.last_price || '').replace(/,/g, '')
        setPrice(cleanPrice || (selectedMarketId === 'ETH-USDT' ? '2780.50' : selectedMarketId === 'SOL-USDT' ? '188.20' : '96450.00'))
      } else {
        setPrice(selectedMarketId === 'ETH-USDT' ? '2780.50' : selectedMarketId === 'SOL-USDT' ? '188.20' : '96450.00')
      }
    } catch {
      setPrice(selectedMarketId === 'ETH-USDT' ? '2780.50' : selectedMarketId === 'SOL-USDT' ? '188.20' : '96450.00')
    }
  }, [selectedMarketId])

  useEffect(() => {
    fetchMarketData()
    fetchBalances()
    fetchOrders()
  }, [fetchMarketData, fetchBalances, fetchOrders])

  // ── WebSocket Subscriptions ────────────────────────────────────────────────
  useEffect(() => {
    const unsubOrderbook = wsService.subscribe(`market:orderbook:${selectedMarketId}`, (data: {
      asks?: [string, string][]
      bids?: [string, string][]
    }) => {
      setWsConnected(true)
      if (data?.asks) {
        let runningTotal = 0
        const parsedAsks: LiveDepthLevel[] = data.asks.map(([p, s]: [string, string]) => {
          const sizeNum = parseFloat(String(s).replace(/,/g, '')) || 0
          runningTotal += sizeNum
          return {
            price: parseFloat(String(p).replace(/,/g, '')).toFixed(2),
            size: sizeNum.toFixed(4),
            total: runningTotal.toFixed(4),
            depth: Math.min(100, (runningTotal / 25) * 100),
          }
        })
        setLiveAsks(parsedAsks.reverse())
      }

      if (data?.bids) {
        let runningTotal = 0
        const parsedBids: LiveDepthLevel[] = data.bids.map(([p, s]: [string, string]) => {
          const sizeNum = parseFloat(String(s).replace(/,/g, '')) || 0
          runningTotal += sizeNum
          return {
            price: parseFloat(String(p).replace(/,/g, '')).toFixed(2),
            size: sizeNum.toFixed(4),
            total: runningTotal.toFixed(4),
            depth: Math.min(100, (runningTotal / 25) * 100),
          }
        })
        setLiveBids(parsedBids)
      }
    })

    const unsubTrades = wsService.subscribe(`market:trades:${selectedMarketId}`, (t: {
      trade_id?: string
      price: string
      quantity: string
      side?: 'BUY' | 'SELL'
      executed_at?: number | string
    }) => {
      setWsConnected(true)
      if (!t?.price) return

      const cleanP = parseFloat(String(t.price).replace(/,/g, '')) || 0
      const cleanQ = parseFloat(String(t.quantity).replace(/,/g, '')) || 0
      const timeMs = typeof t.executed_at === 'number' ? t.executed_at : Date.now()

      const newItem: LiveTradeItem = {
        id: t.trade_id || Math.random().toString(),
        price: cleanP.toFixed(2),
        quantity: cleanQ.toFixed(4),
        time: new Date(timeMs).toLocaleTimeString('en-US', {
          hour12: false,
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit',
        }),
        side: t.side === 'BUY' ? 'BUY' : 'SELL',
      }

      setRecentTrades((prev) => [newItem, ...prev.slice(0, 49)])

      // Update ticker last price
      setTicker((prev) => (prev ? { ...prev, last_price: cleanP.toFixed(2) } : prev))

      // Refresh orders and balances on match
      fetchBalances()
      fetchOrders()
    })

    const unsubTicker = wsService.subscribe(`market:ticker:${selectedMarketId}`, (tick: Ticker24h) => {
      setWsConnected(true)
      if (tick) {
        setTicker(tick)
      }
    })

    return () => {
      unsubOrderbook()
      unsubTrades()
      unsubTicker()
    }
  }, [selectedMarketId, fetchBalances, fetchOrders])

  // ── Order Handlers ─────────────────────────────────────────────────────────
  const handleSubmitOrder = async (orderData: {
    side: 'BUY' | 'SELL'
    type: 'LIMIT' | 'MARKET' | 'STOP_LIMIT'
    price: string
    quantity: string
  }) => {
    setIsSubmitting(true)
    const cleanPrice = (orderData.price || '').replace(/,/g, '').trim()
    const cleanQty = (orderData.quantity || '').replace(/,/g, '').trim()

    try {
      const placed = await orderApi.createOrder({
        market_id: selectedMarketId,
        side: orderData.side,
        order_type: orderData.type === 'STOP_LIMIT' ? 'LIMIT' : orderData.type,
        price: cleanPrice,
        quantity: cleanQty,
      })
      toast.success(`Order placed: ${orderData.side} ${cleanQty} ${baseAsset}`)
      setOrders((prev) => [placed, ...prev])
      fetchBalances()
    } catch (err: unknown) {
      const serverMessage =
        (err as { response?: { data?: { message?: string; error?: string } } })?.response?.data
          ?.message ||
        (err as { response?: { data?: { message?: string; error?: string } } })?.response?.data
          ?.error
      const msg = serverMessage || 'Order placement failed: check microservice connection or funds.'
      toast.error(msg)
    } finally {
      setIsSubmitting(false)
    }
  }

  const handleCancelOrder = async (orderId: string) => {
    try {
      await orderApi.cancelOrder(orderId)
      toast.success('Order cancelled successfully')
      setOrders((prev) => prev.filter((o) => o.id !== orderId))
      fetchBalances()
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Failed to cancel order'
      toast.error(msg)
    }
  }

  return (
    <AppLayout>
      <div className="flex flex-col space-y-4 max-w-[1920px] mx-auto select-none pb-12">
        {/* ── Top Bar: Horizontal Multi-Market Pills & 24h Stats Ribbon ── */}
        <TradeMarketHeader
          markets={markets}
          selectedMarketId={selectedMarketId}
          onSelectMarket={(id) => {
            setSelectedMarketId(id)
            setLiveAsks([])
            setLiveBids([])
            setRecentTrades([])
          }}
          ticker={ticker}
          wsConnected={wsConnected}
        />

        {/* ── Main 3-Column Institutional Trading Terminal Grid ── */}
        <div className="grid grid-cols-1 xl:grid-cols-12 gap-4 items-start">
          {/* ═══════════════════════════════════════════════════════════════════
              COLUMN 1 (Left - 6 Cols / 50% Width): Candlestick Chart + Orders Panel
          ═══════════════════════════════════════════════════════════════════ */}
          <div className="xl:col-span-6 flex flex-col space-y-4 min-w-0">
            {/* Candlestick Chart Area */}
            <div className="h-[460px] flex flex-col">
              <PriceChart selectedMarketId={selectedMarketId} ticker={ticker} />
            </div>

            {/* Bottom Orders Panel (Open Orders with Cancel action, History, Fills) */}
            <OrdersPanel
              orders={orders}
              onCancelOrder={handleCancelOrder}
              loading={loadingOrders}
            />
          </div>

          {/* ═══════════════════════════════════════════════════════════════════
              COLUMN 2 (Middle - 3 Cols / 25% Width): Order Book + Recent Trades Tape
          ═══════════════════════════════════════════════════════════════════ */}
          <div className="xl:col-span-3 flex flex-col space-y-4 min-w-0">
            {/* Level-2 Order Book Ladder */}
            <OrderBook
              baseAsset={baseAsset}
              quoteAsset={quoteAsset}
              asks={liveAsks}
              bids={liveBids}
              lastPrice={ticker?.last_price || (selectedMarketId === 'BTC-USDT' ? '96,450.00' : '2,780.50')}
              onSelectPrice={(p) => setPrice(p)}
            />

            {/* Real-Time Executed Trades Tape */}
            <div className="h-[260px] flex flex-col">
              <RecentTrades
                baseAsset={baseAsset}
                quoteAsset={quoteAsset}
                trades={recentTrades}
              />
            </div>
          </div>

          {/* ═══════════════════════════════════════════════════════════════════
              COLUMN 3 (Right - 3 Cols / 25% Width): Order Form + Market Depth Curve
          ═══════════════════════════════════════════════════════════════════ */}
          <div className="xl:col-span-3 flex flex-col min-w-0">
            {/* Order Placement Form */}
            <OrderForm
              baseAsset={baseAsset}
              quoteAsset={quoteAsset}
              price={price}
              setPrice={setPrice}
              balances={balances}
              onSubmitOrder={handleSubmitOrder}
              isSubmitting={isSubmitting}
            />

            {/* Stepped Cumulative Market Depth Curve */}
            <MarketDepthChart
              baseAsset={baseAsset}
              lastPrice={ticker?.last_price || (selectedMarketId === 'BTC-USDT' ? '96,450.00' : '2,780.50')}
            />
          </div>
        </div>
      </div>
    </AppLayout>
  )
}
