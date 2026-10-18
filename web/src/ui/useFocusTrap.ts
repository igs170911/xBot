import { useEffect, useRef, type KeyboardEvent, type RefObject } from 'react'

export const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

// 開啟時把焦點移進容器（優先 [data-autofocus]）、關閉後還給原本的元素；
// 回傳的 onKeyDown 讓 Tab 在容器內循環、Esc 呼叫 onEscape。
export function useFocusTrap(ref: RefObject<HTMLElement | null>, open: boolean, onEscape: () => void) {
  const esc = useRef(onEscape)
  useEffect(() => {
    esc.current = onEscape
  })
  useEffect(() => {
    if (!open) return
    const prev = document.activeElement as HTMLElement | null
    const node = ref.current
    const first = node?.querySelector<HTMLElement>('[data-autofocus]') ?? node?.querySelector<HTMLElement>(FOCUSABLE)
    ;(first ?? node)?.focus()
    const { overflow } = document.body.style
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = overflow
      prev?.focus?.()
    }
  }, [open, ref])

  return (e: KeyboardEvent<HTMLElement>) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      esc.current()
      return
    }
    const node = ref.current
    if (e.key !== 'Tab' || !node) return
    const items = [...node.querySelectorAll<HTMLElement>(FOCUSABLE)]
    if (items.length === 0) {
      e.preventDefault()
      return
    }
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey && (document.activeElement === first || document.activeElement === node)) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }
}
