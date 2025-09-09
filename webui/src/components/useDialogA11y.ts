import { useEffect } from 'react'

function getFocusable(container: HTMLElement | null): HTMLElement[] {
  if (!container) return []
  const selectors = [
    'a[href]','button:not([disabled])','textarea:not([disabled])','input:not([disabled])','select:not([disabled])',
    '[tabindex]:not([tabindex="-1"])'
  ]
  return Array.from(container.querySelectorAll<HTMLElement>(selectors.join(',')))
    .filter(el => el.offsetParent !== null)
}

export function useDialogA11y(ref: React.RefObject<HTMLElement>, onClose: () => void, initialFocusSelector?: string) {
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const prevActive = document.activeElement as HTMLElement | null
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    // focus initial
    const initial = (initialFocusSelector ? el.querySelector(initialFocusSelector) as HTMLElement | null : null) || getFocusable(el)[0] || el
    initial?.focus()

    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') { e.stopPropagation(); onClose(); return }
      if (e.key === 'Tab') {
        const f = getFocusable(el)
        if (f.length === 0) { e.preventDefault(); return }
        const first = f[0]
        const last = f[f.length - 1]
        if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
        else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
      }
    }

    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      document.body.style.overflow = prevOverflow
      if (prevActive) { try { prevActive.focus() } catch {} }
    }
  }, [ref, onClose, initialFocusSelector])
}

