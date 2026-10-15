import { Page } from '@playwright/test'

export const MOCK_JWT_ACCESS_TOKEN =
  'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoidXNyX2UyZV90cmFkZXIiLCJlbWFpbCI6InRyYWRlckB0cmFkZWRyaWZ0LmxvY2FsIiwidXNlcm5hbWUiOiJFMkUgU2ltdWxhdG9yIFRyYWRlciIsImV4cCI6OTk5OTk5OTk5OX0.mock_signature'

export const MOCK_USER = {
  userId: 'usr_e2e_trader',
  email: 'trader@tradedrift.local',
  username: 'E2E Simulator Trader',
}

/**
 * Standard mock API responses for hermetic frontend E2E tests.
 * Intercepts /api/v1 calls so tests validate complete UI rendering and interaction
 * without depending on transient backend database state.
 */
export async function setupMockApi(page: Page) {
  // 1. Catch-all fallback registered FIRST (in Playwright LIFO, this will be evaluated LAST)
  await page.route('**/api/v1/**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({}),
    })
  })

  // 2. Auth endpoints (registered after, so evaluated FIRST)
  await page.route('**/api/v1/auth/login', async (route) => {
    const raw = route.request().postData() || ''
    if (raw.includes('wrongpassword123')) {
      await route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: JSON.stringify({ message: 'Invalid email or password' }),
      })
      return
    }

    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: MOCK_USER,
        accessToken: MOCK_JWT_ACCESS_TOKEN,
        refreshToken: 'mock_e2e_refresh_token',
      }),
    })
  })

  await page.route('**/api/v1/auth/refresh', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        accessToken: MOCK_JWT_ACCESS_TOKEN,
        refreshToken: 'mock_e2e_refresh_token',
      }),
    })
  })

  await page.route('**/api/v1/auth/logout**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true }),
    })
  })

  // 2. Wallets & Balances
  const mockBalances = [
    { asset: 'USDT', availableBalance: '10000.00', reservedBalance: '0.00', totalBalance: '10000.00' },
    { asset: 'BTC', availableBalance: '0.2500', reservedBalance: '0.00', totalBalance: '0.2500' },
    { asset: 'ETH', availableBalance: '2.5000', reservedBalance: '0.00', totalBalance: '2.5000' },
    { asset: 'SOL', availableBalance: '25.0000', reservedBalance: '0.00', totalBalance: '25.0000' },
  ]

  await page.route('**/api/v1/wallet/balances**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ balances: mockBalances }),
    })
  })

  await page.route('**/api/v1/wallets**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(mockBalances),
    })
  })

  await page.route('**/api/v1/wallet/assets**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        assets: [
          { symbol: 'USDT', name: 'Tether USD', decimals: 2, isEnabled: true, seedAmount: '10000', displayOrder: 1 },
          { symbol: 'BTC', name: 'Bitcoin', decimals: 8, isEnabled: true, seedAmount: '0.25', displayOrder: 2 },
          { symbol: 'ETH', name: 'Ethereum', decimals: 8, isEnabled: true, seedAmount: '2.5', displayOrder: 3 },
          { symbol: 'SOL', name: 'Solana', decimals: 8, isEnabled: true, seedAmount: '25', displayOrder: 4 },
        ],
      }),
    })
  })

  await page.route('**/api/v1/topups**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        dailyUsage: { consumedInr: 2, dailyLimitInr: 10, remainingInr: 8 },
        topUps: [],
      }),
    })
  })

  // 3. Markets & Trading data
  const mockMarketsList = [
    { market_id: 'BTC-USDT', id: 'BTC-USDT', symbol: 'BTC/USDT', base_asset: 'BTC', quote_asset: 'USDT', tick_size: '0.01', lot_size: '0.0001', status: 'ACTIVE' },
    { market_id: 'ETH-USDT', id: 'ETH-USDT', symbol: 'ETH/USDT', base_asset: 'ETH', quote_asset: 'USDT', tick_size: '0.01', lot_size: '0.001', status: 'ACTIVE' },
    { market_id: 'SOL-USDT', id: 'SOL-USDT', symbol: 'SOL/USDT', base_asset: 'SOL', quote_asset: 'USDT', tick_size: '0.01', lot_size: '0.01', status: 'ACTIVE' },
  ]

  await page.route('**/api/v1/markets/overview', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        markets: [
          {
            market_id: 'BTC-USDT',
            symbol: 'BTC/USDT',
            last_price: '80000.00',
            price_change_24h_percent: '2.50',
            high_24h: '82000.00',
            low_24h: '79000.00',
            volume_24h_quote: '12500000.00',
          },
          {
            market_id: 'ETH-USDT',
            symbol: 'ETH/USDT',
            last_price: '2800.00',
            price_change_24h_percent: '1.20',
            high_24h: '2850.00',
            low_24h: '2750.00',
            volume_24h_quote: '8500000.00',
          },
        ],
      }),
    })
  })

  await page.route('**/api/v1/markets/*/ticker', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        market_id: 'BTC-USDT',
        symbol: 'BTC/USDT',
        last_price: '80000.00',
        price_change_24h_percent: '2.50',
        high_24h: '82000.00',
        low_24h: '79000.00',
        volume_24h: '150.00',
      }),
    })
  })

  await page.route('**/api/v1/markets/*/orderbook**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        bids: [['79950.00', '1.5'], ['79900.00', '2.0']],
        asks: [['80050.00', '1.2'], ['80100.00', '2.5']],
      }),
    })
  })

  await page.route('**/api/v1/markets/*/candles**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        candles: [
          { time: Math.floor(Date.now() / 1000) - 3600, open: 79500, high: 80200, low: 79400, close: 80000, volume: 120 },
        ],
      }),
    })
  })

  await page.route('**/api/v1/markets/*/trades**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ trades: [] }),
    })
  })

  await page.route('**/api/v1/markets', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ markets: mockMarketsList }),
    })
  })

  // 4. Orders & Portfolio & Notifications
  await page.route('**/api/v1/orders**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        orders: [],
        total: 0,
      }),
    })
  })

  await page.route('**/api/v1/portfolio**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        totalValue: '30000.00',
        cashBalance: '10000.00',
        realizedPnl: '+150.00',
        dailyChangeValue: '+450.00',
        dailyChangePct: '+1.52',
        holdings: [
          { asset: 'BTC', totalQuantity: '0.2500', averageEntryPrice: '60000.00', currentPrice: '80000.00' },
          { asset: 'USDT', totalQuantity: '10000.00', averageEntryPrice: '1.00', currentPrice: '1.00' },
        ],
      }),
    })
  })

  await page.route('**/api/v1/notifications/unread-count', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ unread_count: 0 }),
    })
  })

  await page.route('**/api/v1/notifications**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        notifications: [],
        unread_count: 0,
      }),
    })
  })
}

/**
 * Helper to initialize an authenticated session in browser localStorage
 * before navigation to any protected TradeDrift route.
 */
export async function authenticateTestUser(page: Page) {
  await setupMockApi(page)
  await page.addInitScript(
    ({ token, user }) => {
      window.localStorage.setItem('access_token', token)
      window.localStorage.setItem('refresh_token', 'mock_e2e_refresh_token')
      window.localStorage.setItem('user', JSON.stringify(user))
    },
    { token: MOCK_JWT_ACCESS_TOKEN, user: MOCK_USER }
  )
}
