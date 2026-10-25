import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import AuthPromptModal from './AuthPromptModal'

describe('AuthPromptModal Component', () => {
  it('renders nothing when closed', () => {
    const html = renderToString(
      <MemoryRouter>
        <AuthPromptModal isOpen={false} onClose={() => {}} />
      </MemoryRouter>
    )
    expect(html).toBe('')
  })

  it('renders title, description and options when open', () => {
    const html = renderToString(
      <MemoryRouter>
        <AuthPromptModal
          isOpen={true}
          onClose={() => {}}
          title="Sign In to Buy BTC"
          targetMarket="BTC-USDT"
        />
      </MemoryRouter>
    )

    expect(html).toContain('Sign In to Buy BTC')
    expect(html).toContain('Sign In to Your Account')
    expect(html).toContain('Create New Account')
    expect(html).toContain('Continue Exploring Markets')
    expect(html).toContain('Free 10,000 USDT Virtual Balance')
  })
})
