import { describe, it, expect } from 'vitest'
import { isOrderActive, isOrderTerminal, type Order } from './order'

describe('Order Status Logic (isOrderActive & isOrderTerminal)', () => {
  describe('isOrderActive', () => {
    it('returns true for active open statuses', () => {
      expect(isOrderActive('OPEN')).toBe(true)
      expect(isOrderActive('open')).toBe(true)
      expect(isOrderActive('ORDER_STATUS_OPEN')).toBe(true)
      expect(isOrderActive('PENDING')).toBe(true)
      expect(isOrderActive('pending')).toBe(true)
    })

    it('returns true for partially filled orders', () => {
      expect(isOrderActive('PARTIALLY_FILLED')).toBe(true)
      expect(isOrderActive('partially_filled')).toBe(true)
      expect(isOrderActive('ORDER_STATUS_PARTIALLY_FILLED')).toBe(true)
      expect(isOrderActive('Partial')).toBe(true)
    })

    it('returns true for in-flight cancelling orders', () => {
      expect(isOrderActive('CANCELLING')).toBe(true)
      expect(isOrderActive('cancelling')).toBe(true)
    })

    it('returns false for terminal settled statuses', () => {
      expect(isOrderActive('FILLED')).toBe(false)
      expect(isOrderActive('filled')).toBe(false)
      expect(isOrderActive('ORDER_STATUS_FILLED')).toBe(false)
      expect(isOrderActive('CANCELLED')).toBe(false)
      expect(isOrderActive('cancelled')).toBe(false)
      expect(isOrderActive('CANCELED')).toBe(false)
      expect(isOrderActive('ORDER_STATUS_CANCELLED')).toBe(false)
      expect(isOrderActive('REJECTED')).toBe(false)
      expect(isOrderActive('rejected')).toBe(false)
      expect(isOrderActive('ORDER_STATUS_REJECTED')).toBe(false)
    })

    it('returns false for nil, undefined, or empty statuses', () => {
      expect(isOrderActive(null)).toBe(false)
      expect(isOrderActive(undefined)).toBe(false)
      expect(isOrderActive('')).toBe(false)
      expect(isOrderActive('UNKNOWN_STATUS')).toBe(false)
    })

    it('works seamlessly when passed Order objects', () => {
      const openOrder: Order = {
        id: 'ord-1',
        user_id: 'user-1',
        market_id: 'BTC-USDT',
        side: 'BUY',
        order_type: 'LIMIT',
        price: '90000',
        quantity: '1.0',
        filled_quantity: '0.0',
        status: 'OPEN',
        created_at: '',
        updated_at: '',
      }
      expect(isOrderActive(openOrder)).toBe(true)

      const partialOrder: Order = {
        ...openOrder,
        id: 'ord-2',
        status: 'PARTIALLY_FILLED',
        filled_quantity: '0.4',
      }
      expect(isOrderActive(partialOrder)).toBe(true)

      const cancellingOrder: Order = {
        ...openOrder,
        id: 'ord-3',
        status: 'CANCELLING',
      }
      expect(isOrderActive(cancellingOrder)).toBe(true)

      const filledOrder: Order = {
        ...openOrder,
        id: 'ord-4',
        status: 'FILLED',
        filled_quantity: '1.0',
      }
      expect(isOrderActive(filledOrder)).toBe(false)

      const cancelledOrder: Order = {
        ...openOrder,
        id: 'ord-5',
        status: 'CANCELLED',
      }
      expect(isOrderActive(cancelledOrder)).toBe(false)
    })
  })

  describe('isOrderTerminal', () => {
    it('returns true strictly for terminal states', () => {
      expect(isOrderTerminal('FILLED')).toBe(true)
      expect(isOrderTerminal('filled')).toBe(true)
      expect(isOrderTerminal('CANCELLED')).toBe(true)
      expect(isOrderTerminal('REJECTED')).toBe(true)
      expect(isOrderTerminal('ORDER_STATUS_FILLED')).toBe(true)
      expect(isOrderTerminal('ORDER_STATUS_CANCELLED')).toBe(true)
      expect(isOrderTerminal('ORDER_STATUS_REJECTED')).toBe(true)
    })

    it('returns false for active or intermediate states', () => {
      expect(isOrderTerminal('OPEN')).toBe(false)
      expect(isOrderTerminal('PARTIALLY_FILLED')).toBe(false)
      expect(isOrderTerminal('CANCELLING')).toBe(false)
      expect(isOrderTerminal('PENDING')).toBe(false)
      expect(isOrderTerminal(null)).toBe(false)
      expect(isOrderTerminal(undefined)).toBe(false)
    })
  })
})
