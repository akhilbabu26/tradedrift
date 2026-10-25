import { useState, useEffect, useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTradeMarket } from '../../hooks/useTradeMarket'
import { useTradeCandles } from '../../hooks/useTradeCandles'
import { useTradeOrderBook } from '../../hooks/useTradeOrderBook'
import { useTradeBalances } from '../../hooks/useTradeBalances'
import { useTradeOrders } from '../../hooks/useTradeOrders'
import { CandlestickChart, Layers, TrendingUp, ClipboardList } from 'lucide-react'
import type { Timeframe, OrderSide } from '../../types/trade'

import TradeMarketHeader from '../../components/trade/TradeMarketHeader'
import TradingChart from '../../components/trade/TradingChart'
import OrderBook from '../../components/trade/OrderBook'
import OrderEntry from '../../components/trade/OrderEntry'
import OpenOrdersPanel from '../../components/trade/OpenOrdersPanel'
import GlobalFooter from '../../components/layout/GlobalFooter'

/**
 * Trade page — authenticated route at /trade.
 *
 * Height hierarchy:
 *   MainLayout  → flex-1 flex flex-col overflow-hidden min-h-0
 *   TradePage   → flex-1 flex flex-col min-h-0          (fills exactly)
 *     MarketHeader   → flex-shrink-0 (h-14 fixed)
 *     TradingWorkspace → flex-1 min-h-0 grid            (absorbs remaining)
 *     OpenOrdersPanel  → flex-shrink-0 (h-[280px] fixed)
 *     GlobalFooter (compact) → flex-shrink-0
 *
 * All hooks are called here; selectedMarketId is the single source of truth
 * and is passed to every child panel as a prop.
 */
