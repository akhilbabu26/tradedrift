import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import ChartScreenshotModal from './ChartScreenshotModal'

describe('ChartScreenshotModal Component', () => {
  const defaultProps = {
    isOpen: true,
    onClose: vi.fn(),
    dataUrl: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==',
    blob: new Blob(['mock'], { type: 'image/png' }),
    filename: 'BTC-USDT_1h_chart.png',
    symbol: 'BTC/USDT',
    timeframe: '1h',
  }

  it('renders modal dialog when isOpen is true', () => {
    const html = renderToString(<ChartScreenshotModal {...defaultProps} />)
    expect(html).toContain('Chart Screenshot')
    expect(html).toContain('BTC/USDT')
    expect(html).toContain('1H')
    expect(html).toContain('BTC-USDT_1h_chart.png')
    expect(html).toContain('Save PNG')
    expect(html).toContain('Copy Image')
    expect(html).toContain('Open in Tab')
  })

  it('does not render anything when isOpen is false', () => {
    const html = renderToString(<ChartScreenshotModal {...defaultProps} isOpen={false} />)
    expect(html).toBe('')
  })
})
