import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import GlobalFooter from './GlobalFooter'
import { wsService } from '../../api/ws'

describe('GlobalFooter', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(wsService, 'getStatus').mockReturnValue('connected')
    vi.spyOn(wsService, 'onStatus').mockImplementation(() => () => {})
  })

  it('renders the brand section correctly', () => {
    const html = renderToString(
      <MemoryRouter>
        <GlobalFooter />
      </MemoryRouter>
    )

    expect(html).toContain('TradeDrift')
    expect(html).toContain('MARKETS · EXECUTION · INTELLIGENCE')
    expect(html).toContain('Explore markets. Execute trades. Understand the engine.')
    expect(html).toContain('#F4F5F6')
    expect(html).toContain('#AEB2B8')
  })

  it('renders all four information groups with expected links', () => {
    const html = renderToString(
      <MemoryRouter>
        <GlobalFooter />
      </MemoryRouter>
    )

    // Headings
    expect(html).toContain('Trading')
    expect(html).toContain('Developer Resources')
    expect(html).toContain('Platform')
    expect(html).toContain('System Status')

    // Trading links
    expect(html).toContain('href="/markets"')
    expect(html).toContain('Markets')
    expect(html).toContain('href="/trade"')
    expect(html).toContain('Trading Terminal')
    expect(html).toContain('href="/portfolio"')
    expect(html).toContain('Portfolio')
    expect(html).toContain('href="/orders"')
    expect(html).toContain('Order History')

    // Developer resources links
    expect(html).toContain('href="https://github.com/akhilbabu26/tradedrift#system-architecture"')
    expect(html).toContain('Architecture')
    expect(html).toContain('href="https://github.com/akhilbabu26/tradedrift/tree/main/docs"')
    expect(html).toContain('API Documentation')
    expect(html).toContain('href="https://github.com/akhilbabu26/tradedrift"')
    expect(html).toContain('GitHub Repository')
    expect(html).toContain('href="https://github.com/akhilbabu26/tradedrift/releases"')
    expect(html).toContain('Changelog')

    // Platform links
    expect(html).toContain('href="/#how"')
    expect(html).toContain('How It Works')
    expect(html).toContain('Trading Guide')
    expect(html).toContain('href="/"')
    expect(html).toContain('About TradeDrift')

    // System status items
    expect(html).toContain('Matching Engine')
    expect(html).toContain('Market Data')
    expect(html).toContain('WebSocket')
    expect(html).toContain('Illustrative status preview')
  })

  it('renders bottom bar with dynamic year, educational note, version and status pill', () => {
    const html = renderToString(
      <MemoryRouter>
        <GlobalFooter />
      </MemoryRouter>
    )

    const currentYear = new Date().getFullYear()
    expect(html).toContain(`© ${currentYear} TradeDrift · Educational simulator`)
    expect(html).toContain('v1.0.0')
    expect(html).toContain('Systems Operational')
  })

  it('displays offline status pill when WebSocket is disconnected', () => {
    vi.spyOn(wsService, 'getStatus').mockReturnValue('offline')

    const html = renderToString(
      <MemoryRouter>
        <GlobalFooter />
      </MemoryRouter>
    )

    expect(html).toContain('Systems Offline')
  })
})
