import { describe, it, expect, beforeEach } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import AppSidebar from './AppSidebar'
import { useAuthStore } from '../../store/authStore'

describe('AppSidebar Component', () => {
  beforeEach(() => {
    useAuthStore.setState({
      user: { userId: 'user-123', email: 'akhil@tradedrift.io', username: 'akhil' },
      isAuthenticated: true,
    })
  })

  it('renders the TradeDrift brand lockup and tagline', () => {
    const html = renderToString(
      <MemoryRouter initialEntries={['/dashboard']}>
        <AppSidebar />
      </MemoryRouter>
    )

    expect(html).toContain('TradeDrift')
    expect(html).toContain('Trade Smarter. Drift Further.')
  })

  it('renders the user account info without welcome greeting', () => {
    const html = renderToString(
      <MemoryRouter initialEntries={['/dashboard']}>
        <AppSidebar />
      </MemoryRouter>
    )

    expect(html).not.toContain('WELCOME')
    expect(html).not.toContain('Back,')
    expect(html).toContain('akhil')
    expect(html).toContain('Last login:')
    expect(html).not.toContain('w-[3px] h-4 rounded-full bg-white')
  })

  it('renders all navigation items and sections', () => {
    const html = renderToString(
      <MemoryRouter initialEntries={['/dashboard']}>
        <AppSidebar />
      </MemoryRouter>
    )

    // Sections
    expect(html).toContain('OVERVIEW')
    expect(html).toContain('TRADING')
    expect(html).toContain('ACCOUNT')
    expect(html).toContain('ANALYTICS')
    expect(html).toContain('SETTINGS')

    // Navigation items
    expect(html).toContain('Dashboard')
    expect(html).toContain('Trade')
    expect(html).toContain('Markets')
    expect(html).toContain('Portfolio')
    expect(html).toContain('Wallet')
    expect(html).toContain('Orders')
    expect(html).toContain('Analytics')
    expect(html).toContain('Profile')
    expect(html).toContain('Settings')
    expect(html).toContain('Log out')
  })

  it('marks active navigation item with aria-current="page"', () => {
    const html = renderToString(
      <MemoryRouter initialEntries={['/dashboard']}>
        <AppSidebar />
      </MemoryRouter>
    )

    expect(html).toContain('aria-current="page"')
    expect(html).toContain('href="/dashboard"')
  })

  it('marks Trade active when on /trade route', () => {
    const html = renderToString(
      <MemoryRouter initialEntries={['/trade']}>
        <AppSidebar />
      </MemoryRouter>
    )

    expect(html).toContain('aria-current="page"')
    expect(html).toContain('href="/trade"')
  })
})
