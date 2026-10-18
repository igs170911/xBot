import { useEffect, useId, useRef, useState, type ReactNode, type Ref } from 'react'
import { cx } from '../ui/cx'

export type PopoverTrigger = { ref: Ref<HTMLButtonElement>; id: string; onClick: () => void; 'aria-expanded': boolean; 'aria-haspopup': 'menu' | 'dialog' }

// 標題列的浮層（對話串選單、模型選擇器）：點外面或按 Esc 關閉，關閉後焦點回到按鈕。
// 一次只開一個（C03：「一次只開一個浮層」）——開新的會關掉舊的。
let closeOpen: (() => void) | null = null

export function Popover({
  trigger,
  children,
  align = 'start',
  kind = 'menu',
  className,
}: {
  trigger: (p: PopoverTrigger) => ReactNode
  children: (close: () => void) => ReactNode
  align?: 'start' | 'end'
  kind?: 'menu' | 'dialog'
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const btn = useRef<HTMLButtonElement>(null)
  const pop = useRef<HTMLDivElement>(null)
  const id = useId()

  useEffect(() => {
    if (!open) return
    const mine = () => setOpen(false)
    if (closeOpen && closeOpen !== mine) closeOpen()
    closeOpen = mine
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node
      if (btn.current?.contains(t) || pop.current?.contains(t)) return
      setOpen(false)
    }
    document.addEventListener('pointerdown', onDown)
    // 焦點移到第一個可以按的項目
    pop.current?.querySelector<HTMLElement>('[aria-checked="true"], [role="menuitem"], [role="menuitemradio"], button, input')?.focus()
    return () => {
      document.removeEventListener('pointerdown', onDown)
      if (closeOpen === mine) closeOpen = null
    }
  }, [open])

  const close = () => {
    setOpen(false)
    document.getElementById(id)?.focus()
  }

  return (
    <span className="relative inline-flex min-w-0">
      {trigger({ ref: btn, id, onClick: () => setOpen((o) => !o), 'aria-expanded': open, 'aria-haspopup': kind })}
      {open && (
        <div
          ref={pop}
          className={cx('absolute top-full z-40 mt-1.5', align === 'end' ? 'right-0' : 'left-0', className)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              e.preventDefault()
              e.stopPropagation()
              close()
            }
            if (kind === 'menu' && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
              const els = [...(pop.current?.querySelectorAll<HTMLElement>('[role^="menuitem"]:not([disabled])') ?? [])]
              const i = els.indexOf(document.activeElement as HTMLElement)
              const j = e.key === 'ArrowDown' ? (i + 1) % els.length : (i - 1 + els.length) % els.length
              e.preventDefault()
              els[j]?.focus()
            }
          }}
        >
          {children(close)}
        </div>
      )}
    </span>
  )
}
