import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import {
  RaisedSurface,
  SurfaceHeader,
  SurfaceDivider,
  SurfaceBadge,
  surfaceTokens,
} from './Surface'

describe('Shared 3D Surface System', () => {
  it('renders RaisedSurface with raised variant styles and border highlights', () => {
    const html = renderToString(
      <RaisedSurface variant="raised" className="test-card">
        <div>Content Inside</div>
      </RaisedSurface>
    )

    expect(html).toContain('Content Inside')
    expect(html).toContain('test-card')
    // Check gradient in inline style
    expect(html).toContain('linear-gradient')
    expect(html).toContain('rgba(255, 255, 255, 0.07)')
  })

  it('renders RaisedSurface with raised-subtle, flat, and inset variants', () => {
    const raisedSubtleHtml = renderToString(
      <RaisedSurface variant="raised-subtle">Subtle</RaisedSurface>
    )
    expect(raisedSubtleHtml).toContain('Subtle')

    const flatHtml = renderToString(
      <RaisedSurface variant="flat">Flat</RaisedSurface>
    )
    expect(flatHtml).toContain('Flat')

    const insetHtml = renderToString(
      <RaisedSurface variant="inset">Inset</RaisedSurface>
    )
    expect(insetHtml).toContain('Inset')
  })

  it('renders SurfaceHeader with title, subtitle, and action slot', () => {
    const html = renderToString(
      <SurfaceHeader
        title="Portfolio Overview"
        subtitle="Updated 1m ago"
        action={<button>Action</button>}
      />
    )

    expect(html).toContain('Portfolio Overview')
    expect(html).toContain('Updated 1m ago')
    expect(html).toContain('Action')
  })

  it('renders horizontal and vertical SurfaceDivider', () => {
    const hHtml = renderToString(<SurfaceDivider orientation="horizontal" />)
    expect(hHtml).toContain('h-px')

    const vHtml = renderToString(<SurfaceDivider orientation="vertical" />)
    expect(vHtml).toContain('w-px')
  })

  it('renders SurfaceBadge with custom variants', () => {
    const emeraldHtml = renderToString(<SurfaceBadge variant="emerald">ACTIVE</SurfaceBadge>)
    expect(emeraldHtml).toContain('ACTIVE')
    expect(emeraldHtml).toContain('text-[#10B981]')

    const cyanHtml = renderToString(<SurfaceBadge variant="cyan">SYNCED</SurfaceBadge>)
    expect(cyanHtml).toContain('SYNCED')
    expect(cyanHtml).toContain('text-[#06B6D4]')
  })

  it('exports valid tokens with required monochrome palette', () => {
    expect(surfaceTokens.colors.canvas).toBe('#08090B')
    expect(surfaceTokens.colors.border).toBe('#292C30')
    expect(surfaceTokens.colors.textPrimary).toBe('#F2F3F5')
    expect(surfaceTokens.colors.textSecondary).toBe('#A7ADB5')
  })
})
