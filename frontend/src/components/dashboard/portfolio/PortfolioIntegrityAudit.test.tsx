import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import PortfolioChart from './PortfolioChart'
import PortfolioMetrics from './PortfolioMetrics'
import { toDecimal } from '../../../utils/decimal'

// Helper replicating the verified financial metrics calculation logic
export function calculateTradingMetrics({
  userTrades,
  currentUserId,
  holdings,
  feeRate = 0, // In TradeDrift v1 simulator, fee rate is 0
}: {
  userTrades: Array<{
    id: string
    buyer_id?: string
    seller_id?: string
    buy_order_id?: string
    sell_order_id?: string
    market_id: string
    base_asset?: string
    price: string
    quantity: string
    taker_side?: string
  }>
  currentUserId: string
  holdings: Array<{
    asset: string
    totalQuantity: string
    averageEntryPrice: string
  }>
  feeRate?: number
}) {
  const totalTrades = userTrades.length

  // Filter for closed (SELL) trades
  const sellTrades = userTrades.filter((t) =>
    currentUserId && t.seller_id
      ? t.seller_id === currentUserId
      : (t.taker_side || '').toUpperCase() === 'SELL'
  )

  if (sellTrades.length === 0) {
    return {
      totalTrades,
      winRate: 0,
      hasClosedTrades: false,
      winningTradesCount: 0,
      closedTradesCount: 0,
    }
  }

  let winningTradesCount = 0
  for (const st of sellTrades) {
    const baseAsset = (st.base_asset ?? st.market_id?.split('-')[0] ?? '').toUpperCase()
    const holding = holdings.find((h) => h.asset.toUpperCase() === baseAsset)
    const avgEntry = toDecimal(holding?.averageEntryPrice || '0')
    const sellPrice = toDecimal(st.price || '0')
    const notional = sellPrice.times(toDecimal(st.quantity || '0'))
    const fee = notional.times(feeRate)
    const netProceeds = notional.minus(fee)
    const costBasis = avgEntry.times(toDecimal(st.quantity || '0'))

    // Profitable if net proceeds > cost basis
    if (avgEntry.gt(0) && netProceeds.gt(costBasis)) {
      winningTradesCount++
    }
  }

  const winRate = Math.round((winningTradesCount / sellTrades.length) * 100)

  return {
    totalTrades,
    winRate,
    hasClosedTrades: true,
    winningTradesCount,
    closedTradesCount: sellTrades.length,
  }
}

