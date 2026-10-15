import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import SimulatorBalance from './SimulatorBalance'

describe('SimulatorBalance Component', () => {
  const defaultProps = {
    balance: '2387.89',
    availablePercent: 100,
    inOrdersPercent: 0,
    availableUsdt: '2387.89',
    inOrdersUsdt: '0.00',
  }

  it('renders available balance with emerald green fill and in-orders segment with blue fill', () => {
    const html = renderToString(<SimulatorBalance {...defaultProps} />)

    // Available bar segment has emerald green fill (#10B981)
    expect(html).toContain('bg-[#10B981]')
    expect(html).toContain('style="width:100%"')
    expect(html).toContain('aria-label="Available balance"')

    // In orders bar segment has blue fill (#3B82F6)
    expect(html).toContain('bg-[#3B82F6]')
    expect(html).toContain('style="width:0%"')
    expect(html).toContain('aria-label="In orders"')
  })

  it('renders semantic colors for formatted balance values below the bar', () => {
    const html = renderToString(<SimulatorBalance {...defaultProps} />)

    // Available USDT formatted value is green
    expect(html).toContain('text-[#10B981]')
    expect(html).toContain('2,387.89 USDT')

    // In-orders USDT formatted value is blue
    expect(html).toContain('text-[#3B82F6]')
    expect(html).toContain('0.00 USDT')
  })

  it('retains the dark charcoal track background styling', () => {
    const html = renderToString(<SimulatorBalance {...defaultProps} />)

    expect(html).toContain('bg-[#090A0C]')
    expect(html).toContain('border-[#292C30]')
  })

  it('correctly handles split balances with proportional widths', () => {
    const splitProps = {
      balance: '10000.00',
      availablePercent: 70,
      inOrdersPercent: 30,
      availableUsdt: '7000.00',
      inOrdersUsdt: '3000.00',
    }

    const html = renderToString(<SimulatorBalance {...splitProps} />)

    expect(html).toContain('style="width:70%"')
    expect(html).toContain('style="width:30%"')
    expect(html).toContain('70%')
    expect(html).toContain('30%')
  })
})
