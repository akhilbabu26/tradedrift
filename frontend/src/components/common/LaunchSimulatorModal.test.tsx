import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import LaunchSimulatorModal from './LaunchSimulatorModal'

describe('LaunchSimulatorModal Component', () => {
  it('renders nothing when isOpen is false', () => {
    const html = renderToString(
      <MemoryRouter>
        <LaunchSimulatorModal isOpen={false} onClose={() => {}} />
      </MemoryRouter>
    )
    expect(html).toBe('')
  })

  it('renders both options when isOpen is true', () => {
    const html = renderToString(
      <MemoryRouter>
        <LaunchSimulatorModal isOpen={true} onClose={() => {}} />
      </MemoryRouter>
    )

    expect(html).toContain('Launch TradeDrift Simulator')
    expect(html).toContain('Option A: Full Trading Access')
    expect(html).toContain('Option B: Explore as Guest')
    expect(html).toContain('Explore as Guest')
    expect(html).toContain('Create Account')
    expect(html).toContain('Sign In')
  })
})
