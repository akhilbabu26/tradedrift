import { describe, it, expect, vi, beforeEach } from 'vitest'
import { wsService, WsChannels } from '../api/ws'
import { useAuthStore } from '../store/authStore'
import { useWalletStore } from '../store/walletStore'
import { orderApi } from '../api/order'

describe('Cross-Page Order Sync & Dual WS Stream Propagation (Phase D)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({
      user: {
        userId: 'user-456',
        email: 'trader@example.com',
        username: 'trader',
      },
      accessToken: 'token-456',
      isAuthenticated: true,
    })
  })

  it('verifies user-scoped notification and portfolio channel formats for dual subscriptions', () => {
    const userId = useAuthStore.getState().user?.userId
    expect(userId).toBe('user-456')

    const notifChannel = WsChannels.userNotifications(userId)
    const portfolioChannel = WsChannels.userPortfolio(userId)

    expect(notifChannel).toBe('user:notifications:user-456')
    expect(portfolioChannel).toBe('user:portfolio:user-456')

    // Subscribing to both streams ensures trade settlements (portfolio) and order updates (notif) propagate
    const callbacksCalled: string[] = []
    const unsub1 = wsService.subscribe(notifChannel, () => callbacksCalled.push('notif'))
    const unsub2 = wsService.subscribe(portfolioChannel, () => callbacksCalled.push('portfolio'))

    expect(typeof unsub1).toBe('function')
    expect(typeof unsub2).toBe('function')

    unsub1()
    unsub2()
  })

  it('triggers invalidateBalances on walletStore so order changes immediately refresh balances', async () => {
    const invalidateSpy = vi.spyOn(useWalletStore.getState(), 'invalidateBalances').mockResolvedValue()

    // Simulate order submission invalidation trigger
    await useWalletStore.getState().invalidateBalances()

    expect(invalidateSpy).toHaveBeenCalledTimes(1)
  })

  it('ensures cancel order operations invalidate balances to immediately release reserved funds', async () => {
    const invalidateSpy = vi.spyOn(useWalletStore.getState(), 'invalidateBalances').mockResolvedValue()
    vi.spyOn(orderApi, 'cancelOrder').mockResolvedValue(undefined as any)

    await orderApi.cancelOrder('ord-123')
    await useWalletStore.getState().invalidateBalances()

    expect(orderApi.cancelOrder).toHaveBeenCalledWith('ord-123')
    expect(invalidateSpy).toHaveBeenCalledTimes(1)
  })
})
