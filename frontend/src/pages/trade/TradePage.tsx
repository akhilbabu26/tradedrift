import { useState, useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTradeMarket } from '../../hooks/useTradeMarket'
import { useTradeCandles } from '../../hooks/useTradeCandles'
import { useTradeOrderBook } from '../../hooks/useTradeOrderBook'
import { useTradeBalances } from '../../hooks/useTradeBalances'
import { useTradeOrders } from '../../hooks/useTradeOrders'
import type { Timeframe } from '../../types/trade'

import TradeMarketHeader from '../../components/trade/TradeMarketHeader'
import TradingChart from '../../components/trade/TradingChart'
import OrderBook from '../../components/trade/OrderBook'
import OrderEntry from '../../components/trade/OrderEntry'
import OpenOrdersPanel from '../../components/trade/OpenOrdersPanel'
import TradeFooter from '../../components/trade/TradeFooter'

/**
 * Trade page — authenticated route at /trade.
 *
 * Height hierarchy:
 *   MainLayout  → flex-1 flex flex-col overflow-hidden min-h-0
 *   TradePage   → flex-1 flex flex-col min-h-0          (fills exactly)
 *     MarketHeader   → flex-shrink-0 (h-14 fixed)
 *     TradingWorkspace → flex-1 min-h-0 grid            (absorbs remaining)
 *     OpenOrdersPanel  → flex-shrink-0 (h-[280px] fixed)
 *     TradeFooter      → flex-shrink-0
 *
 * All hooks are called here; selectedMarketId is the single source of truth
 * and is passed to every child panel as a prop.
 */
export default function TradePage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const initialMarket = searchParams.get('market') || 'BTC-USDT'

  // ── Timeframe state ─────────────────────────────────────────────────────
  const [timeframe, setTimeframe] = useState<Timeframe>('1h')

  // ── Market + ticker (owns selectedMarketId) ──────────────────────────────
  const {
    markets,
    selectedMarketId,
    setSelectedMarketId,
    selectedMarket,
    ticker,
    isDemoData: marketDemoData,
    loading: marketLoading,
  } = useTradeMarket(initialMarket)

  const handleSelectMarket = (id: string) => {
    setSelectedMarketId(id)
    setSearchParams({ market: id }, { replace: true })
  }

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
    submitting,
    cancellingId,
    cancellingAll,
    submitOrder,
    cancelOrder,
    cancelAll,
  } = useTradeOrders(selectedMarketId, refetchBalances)

  // ── Derived ──────────────────────────────────────────────────────────────
  const baseAsset = selectedMarket.base_asset
  const quoteAsset = selectedMarket.quote_asset
  const symbol = `${baseAsset}/${quoteAsset}`
  const lastPrice = ticker?.last_price ?? orderBook.lastPrice ?? '0'

  const quoteBalance = getBalance(quoteAsset)   // e.g. USDT
  const baseBalance = getBalance(baseAsset)    // e.g. BTC

  // isDemoData — any source using mock fallback
  const anyDemoData = marketDemoData || candlesDemoData || obDemoData

  return (
    /*
     * flex-1: grows to fill MainLayout's <main> (which is flex-col)
     * flex flex-col: children stack vertically
     * min-h-0: critical — allows this element to shrink below its content height
     * overflow-hidden: prevents page-level scroll; each panel manages its own
     *
     * NOTE: do NOT add min-h-[Npx] here — it conflicts with overflow-hidden on
     * the parent <main> and pushes the OpenOrdersPanel out of view on short
     * viewports where the page overflows before overflow-hidden can clip it.
     */
    <div className="w-full flex-1 flex flex-col min-h-0 overflow-y-auto bg-[#0a0b0e]">

      {/* ── Market Header ─────────────────────────────────────────────────── */}
      {/* sticky top-0: header stays pinned when user scrolls down to lower panel */}
      <div className="sticky top-0 z-20 flex-shrink-0">
        <TradeMarketHeader
          markets={markets}
          selectedMarketId={selectedMarketId}
          onSelectMarket={handleSelectMarket}
          ticker={ticker}
          isDemoData={anyDemoData}
          usdtBalance={quoteBalance}
          baseBalance={baseBalance}
        />
      </div>

      {/*
       * ── Trading Workspace ─────────────────────────────────────────────────
       * flex-1 min-h-0: absorbs all remaining vertical space between header and orders panel
       * grid: horizontal 3-column layout
       *   col 1 (chart): minmax(0, 1fr) — grows to fill
       *   col 2 (order book): 280px reference, min 240px
       *   col 3 (order entry): 300px reference, min 260px
       * overflow-hidden: clips internal content, each child scrolls itself
       */}
      <div
        className="flex-1 flex-shrink-0 min-h-[420px] overflow-hidden grid"
        style={{
          gridTemplateColumns: 'minmax(0, 1fr) minmax(240px, 280px) minmax(260px, 300px)',
        }}
      >
        {/* Chart — grows to fill remaining width */}
        <div className="flex flex-col min-h-0 min-w-0 border-r border-[#1e2530]">
          <TradingChart
            marketId={selectedMarketId}
            symbol={symbol}
            lastPrice={lastPrice}
            candles={candles}
            loading={candlesLoading || marketLoading}
            isDemoData={candlesDemoData}
            timeframe={timeframe}
            onTimeframeChange={setTimeframe}
          />
        </div>

        {/* Order Book */}
        <OrderBook
          orderBook={orderBook}
          isDemoData={obDemoData}
          precision={precision}
          onPrecisionChange={setPrecision}
          lastPrice={lastPrice}
          baseAsset={baseAsset}
          quoteAsset={quoteAsset}
        />

        {/* Order Entry */}
        <OrderEntry
          market={selectedMarket}
          lastPrice={lastPrice}
          quoteBalance={quoteBalance}
          baseBalance={baseBalance}
          submitting={submitting}
          onSubmit={submitOrder}
        />
      </div>

      {/*
       * ── Open Orders Panel ─────────────────────────────────────────────────
       * flex-shrink-0 + fixed height: compact fixed strip at the bottom
       * Table body scrolls internally via overflow-y-auto inside the panel
       */}
      <OpenOrdersPanel
        orders={orders}
        loading={ordersLoading}
        cancellingId={cancellingId}
        cancellingAll={cancellingAll}
        balances={balances}
        onCancel={cancelOrder}
        onCancelAll={cancelAll}
      />

      {/* ── Footer ────────────────────────────────────────────────────────── */}
      <TradeFooter />
    </div>
  )
}
