import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import LinkGoogleAccountModal from './LinkGoogleAccountModal'

describe('LinkGoogleAccountModal Component', () => {
  const onClose = vi.fn()
  const onSuccess = vi.fn()

  it('renders nothing when isOpen is false', () => {
    const html = renderToString(
      <LinkGoogleAccountModal
        isOpen={false}
        onClose={onClose}
        email="trader@tradedrift.local"
        linkToken="lt_12345"
        onSuccess={onSuccess}
      />
    )
    expect(html).toBe('')
  })

  it('renders dialog structure, email and required fields when isOpen is true', () => {
    const html = renderToString(
      <LinkGoogleAccountModal
        isOpen={true}
        onClose={onClose}
        email="trader@tradedrift.local"
        linkToken="lt_12345"
        onSuccess={onSuccess}
      />
    )
    expect(html).toContain('role="dialog"')
    expect(html).toContain('aria-modal="true"')
    expect(html).toContain('Link TradeDrift Account')
    expect(html).toContain('trader@tradedrift.local')
    expect(html).toContain('TradeDrift Password')
    expect(html).toContain('Confirm &amp; Link')
    expect(html).toContain('Cancel')
  })
})
