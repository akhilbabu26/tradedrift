import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useWalletStore } from './walletStore'
import { walletApi } from '../api/wallet'
import { wsService } from '../api/ws'
import { useAuthStore } from './authStore'

vi.mock('../api/wallet', () => ({
  walletApi: {
    getAllBalances: vi.fn(),
    getBalance: vi.fn(),
  },
}))

describe('walletStore', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useWalletStore.getState().reset()
    useAuthStore.setState({
      user: { userId: '00000000-0000-0000-0000-000000000001', email: 'test@trade.com', username: 'trader1' },
      isAuthenticated: true,
      accessToken: 'token',
      refreshToken: 'refresh',
    })
  })

  it('loads balances and indexes them by asset', async () => {
    vi.mocked(walletApi.getAllBalances).mockResolvedValueOnce([
      { asset: 'BTC', availableBalance: '1.50000000', reservedBalance: '0.20000000' },
      { asset: 'USDT', availableBalance: '10000.00', reservedBalance: '500.00' },
    ])

    await useWalletStore.getState().loadBalances()

    const state = useWalletStore.getState()
    expect(state.balances).toHaveLength(2)

    const btc = state.getBalance('BTC')
    expect(btc.availableBalance).toBe('1.50000000')
    expect(btc.reservedBalance).toBe('0.20000000')

    const usdt = state.getBalance('usdt') // case-insensitive
    expect(usdt.availableBalance).toBe('10000.00')

    const eth = state.getBalance('ETH') // fallback for unknown asset
    expect(eth.availableBalance).toBe('0.00')
    expect(eth.reservedBalance).toBe('0.00')
  })

  it('subscribes to WebSocket user streams and refetches balances on events', async () => {
    let notifCallback: (() => void) | undefined
    vi.spyOn(wsService, 'subscribe').mockImplementation((_channel, cb) => {
      notifCallback = cb as () => void
      return () => {}
    })

    vi.mocked(walletApi.getAllBalances).mockResolvedValue([
      { asset: 'USDT', availableBalance: '5000.00', reservedBalance: '0.00' },
    ])

    const unsub = useWalletStore.getState().subscribeToBalanceUpdates()
    expect(wsService.subscribe).toHaveBeenCalled()
    expect(notifCallback).toBeDefined()

    // Simulate incoming trade fill notification triggering balance invalidation
    notifCallback!()

    // Verify getAllBalances was called to refresh
    expect(walletApi.getAllBalances).toHaveBeenCalled()

    unsub()
  })
})
