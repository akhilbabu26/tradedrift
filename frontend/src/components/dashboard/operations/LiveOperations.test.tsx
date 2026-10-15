import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import LiveOperations from './LiveOperations'
import { isOrderActive } from '../../../api/order'
import type { ActiveOrder } from '../../../types/dashboard'

describe('Active Order Status and LiveOperations Component', () => {
  describe('isOrderActive helper', () => {
    it('classifies open and pending orders as active', () => {
      expect(isOrderActive('OPEN')).toBe(true)
      expect(isOrderActive('open')).toBe(true)
      expect(isOrderActive('ORDER_STATUS_OPEN')).toBe(true)
      expect(isOrderActive('PENDING')).toBe(true)
      expect(isOrderActive({ status: 'OPEN' } as any)).toBe(true)
    })

    it('classifies partially filled orders as active', () => {
      expect(isOrderActive('PARTIALLY_FILLED')).toBe(true)
      expect(isOrderActive('partially_filled')).toBe(true)
      expect(isOrderActive('ORDER_STATUS_PARTIALLY_FILLED')).toBe(true)
      expect(isOrderActive({ status: 'PARTIALLY_FILLED' } as any)).toBe(true)
    })

    it('rejects terminal orders as not active', () => {
      expect(isOrderActive('FILLED')).toBe(false)
      expect(isOrderActive('CANCELLED')).toBe(false)
      expect(isOrderActive('REJECTED')).toBe(false)
      expect(isOrderActive(null)).toBe(false)
      expect(isOrderActive(undefined)).toBe(false)
      expect(isOrderActive({ status: 'FILLED' } as any)).toBe(false)
    })
  })

  describe('LiveOperations UI rendering', () => {
    const mockOrders: ActiveOrder[] = [
      {
        id: 'ord-1',
        type: 'Limit Buy',
        side: 'BUY',
        pair: 'BTC/USDT',
        price: '64000.00',
        amount: '0.5000',
        status: 'Open',
      },
      {
        id: 'ord-2',
        type: 'Limit Sell',
        side: 'SELL',
        pair: 'ETH/USDT',
        price: '3400.00',
        amount: '1.2000',
        status: 'Partial',
      },
    ]

    it('renders both open and partially filled orders', () => {
      const html = renderToString(
        <MemoryRouter>
          <LiveOperations orders={mockOrders} />
        </MemoryRouter>
      )

      expect(html).toContain('Live Operations')
      expect(html).toContain('Active Orders')
      expect(html).toContain('BTC/USDT')
      expect(html).toContain('ETH/USDT')
      expect(html).toContain('Open')
      expect(html).toContain('Partial')
      expect(html).toContain('text-amber-400')
      expect(html).toContain('Cancel')
    })

    it('renders empty state when there are no active orders', () => {
      const html = renderToString(
        <MemoryRouter>
          <LiveOperations orders={[]} />
        </MemoryRouter>
      )

      expect(html).toContain('No active orders')
      expect(html).toContain('Place an order on the Trade page')
    })
  })
})
