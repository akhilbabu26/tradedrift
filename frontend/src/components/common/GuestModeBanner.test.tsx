import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import GuestModeBanner from './GuestModeBanner'

describe('GuestModeBanner Component', () => {
  it('renders guest mode text and persistent CTAs', () => {
    const html = renderToString(
      <MemoryRouter initialEntries={['/trade?market=BTC-USDT']}>
        <GuestModeBanner />
      </MemoryRouter>
    )

    expect(html).toContain('GUEST MODE')
    expect(html).toContain('Explore markets freely. Sign in to start trading.')
    expect(html).toContain('Sign In')
    expect(html).toContain('Create Account')
    expect(html).toContain(encodeURIComponent('/trade?market=BTC-USDT'))
  })
})
