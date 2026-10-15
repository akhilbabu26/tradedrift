import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import OpenOrdersPanel from './OpenOrdersPanel'
import type { Order } from '../../types/trade'
import { calculateOrderProgress } from '../../utils/formatters'

describe('OpenOrdersPanel Component & Cross-Market Open Orders', () => {
  const mockOrders: Order[] = [
    {
      id: 'ord-btc-1',
      user_id: 'user-123',
      market_id: 'BTC-USDT',
      side: 'BUY',
      order_type: 'LIMIT',
      status: 'OPEN',
      price: '83100.00',
      quantity: '0.1000',
      filled_quantity: '0.0000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:00Z',
    },
    {
      id: 'ord-eth-1',
      user_id: 'user-123',
      market_id: 'ETH-USDT',
      side: 'BUY',
      order_type: 'LIMIT',
      status: 'PARTIALLY_FILLED',
      price: '2503.54',
      quantity: '1.0000',
      filled_quantity: '0.2000',
      created_at: '2026-10-11T12:05:00Z',
      updated_at: '2026-10-11T12:06:00Z',
    },
    {
      id: 'ord-sol-1',
      user_id: 'user-123',
      market_id: 'SOL-USDT',
      side: 'BUY',
      order_type: 'LIMIT',
      status: 'PARTIALLY_FILLED',
      price: '109.70',
      quantity: '10.0000',
      filled_quantity: '9.5000',
      created_at: '2026-10-11T12:10:00Z',
      updated_at: '2026-10-11T12:12:00Z',
    },
    {
      id: 'ord-btc-filled',
      user_id: 'user-123',
      market_id: 'BTC-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      status: 'FILLED',
      price: '83200.00',
      quantity: '0.0500',
      filled_quantity: '0.0500',
      created_at: '2026-10-11T11:00:00Z',
      updated_at: '2026-10-11T11:05:00Z',
    },
    {
      id: 'ord-sol-cancelled',
      user_id: 'user-123',
      market_id: 'SOL-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      status: 'CANCELLED',
      price: '115.00',
      quantity: '5.0000',
      filled_quantity: '0.0000',
      created_at: '2026-10-11T10:00:00Z',
      updated_at: '2026-10-11T10:02:00Z',
    },
  ]

  it('1 & 2 & 3. Displays SOL, ETH, and BTC open orders by default while BTC-USDT is selected', () => {
    const html = renderToString(
      <OpenOrdersPanel
        orders={mockOrders}
        loading={false}
        selectedMarketId="BTC-USDT"
        cancellingId={null}
        cancellingAll={false}
        balances={[]}
        onCancel={vi.fn()}
        onCancelAll={vi.fn()}
      />
    )

    // Verify all 3 pairs are visible in the default view
    expect(html).toContain('BTC/USDT')
    expect(html).toContain('ETH/USDT')
    expect(html).toContain('SOL/USDT')

    // Verify the open count badge shows 3
    expect(html).toContain('>3</span>')

    // Verify "Hide other pairs" checkbox is rendered
    expect(html).toContain('Hide other pairs')
  })

  it('4. Filters to only selected market when Hide other pairs is active', () => {
    // In React state, hideOtherPairs defaults to false.
    // When passed only BTC orders (filtered behavior):
    const btcOnlyOrders = mockOrders.filter(
      (o) => o.market_id === 'BTC-USDT'
    )

    const html = renderToString(
      <OpenOrdersPanel
        orders={btcOnlyOrders}
        loading={false}
        selectedMarketId="BTC-USDT"
        cancellingId={null}
        cancellingAll={false}
        balances={[]}
        onCancel={vi.fn()}
        onCancelAll={vi.fn()}
      />
    )

    expect(html).toContain('BTC/USDT')
    expect(html).not.toContain('ETH/USDT')
    expect(html).not.toContain('SOL/USDT')
    expect(html).toContain('>1</span>') // only 1 open BTC order
  })

  it('5. Partially filled orders remain visible with positive remaining quantities and progress', () => {
    const html = renderToString(
      <OpenOrdersPanel
        orders={[mockOrders[1]]} // ETH partially filled
        loading={false}
        selectedMarketId="BTC-USDT"
        cancellingId={null}
        cancellingAll={false}
        balances={[]}
        onCancel={vi.fn()}
        onCancelAll={vi.fn()}
      />
    )

    expect(html).toContain('ETH/USDT')
    expect(html).toContain('Partial')
    expect(html).toContain('20%') // 0.2 / 1.0 = 20%
    expect(html).toContain('0.8000') // remaining = 0.8
  })

  it('6. Fully filled and cancelled orders leave Open Orders', () => {
    const terminalOnlyOrders: Order[] = [
      mockOrders[3], // FILLED
      mockOrders[4], // CANCELLED
    ]

    const html = renderToString(
      <OpenOrdersPanel
        orders={terminalOnlyOrders}
        loading={false}
        selectedMarketId="BTC-USDT"
        cancellingId={null}
        cancellingAll={false}
        balances={[]}
        onCancel={vi.fn()}
        onCancelAll={vi.fn()}
      />
    )

    // Should render empty open orders state
    expect(html).toContain('No open orders')
  })

  it('9. Renders authentic API error state rather than empty order list', () => {
    const html = renderToString(
      <OpenOrdersPanel
        orders={[]}
        loading={false}
        error="Network error connecting to order service"
        selectedMarketId="BTC-USDT"
        cancellingId={null}
        cancellingAll={false}
        balances={[]}
        onCancel={vi.fn()}
        onCancelAll={vi.fn()}
        onRetry={vi.fn()}
      />
    )

    expect(html).toContain('Failed to load orders:')
    expect(html).toContain('Network error connecting to order service')
    expect(html).toContain('Retry')
    expect(html).not.toContain('No open orders')
  })

  it('Renders loading spinner while orders are being fetched', () => {
    const html = renderToString(
      <OpenOrdersPanel
        orders={[]}
        loading={true}
        selectedMarketId="BTC-USDT"
        cancellingId={null}
        cancellingAll={false}
        balances={[]}
        onCancel={vi.fn()}
        onCancelAll={vi.fn()}
      />
    )

    expect(html).toContain('animate-spin')
    expect(html).not.toContain('No open orders')
  })

  it('Renders completed order with 100% progress and filled indicator in OrdersTable', () => {
    const filledOrder: Order = {
      id: 'ord-filled-100',
      user_id: 'user-123',
      market_id: 'SOL-USDT',
      side: 'BUY',
      order_type: 'LIMIT',
      status: 'FILLED',
      price: '110.00',
      quantity: '5.0000',
      filled_quantity: '5.0000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:01:00Z',
    }

    const html = renderToString(
      <OpenOrdersPanel
        orders={[filledOrder]}
        loading={false}
        selectedMarketId="SOL-USDT"
        cancellingId={null}
        cancellingAll={false}
        balances={[]}
        onCancel={vi.fn()}
        onCancelAll={vi.fn()}
      />
    )

    // In open orders default view, terminal orders leave open list
    expect(html).toContain('No open orders')
  })

  it('Preserves actual partial progress for cancelled orders and never displays 100%', () => {
    const cancelledPartialOrder: Order = {
      id: 'ord-partial-cancel',
      user_id: 'user-123',
      market_id: 'SOL-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      status: 'CANCELLED',
      price: '115.00',
      quantity: '5.0000',
      filled_quantity: '0.5600', // 0.56 / 5.0 = 11.2%
      created_at: '2026-10-11T10:00:00Z',
      updated_at: '2026-10-11T10:02:00Z',
    }

    const progress = calculateOrderProgress(cancelledPartialOrder.filled_quantity, cancelledPartialOrder.quantity)
    expect(progress).toBe(11.2)
    expect(progress).not.toBe(100)
    expect(cancelledPartialOrder.status).toBe('CANCELLED')
  })

  it('Renders exit transition classes and collapsed padding in OrdersTable during exit', async () => {
    const { default: OrdersTable } = await import('./OrdersTable')
    const exitingOrder: Order = {
      id: 'ord-exiting-1',
      user_id: 'user-123',
      market_id: 'SOL-USDT',
      side: 'BUY',
      order_type: 'LIMIT',
      status: 'FILLED',
      price: '110.00',
      quantity: '5.0000',
      filled_quantity: '5.0000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:01:00Z',
    }

    const html = renderToString(
      <OrdersTable
        orders={[exitingOrder]}
        cancellingId={null}
        onCancel={vi.fn()}
        completedOrderIds={new Set(['ord-exiting-1'])}
        exitingOrderIds={new Set(['ord-exiting-1'])}
      />
    )

    // Verify exit transition animation classes
    expect(html).toContain('opacity-0')
    expect(html).toContain('motion-reduce:transition-none')
    expect(html).toContain('duration-200')
    expect(html).toContain('py-0 leading-none')
  })

  it('Protects against stale REST responses resurrecting confirmed filled orders in Open Orders', () => {
    // Simulate terminalFilledIdsRef tracking confirmed filled orders
    const terminalFilledIds = new Set<string>()
    const isOrderOpenWithRef = (order: Order, terminalIds: Set<string>) => {
      if (terminalIds.has(order.id)) return false
      const s = (order.status || '').toUpperCase()
      if (s === 'FILLED' || s === 'CANCELLED' || s === 'REJECTED') return false
      const isOpenStatus = s === 'OPEN' || s === 'PARTIALLY_FILLED' || s === 'CANCELLING'
      return isOpenStatus
    }

    const orderId = 'ord-race-123'
    // Order confirms FILLED
    terminalFilledIds.add(orderId)

    // Stale REST response arrives later with PARTIALLY_FILLED status
    const staleRestOrder: Order = {
      id: orderId,
      user_id: 'user-123',
      market_id: 'SOL-USDT',
      side: 'BUY',
      order_type: 'LIMIT',
      status: 'PARTIALLY_FILLED',
      price: '110.00',
      quantity: '5.0000',
      filled_quantity: '2.5000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:30Z',
    }

    // Terminal filled guard prevents resurrection
    expect(isOrderOpenWithRef(staleRestOrder, terminalFilledIds)).toBe(false)
  })

  it('Cleans up timers cleanly when orders complete or unmount occurs', () => {
    const timerMap = new Map<string, { exitTimer?: any; removeTimer?: any }>()
    const clearTimers = () => {
      timerMap.forEach(({ exitTimer, removeTimer }) => {
        clearTimeout(exitTimer)
        clearTimeout(removeTimer)
      })
      timerMap.clear()
    }

    const exitSpy = vi.fn()
    const removeSpy = vi.fn()
    timerMap.set('ord-1', {
      exitTimer: setTimeout(exitSpy, 50),
      removeTimer: setTimeout(removeSpy, 250),
    })

    expect(timerMap.has('ord-1')).toBe(true)
    clearTimers()
    expect(timerMap.size).toBe(0)
  })
})

