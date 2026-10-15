import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import AuthCard from './AuthCard'

describe('AuthCard Component', () => {
  it('renders children within the standardized glass card and logo', () => {
    const html = renderToString(
      <MemoryRouter>
        <AuthCard>
          <div data-testid="auth-child">Login Content</div>
        </AuthCard>
      </MemoryRouter>
    )

    expect(html).toContain('data-testid="auth-child"')
    expect(html).toContain('Login Content')
    expect(html).toContain('aria-label="TradeDrift home"')
    expect(html).toContain('backdrop-blur-xl')
    expect(html).toContain('rounded-2xl')
  })
})
