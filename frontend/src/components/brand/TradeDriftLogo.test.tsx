import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import TradeDriftLogo, { TradeDriftSymbol, TradeDriftWordmark } from './TradeDriftLogo'

describe('TradeDrift Brand Identity & Logo Components', () => {
  it('renders TradeDriftSymbol with minimalist silver TD monogram geometry', () => {
    const html = renderToString(<TradeDriftSymbol size={32} />)

    // Vector viewBox
    expect(html).toContain('viewBox="0 0 100 100"')
    expect(html).toContain('width="32"')
    expect(html).toContain('height="32"')

    // Metallic silver gradient colors
    expect(html).toContain('#F4F5F6')
    expect(html).toContain('#D9DADD')
    expect(html).toContain('#AEB2B8')
    expect(html).toContain('silver-td-monogram')

    // Accessibility
    expect(html).toContain('aria-hidden="true"')
  })

  it('renders TradeDriftSymbol with accessible role and aria-label when standalone', () => {
    const html = renderToString(
      <TradeDriftSymbol size={40} ariaHidden={false} ariaLabel="TradeDrift Logo Symbol" />
    )

    expect(html).toContain('role="img"')
    expect(html).toContain('aria-label="TradeDrift Logo Symbol"')
    expect(html).not.toContain('aria-hidden="true"')
  })

  it('renders TradeDriftWordmark with brand name and uppercase descriptor', () => {
    const html = renderToString(<TradeDriftWordmark size="md" showDescriptor={true} />)

    expect(html).toContain('TradeDrift')
    expect(html).toContain('MARKETS · EXECUTION · INTELLIGENCE')
  })

  it('renders navbar logo lockup variant seamlessly with silver monogram', () => {
    const html = renderToString(<TradeDriftLogo variant="navbar" symbolSize={28} />)

    expect(html).toContain('TradeDrift')
    expect(html).toContain('MARKETS · EXECUTION · INTELLIGENCE')
    expect(html).toContain('#F4F5F6')
    expect(html).toContain('#AEB2B8')
  })

  it('renders footer brand lockup with exact required copy and tagline', () => {
    const html = renderToString(<TradeDriftLogo variant="footer" symbolSize={32} />)

    expect(html).toContain('TradeDrift')
    expect(html).toContain('MARKETS · EXECUTION · INTELLIGENCE')
    expect(html).toContain('Explore markets. Execute trades. Understand the engine.')
    expect(html).toContain('#F4F5F6')
    expect(html).toContain('#AEB2B8')
  })

  it('renders auth variant for authentication screens with high-contrast badge', () => {
    const html = renderToString(<TradeDriftLogo variant="auth" symbolSize={40} />)

    expect(html).toContain('TradeDrift')
    expect(html).toContain('MARKETS · EXECUTION · INTELLIGENCE')
    expect(html).toContain('w-14 h-14')
    expect(html).toContain('#F4F5F6')
    expect(html).toContain('#AEB2B8')
  })
})
