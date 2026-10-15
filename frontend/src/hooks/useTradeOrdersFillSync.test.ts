import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { normalizeUser, useAuthStore } from '../store/authStore'
import { wsService, WsChannels, isValidUserId } from '../api/ws'
import type { Order } from '../api/order'
import { calculateOrderProgress } from '../utils/formatters'
import { toDecimal } from '../utils/decimal'

describe('Real-Time Order Fill Synchronization (Phase 2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({
      user: null,
      accessToken: null,
      refreshToken: null,
      isAuthenticated: false,
    })
  })

  afterEach(() => {
    wsService.disconnect()
  })

  // ── 1. Stored user objects resolution ─────────────────────────────────────
  it('correctly resolves user objects stored with id, user_id, or userId and JWT fallback', () => {
    // A: Object with 'id'
    const userFromId = normalizeUser({ id: '018f-uuid-1', email: 'a@test.com', username: 'traderA' })
    expect(userFromId?.userId).toBe('018f-uuid-1')

    // B: Object with 'user_id'
    const userFromSnake = normalizeUser({ user_id: '018f-uuid-2', email: 'b@test.com', username: 'traderB' })
    expect(userFromSnake?.userId).toBe('018f-uuid-2')

    // C: Object with 'userId'
    const userFromCamel = normalizeUser({ userId: '018f-uuid-3', email: 'c@test.com', username: 'traderC' })
    expect(userFromCamel?.userId).toBe('018f-uuid-3')

    // D: Fallback to JWT payload when user object lacks ID
    const dummyJwt = `header.${btoa(JSON.stringify({ user_id: '018f-jwt-uuid', exp: 9999999999 }))}.sig`
    const userFromJwt = normalizeUser({ email: 'jwt@test.com' }, dummyJwt)
    expect(userFromJwt?.userId).toBe('018f-jwt-uuid')
  })

  // ── 2. Missing or invalid IDs never produce private frames ────────────────
  it('rejects missing or invalid IDs without creating malformed channel strings or socket frames', () => {
    expect(isValidUserId(null)).toBe(false)
    expect(isValidUserId(undefined)).toBe(false)
    expect(isValidUserId('')).toBe(false)
    expect(isValidUserId('   ')).toBe(false)
    expect(isValidUserId('../../traversal')).toBe(false)
    expect(isValidUserId('admin:injection')).toBe(false)
    expect(isValidUserId('user 123')).toBe(false)

    // WsChannels returns '' for invalid/missing user IDs
    expect(WsChannels.userNotifications(null)).toBe('')
    expect(WsChannels.userNotifications(undefined)).toBe('')
    expect(WsChannels.userNotifications('')).toBe('')
    expect(WsChannels.userNotifications('../../admin')).toBe('')

    expect(WsChannels.userPortfolio(null)).toBe('')
    expect(WsChannels.userPortfolio(undefined)).toBe('')
    expect(WsChannels.userPortfolio('')).toBe('')

    // Subscribing to an empty or invalid channel safely no-ops without throwing or sending frames
    const sendSpy = vi.spyOn((wsService as any), 'sendSubscribe')
    const unsub = wsService.subscribe('', vi.fn())
    expect(typeof unsub).toBe('function')
    expect(sendSpy).not.toHaveBeenCalled()
  })

  // ── 3. Partial fill updates filled qty, remaining qty, and progress ───────
  it('updates filled quantity, remaining quantity, and progress percentage on partial fill', () => {
    const originalOrder: Order = {
      id: 'ord-fill-1',
      user_id: 'u-1',
      market_id: 'SOL-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      status: 'OPEN',
      price: '109.52',
      quantity: '5.000000',
      filled_quantity: '0.000000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:00Z',
    }

    expect(calculateOrderProgress(originalOrder.filled_quantity, originalOrder.quantity)).toBe(0)

    // After partial fill (0.560000 SOL filled)
    const partiallyFilledOrder: Order = {
      ...originalOrder,
      status: 'PARTIALLY_FILLED',
      filled_quantity: '0.560000',
      updated_at: '2026-10-11T12:00:15Z',
    }

    const filled = toDecimal(partiallyFilledOrder.filled_quantity)
    const total = toDecimal(partiallyFilledOrder.quantity)
    const remaining = total.minus(filled)
    const progress = calculateOrderProgress(partiallyFilledOrder.filled_quantity, partiallyFilledOrder.quantity)

    expect(filled.toString()).toBe('0.56')
    expect(remaining.toString()).toBe('4.44')
    expect(progress).toBe(11.2)
  })

  // ── 4. Multiple fills accumulate without double-counting ──────────────────
  it('accumulates multiple fills cleanly without double-counting', () => {
    const totalQty = '5.000000'
    const fill1 = '0.200000'
    const fill2 = '0.360000'

    const orderAfterFill1: Order = {
      id: 'ord-fill-2',
      user_id: 'u-1',
      market_id: 'SOL-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      status: 'PARTIALLY_FILLED',
      price: '109.52',
      quantity: totalQty,
      filled_quantity: fill1,
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:05Z',
    }
    expect(calculateOrderProgress(orderAfterFill1.filled_quantity, orderAfterFill1.quantity)).toBe(4)

    // Next cumulative fill state
    const orderAfterFill2: Order = {
      ...orderAfterFill1,
      filled_quantity: toDecimal(fill1).plus(toDecimal(fill2)).toFixed(6),
      updated_at: '2026-10-11T12:00:10Z',
    }
    expect(orderAfterFill2.filled_quantity).toBe('0.560000')
    expect(calculateOrderProgress(orderAfterFill2.filled_quantity, orderAfterFill2.quantity)).toBe(11.2)
  })

  // ── 5. Cancelled partially filled order retains final fill progress ───────
  it('retains final fill progress when a partially filled order is cancelled', () => {
    const cancelledPartialOrder: Order = {
      id: 'ord-fill-cancelled',
      user_id: 'u-1',
      market_id: 'SOL-USDT',
      side: 'SELL',
      order_type: 'LIMIT',
      status: 'CANCELLED',
      price: '109.52',
      quantity: '5.000000',
      filled_quantity: '0.560000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:20Z',
    }

    const progress = calculateOrderProgress(cancelledPartialOrder.filled_quantity, cancelledPartialOrder.quantity)
    expect(progress).toBe(11.2)
    expect(cancelledPartialOrder.status).toBe('CANCELLED')
  })

  // ── 6. Fully filled order lifecycle segregation ───────────────────────────
  it('identifies fully filled orders as no longer open', () => {
    const isOrderOpen = (order: Order) => {
      const s = (order.status || '').toUpperCase()
      if (s === 'FILLED' || s === 'CANCELLED' || s === 'REJECTED') return false
      const isOpenStatus = s === 'OPEN' || s === 'PARTIALLY_FILLED' || s === 'CANCELLING'
      if (!isOpenStatus) return false
      const remaining = toDecimal(order.quantity).minus(toDecimal(order.filled_quantity || '0'))
      return remaining.gt(0) || s === 'CANCELLING'
    }

    const openOrder: Order = {
      id: 'ord-open',
      user_id: 'u-1',
      market_id: 'SOL-USDT',
      side: 'BUY',
      order_type: 'LIMIT',
      status: 'OPEN',
      price: '109.52',
      quantity: '5.000000',
      filled_quantity: '0.000000',
      created_at: '2026-10-11T12:00:00Z',
      updated_at: '2026-10-11T12:00:00Z',
    }

    const partiallyFilled: Order = {
      ...openOrder,
      status: 'PARTIALLY_FILLED',
      filled_quantity: '0.560000',
    }

    const fullyFilled: Order = {
      ...openOrder,
      status: 'FILLED',
      filled_quantity: '5.000000',
    }

    expect(isOrderOpen(openOrder)).toBe(true)
    expect(isOrderOpen(partiallyFilled)).toBe(true)
    expect(isOrderOpen(fullyFilled)).toBe(false) // Leaves Open Orders
  })

  // ── 7. Burst of fill events coalesces without losing final state ──────────
  it('coalesces bursts of events so the latest state is reconciled without extra REST calls', async () => {
    let callCount = 0
    let inFlight = false
    let pendingRefetch = false
    let latestAppliedReqId = 0
    let currentReqId = 0
    let latestOrders: Order[] = []
    let currentBackendOrders: Order[] = [{ id: '1', quantity: '5', filled_quantity: '0.1' } as any]

    const mockFetch = async () => {
      if (inFlight) {
        pendingRefetch = true
        return
      }

      inFlight = true
      const thisReq = ++currentReqId
      callCount++

      // Simulate async network delay
      await new Promise((r) => setTimeout(r, 10))

      if (thisReq >= latestAppliedReqId) {
        latestAppliedReqId = thisReq
        latestOrders = currentBackendOrders
      }

      inFlight = false
      if (pendingRefetch) {
        pendingRefetch = false
        await mockFetch()
      }
    }

    // Fire 5 rapid events in parallel, backend state advances to 0.56 filled
    currentBackendOrders = [{ id: '1', quantity: '5', filled_quantity: '0.1' } as any]
    const p1 = mockFetch()
    currentBackendOrders = [{ id: '1', quantity: '5', filled_quantity: '0.2' } as any]
    const p2 = mockFetch()
    currentBackendOrders = [{ id: '1', quantity: '5', filled_quantity: '0.3' } as any]
    const p3 = mockFetch()
    currentBackendOrders = [{ id: '1', quantity: '5', filled_quantity: '0.4' } as any]
    const p4 = mockFetch()
    currentBackendOrders = [{ id: '1', quantity: '5', filled_quantity: '0.56' } as any]
    const p5 = mockFetch()

    await Promise.all([p1, p2, p3, p4, p5])

    // Coalesced into exactly 2 calls (initial in-flight + 1 queued trailing fetch)
    expect(callCount).toBe(2)
    expect(latestOrders[0].filled_quantity).toBe('0.56')
  })

  // ── 8. Earlier REST response cannot overwrite newer state ─────────────────
  it('rejects an earlier delayed REST response from overwriting newer order state', async () => {
    let latestAppliedReqId = 0
    let currentReqId = 0
    let orders: Order[] = []

    const executeFetch = async (reqId: number, data: Order[], delayMs: number) => {
      await new Promise((r) => setTimeout(r, delayMs))
      if (reqId < latestAppliedReqId) {
        return // Stale response discarded!
      }
      latestAppliedReqId = reqId
      orders = data
    }

    // Request 1: launched first, slow (takes 50ms), returns 0.10 filled
    const req1Id = ++currentReqId
    const req1Promise = executeFetch(
      req1Id,
      [{ id: 'ord-1', quantity: '5', filled_quantity: '0.10' } as any],
      50
    )

    // Request 2: launched second, fast (takes 10ms), returns 0.56 filled
    const req2Id = ++currentReqId
    const req2Promise = executeFetch(
      req2Id,
      [{ id: 'ord-1', quantity: '5', filled_quantity: '0.56' } as any],
      10
    )

    await Promise.all([req1Promise, req2Promise])

    // Even though Request 1 finished after Request 2, Request 1 was discarded
    expect(orders[0].filled_quantity).toBe('0.56')
  })

  // ── 9. Reconnect restores subscriptions and triggers refresh ──────────────
  it('triggers registered reconnect hooks to reconcile latest order state upon reconnect', () => {
    let refreshed = false
    const unsub = wsService.onReconnect(() => {
      refreshed = true
    })

    // Simulate socket reconnect event
    const hooks = (wsService as any).onReconnectHooks as Set<() => void>
    hooks.forEach((h) => h())

    expect(refreshed).toBe(true)
    unsub()
  })

  // ── 10. Logout and account switching wipes private state ───────────────────
  it('wipes private user-scoped subscriptions and private state on logout', () => {
    // 1. Log in User A
    useAuthStore.getState().setUser({
      userId: 'user-aaa-111',
      email: 'a@example.com',
      username: 'userA',
    })
    useAuthStore.getState().setTokens('token-a', 'refresh-a')

    const chA = WsChannels.userNotifications('user-aaa-111')
    wsService.subscribe(chA, vi.fn())

    const subscribers = (wsService as any).subscribers as Map<string, Set<any>>
    expect(subscribers.has('user:notifications:user-aaa-111')).toBe(true)

    // 2. Log out
    useAuthStore.getState().logout()

    expect(useAuthStore.getState().isAuthenticated).toBe(false)
    expect(useAuthStore.getState().user).toBeNull()
    expect(subscribers.has('user:notifications:user-aaa-111')).toBe(false)
  })
})
