import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import { GoogleOAuthProvider } from '@react-oauth/google'
import ConnectedAccountsCard from './ConnectedAccountsCard'

describe('ConnectedAccountsCard Component', () => {
  it('renders Google connected accounts section', () => {
    const html = renderToString(
      <GoogleOAuthProvider clientId="mock-client-id">
        <ConnectedAccountsCard />
      </GoogleOAuthProvider>
    )
    expect(html).toContain('Connected Accounts')
    expect(html).toContain('Google')
    expect(html).toContain('Manage third-party authentication methods linked to your TradeDrift identity.')
  })
})