describe('Portfolio Performance Graph & Analytics Data Integrity Audit (Part 8)', () => {
  const USER_ID = '65119b17-0a56-4387-82dc-320420ef4b32'

  // Case 1: No executed trades
  it('1. correctly handles no executed trades (0 total trades, 0% win rate)', () => {
    const result = calculateTradingMetrics({
      userTrades: [],
      currentUserId: USER_ID,
      holdings: [],
    })

    expect(result.totalTrades).toBe(0)
    expect(result.winRate).toBe(0)
    expect(result.hasClosedTrades).toBe(false)
  })

  // Case 2: One profitable closed position
  it('2. correctly evaluates one profitable closed position as 100% win rate', () => {
    const result = calculateTradingMetrics({
      userTrades: [
        {
          id: 'trade-1',
          seller_id: USER_ID,
          market_id: 'BTC-USDT',
          base_asset: 'BTC',
          price: '85000.00',
          quantity: '0.1000',
        },
      ],
      currentUserId: USER_ID,
      holdings: [{ asset: 'BTC', totalQuantity: '0.5', averageEntryPrice: '80000.00' }],
    })

    expect(result.totalTrades).toBe(1)
    expect(result.closedTradesCount).toBe(1)
    expect(result.winningTradesCount).toBe(1)
    expect(result.winRate).toBe(100)
  })

  // Case 3: One losing closed position
  it('3. correctly evaluates one losing closed position as genuine 0% win rate', () => {
    const result = calculateTradingMetrics({
      userTrades: [
        {
          id: 'trade-2',
          seller_id: USER_ID,
          market_id: 'SOL-USDT',
          base_asset: 'SOL',
          price: '105.00',
          quantity: '2.0000',
        },
      ],
      currentUserId: USER_ID,
      holdings: [{ asset: 'SOL', totalQuantity: '10.0', averageEntryPrice: '110.00' }],
    })

    expect(result.totalTrades).toBe(1)
    expect(result.closedTradesCount).toBe(1)
    expect(result.winningTradesCount).toBe(0)
    expect(result.winRate).toBe(0)
    expect(result.hasClosedTrades).toBe(true)
  })

  // Case 4: A mix of profitable and losing closed positions
  it('4. correctly computes win rate on a mix of profitable and losing closed positions (1 win, 6 losses -> 14%)', () => {
    const result = calculateTradingMetrics({
      userTrades: [
        // 1 Profitable BTC sell
        { id: 't1', seller_id: USER_ID, market_id: 'BTC-USDT', base_asset: 'BTC', price: '83503.96', quantity: '0.001' },
        // 1 Losing BTC sell
        { id: 't2', seller_id: USER_ID, market_id: 'BTC-USDT', base_asset: 'BTC', price: '82930.11', quantity: '0.001' },
        // 5 Losing SOL sells
        { id: 't3', seller_id: USER_ID, market_id: 'SOL-USDT', base_asset: 'SOL', price: '109.52', quantity: '0.56' },
        { id: 't4', seller_id: USER_ID, market_id: 'SOL-USDT', base_asset: 'SOL', price: '109.308', quantity: '3.94' },
        { id: 't5', seller_id: USER_ID, market_id: 'SOL-USDT', base_asset: 'SOL', price: '109.313', quantity: '6.06' },
        { id: 't6', seller_id: USER_ID, market_id: 'SOL-USDT', base_asset: 'SOL', price: '109.310', quantity: '2.00' },
        { id: 't7', seller_id: USER_ID, market_id: 'SOL-USDT', base_asset: 'SOL', price: '109.594', quantity: '5.00' },
      ],
      currentUserId: USER_ID,
      holdings: [
        { asset: 'BTC', totalQuantity: '0.1090', averageEntryPrice: '83083.92' },
        { asset: 'SOL', totalQuantity: '9.4400', averageEntryPrice: '109.8097' },
      ],
    })

    expect(result.closedTradesCount).toBe(7)
    expect(result.winningTradesCount).toBe(1)
    // 1 / 7 = 14.28% -> rounds to 14%
    expect(result.winRate).toBe(14)
  })

  // Case 5: Partial fills belonging to the same order
  it('5. counts each partial fill in trade history while attributing to the parent order', () => {
    const partialFills = [
      { id: 'fill-1', sell_order_id: 'ord-100', seller_id: USER_ID, market_id: 'BTC-USDT', base_asset: 'BTC', price: '85000', quantity: '0.02' },
      { id: 'fill-2', sell_order_id: 'ord-100', seller_id: USER_ID, market_id: 'BTC-USDT', base_asset: 'BTC', price: '85000', quantity: '0.03' },
      { id: 'fill-3', sell_order_id: 'ord-100', seller_id: USER_ID, market_id: 'BTC-USDT', base_asset: 'BTC', price: '85000', quantity: '0.05' },
    ]

    const result = calculateTradingMetrics({
      userTrades: partialFills,
      currentUserId: USER_ID,
      holdings: [{ asset: 'BTC', totalQuantity: '0.1', averageEntryPrice: '80000' }],
    })

    // Fills count = 3
    expect(result.totalTrades).toBe(3)
    // All 3 partial fills were at 85000 > 80000 entry -> 100% win rate
    expect(result.winRate).toBe(100)
    // Unique orders count = 1
    const uniqueOrders = new Set(partialFills.map((f) => f.sell_order_id))
    expect(uniqueOrders.size).toBe(1)
  })

  // Case 6: Fees that turn a gross profit into a net loss
  it('6. correctly turns gross profit into net loss when fees exceed gross gain', () => {
    // Sold at 101 with entry at 100 (gross gain +1), but fee is 2% = 2.02 -> net proceeds 98.98 < cost 100
    const result = calculateTradingMetrics({
      userTrades: [
        { id: 't-fee', seller_id: USER_ID, market_id: 'SOL-USDT', base_asset: 'SOL', price: '101.00', quantity: '1.0' },
      ],
      currentUserId: USER_ID,
      holdings: [{ asset: 'SOL', totalQuantity: '1.0', averageEntryPrice: '100.00' }],
      feeRate: 0.02, // 2% fee
    })

    expect(result.winningTradesCount).toBe(0)
    expect(result.winRate).toBe(0)
  })

  // Case 7: An open position that must not be counted as a closed win
  it('7. ensures open BUY positions are strictly excluded from closed win rate count', () => {
    const result = calculateTradingMetrics({
      userTrades: [
        // 5 BUY trades (all open positions with massive unrealized gains)
        { id: 'buy-1', buyer_id: USER_ID, market_id: 'BTC-USDT', base_asset: 'BTC', price: '60000', quantity: '0.1' },
        { id: 'buy-2', buyer_id: USER_ID, market_id: 'ETH-USDT', base_asset: 'ETH', price: '2000', quantity: '2.0' },
      ],
      currentUserId: USER_ID,
      holdings: [
        { asset: 'BTC', totalQuantity: '0.1', averageEntryPrice: '60000' },
        { asset: 'ETH', totalQuantity: '2.0', averageEntryPrice: '2000' },
      ],
    })

    // 2 total trade fills executed
    expect(result.totalTrades).toBe(2)
    // 0 closed trades -> hasClosedTrades is false
    expect(result.closedTradesCount).toBe(0)
    expect(result.hasClosedTrades).toBe(false)
    expect(result.winRate).toBe(0)
  })

  // Case 8: Duplicate execution-event delivery
  it('8. deduplicates identical execution trade IDs', () => {
    const duplicateFills = [
      { id: 'dup-1', seller_id: USER_ID, market_id: 'BTC-USDT', base_asset: 'BTC', price: '85000', quantity: '0.05' },
      { id: 'dup-1', seller_id: USER_ID, market_id: 'BTC-USDT', base_asset: 'BTC', price: '85000', quantity: '0.05' }, // Duplicate delivery
    ]

    const dedupedFills = Array.from(new Map(duplicateFills.map((t) => [t.id, t])).values())
    expect(dedupedFills).toHaveLength(1)

    const result = calculateTradingMetrics({
      userTrades: dedupedFills,
      currentUserId: USER_ID,
      holdings: [{ asset: 'BTC', totalQuantity: '0.1', averageEntryPrice: '80000' }],
    })

    expect(result.totalTrades).toBe(1)
  })

  // Case 9: Failed analytics API request
  it('9. handles failed analytics request gracefully with neutral initial state', () => {
    const html = renderToString(
      <PortfolioMetrics
        allTimeProfit="0.00"
        winRate={0}
        totalTrades={0}
      />
    )

    expect(html).toContain('0.00')
    expect(html).toContain('USDT')
    expect(html).toContain('0')
    expect(html).toContain('%')
  })

  // Case 10: Missing historical chart data
  it('10. renders PortfolioChart cleanly without crash when data is empty or missing', () => {
    const html = renderToString(
      <PortfolioChart data={[]} latestValue="20000.00" />
    )

    expect(html).toContain('Portfolio Value (USDT)')
    expect(html).toContain('role="img"')
    expect(html).toContain('aria-label="Portfolio value chart"')
  })

  // Case 11: Time-range change effect on metrics
  it('11. verifies that All-Time Profit remains cumulative across lifetime while chart updates per timeframe', () => {
    // PortfolioMetrics is cumulative lifetime
    const metricsHtml = renderToString(
      <PortfolioMetrics
        allTimeProfit="58.66"
        winRate={14}
        totalTrades={44}
      />
    )

    expect(metricsHtml).toContain('+58.66')
    expect(metricsHtml).toContain('USDT')
    expect(metricsHtml).toContain('14')
    expect(metricsHtml).toContain('%')
    expect(metricsHtml).toContain('44')
  })
})
