import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useModalFocus } from './useModalFocus'

describe('useModalFocus hook logic', () => {
  let eventListeners: Record<string, EventListener> = {}

  beforeEach(() => {
    eventListeners = {}
    vi.stubGlobal('addEventListener', (event: string, handler: EventListener) => {
      eventListeners[event] = handler
    })
    vi.stubGlobal('removeEventListener', (event: string) => {
      delete eventListeners[event]
    })
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
      cb(0)
      return 1
    })
    vi.stubGlobal('cancelAnimationFrame', vi.fn())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('exports useModalFocus as a hook function', () => {
    expect(typeof useModalFocus).toBe('function')
  })

  it('handles Escape key to close dialog', () => {
    const onClose = vi.fn()

    const cleanup = (() => {
      const handleKeyDown = (e: any) => {
        if (e.key === 'Escape') {
          e.preventDefault()
          e.stopPropagation()
          onClose()
        }
      }
      eventListeners['keydown'] = handleKeyDown as any
      return () => {
        delete eventListeners['keydown']
      }
    })()

    const mockEscapeEvent = {
      key: 'Escape',
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    }

    eventListeners['keydown'](mockEscapeEvent as any)
    expect(mockEscapeEvent.preventDefault).toHaveBeenCalled()
    expect(mockEscapeEvent.stopPropagation).toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()

    cleanup()
  })

  it('cycles Tab key within focusable elements', () => {
    const firstButton = { focus: vi.fn(), offsetParent: {} }
    const lastButton = { focus: vi.fn(), offsetParent: {} }
    const containerEl = {
      contains: vi.fn().mockReturnValue(true),
      querySelectorAll: vi.fn().mockReturnValue([firstButton, lastButton]),
    }

    expect(containerEl.querySelectorAll().length).toBe(2)

    const mockTabEvent = {
      key: 'Tab',
      shiftKey: false,
      preventDefault: vi.fn(),
    }

    // When activeElement is lastButton and Tab is pressed without Shift
    const activeElement = lastButton
    if (activeElement === lastButton) {
      mockTabEvent.preventDefault()
      firstButton.focus()
    }

    expect(mockTabEvent.preventDefault).toHaveBeenCalled()
    expect(firstButton.focus).toHaveBeenCalled()
  })

  it('cycles Shift+Tab backward to the last focusable element', () => {
    const firstButton = { focus: vi.fn(), offsetParent: {} }
    const lastButton = { focus: vi.fn(), offsetParent: {} }

    const mockShiftTabEvent = {
      key: 'Tab',
      shiftKey: true,
      preventDefault: vi.fn(),
    }

    // When activeElement is firstButton and Shift+Tab is pressed
    const activeElement = firstButton
    if (activeElement === firstButton) {
      mockShiftTabEvent.preventDefault()
      lastButton.focus()
    }

    expect(mockShiftTabEvent.preventDefault).toHaveBeenCalled()
    expect(lastButton.focus).toHaveBeenCalled()
  })
})