export default function TradePage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const initialMarket = searchParams.get('market') || 'BTC-USDT'

  // ── Timeframe state ─────────────────────────────────────────────────────
  const [timeframe, setTimeframe] = useState<Timeframe>('1m')

  // ── Market + ticker (owns selectedMarketId) ──────────────────────────────
  const {
    markets,
    selectedMarketId,
    setSelectedMarketId,
    selectedMarket,
    ticker,
    marketTrends,
    isDemoData: marketDemoData,
    loading: marketLoading,
  } = useTradeMarket(initialMarket)

  const handleSelectMarket = useCallback((id: string) => {
    setSelectedMarketId(id)
    setSearchParams({ market: id }, { replace: true })
  }, [setSelectedMarketId, setSearchParams])

  useEffect(() => {
    const param = searchParams.get('market')
    if (param && param !== selectedMarketId) {
      setSelectedMarketId(param)
    }
  }, [searchParams, selectedMarketId, setSelectedMarketId])

  // ── Candles ──────────────────────────────────────────────────────────────
  const {
    candles,
    loading: candlesLoading,
    isDemoData: candlesDemoData,
    refetch: refetchCandles,
  } = useTradeCandles(selectedMarketId, timeframe)

  // ── Order book ───────────────────────────────────────────────────────────
  const {
    orderBook,
    isDemoData: obDemoData,
    precision,
    setPrecision,
  } = useTradeOrderBook(selectedMarketId)

  // ── Balances ─────────────────────────────────────────────────────────────
  const { balances, getBalance, refetchBalances } = useTradeBalances()

  // ── Orders ───────────────────────────────────────────────────────────────
  const {
    orders,
    loading: ordersLoading,
    error: ordersError,
    submitting,
    cancellingId,
    cancellingAll,
    fetchOrders,
    submitOrder,
    cancelOrder,
    cancelAll,
  } = useTradeOrders(selectedMarketId, refetchBalances)

  // ── Derived ──────────────────────────────────────────────────────────────
  const baseAsset = selectedMarket.base_asset
  const quoteAsset = selectedMarket.quote_asset
  const symbol = `${baseAsset}/${quoteAsset}`

  // Market Isolation Invariant: validate market ownership before resolving lastPrice
  const isTickerMatching = ticker?.market_id === selectedMarketId
  const isBookMatching   = orderBook.marketId === selectedMarketId && orderBook.lastPrice !== '0.00' && orderBook.lastPrice !== ''
  const lastPrice = (isTickerMatching ? ticker?.last_price : null)
    ?? (isBookMatching ? orderBook.lastPrice : null)
    ?? ''

  const bestAsk = isBookMatching && orderBook.asks.length > 0 ? orderBook.asks[0].price : ''
  const bestBid = isBookMatching && orderBook.bids.length > 0 ? orderBook.bids[0].price : ''
  const [selectedPrice, setSelectedPrice] = useState<string | null>(null)
  const [mobileTab, setMobileTab] = useState<'chart' | 'orderbook' | 'trade' | 'orders'>('chart')
  const [quickSide, setQuickSide] = useState<OrderSide>('BUY')

  const quoteBalance = getBalance(quoteAsset)   // e.g. USDT
  const baseBalance = getBalance(baseAsset)    // e.g. BTC

  // isDemoData — any source using mock fallback
  const anyDemoData = marketDemoData || candlesDemoData || obDemoData

  const handleQuickTrade = useCallback((side: OrderSide) => {
    setQuickSide(side)
    setMobileTab('trade')
  }, [])

  const handlePriceSelectFromBook = useCallback((price: string) => {
    setSelectedPrice(price)
    // On mobile, automatically open the trade tab with prefilled price
    if (typeof window !== 'undefined' && window.innerWidth < 1024) {
      setMobileTab('trade')
    }
  }, [])

  return (
    <div className="w-full flex-1 flex flex-col min-h-0 overflow-y-auto bg-[#0a0b0e]">

      {/* ── Market Header ─────────────────────────────────────────────────── */}
      <div className="sticky top-0 z-20 flex-shrink-0">
        <TradeMarketHeader
          markets={markets}
          selectedMarketId={selectedMarketId}
          onSelectMarket={handleSelectMarket}
          ticker={isTickerMatching ? ticker : null}
          isDemoData={anyDemoData}
          usdtBalance={quoteBalance}
          baseBalance={baseBalance}
          marketTrends={marketTrends}
        />
      </div>

      {/* ── Mobile & Tablet Segmented Tab Bar (< lg / < 1024px) ───────────── */}
      <div className="lg:hidden flex items-center justify-between border-b border-[#1e2530] bg-[#111318] px-2.5 py-1.5 flex-shrink-0 z-10 sticky top-[58px]">
        <div className="flex items-center gap-1 w-full bg-[#08090b] p-0.5 rounded-lg border border-[#1e2530]" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={mobileTab === 'chart'}
            onClick={() => setMobileTab('chart')}
            className={`flex-1 flex items-center justify-center gap-1.5 py-1.5 text-xs font-semibold rounded-md transition-all cursor-pointer ${
              mobileTab === 'chart'
                ? 'bg-[#151719] text-[#10b981] shadow-sm border border-[#10b981]/30'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <CandlestickChart size={13} />
            <span>Chart</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mobileTab === 'orderbook'}
            onClick={() => setMobileTab('orderbook')}
            className={`flex-1 flex items-center justify-center gap-1.5 py-1.5 text-xs font-semibold rounded-md transition-all cursor-pointer ${
              mobileTab === 'orderbook'
                ? 'bg-[#151719] text-[#10b981] shadow-sm border border-[#10b981]/30'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <Layers size={13} />
            <span>Order Book</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mobileTab === 'trade'}
            onClick={() => setMobileTab('trade')}
            className={`flex-1 flex items-center justify-center gap-1.5 py-1.5 text-xs font-semibold rounded-md transition-all cursor-pointer ${
              mobileTab === 'trade'
                ? 'bg-[#151719] text-[#10b981] shadow-sm border border-[#10b981]/30'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <TrendingUp size={13} />
            <span>Trade</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mobileTab === 'orders'}
            onClick={() => setMobileTab('orders')}
            className={`flex-1 flex items-center justify-center gap-1.5 py-1.5 text-xs font-semibold rounded-md transition-all cursor-pointer ${
              mobileTab === 'orders'
                ? 'bg-[#151719] text-[#10b981] shadow-sm border border-[#10b981]/30'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <ClipboardList size={13} />
            <span>Orders</span>
            {orders.length > 0 && (
              <span className="w-4 h-4 rounded-full bg-[#10b981] text-[#08090b] text-[10px] font-bold flex items-center justify-center">
                {orders.length}
              </span>
            )}
          </button>
        </div>
      </div>

      {/*
       * ── Responsive Trading Workspace ──────────────────────────────────────
       * Unified single-instance components:
       * - On desktop (>= lg / >= 1024px): 3-column classic trading terminal grid
       * - On mobile & tablet (< lg): controlled by mobileTab ('chart', 'orderbook', 'trade', 'orders')
       */}
      <div className="flex-1 flex flex-col lg:grid lg:grid-cols-[minmax(0,1fr)_240px_265px] xl:grid-cols-[minmax(0,1fr)_275px_300px] min-h-0 lg:min-h-[460px] overflow-hidden">
        {/* Panel 1: Candlestick Chart */}
        <div
          className={`flex flex-col min-h-0 min-w-0 lg:border-r border-[#1e2530] lg:h-full ${
            mobileTab === 'chart' ? 'flex-1 h-[420px] sm:h-[480px]' : 'hidden lg:flex'
          }`}
        >
          <TradingChart
            marketId={selectedMarketId}
            symbol={symbol}
            lastPrice={lastPrice}
            candles={candles}
            loading={candlesLoading || marketLoading}
            isDemoData={candlesDemoData}
            timeframe={timeframe}
            tickSize={selectedMarket?.tick_size}
            onTimeframeChange={setTimeframe}
            onRefreshCandles={refetchCandles}
          />
          {/* Quick Buy/Sell Bar on mobile chart tab */}
          <div className="lg:hidden p-2.5 sm:p-3 bg-[#111318] border-t border-[#1e2530] flex items-center gap-2.5 flex-shrink-0">
            <button
              type="button"
              onClick={() => handleQuickTrade('BUY')}
              className="flex-1 py-2.5 sm:py-3 rounded-xl bg-[#10b981] hover:bg-[#059669] text-black font-bold text-xs sm:text-sm flex items-center justify-center gap-2 shadow-[0_2px_8px_rgba(16,185,129,0.25)] active:scale-[0.98] transition-all cursor-pointer"
            >
              <span>Buy {baseAsset}</span>
            </button>
            <button
              type="button"
              onClick={() => handleQuickTrade('SELL')}
              className="flex-1 py-2.5 sm:py-3 rounded-xl bg-[#ef4444] hover:bg-[#dc2626] text-white font-bold text-xs sm:text-sm flex items-center justify-center gap-2 shadow-[0_2px_8px_rgba(239,68,68,0.25)] active:scale-[0.98] transition-all cursor-pointer"
            >
              <span>Sell {baseAsset}</span>
            </button>
          </div>
        </div>

        {/* Panel 2: Order Book & Depth */}
        <div
          className={`flex flex-col min-h-0 min-w-0 lg:border-r border-[#1e2530] lg:h-full ${
            mobileTab === 'orderbook' ? 'flex-1 min-h-[420px]' : 'hidden lg:flex'
          }`}
        >
          <OrderBook
            orderBook={orderBook}
            isDemoData={obDemoData}
            precision={precision}
            onPrecisionChange={setPrecision}
            onSelectPrice={handlePriceSelectFromBook}
            lastPrice={lastPrice}
            baseAsset={baseAsset}
            quoteAsset={quoteAsset}
          />
          {/* Quick Buy/Sell Bar on mobile orderbook tab */}
          <div className="lg:hidden p-2.5 sm:p-3 bg-[#111318] border-t border-[#1e2530] flex items-center gap-2.5 flex-shrink-0">
            <button
              type="button"
              onClick={() => handleQuickTrade('BUY')}
              className="flex-1 py-2.5 sm:py-3 rounded-xl bg-[#10b981] hover:bg-[#059669] text-black font-bold text-xs sm:text-sm flex items-center justify-center gap-2 shadow-[0_2px_8px_rgba(16,185,129,0.25)] active:scale-[0.98] transition-all cursor-pointer"
            >
              <span>Buy {baseAsset}</span>
            </button>
            <button
              type="button"
              onClick={() => handleQuickTrade('SELL')}
              className="flex-1 py-2.5 sm:py-3 rounded-xl bg-[#ef4444] hover:bg-[#dc2626] text-white font-bold text-xs sm:text-sm flex items-center justify-center gap-2 shadow-[0_2px_8px_rgba(239,68,68,0.25)] active:scale-[0.98] transition-all cursor-pointer"
            >
              <span>Sell {baseAsset}</span>
            </button>
          </div>
        </div>

        {/* Panel 3: Order Entry Form */}
        <div
          className={`flex flex-col min-h-0 min-w-0 overflow-y-auto lg:h-full ${
            mobileTab === 'trade' ? 'flex-1 min-h-[420px]' : 'hidden lg:flex'
          }`}
        >
          <OrderEntry
            market={selectedMarket}
            lastPrice={lastPrice}
            bestAsk={bestAsk}
            bestBid={bestBid}
            selectedPrice={selectedPrice}
            quoteBalance={quoteBalance}
            baseBalance={baseBalance}
            submitting={submitting}
            onSubmit={submitOrder}
            initialSide={quickSide}
          />
        </div>
      </div>

      {/* ── Open Orders Panel ─────────────────────────────────────────────── */}
      {/* On desktop: rendered across the bottom */}
      {/* On mobile: visible on orders tab, or below chart when scrolled */}
      <div
        className={`flex-shrink-0 ${
          mobileTab === 'orders' ? 'flex-1 min-h-[300px]' : mobileTab === 'chart' ? 'block' : 'hidden lg:block'
        }`}
      >
        <OpenOrdersPanel
          orders={orders}
          loading={ordersLoading}
          error={ordersError}
          selectedMarketId={selectedMarketId}
          cancellingId={cancellingId}
          cancellingAll={cancellingAll}
          balances={balances}
          onCancel={cancelOrder}
          onCancelAll={cancelAll}
          onRetry={fetchOrders}
        />
      </div>

      {/* ── Desktop Footer ─────────────────────────────────────────────────── */}
      <div className="hidden lg:block flex-shrink-0">
        <GlobalFooter compact />
      </div>
    </div>
  )
}
