import React, { useEffect, useState, useMemo, useCallback, useRef } from 'react'
import {
  Star, ChevronDown, Wifi, Settings, Maximize2,
  TrendingUp, TrendingDown,
  Clock, CheckCircle, RefreshCw, Activity
} from 'lucide-react'
import toast from 'react-hot-toast'
import Sidebar from '../components/dashboard/Sidebar'
import { walletApi, type Balance } from '../api/wallet'
import { orderApi, type Order } from '../api/order'
import { marketApi, type Market, type Ticker24h } from '../api/market'
import { wsService } from '../api/ws'

interface LiveDepthLevel {
  price: string
  size: string
  total: string
  depth: number
}

interface LiveTradeItem {
  id: string
  price: string
  quantity: string
  time: string
  side: 'BUY' | 'SELL'
}

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
  const [wsConnected, setWsConnected] = useState(false)

  // Real-time L2 orderbook state
  const [liveAsks, setLiveAsks] = useState<LiveDepthLevel[]>([])
  const [liveBids, setLiveBids] = useState<LiveDepthLevel[]>([])

  // Order form state
  const [side, setSide] = useState<'BUY' | 'SELL'>('BUY')
  const [orderType, setOrderType] = useState<'LIMIT' | 'MARKET'>('LIMIT')
  const [price, setPrice] = useState('64230.50')
  const [quantity, setQuantity] = useState('')
  const [timeframe, setTimeframe] = useState('1H')
  const [bottomTab, setBottomTab] = useState<'open' | 'history' | 'trades' | 'funds'>('open')
  const [rightPanelTab, setRightPanelTab] = useState<'book' | 'trades'>('book')
  const [postOnly, setPostOnly] = useState(false)
  const [reduceOnly, setReduceOnly] = useState(false)
  const [isMarketDropdownOpen, setIsMarketDropdownOpen] = useState(false)

  const priceFlashRef = useRef<'up' | 'down' | null>(null)

  // Current market details
  const currentMarket = useMemo(() => {
    return markets.find((m) => m.id === selectedMarketId) || {
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
  }, [markets, selectedMarketId])

  const baseAsset = currentMarket.base_asset
  const quoteAsset = currentMarket.quote_asset

  // ── REST Fetch & Resync ────────────────────────────────────────────────────
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
      const allMarkets = await marketApi.getMarkets()
      if (allMarkets.length > 0) setMarkets(allMarkets)
    } catch {
      setMarkets([
        { id: 'BTC-USDT', base_asset: 'BTC', quote_asset: 'USDT', tick_size: '0.01', lot_size: '0.0001', min_quantity: '0.0001', status: 'ACTIVE', created_at: '', updated_at: '' },
        { id: 'ETH-USDT', base_asset: 'ETH', quote_asset: 'USDT', tick_size: '0.01', lot_size: '0.001', min_quantity: '0.001', status: 'ACTIVE', created_at: '', updated_at: '' },
        { id: 'SOL-USDT', base_asset: 'SOL', quote_asset: 'USDT', tick_size: '0.01', lot_size: '0.01', min_quantity: '0.01', status: 'ACTIVE', created_at: '', updated_at: '' },
      ])
    }

    try {
      const t = await marketApi.getTicker(selectedMarketId)
      if (t && t.last_price) {
        setTicker(t)
        if (orderType === 'LIMIT' && (!price || price === '64230.50')) {
          setPrice(t.last_price)
        }
      }
    } catch {
      setTicker({
        market_id: selectedMarketId,
        last_price: selectedMarketId.startsWith('ETH') ? '2450.00' : selectedMarketId.startsWith('SOL') ? '145.20' : '64230.50',
        high_24h: selectedMarketId.startsWith('ETH') ? '2520.00' : selectedMarketId.startsWith('SOL') ? '152.00' : '65100.00',
        low_24h: selectedMarketId.startsWith('ETH') ? '2380.00' : selectedMarketId.startsWith('SOL') ? '138.50' : '62850.25',
        volume_24h: '45231.84',
        quote_volume_24h: '289124012.00',
        price_change_24h_percent: '+2.45',
      })
    }
  }, [selectedMarketId, orderType, price])

  // ── WebSocket Real-Time Streaming Subscriptions ───────────────────────────
  useEffect(() => {
    fetchMarketData()
    fetchBalances()
    fetchOrders()

    // 1. Subscribe to Live L2 OrderBook Stream
    const unsubsOrderBook = wsService.subscribe(`market:orderbook:${selectedMarketId}`, (data) => {
      setWsConnected(true)
      if (data && (data.asks || data.bids)) {
        // Calculate cumulative totals & depths
        let askAcc = 0
        const parsedAsks: LiveDepthLevel[] = (data.asks || []).slice(0, 7).map((a: [string, string]) => {
          const size = parseFloat(a[1]) || 0
          askAcc += size
          return { price: a[0], size: a[1], total: askAcc.toFixed(3), depth: Math.min(100, Math.round(askAcc * 15)) }
        })

        let bidAcc = 0
        const parsedBids: LiveDepthLevel[] = (data.bids || []).slice(0, 7).map((b: [string, string]) => {
          const size = parseFloat(b[1]) || 0
          bidAcc += size
          return { price: b[0], size: b[1], total: bidAcc.toFixed(3), depth: Math.min(100, Math.round(bidAcc * 15)) }
        })

        setLiveAsks(parsedAsks)
        setLiveBids(parsedBids)
      }
    })

    // 2. Subscribe to Live Executed Trades Stream
    const unsubsTrades = wsService.subscribe(`market:trades:${selectedMarketId}`, (data) => {
      setWsConnected(true)
      if (data && data.price) {
        const newTrade: LiveTradeItem = {
          id: data.tradeId || String(Date.now()),
          price: data.price,
          quantity: data.quantity,
          time: new Date(data.executedAt || Date.now()).toLocaleTimeString(),
          side: data.side === 'SELL' ? 'SELL' : 'BUY',
        }
        setRecentTrades((prev) => [newTrade, ...prev.slice(0, 30)])

        // Flash last price
        setTicker((prev) => {
          if (!prev) return null
          const oldPrice = parseFloat(prev.last_price || '0')
          const newPrice = parseFloat(data.price || '0')
          priceFlashRef.current = newPrice >= oldPrice ? 'up' : 'down'
          return { ...prev, last_price: data.price }
        })

        // Refresh orders and balances on match
        fetchBalances()
        fetchOrders()
      }
    })

    // 3. Subscribe to 24h Ticker Stream
    const unsubsTicker = wsService.subscribe(`market:ticker:${selectedMarketId}`, (data) => {
      setWsConnected(true)
      if (data && data.lastPrice) {
        setTicker((prev) => ({
          market_id: data.marketId || selectedMarketId,
          last_price: data.lastPrice,
          high_24h: data.high24h || prev?.high_24h || '0',
          low_24h: data.low24h || prev?.low_24h || '0',
          volume_24h: data.volume24h || prev?.volume_24h || '0',
          quote_volume_24h: data.quoteVolume24h || prev?.quote_volume_24h || '0',
          price_change_24h_percent: data.priceChange24hPercent || prev?.price_change_24h_percent || '+0.00',
        }))
      }
    })

    // Register on-reconnect REST resync hook and status hook
    const unhookReconnect = wsService.onReconnect(() => {
      fetchMarketData()
      fetchBalances()
      fetchOrders()
    })

    const unhookStatus = wsService.onStatus((connected) => {
      setWsConnected(connected)
    })

    return () => {
      unsubsOrderBook()
      unsubsTrades()
      unsubsTicker()
      unhookReconnect()
      unhookStatus()
    }
  }, [selectedMarketId, fetchMarketData, fetchBalances, fetchOrders])

  // Available Balances
  const quoteBalance = useMemo(() => {
    const b = balances.find((x) => x.asset === quoteAsset)
    return parseFloat(b?.availableBalance || '0')
  }, [balances, quoteAsset])

  const baseBalance = useMemo(() => {
    const b = balances.find((x) => x.asset === baseAsset)
    return parseFloat(b?.availableBalance || '0')
  }, [balances, baseAsset])

  const activeBalance = side === 'BUY' ? quoteBalance : baseBalance
  const activeBalanceAsset = side === 'BUY' ? quoteAsset : baseAsset

  // Total order value
  const numPrice = parseFloat(price) || 0
  const numQty = parseFloat(quantity) || 0
  const orderTotal = numPrice * numQty
  const estimatedFee = orderTotal * 0.001 // 0.1%

  // Handle Percentage Fill
  const handlePercentage = (pct: number) => {
    if (side === 'BUY') {
      if (numPrice <= 0) return
      const maxSpend = activeBalance * (pct / 100)
      const calculatedQty = maxSpend / numPrice
      setQuantity(calculatedQty.toFixed(4))
    } else {
      const calculatedQty = activeBalance * (pct / 100)
      setQuantity(calculatedQty.toFixed(4))
    }
  }

  // Handle Order Submit
  const handlePlaceOrder = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!quantity || parseFloat(quantity) <= 0) {
      toast.error('Please enter a valid quantity')
      return
    }
    if (orderType === 'LIMIT' && (!price || parseFloat(price) <= 0)) {
      toast.error('Please enter a valid limit price')
      return
    }

    setIsSubmitting(true)
    try {
      const res = await orderApi.createOrder({
        market_id: selectedMarketId,
        side,
        order_type: orderType,
        price: orderType === 'LIMIT' ? price : undefined,
        quantity,
      })
      toast.success(`${side} order created for ${res.quantity} ${baseAsset}!`)
      setQuantity('')
      fetchBalances()
      fetchOrders()
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Failed to place order'
      toast.error(msg)
    } finally {
      setIsSubmitting(false)
    }
  }

  // Handle Order Cancel
  const handleCancelOrder = async (orderId: string) => {
    try {
      await orderApi.cancelOrder(orderId)
      toast.success('Order cancelled')
      fetchOrders()
      fetchBalances()
    } catch (err: unknown) {
      const msg =
        (err as { response?: { data?: { message?: string } } })?.response?.data?.message ||
        'Failed to cancel order'
      toast.error(msg)
    }
  }

  // Fallback depth if live stream is empty
  const currentPriceNum = parseFloat(ticker?.last_price || '64230.50')
  const isUp = (ticker?.price_change_24h_percent || '+').startsWith('+')

  const fallbackAsks: LiveDepthLevel[] = useMemo(() => [
    { price: (currentPriceNum + 10.0).toFixed(2), size: '1.250', total: '5.650', depth: 85 },
    { price: (currentPriceNum + 8.5).toFixed(2), size: '0.850', total: '4.400', depth: 60 },
    { price: (currentPriceNum + 5.0).toFixed(2), size: '2.100', total: '3.550', depth: 75 },
    { price: (currentPriceNum + 2.0).toFixed(2), size: '0.450', total: '1.450', depth: 40 },
    { price: (currentPriceNum + 1.0).toFixed(2), size: '1.000', total: '1.000', depth: 25 },
  ], [currentPriceNum])

  const fallbackBids: LiveDepthLevel[] = useMemo(() => [
    { price: (currentPriceNum - 0.5).toFixed(2), size: '0.500', total: '0.500', depth: 30 },
    { price: (currentPriceNum - 2.0).toFixed(2), size: '1.200', total: '1.700', depth: 55 },
    { price: (currentPriceNum - 5.5).toFixed(2), size: '0.800', total: '2.500', depth: 45 },
    { price: (currentPriceNum - 10.0).toFixed(2), size: '3.500', total: '6.000', depth: 90 },
    { price: (currentPriceNum - 15.0).toFixed(2), size: '2.100', total: '8.100', depth: 70 },
  ], [currentPriceNum])

  const displayAsks = liveAsks.length > 0 ? liveAsks : fallbackAsks
  const displayBids = liveBids.length > 0 ? liveBids : fallbackBids

  // Filtered orders for bottom tabs
  const openOrders = useMemo(() => orders.filter((o) => o.status === 'OPEN' || o.status === 'PARTIALLY_FILLED'), [orders])
  const historyOrders = useMemo(() => orders.filter((o) => o.status !== 'OPEN' && o.status !== 'PARTIALLY_FILLED'), [orders])

  return (
    <div className="bg-[#0a0b0e] text-[#d4e4fa] h-screen w-screen overflow-hidden flex font-sans select-none">
      {/* Sidebar */}
      <Sidebar />

      {/* Main Terminal Workspace */}
      <div className="flex-1 flex flex-col min-w-0 h-screen bg-[#1f2229] gap-[1px]">

        {/* ── 1. Top Ticker Header Bar ── */}
        <header className="h-14 bg-[#111318] border-b border-[#1f2229] flex items-center px-4 justify-between flex-shrink-0 z-30">
          <div className="flex items-center gap-6">
            {/* Market Pair Dropdown */}
            <div className="relative">
              <div
                onClick={() => setIsMarketDropdownOpen(!isMarketDropdownOpen)}
                className="flex items-center gap-2 cursor-pointer hover:bg-white/5 px-2 py-1.5 rounded transition-colors"
              >
                <Star size={16} className="text-amber-400 fill-amber-400/20" />
                <h1 className="text-lg font-bold text-white tracking-wide">{selectedMarketId}</h1>
                <ChevronDown size={14} className="text-slate-400" />
                <span className="px-2 py-0.5 bg-[#1f2229] text-slate-400 font-mono text-xs rounded ml-1">SPOT</span>
              </div>

              {/* Dropdown Menu */}
              {isMarketDropdownOpen && (
                <div className="absolute top-full left-0 mt-1 w-56 bg-[#111318] border border-[#1f2229] rounded-xl shadow-2xl z-50 py-2">
                  <div className="px-3 py-1 text-[11px] font-semibold text-slate-500 uppercase tracking-wider">
                    Select Market
                  </div>
                  {markets.map((m) => (
                    <div
                      key={m.id}
                      onClick={() => {
                        setSelectedMarketId(m.id)
                        setIsMarketDropdownOpen(false)
                        setLiveAsks([])
                        setLiveBids([])
                        setRecentTrades([])
                      }}
                      className={`px-4 py-2 hover:bg-[#1e2025] cursor-pointer flex justify-between items-center text-xs font-mono ${
                        selectedMarketId === m.id ? 'bg-[#10b981]/10 text-[#10b981] font-bold' : 'text-white'
                      }`}
                    >
                      <span>{m.id}</span>
                      <span className="text-slate-500 text-[10px]">{m.base_asset}/{m.quote_asset}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Last Price */}
            <div className="flex flex-col">
              <span className={`font-mono text-lg font-bold ${isUp ? 'text-[#10b981]' : 'text-red-400'}`}>
                {ticker?.last_price || '64,230.50'}
              </span>
              <span className="font-mono text-[11px] text-slate-400">${ticker?.last_price || '64,230.50'}</span>
            </div>

            {/* 24h Change */}
            <div className="flex flex-col">
              <span className="text-[11px] text-slate-400">24h Change</span>
              <span className={`font-mono text-xs font-semibold flex items-center gap-0.5 ${isUp ? 'text-[#10b981]' : 'text-red-400'}`}>
                {isUp ? <TrendingUp size={12} /> : <TrendingDown size={12} />}
                {ticker?.price_change_24h_percent || '+2.45%'}
              </span>
            </div>

            {/* 24h High */}
            <div className="hidden lg:flex flex-col">
              <span className="text-[11px] text-slate-400">24h High</span>
              <span className="font-mono text-xs text-white">{ticker?.high_24h || '65,100.00'}</span>
            </div>

            {/* 24h Low */}
            <div className="hidden lg:flex flex-col">
              <span className="text-[11px] text-slate-400">24h Low</span>
              <span className="font-mono text-xs text-white">{ticker?.low_24h || '62,850.25'}</span>
            </div>

            {/* 24h Volume */}
            <div className="hidden xl:flex flex-col">
              <span className="text-[11px] text-slate-400">24h Vol ({baseAsset})</span>
              <span className="font-mono text-xs text-white">{ticker?.volume_24h || '45,231.84'}</span>
            </div>
          </div>

          {/* WebSocket & Status */}
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2 px-2.5 py-1 bg-[#1e2025] rounded-full border border-[#1f2229] text-[11px] font-mono">
              <span className={`w-2 h-2 rounded-full ${wsConnected ? 'bg-[#10b981] animate-pulse' : 'bg-amber-400'}`} />
              <span className="text-slate-400">WS: <span className={wsConnected ? 'text-[#10b981]' : 'text-amber-400'}>{wsConnected ? 'Connected' : 'Connecting'}</span></span>
            </div>
            <button className="p-1.5 hover:bg-white/5 rounded text-slate-400 hover:text-white transition-colors">
              <Settings size={16} />
            </button>
          </div>
        </header>

        {/* ── 2. Middle Trading Area (Chart | Order Book | Order Form) ── */}
        <div className="flex-1 flex min-h-0 gap-[1px] bg-[#1f2229]">

          {/* ── Column A: Main Candlestick Chart ── */}
          <div className="flex-1 bg-[#111318] flex flex-col min-w-0">
            {/* Chart Toolbar */}
            <div className="h-10 border-b border-[#1f2229] flex items-center px-3 justify-between flex-shrink-0">
              <div className="flex items-center gap-1">
                <div className="flex bg-[#1e2025] rounded-lg p-0.5 border border-[#1f2229]/60">
                  {['1m', '5m', '15m', '1H', '4H', '1D', '1W'].map((tf) => (
                    <button
                      key={tf}
                      onClick={() => setTimeframe(tf)}
                      className={`px-2 py-0.5 text-[11px] font-mono rounded transition-colors ${
                        timeframe === tf
                          ? 'bg-[#10b981]/20 text-[#10b981] font-bold'
                          : 'text-slate-400 hover:text-white'
                      }`}
                    >
                      {tf}
                    </button>
                  ))}
                </div>
                <div className="w-px h-4 bg-[#1f2229] mx-2" />
                <span className="text-[11px] text-slate-400 hover:text-white cursor-pointer px-2 py-1 rounded hover:bg-white/5">
                  Indicators (EMA, RSI)
                </span>
              </div>
              <div className="flex items-center gap-2 text-slate-400">
                <button className="p-1 hover:bg-white/5 rounded hover:text-white"><Maximize2 size={14} /></button>
              </div>
            </div>

            {/* Candlestick SVG Container */}
            <div className="flex-1 relative bg-[#0a0b0e] overflow-hidden cursor-crosshair">
              <div
                className="absolute inset-0 opacity-20"
                style={{
                  backgroundImage: 'linear-gradient(to right, #1f2229 1px, transparent 1px), linear-gradient(to bottom, #1f2229 1px, transparent 1px)',
                  backgroundSize: '50px 50px',
                }}
              />

              <svg className="absolute inset-0 w-full h-full" preserveAspectRatio="none" viewBox="0 0 1000 400">
                <text fill="#64748b" fontFamily="monospace" fontSize="10" x="930" y="50">{(currentPriceNum + 400).toFixed(0)}</text>
                <text fill="#64748b" fontFamily="monospace" fontSize="10" x="930" y="150">{(currentPriceNum + 150).toFixed(0)}</text>
                <text fill="#64748b" fontFamily="monospace" fontSize="10" x="930" y="250">{(currentPriceNum - 150).toFixed(0)}</text>
                <text fill="#64748b" fontFamily="monospace" fontSize="10" x="930" y="350">{(currentPriceNum - 400).toFixed(0)}</text>

                {/* Simulated live bars */}
                <line x1="100" x2="100" y1="280" y2="200" stroke="#10b981" strokeWidth="1" />
                <rect x="96" y="220" width="8" height="50" fill="#10b981" />

                <line x1="160" x2="160" y1="260" y2="300" stroke="#ef4444" strokeWidth="1" />
                <rect x="156" y="260" width="8" height="30" fill="#ef4444" />

                <line x1="220" x2="220" y1="310" y2="180" stroke="#10b981" strokeWidth="1" />
                <rect x="216" y="200" width="8" height="80" fill="#10b981" />

                <line x1="280" x2="280" y1="250" y2="120" stroke="#10b981" strokeWidth="1" />
                <rect x="276" y="140" width="8" height="90" fill="#10b981" />

                <line x1="340" x2="340" y1="180" y2="240" stroke="#ef4444" strokeWidth="1" />
                <rect x="336" y="190" width="8" height="40" fill="#ef4444" />

                <line x1="400" x2="400" y1="210" y2="130" stroke="#10b981" strokeWidth="1" />
                <rect x="396" y="150" width="8" height="50" fill="#10b981" />

                <line x1="460" x2="460" y1="160" y2="90" stroke="#10b981" strokeWidth="1" />
                <rect x="456" y="110" width="8" height="45" fill="#10b981" />

                {/* Moving Average Curves */}
                <path d="M0,300 Q140,260 260,180 T500,100" fill="none" opacity="0.8" stroke="#f59e0b" strokeWidth="1.5" />
                <path d="M0,320 Q180,280 340,200 T500,140" fill="none" opacity="0.8" stroke="#3b82f6" strokeWidth="1.5" />

                {/* Current Price Line */}
                <line x1="0" x2="1000" y1="150" y2="150" stroke="#10b981" strokeDasharray="4" strokeWidth="1" />
                <rect x="910" y="138" width="80" height="24" rx="4" fill="#10b981" />
                <text fill="#000" fontFamily="monospace" fontSize="11" fontWeight="bold" x="918" y="154">{ticker?.last_price || '64230.50'}</text>

                {/* Bottom Volume Histogram */}
                <rect x="96" y="360" width="8" height="40" fill="#10b981" opacity="0.4" />
                <rect x="156" y="370" width="8" height="30" fill="#ef4444" opacity="0.4" />
                <rect x="216" y="340" width="8" height="60" fill="#10b981" opacity="0.4" />
                <rect x="276" y="330" width="8" height="70" fill="#10b981" opacity="0.4" />
                <rect x="336" y="365" width="8" height="35" fill="#ef4444" opacity="0.4" />
                <rect x="396" y="350" width="8" height="50" fill="#10b981" opacity="0.4" />
                <rect x="456" y="320" width="8" height="80" fill="#10b981" opacity="0.4" />
              </svg>
            </div>
          </div>

          {/* ── Column B: Order Book / Recent Trades ── */}
          <div className="w-72 bg-[#111318] flex flex-col flex-shrink-0">
            {/* Header Tabs */}
            <div className="h-10 border-b border-[#1f2229] flex items-center px-3 justify-between flex-shrink-0">
              <div className="flex gap-3">
                <button
                  onClick={() => setRightPanelTab('book')}
                  className={`text-xs font-semibold pb-1 transition-colors ${
                    rightPanelTab === 'book' ? 'text-white border-b-2 border-[#10b981]' : 'text-slate-400 hover:text-white'
                  }`}
                >
                  Order Book
                </button>
                <button
                  onClick={() => setRightPanelTab('trades')}
                  className={`text-xs font-semibold pb-1 transition-colors ${
                    rightPanelTab === 'trades' ? 'text-white border-b-2 border-[#10b981]' : 'text-slate-400 hover:text-white'
                  }`}
                >
                  Market Trades ({recentTrades.length})
                </button>
              </div>
            </div>

            {rightPanelTab === 'book' ? (
              <>
                {/* Column Titles */}
                <div className="grid grid-cols-3 px-3 py-1.5 text-[10px] font-mono text-slate-500 border-b border-[#1f2229]/40">
                  <div className="text-left">Price ({quoteAsset})</div>
                  <div className="text-right">Size ({baseAsset})</div>
                  <div className="text-right">Total</div>
                </div>

                {/* Asks (Red / Sell Orders) */}
                <div className="flex-1 overflow-hidden flex flex-col justify-end text-[11px] font-mono pb-1">
                  {displayAsks.map((a, i) => (
                    <div
                      key={i}
                      onClick={() => setPrice(a.price)}
                      className="relative px-3 py-0.5 grid grid-cols-3 hover:bg-white/5 cursor-pointer transition-colors"
                    >
                      <div className="absolute right-0 top-0 bottom-0 bg-red-500/10 z-0" style={{ width: `${a.depth}%` }} />
                      <div className="relative z-10 text-left text-red-400">{a.price}</div>
                      <div className="relative z-10 text-right text-slate-200">{a.size}</div>
                      <div className="relative z-10 text-right text-slate-500">{a.total}</div>
                    </div>
                  ))}
                </div>

                {/* Spread Divider */}
                <div className="py-2 border-y border-[#1f2229] flex items-center justify-between px-3 bg-[#0a0b0e]/60">
                  <div className="flex items-center gap-2">
                    <span className={`font-mono text-sm font-bold ${isUp ? 'text-[#10b981]' : 'text-red-400'}`}>
                      {ticker?.last_price || '64,230.50'}
                    </span>
                    {isUp ? <TrendingUp size={14} className="text-[#10b981]" /> : <TrendingDown size={14} className="text-red-400" />}
                  </div>
                  <span className="text-[10px] font-mono text-slate-500">Live L2 Depth</span>
                </div>

                {/* Bids (Green / Buy Orders) */}
                <div className="flex-1 overflow-hidden flex flex-col text-[11px] font-mono pt-1">
                  {displayBids.map((b, i) => (
                    <div
                      key={i}
                      onClick={() => setPrice(b.price)}
                      className="relative px-3 py-0.5 grid grid-cols-3 hover:bg-white/5 cursor-pointer transition-colors"
                    >
                      <div className="absolute right-0 top-0 bottom-0 bg-[#10b981]/10 z-0" style={{ width: `${b.depth}%` }} />
                      <div className="relative z-10 text-left text-[#10b981]">{b.price}</div>
                      <div className="relative z-10 text-right text-slate-200">{b.size}</div>
                      <div className="relative z-10 text-right text-slate-500">{b.total}</div>
                    </div>
                  ))}
                </div>
              </>
            ) : (
              /* Recent Executed Trades Stream */
              <div className="flex-1 overflow-y-auto font-mono text-xs p-2">
                <div className="grid grid-cols-3 text-[10px] text-slate-500 pb-2 border-b border-[#1f2229]">
                  <span>Price ({quoteAsset})</span>
                  <span className="text-right">Qty ({baseAsset})</span>
                  <span className="text-right">Time</span>
                </div>
                {recentTrades.length === 0 ? (
                  <div className="py-8 text-center text-slate-500 font-sans text-xs">
                    <Activity size={18} className="mx-auto mb-1 opacity-40 animate-pulse" />
                    Waiting for real-time trade executions...
                  </div>
                ) : (
                  recentTrades.map((t) => (
                    <div key={t.id} className="grid grid-cols-3 py-1 text-[11px] border-b border-[#1f2229]/20 hover:bg-white/5">
                      <span className={t.side === 'BUY' ? 'text-[#10b981]' : 'text-red-400'}>{t.price}</span>
                      <span className="text-right text-white">{t.quantity}</span>
                      <span className="text-right text-slate-500 text-[10px]">{t.time}</span>
                    </div>
                  ))
                )}
              </div>
            )}
          </div>

          {/* ── Column C: Order Entry Terminal ── */}
          <div className="w-80 bg-[#111318] flex flex-col flex-shrink-0 p-4">
            {/* Buy / Sell Tabs */}
            <div className="flex p-1 bg-[#1e2025] rounded-xl border border-[#1f2229] gap-1 mb-4">
              <button
                type="button"
                onClick={() => setSide('BUY')}
                className={`flex-1 py-2 rounded-lg font-bold text-xs transition-all ${
                  side === 'BUY'
                    ? 'bg-[#10b981] text-black shadow-[0_0_12px_rgba(16,185,129,0.3)]'
                    : 'text-slate-400 hover:text-white'
                }`}
              >
                Buy {baseAsset}
              </button>
              <button
                type="button"
                onClick={() => setSide('SELL')}
                className={`flex-1 py-2 rounded-lg font-bold text-xs transition-all ${
                  side === 'SELL'
                    ? 'bg-[#ef4444] text-white shadow-[0_0_12px_rgba(239,68,68,0.3)]'
                    : 'text-slate-400 hover:text-white'
                }`}
              >
                Sell {baseAsset}
              </button>
            </div>

            {/* Order Type Selector */}
            <div className="flex border-b border-[#1f2229] gap-4 mb-4 text-xs">
              <button
                type="button"
                onClick={() => setOrderType('LIMIT')}
                className={`pb-2 transition-colors ${
                  orderType === 'LIMIT'
                    ? 'text-white border-b-2 border-[#10b981] font-semibold'
                    : 'text-slate-400 hover:text-white border-b-2 border-transparent'
                }`}
              >
                Limit
              </button>
              <button
                type="button"
                onClick={() => setOrderType('MARKET')}
                className={`pb-2 transition-colors ${
                  orderType === 'MARKET'
                    ? 'text-white border-b-2 border-[#10b981] font-semibold'
                    : 'text-slate-400 hover:text-white border-b-2 border-transparent'
                }`}
              >
                Market
              </button>
            </div>

            {/* Available Balance Box */}
            <div className="flex justify-between items-center text-xs mb-3 font-mono">
              <span className="text-slate-400">Avail:</span>
              <span className="text-white font-bold">{activeBalance.toFixed(4)} {activeBalanceAsset}</span>
            </div>

            {/* Form */}
            <form onSubmit={handlePlaceOrder} className="flex-1 flex flex-col gap-3">
              {orderType === 'LIMIT' && (
                <div className="flex flex-col gap-1">
                  <div className="flex bg-[#0a0b0e] border border-[#1f2229] rounded-lg overflow-hidden focus-within:border-[#10b981] transition-colors">
                    <span className="py-2.5 pl-3 text-slate-500 text-xs w-16 flex items-center">Price</span>
                    <input
                      type="number"
                      step="any"
                      required
                      value={price}
                      onChange={(e) => setPrice(e.target.value)}
                      placeholder="0.00"
                      className="flex-1 bg-transparent border-none text-right font-mono text-white text-xs focus:outline-none pr-3"
                    />
                    <span className="py-2.5 pr-3 text-slate-400 font-mono text-xs flex items-center">{quoteAsset}</span>
                  </div>
                </div>
              )}

              <div className="flex flex-col gap-1">
                <div className="flex bg-[#0a0b0e] border border-[#1f2229] rounded-lg overflow-hidden focus-within:border-[#10b981] transition-colors">
                  <span className="py-2.5 pl-3 text-slate-500 text-xs w-16 flex items-center">Amount</span>
                  <input
                    type="number"
                    step="any"
                    required
                    value={quantity}
                    onChange={(e) => setQuantity(e.target.value)}
                    placeholder="0.00"
                    className="flex-1 bg-transparent border-none text-right font-mono text-white text-xs focus:outline-none pr-3"
                  />
                  <span className="py-2.5 pr-3 text-slate-400 font-mono text-xs flex items-center">{baseAsset}</span>
                </div>
              </div>

              {/* Percentage Buttons */}
              <div className="flex gap-1.5 justify-between my-1">
                {[25, 50, 75, 100].map((pct) => (
                  <button
                    key={pct}
                    type="button"
                    onClick={() => handlePercentage(pct)}
                    className="flex-1 py-1.5 bg-[#1e2025] hover:bg-[#282a2f] border border-[#1f2229] rounded text-[11px] font-mono text-slate-300 hover:text-white transition-colors"
                  >
                    {pct}%
                  </button>
                ))}
              </div>

              {/* Order Summary */}
              <div className="p-3 bg-[#0a0b0e]/70 rounded-lg border border-[#1f2229] space-y-1.5 text-[11px] font-mono">
                <div className="flex justify-between text-slate-400">
                  <span>Order Value</span>
                  <span className="text-white">${orderTotal.toFixed(2)} {quoteAsset}</span>
                </div>
                <div className="flex justify-between text-slate-400">
                  <span>Est. Fee (0.1%)</span>
                  <span className="text-slate-300">${estimatedFee.toFixed(3)} {quoteAsset}</span>
                </div>
              </div>

              {/* Advanced Flags */}
              <div className="flex items-center gap-4 text-xs text-slate-400">
                <label className="flex items-center gap-1.5 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={postOnly}
                    onChange={(e) => setPostOnly(e.target.checked)}
                    className="rounded border-[#1f2229] bg-[#0a0b0e] text-[#10b981] focus:ring-0"
                  />
                  <span>Post Only</span>
                </label>
                <label className="flex items-center gap-1.5 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={reduceOnly}
                    onChange={(e) => setReduceOnly(e.target.checked)}
                    className="rounded border-[#1f2229] bg-[#0a0b0e] text-[#10b981] focus:ring-0"
                  />
                  <span>Reduce Only</span>
                </label>
              </div>

              {/* Submit CTA */}
              <button
                type="submit"
                disabled={isSubmitting}
                className={`mt-auto w-full py-3 rounded-lg font-bold text-sm transition-all disabled:opacity-50 ${
                  side === 'BUY'
                    ? 'bg-[#10b981] text-black hover:bg-[#0e9f6e] shadow-[0_0_15px_rgba(16,185,129,0.3)] hover:shadow-[0_0_20px_rgba(16,185,129,0.5)]'
                    : 'bg-[#ef4444] text-white hover:bg-[#dc2626] shadow-[0_0_15px_rgba(239,68,68,0.3)] hover:shadow-[0_0_20px_rgba(239,68,68,0.5)]'
                }`}
              >
                {isSubmitting ? 'Submitting...' : `${side === 'BUY' ? 'Buy' : 'Sell'} ${baseAsset}`}
              </button>
            </form>
          </div>
        </div>

        {/* ── 3. Bottom Panel (Orders / History / Trades) ── */}
        <div className="h-64 bg-[#111318] flex flex-col flex-shrink-0 overflow-hidden">
          {/* Tabs */}
          <div className="flex px-4 border-b border-[#1f2229] gap-6 h-10 flex-shrink-0 items-center justify-between">
            <div className="flex gap-6 h-full">
              <button
                onClick={() => setBottomTab('open')}
                className={`text-xs font-semibold py-2 transition-colors ${
                  bottomTab === 'open'
                    ? 'text-white border-b-2 border-[#10b981]'
                    : 'text-slate-400 hover:text-white border-b-2 border-transparent'
                }`}
              >
                Open Orders ({openOrders.length})
              </button>
              <button
                onClick={() => setBottomTab('history')}
                className={`text-xs font-semibold py-2 transition-colors ${
                  bottomTab === 'history'
                    ? 'text-white border-b-2 border-[#10b981]'
                    : 'text-slate-400 hover:text-white border-b-2 border-transparent'
                }`}
              >
                Order History
              </button>
              <button
                onClick={() => setBottomTab('funds')}
                className={`text-xs font-semibold py-2 transition-colors ${
                  bottomTab === 'funds'
                    ? 'text-white border-b-2 border-[#10b981]'
                    : 'text-slate-400 hover:text-white border-b-2 border-transparent'
                }`}
              >
                Market Funds
              </button>
            </div>
            <button
              onClick={() => { fetchOrders(); fetchBalances() }}
              className="text-slate-400 hover:text-white p-1 rounded hover:bg-white/5"
              title="Refresh"
            >
              <RefreshCw size={14} className={loadingOrders ? 'animate-spin' : ''} />
            </button>
          </div>

          {/* Table Container */}
          <div className="flex-1 overflow-y-auto">
            {bottomTab === 'open' && (
              <table className="w-full text-left font-mono text-xs whitespace-nowrap">
                <thead className="sticky top-0 bg-[#111318] border-b border-[#1f2229] text-slate-500 text-[11px]">
                  <tr>
                    <th className="py-2.5 px-4 font-normal">Time</th>
                    <th className="py-2.5 px-4 font-normal">Pair</th>
                    <th className="py-2.5 px-4 font-normal">Type</th>
                    <th className="py-2.5 px-4 font-normal">Side</th>
                    <th className="py-2.5 px-4 font-normal text-right">Price</th>
                    <th className="py-2.5 px-4 font-normal text-right">Amount</th>
                    <th className="py-2.5 px-4 font-normal text-right">Filled</th>
                    <th className="py-2.5 px-4 font-normal text-center">Action</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#1f2229]/40 text-slate-200">
                  {openOrders.length === 0 ? (
                    <tr>
                      <td colSpan={8} className="py-8 text-center text-slate-500 font-sans">
                        No open orders in this market
                      </td>
                    </tr>
                  ) : (
                    openOrders.map((o) => (
                      <tr key={o.id} className="hover:bg-white/5 transition-colors">
                        <td className="py-2 px-4 text-slate-400">{new Date(o.created_at).toLocaleTimeString()}</td>
                        <td className="py-2 px-4 font-bold text-white">{o.market_id}</td>
                        <td className="py-2 px-4">{o.order_type}</td>
                        <td className={`py-2 px-4 font-bold ${o.side === 'BUY' ? 'text-[#10b981]' : 'text-red-400'}`}>
                          {o.side}
                        </td>
                        <td className="py-2 px-4 text-right text-white">{o.price}</td>
                        <td className="py-2 px-4 text-right text-white">{o.quantity}</td>
                        <td className="py-2 px-4 text-right text-slate-400">{o.filled_quantity || '0.00'}</td>
                        <td className="py-2 px-4 text-center">
                          <button
                            onClick={() => handleCancelOrder(o.id)}
                            className="px-2.5 py-1 text-[11px] border border-[#ef4444]/40 hover:bg-[#ef4444] text-[#ef4444] hover:text-white rounded transition-colors"
                          >
                            Cancel
                          </button>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            )}

            {bottomTab === 'history' && (
              <table className="w-full text-left font-mono text-xs whitespace-nowrap">
                <thead className="sticky top-0 bg-[#111318] border-b border-[#1f2229] text-slate-500 text-[11px]">
                  <tr>
                    <th className="py-2.5 px-4 font-normal">Time</th>
                    <th className="py-2.5 px-4 font-normal">Pair</th>
                    <th className="py-2.5 px-4 font-normal">Side</th>
                    <th className="py-2.5 px-4 font-normal text-right">Price</th>
                    <th className="py-2.5 px-4 font-normal text-right">Amount</th>
                    <th className="py-2.5 px-4 font-normal text-center">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#1f2229]/40 text-slate-200">
                  {historyOrders.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="py-8 text-center text-slate-500 font-sans">
                        No previous order history
                      </td>
                    </tr>
                  ) : (
                    historyOrders.map((o) => (
                      <tr key={o.id} className="hover:bg-white/5 transition-colors">
                        <td className="py-2 px-4 text-slate-400">{new Date(o.created_at).toLocaleTimeString()}</td>
                        <td className="py-2 px-4 text-white font-bold">{o.market_id}</td>
                        <td className={`py-2 px-4 font-bold ${o.side === 'BUY' ? 'text-[#10b981]' : 'text-red-400'}`}>{o.side}</td>
                        <td className="py-2 px-4 text-right text-white">{o.price}</td>
                        <td className="py-2 px-4 text-right text-white">{o.quantity}</td>
                        <td className="py-2 px-4 text-center">
                          <span className={`px-2 py-0.5 rounded text-[10px] font-semibold ${
                            o.status === 'FILLED' ? 'bg-[#10b981]/20 text-[#10b981]' : 'bg-slate-500/20 text-slate-400'
                          }`}>
                            {o.status}
                          </span>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            )}

            {bottomTab === 'funds' && (
              <div className="p-4 grid grid-cols-2 gap-4">
                <div className="p-3 bg-[#0a0b0e] border border-[#1f2229] rounded-xl flex justify-between items-center">
                  <div>
                    <span className="text-xs text-slate-400">{quoteAsset} Balance</span>
                    <p className="text-lg font-mono font-bold text-white">{quoteBalance.toFixed(2)}</p>
                  </div>
                  <span className="text-xs font-bold text-[#10b981]">Quote Asset</span>
                </div>
                <div className="p-3 bg-[#0a0b0e] border border-[#1f2229] rounded-xl flex justify-between items-center">
                  <div>
                    <span className="text-xs text-slate-400">{baseAsset} Balance</span>
                    <p className="text-lg font-mono font-bold text-white">{baseBalance.toFixed(4)}</p>
                  </div>
                  <span className="text-xs font-bold text-[#38bdf8]">Base Asset</span>
                </div>
              </div>
            )}
          </div>
        </div>

        {/* ── 4. Sticky Bottom Status Footer ── */}
        <footer className="h-7 bg-[#0c0e13] border-t border-[#1f2229] flex items-center justify-between px-4 text-[11px] font-mono text-slate-400 flex-shrink-0">
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-1.5">
              <CheckCircle size={12} className="text-[#10b981]" />
              <span>All Matching Engines Live</span>
            </div>
            <span>|</span>
            <div className="flex items-center gap-1">
              <Wifi size={11} className={wsConnected ? 'text-[#10b981]' : 'text-amber-400'} />
              <span>WS Feed: {wsConnected ? 'Live' : 'Connecting'}</span>
            </div>
          </div>
          <div className="flex items-center gap-4">
            <span>Trading Fee: 0.1% Taker / 0.1% Maker</span>
            <span>|</span>
            <div className="flex items-center gap-1">
              <Clock size={11} />
              <span>{new Date().toUTCString()}</span>
            </div>
          </div>
        </footer>
      </div>
    </div>
  )
}
