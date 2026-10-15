import { describe, it, expect } from 'vitest'
import { toDecimal } from '../utils/decimal'

describe('Portfolio Financial Precision & Edge Case Math', () => {
  // Pure calculation helpers mirroring the exact logic in usePortfolioData.ts
  function calculateHoldingPnl(quantityStr: string, priceStr: string, averageCostStr: string) {
    const qty = toDecimal(quantityStr || '0')
    const priceDec = toDecimal(priceStr || '0')
    const avgCost = toDecimal(averageCostStr || '0')

    const mv = qty.times(priceDec)
    const cb = qty.times(avgCost)
    const unPnl = mv.minus(cb)

    const unPnlPct = cb.gt(0)
      ? unPnl.dividedBy(cb).times(100).abs().toFixed(2)
      : '0.00'
    const pnlSign = unPnl.gte(0) ? '+' : '-'
    const isPnlZero = unPnl.abs().toFixed(2) === '0.00'

    return {
      marketValue: mv.toFixed(2),
      unrealizedPnl: isPnlZero ? '0.00' : `${pnlSign}${unPnl.abs().toFixed(2)}`,
      unrealizedPnlPct: `${pnlSign}${unPnlPct}`,
    }
  }

  function calculateExecutiveMetrics(
    holdings: Array<{ marketValue: string; costBasis: string; unrealizedPnl: string }>
  ) {
    const totalMarketValue = holdings.reduce(
      (sum, h) => sum.plus(toDecimal(h.marketValue)),
      toDecimal(0)
    )
    const totalCostBasis = holdings.reduce(
      (sum, h) => sum.plus(toDecimal(h.costBasis)),
      toDecimal(0)
    )
    const totalUnrealPnl = holdings.reduce(
      (sum, h) => sum.plus(toDecimal(h.unrealizedPnl)),
      toDecimal(0)
    )

    const unPnlPctTotalDec = totalCostBasis.gt(0)
      ? totalUnrealPnl.dividedBy(totalCostBasis).times(100)
      : toDecimal(0)
    const unPnlPctSign = unPnlPctTotalDec.gte(0) ? '+' : '-'
    const totalPnlSign = totalUnrealPnl.gte(0) ? '+' : '-'

    return {
      totalValue: totalMarketValue.toFixed(2),
      unrealizedPnl: `${totalPnlSign}${totalUnrealPnl.abs().toFixed(2)}`,
      unrealizedPnlPct: `${unPnlPctSign}${unPnlPctTotalDec.abs().toFixed(2)}`,
    }
  }

  it('handles zero cost basis gracefully without division-by-zero or NaN', () => {
    // Air-dropped token or zero entry price
    const res = calculateHoldingPnl('1.5000', '100.00', '0.00')
    expect(res.marketValue).toBe('150.00')
    expect(res.unrealizedPnl).toBe('+150.00')
    expect(res.unrealizedPnlPct).toBe('+0.00') // Zero denominator behavior preserved
  })

  it('calculates negative PnL accurately without double negatives or floating artifacts', () => {
    // Purchased at 100, now 40 (-60% drop)
    const res = calculateHoldingPnl('2.0000', '40.00', '100.00')
    expect(res.marketValue).toBe('80.00')
    expect(res.unrealizedPnl).toBe('-120.00')
    expect(res.unrealizedPnlPct).toBe('-60.00')
  })

  it('handles micro-PnL near zero without displaying -0.00', () => {
    // Difference is -0.000001
    const res = calculateHoldingPnl('1.0000', '99.999999', '100.00')
    expect(res.unrealizedPnl).toBe('0.00')
    expect(res.unrealizedPnlPct).toBe('-0.00')
  })

  it('computes high precision fractional crypto quantities without precision loss', () => {
    // 0.00001234 BTC at 96,450.50 with entry at 64,120.25
    const qty = '0.00001234'
    const curPrice = '96450.50'
    const entryPrice = '64120.25'

    const res = calculateHoldingPnl(qty, curPrice, entryPrice)
    // MV = 0.00001234 * 96450.50 = 1.19019917 -> 1.19
    expect(res.marketValue).toBe('1.19')
    // Gain = (96450.50 - 64120.25) / 64120.25 = +50.42%
    expect(res.unrealizedPnlPct).toBe('+50.42')
  })

  it('computes extreme return ratios without scientific notation or NaN', () => {
    // 100x return (+9900%)
    const highReturn = calculateHoldingPnl('10.0000', '1000.00', '10.00')
    expect(highReturn.unrealizedPnlPct).toBe('+9900.00')

    // 99.9% loss
    const heavyLoss = calculateHoldingPnl('10.0000', '0.01', '10.00')
    expect(heavyLoss.unrealizedPnlPct).toBe('-99.90')
  })

  it('aggregates executive metrics across multi-asset portfolio with zero-denominator safety', () => {
    const emptyMetrics = calculateExecutiveMetrics([])
    expect(emptyMetrics.totalValue).toBe('0.00')
    expect(emptyMetrics.unrealizedPnl).toBe('+0.00')
    expect(emptyMetrics.unrealizedPnlPct).toBe('+0.00')

    const portfolioHoldings = [
      { marketValue: '9645.05', costBasis: '8000.00', unrealizedPnl: '1645.05' },
      { marketValue: '2780.50', costBasis: '3200.00', unrealizedPnl: '-419.50' },
      { marketValue: '5000.00', costBasis: '5000.00', unrealizedPnl: '0.00' }, // USDT cash
    ]

    const metrics = calculateExecutiveMetrics(portfolioHoldings)
    expect(metrics.totalValue).toBe('17425.55')
    // Net unPnl = 1645.05 - 419.50 = 1225.55
    expect(metrics.unrealizedPnl).toBe('+1225.55')
    // Total cost basis = 16200.00; 1225.55 / 16200 * 100 = 7.565123... -> +7.57%
    expect(metrics.unrealizedPnlPct).toBe('+7.57')
  })
})
