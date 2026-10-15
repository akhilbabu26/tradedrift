import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useMarketStore, resetMarketStoreForTesting } from './marketStore'
import { formatHighLow, formatTurnover } from '../utils/formatters'
import { marketApi } from '../api/market'
import { wsService } from '../api/ws'

vi.mock('../api/market', () => ({
  marketApi: {
    getMarkets: vi.fn(),
    getMarketsOverview: vi.fn(),
    getTicker: vi.fn(),
  },
}))

describe('marketStore', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetMarketStoreForTesting()
  })

  it('populates initial markets and overview from REST API', async () => {
    vi.mocked(marketApi.getMarkets).mockResolvedValueOnce([
      {
        id: 'BTC-USDT',
        base_asset: 'BTC',
        quote_asset: 'USDT',
        tick_size: '0.01',
        lot_size: '0.0001',
        status: 'ACTIVE',
        min_quantity: '0.0001',
        created_at: '',
        updated_at: '',
      },
    ])
    vi.mocked(marketApi.getMarketsOverview).mockResolvedValueOnce([
      {
        market_id: 'BTC-USDT',
        symbol: 'BTC/USDT',
        base_asset: 'BTC',
        quote_asset: 'USDT',
        last_price: '65000.00',
        high_24h: '66000.00',
        low_24h: '64000.00',
        volume_24h: '150.5',
        quote_volume_24h: '9782500',
        price_change_24h_percent: '1.5',
        trend: ['64500', '65000'],
      },
    ])

    await useMarketStore.getState().loadMarketsAndOverview()

    const state = useMarketStore.getState()
    expect(state.markets).toHaveLength(1)
    expect(state.tickers['BTC-USDT']?.last_price).toBe('65000.00')
    expect(state.trends['BTC-USDT']).toEqual([64500, 65000])
  })

  it('timestamp guard prevents stale REST response from overwriting newer WebSocket ticks', async () => {
    let wsCallback: ((data: any) => void) | undefined
    vi.spyOn(wsService, 'subscribe').mockImplementation((_channel, cb) => {
      wsCallback = cb
      return () => {}
    })

    const unsub = useMarketStore.getState().subscribeTicker('BTC-USDT')
    expect(wsCallback).toBeDefined()

    // Simulate in-flight race condition:
    // REST request is initiated, but while it is on the wire, a newer WebSocket tick arrives
    vi.mocked(marketApi.getMarkets).mockResolvedValueOnce([])
    vi.mocked(marketApi.getMarketsOverview).mockImplementationOnce(async () => {
      // Fast WS tick arrives with 67000.00 while REST is in flight
      wsCallback!({
        market_id: 'BTC-USDT',
        lastPrice: '67000.00',
        high24h: '67500.00',
        low24h: '64000.00',
      })

      // Older stale price from slow REST query
      return [
        {
          market_id: 'BTC-USDT',
          symbol: 'BTC/USDT',
          base_asset: 'BTC',
          quote_asset: 'USDT',
          last_price: '65000.00',
          high_24h: '66000.00',
          low_24h: '64000.00',
          volume_24h: '100',
          quote_volume_24h: '6500000',
          price_change_24h_percent: '0.5',
        },
      ]
    })

    await useMarketStore.getState().loadMarketsAndOverview()

    // Verify the fresher in-flight WS tick price of 67000.00 is preserved!
    expect(useMarketStore.getState().tickers['BTC-USDT']?.last_price).toBe('67000.00')

    unsub()
  })

  it('reference counting manages single underlying WS subscription per market', () => {
    let subscribeCalls = 0
    let unsubscribeCalls = 0

    vi.spyOn(wsService, 'subscribe').mockImplementation(() => {
      subscribeCalls++
      return () => {
        unsubscribeCalls++
      }
    })

    // Component A subscribes
    const unsubA = useMarketStore.getState().subscribeTicker('ETH-USDT')
    expect(subscribeCalls).toBe(1)

    // Component B subscribes to the same market
    const unsubB = useMarketStore.getState().subscribeTicker('ETH-USDT')
    // wsService.subscribe should NOT be called again
    expect(subscribeCalls).toBe(1)

    // Component A unmounts
    unsubA()
    // Should NOT unsubscribe from wsService yet because Component B is still mounted
    expect(unsubscribeCalls).toBe(0)

    // Component B unmounts
    unsubB()
    // Now both have unmounted, so it should clean up the WS subscription
    expect(unsubscribeCalls).toBe(1)
  })

  it('updates both base volume and quote volume from incoming WebSocket payload', () => {
    let wsCallback: ((data: any) => void) | undefined
    vi.spyOn(wsService, 'subscribe').mockImplementation((_channel, cb) => {
      wsCallback = cb
      return () => {}
    })

    const unsub = useMarketStore.getState().subscribeTicker('SOL-USDT')
    expect(wsCallback).toBeDefined()

    // Dispatch WS tick with both volume metrics
    wsCallback!({
      market_id: 'SOL-USDT',
      lastPrice: '185.50',
      high24h: '190.00',
      low24h: '180.00',
      volume24h: '4500.25',
      quoteVolume24h: '834796.37',
      priceChange24hPercent: '3.25',
    })

    const ticker = useMarketStore.getState().getTicker('SOL-USDT')
    expect(ticker).not.toBeNull()
    expect(ticker?.last_price).toBe('185.50')
    expect(ticker?.volume_24h).toBe('4500.25')
    expect(ticker?.quote_volume_24h).toBe('834796.37')
    expect(ticker?.high_24h).toBe('190.00')
    expect(ticker?.low_24h).toBe('180.00')

    unsub()
  })

  it('formatHighLow safely maps 0 or non-positive to em-dash and formats valid prices', () => {
    expect(formatHighLow('0')).toBe('—')
    expect(formatHighLow('0.00')).toBe('—')
    expect(formatHighLow(0)).toBe('—')
    expect(formatHighLow(undefined)).toBe('—')
    expect(formatHighLow('-10')).toBe('—')
    expect(formatHighLow('68450.50')).toBe('68,450.50')
  })

  it('formatTurnover handles various ranges cleanly', () => {
    expect(formatTurnover('0')).toBe('$0')
    expect(formatTurnover(undefined)).toBe('$0')
    expect(formatTurnover('45.20')).toBe('$45.20')
    expect(formatTurnover('150250.00')).toBe('$150.3K')
    expect(formatTurnover('3247011.56')).toBe('$3.25M')
    expect(formatTurnover('1500000000')).toBe('$1.50B')
  })
})
