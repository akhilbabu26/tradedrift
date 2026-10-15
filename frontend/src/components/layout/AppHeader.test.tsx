import { describe, it, expect, beforeEach } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import AppHeader from './AppHeader'
import { useAuthStore } from '../../store/authStore'

describe('AppHeader Component', () => {
  beforeEach(() => {
    useAuthStore.setState({
      user: { userId: 'user-123', email: 'akhil@tradedrift.io', username: 'akhil' },
      isAuthenticated: true,
    })
  })

  it('renders search input and placeholder', () => {
    const html = renderToString(
      <MemoryRouter initialEntries={['/dashboard']}>
        <AppHeader />
      </MemoryRouter>
    )

    expect(html).toContain('placeholder="Search markets..."')
  })

  it('renders LIVE indicator and notifications bell without user avatar', () => {
    const html = renderToString(
      <MemoryRouter initialEntries={['/dashboard']}>
        <AppHeader />
      </MemoryRouter>
    )

    expect(html).toContain('LIVE')
    expect(html).toContain('aria-label="Notifications"')
    expect(html).not.toContain('aria-label="User menu"')
  })
})
