import { useEffect, useRef } from 'react'

interface UseModalFocusOptions {
  isOpen: boolean
  onClose: () => void
  initialFocusRef?: React.RefObject<HTMLElement | null>
}

/**
 * useModalFocus — lightweight, dependency-free accessibility hook for modals:
 * - Traps keyboard focus (Tab and Shift+Tab) within the modal dialog.
 * - Restores focus to the triggering element upon dialog closure.
 * - Handles Escape key to dismiss dialog.
 */
export function useModalFocus<T extends HTMLElement = HTMLDivElement>({
  isOpen,
  onClose,
  initialFocusRef,
}: UseModalFocusOptions) {
  const containerRef = useRef<T>(null)
  const previousFocusRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!isOpen) return

    // 1. Capture previously focused element to restore when closing
    if (typeof document !== 'undefined' && document.activeElement instanceof HTMLElement) {
      previousFocusRef.current = document.activeElement
    }

    // 2. Set initial focus inside modal
    const frame = requestAnimationFrame(() => {
      if (initialFocusRef?.current) {
        initialFocusRef.current.focus()
      } else if (containerRef.current) {
        const autofocusEl = containerRef.current.querySelector<HTMLElement>('[autofocus], [data-autofocus]')
        if (autofocusEl) {
          autofocusEl.focus()
        } else {
          const focusable = containerRef.current.querySelectorAll<HTMLElement>(
            'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
          )
          if (focusable.length > 0) {
            focusable[0].focus()
          }
        }
      }
    })

    // 3. Focus trap and Escape listener
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        onClose()
        return
      }

      if (e.key === 'Tab' && containerRef.current) {
        const focusable = Array.from(
          containerRef.current.querySelectorAll<HTMLElement>(
            'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
          )
        ).filter((el) => el.offsetParent !== null || el.getClientRects().length > 0)

        if (focusable.length === 0) {
          e.preventDefault()
          return
        }

        const first = focusable[0]
        const last = focusable[focusable.length - 1]

        if (e.shiftKey) {
          if (document.activeElement === first || !containerRef.current.contains(document.activeElement)) {
            e.preventDefault()
            last.focus()
          }
        } else {
          if (document.activeElement === last || !containerRef.current.contains(document.activeElement)) {
            e.preventDefault()
            first.focus()
          }
        }
      }
    }

    window.addEventListener('keydown', handleKeyDown)

    return () => {
      cancelAnimationFrame(frame)
      window.removeEventListener('keydown', handleKeyDown)
      // 4. Restore focus on unmount / close
      if (previousFocusRef.current && typeof previousFocusRef.current.focus === 'function') {
        try {
          previousFocusRef.current.focus()
        } catch {
          // Ignore focus errors if element was unmounted
        }
      }
    }
  }, [isOpen, onClose, initialFocusRef])

  return containerRef
}
