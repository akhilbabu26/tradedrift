import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import { calculateOrderProgress, formatOrderProgress } from '../../utils/formatters'
import { normalizeOrder } from '../../api/order'
import OrdersTable from './OrdersTable'
import OpenOrdersSection from '../orders/OpenOrdersSection'
import type { Order } from '../../types/trade'
import type { OpenOrderItem } from '../../types/orders'

describe('Order Execution Progress & Cancellation Preservation', () => {
  // ── 1. A 5 SOL order with 0.56 SOL filled displays 11.2% ─────────────────
  it('calculates and formats 11.2% for 0.56 SOL filled out of 5.0 SOL', () => {
    const rawProgress = calculateOrderProgress('0.560000', '5.000000')
    expect(rawProgress).toBe(11.2)
    expect(formatOrderProgress(rawProgress)).toBe('11.2%')
  })

  // ── 2. A cancelled, partially filled order retains its fill progress ────
  it('retains 11.2% fill progress for a cancelled partially filled order', () => {
    const cancelledOrder: Order = {
      id: '01a12b35-206f-7cc4-828d-569fa4b7cc6d',
      user_id: 'user-audit-1',
      market_id: 'SOL-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      status: 'CANCELLED',
      price: '109.52',
      quantity: '5.000000',
      filled_quantity: '0.560000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:15Z',
    }

    const progress = calculateOrderProgress(cancelledOrder.filled_quantity, cancelledOrder.quantity)
    expect(progress).toBe(11.2)
    expect(formatOrderProgress(progress)).toBe('11.2%')

    // Renders in Trading Terminal OrdersTable
    const html = renderToString(
      <OrdersTable orders={[cancelledOrder]} cancellingId={null} onCancel={vi.fn()} />
    )
    expect(html).toContain('11.2%')
    expect(html).toContain('style="width:11.2%"')
    expect(html).toContain('4.4400') // remaining quantity at cancellation
    expect(html).toContain('Cancelled')
  })

  // ── 3. An unfilled cancelled order displays 0% ──────────────────────────
  it('displays 0% for an unfilled cancelled order', () => {
    const unfilledCancelledOrder: Order = {
      id: 'ord-unfilled-cancelled',
      user_id: 'user-audit-1',
      market_id: 'SOL-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      status: 'CANCELLED',
      price: '109.52',
      quantity: '5.000000',
      filled_quantity: '0.000000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:15Z',
    }

    const progress = calculateOrderProgress(unfilledCancelledOrder.filled_quantity, unfilledCancelledOrder.quantity)
    expect(progress).toBe(0)
    expect(formatOrderProgress(progress)).toBe('0%')

    const html = renderToString(
      <OrdersTable orders={[unfilledCancelledOrder]} cancellingId={null} onCancel={vi.fn()} />
    )
    expect(html).toContain('0%')
  })

  // ── 4. A fully filled order displays 100% ───────────────────────────────
  it('displays 100% for a fully filled order', () => {
    const fullyFilledOrder: Order = {
      id: 'ord-fully-filled',
      user_id: 'user-audit-1',
      market_id: 'SOL-USDT',
      side: 'BUY',
      order_type: 'LIMIT',
      status: 'FILLED',
      price: '109.52',
      quantity: '5.000000',
      filled_quantity: '5.000000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:15Z',
    }

    const progress = calculateOrderProgress(fullyFilledOrder.filled_quantity, fullyFilledOrder.quantity)
    expect(progress).toBe(100)
    expect(formatOrderProgress(progress)).toBe('100%')

    const html = renderToString(
      <OrdersTable orders={[fullyFilledOrder]} cancellingId={null} onCancel={vi.fn()} />
    )
    expect(html).toContain('100%')
  })

  // ── 5. Snake_case and camelCase API payloads are normalized correctly ───
  it('normalizes both snake_case and camelCase API payloads without losing filled quantities', () => {
    const snakePayload = {
      id: 'ord-snake-1',
      user_id: 'usr-1',
      market_id: 'SOL-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      price: '109.52',
      quantity: '5.000000',
      filled_quantity: '0.560000',
      status: 'CANCELLED',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:15Z',
    }

    const camelPayload = {
      id: 'ord-camel-1',
      userId: 'usr-1',
      marketId: 'SOL-USDT',
      side: 'SELL',
      orderType: 'LIMIT',
      price: '109.52',
      totalQuantity: '5.000000',
      filledQuantity: '0.560000',
      status: 'CANCELLED',
      createdAt: '2026-10-11T12:00:00Z',
      updatedAt: '2026-10-11T12:00:15Z',
    }

    const normalizedSnake = normalizeOrder(snakePayload)
    const normalizedCamel = normalizeOrder(camelPayload)

    expect(normalizedSnake.quantity).toBe('5.000000')
    expect(normalizedSnake.filled_quantity).toBe('0.560000')
    expect(normalizedSnake.status).toBe('CANCELLED')

    expect(normalizedCamel.quantity).toBe('5.000000')
    expect(normalizedCamel.filled_quantity).toBe('0.560000')
    expect(normalizedCamel.market_id).toBe('SOL-USDT')
    expect(normalizedCamel.status).toBe('CANCELLED')

    expect(calculateOrderProgress(normalizedSnake.filled_quantity, normalizedSnake.quantity)).toBe(11.2)
    expect(calculateOrderProgress(normalizedCamel.filled_quantity, normalizedCamel.quantity)).toBe(11.2)
  })

  // ── 6. Missing or zero original quantity does not cause NaN or Infinity ─
  it('handles zero, missing, or invalid quantities safely without NaN or Infinity', () => {
    expect(calculateOrderProgress('0.56', '0')).toBe(0)
    expect(formatOrderProgress(calculateOrderProgress('0.56', '0'))).toBe('0%')

    expect(calculateOrderProgress('0.56', '')).toBe(0)
    expect(formatOrderProgress(calculateOrderProgress('0.56', ''))).toBe('0%')

    expect(calculateOrderProgress(undefined as any, '5.0')).toBe(0)
    expect(formatOrderProgress(calculateOrderProgress(undefined as any, '5.0'))).toBe('0%')

    expect(calculateOrderProgress('invalid', '5.0')).toBe(0)
    expect(calculateOrderProgress('0.56', 'invalid')).toBe(0)

    // Clamping to [0, 100]
    expect(calculateOrderProgress('12.0', '10.0')).toBe(100)
    expect(calculateOrderProgress('-1.0', '10.0')).toBe(0)
  })

  // ── 7. Trading Terminal and Orders page display consistent fill progress ─
  it('displays identical 11.2% fill progress between Trading Terminal and Orders page', () => {
    const rawFilled = '0.560000'
    const rawTotal = '5.000000'
    const progressVal = calculateOrderProgress(rawFilled, rawTotal)

    // 1. Trading Terminal order
    const terminalOrder: Order = {
      id: 'ord-tt-1',
      user_id: 'usr-1',
      market_id: 'SOL-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      status: 'PARTIALLY_FILLED',
      price: '109.52',
      quantity: rawTotal,
      filled_quantity: rawFilled,
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:15Z',
    }
    const terminalHtml = renderToString(
      <OrdersTable orders={[terminalOrder]} cancellingId={null} onCancel={vi.fn()} />
    )

    // 2. Orders Page OpenOrderItem
    const ordersPageItem: OpenOrderItem = {
      id: 'ord-tt-1',
      time: 'Just now',
      pair: 'SOL/USDT',
      type: 'Limit',
      side: 'SELL',
      price: '109.52',
      amount: '5.0000 SOL',
      filledRemaining: '0.5600 / 4.4400',
      progress: progressVal,
      status: 'Open',
    }
    const ordersPageHtml = renderToString(
      <OpenOrdersSection
        orders={[ordersPageItem]}
        cancellingId={null}
        onCancel={vi.fn()}
        onScrollToSection={vi.fn()}
      />
    )

    // Both must present 11.2%
    expect(terminalHtml).toContain('11.2%')
    expect(ordersPageHtml).toContain('11.2%')
  })
})
