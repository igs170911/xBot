import { Fragment, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type ReactNode, type Ref, type RefObject, type TouchEvent } from 'react'
import { createPortal } from 'react-dom'
import { Check, ChevronDown, type LucideIcon } from 'lucide-react'
import { Icon } from './Icon'
import { cx } from './cx'

// 點外面或按 Esc 就關。
function useDismiss(open: boolean, close: () => void, a: RefObject<HTMLElement | null>, b: RefObject<HTMLElement | null>) {
  const closeRef = useRef(close)
  useEffect(() => {
    closeRef.current = close
  })
  useEffect(() => {
    if (!open) return
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node
      if (a.current?.contains(t) || b.current?.contains(t)) return
      closeRef.current()
    }
    document.addEventListener('pointerdown', onDown)
    return () => document.removeEventListener('pointerdown', onDown)
  }, [open, a, b])
}

function moveFocus(e: KeyboardEvent, items: HTMLElement[]) {
  const i = items.indexOf(document.activeElement as HTMLElement)
  let j = -1
  if (e.key === 'ArrowDown') j = (i + 1) % items.length
  else if (e.key === 'ArrowUp') j = (i - 1 + items.length) % items.length
  else if (e.key === 'Home') j = 0
  else if (e.key === 'End') j = items.length - 1
  if (j < 0) return false
  e.preventDefault()
  items[j]?.focus()
  return true
}

export type MenuItem = {
  label: string
  icon?: LucideIcon
  onSelect?: () => void
  danger?: boolean
  /** 在這一項上面畫分隔線。 */
  separator?: boolean
  disabled?: boolean
}

export type MenuListProps = {
  label: string
  items: MenuItem[]
  onDone?: () => void
  /** 示範用：標出 hover 樣子的那一項。 */
  highlight?: number
  className?: string
}

// 選單本體：#262626、圓角 12、陰影；項目高 34、圓角 8、圖示 18（N03）。
export function MenuList({ label, items, onDone, highlight, className }: MenuListProps) {
  const ref = useRef<HTMLDivElement>(null)
  return (
    <div
      ref={ref}
      role="menu"
      aria-label={label}
      onKeyDown={(e) => {
        const els = [...(ref.current?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])') ?? [])]
        moveFocus(e, els)
      }}
      className={cx('w-[280px] rounded-menu bg-card p-1.5 shadow-pop', className)}
    >
      {items.map((m, i) => (
        <Fragment key={m.label}>
          {m.separator && <div role="separator" className="mx-2 my-[5px] h-px bg-line" />}
          <button
            type="button"
            role="menuitem"
            tabIndex={-1}
            disabled={m.disabled}
            onClick={() => {
              m.onSelect?.()
              onDone?.()
            }}
            className={cx(
              'flex h-[34px] w-full items-center gap-2.5 rounded-seg px-2.5 text-left text-ui outline-none hover:bg-hover focus-visible:bg-hover disabled:opacity-40 max-sm:h-11',
              m.danger ? 'text-danger' : 'text-ink',
              highlight === i ? 'bg-hover' : 'bg-transparent',
            )}
          >
            {m.icon && <Icon icon={m.icon} size={18} />}
            {m.label}
          </button>
        </Fragment>
      ))}
    </div>
  )
}

export type MenuButtonProps = {
  label: string
  items: MenuItem[]
  /** 觸發元素；會拿到 ref 與開關屬性。 */
  trigger: (p: { ref: Ref<HTMLButtonElement>; onClick: () => void; 'aria-haspopup': 'menu'; 'aria-expanded': boolean }) => ReactNode
  align?: 'start' | 'end'
}

// 按一下打開選單；開啟後焦點到第一項，方向鍵移動，Esc 關閉並回到按鈕。
export function MenuButton({ label, items, trigger, align = 'start' }: MenuButtonProps) {
  const [open, setOpen] = useState(false)
  const btn = useRef<HTMLButtonElement>(null)
  const pop = useRef<HTMLDivElement>(null)
  useDismiss(open, () => setOpen(false), btn, pop)
  const close = () => {
    setOpen(false)
    btn.current?.focus()
  }
  // 用 fixed 貼著按鈕：不會被外面 overflow-hidden 的卡片裁掉；下面不夠就往上開。
  const [pos, setPos] = useState<{ left?: number; right?: number; top?: number; bottom?: number } | null>(null)
  const placed = open && !!pos
  useEffect(() => {
    if (placed) pop.current?.querySelector<HTMLElement>('[role="menuitem"]:not([disabled])')?.focus()
  }, [placed])
  // 畫出來之後量一下：超出畫面左右邊就往內推（例：側欄最下面靠右的「⋯」往左長出螢幕）
  useLayoutEffect(() => {
    const el = pop.current
    if (!placed || !el) return
    const r = el.getBoundingClientRect()
    const margin = 8
    if (r.left < margin) {
      el.style.left = `${margin}px`
      el.style.right = 'auto'
    } else if (r.right > window.innerWidth - margin) {
      el.style.right = `${margin}px`
      el.style.left = 'auto'
    }
  }, [placed, pos])
  useLayoutEffect(() => {
    if (!open || !btn.current) return
    const update = () => {
      if (!btn.current) return
      const r = btn.current.getBoundingClientRect()
      const x = align === 'end' ? { right: Math.max(8, window.innerWidth - r.right) } : { left: Math.max(8, r.left) }
      setPos(window.innerHeight - r.bottom < 260 && r.top > window.innerHeight - r.bottom ? { ...x, bottom: window.innerHeight - r.top + 4 } : { ...x, top: r.bottom + 4 })
    }
    update()
    window.addEventListener('resize', update)
    window.addEventListener('scroll', update, true)
    return () => {
      window.removeEventListener('resize', update)
      window.removeEventListener('scroll', update, true)
    }
  }, [open, align])
  return (
    <span className="relative inline-flex">
      {trigger({ ref: btn, onClick: () => setOpen((o) => !o), 'aria-haspopup': 'menu', 'aria-expanded': open })}
      {/* 放到 body 底下：側欄是 sticky（自成一層），留在裡面會被右邊的頁面蓋住（縮小的側欄「⋯」，2026-10-19） */}
      {open &&
        pos &&
        createPortal(
          <div
            ref={pop}
            style={pos}
            className="fixed z-[70]"
            onKeyDown={(e) => {
              if (e.key === 'Escape' || e.key === 'Tab') {
                if (e.key === 'Escape') e.preventDefault()
                close()
              }
            }}
          >
            <MenuList label={label} items={items} onDone={close} />
          </div>,
          document.body,
        )}
    </span>
  )
}

type MenuAt = { x: number; y: number; label: string; items: MenuItem[] }

const LONG_PRESS_MS = 500

/**
 * 右鍵選單（手機長按）：bind(標題, 項目) 展開到要接的元素上，menu 放在畫面任何地方。
 * 選單貼著點下去的位置開，超出畫面就往內推；點外面、Esc、捲動都會關。長按開了選單就不算點擊。
 */
export function useContextMenu() {
  const [at, setAt] = useState<MenuAt | null>(null)
  const timer = useRef(0)
  const fired = useRef(false)
  const start = useRef<{ x: number; y: number } | null>(null)
  const bind = (label: string, items: MenuItem[]) => ({
    onContextMenu: (e: MouseEvent<HTMLElement>) => {
      e.preventDefault()
      window.clearTimeout(timer.current)
      // 鍵盤（Shift+F10）沒有座標：貼著元素開
      const r = e.currentTarget.getBoundingClientRect()
      setAt(e.clientX || e.clientY ? { x: e.clientX, y: e.clientY, label, items } : { x: r.left + 12, y: r.bottom, label, items })
    },
    onTouchStart: (e: TouchEvent<HTMLElement>) => {
      const t = e.touches[0]
      fired.current = false
      window.clearTimeout(timer.current)
      if (!t || e.touches.length > 1) return
      start.current = { x: t.clientX, y: t.clientY }
      timer.current = window.setTimeout(() => {
        fired.current = true
        navigator.vibrate?.(10)
        if (start.current) setAt({ ...start.current, label, items })
      }, LONG_PRESS_MS)
    },
    onTouchMove: (e: TouchEvent<HTMLElement>) => {
      const t = e.touches[0]
      if (start.current && t && Math.hypot(t.clientX - start.current.x, t.clientY - start.current.y) > 10) window.clearTimeout(timer.current)
    },
    onTouchEnd: () => window.clearTimeout(timer.current),
    onTouchCancel: () => window.clearTimeout(timer.current),
    onClickCapture: (e: MouseEvent<HTMLElement>) => {
      if (!fired.current) return
      fired.current = false
      e.preventDefault()
      e.stopPropagation()
    },
  })
  /** 從按鈕打開同一個選單（列上的「⋯」：不會右鍵、長按的人也找得到）。 */
  const open = (el: HTMLElement, label: string, items: MenuItem[]) => {
    const r = el.getBoundingClientRect()
    setAt({ x: r.left, y: r.bottom + 4, label, items })
  }
  return { bind, open, menu: <ContextMenu at={at} onClose={() => setAt(null)} /> }
}

function ContextMenu({ at, onClose }: { at: MenuAt | null; onClose: () => void }) {
  const pop = useRef<HTMLDivElement>(null)
  const none = useRef<HTMLElement>(null)
  useDismiss(!!at, onClose, pop, none)
  useLayoutEffect(() => {
    const el = pop.current
    if (!at || !el) return
    const r = el.getBoundingClientRect()
    const m = 8
    el.style.left = `${Math.max(m, Math.min(at.x, window.innerWidth - r.width - m))}px`
    el.style.top = `${Math.max(m, at.y + r.height > window.innerHeight - m ? at.y - r.height : at.y)}px`
    el.querySelector<HTMLElement>('[role="menuitem"]:not([disabled])')?.focus({ preventScroll: true })
  }, [at])
  useEffect(() => {
    if (!at) return
    const close = () => onClose()
    window.addEventListener('resize', close)
    window.addEventListener('scroll', close, true)
    return () => {
      window.removeEventListener('resize', close)
      window.removeEventListener('scroll', close, true)
    }
  }, [at, onClose])
  if (!at) return null
  return createPortal(
    <div
      ref={pop}
      style={{ left: at.x, top: at.y }}
      className="fixed z-[70] [-webkit-touch-callout:none]"
      onKeyDown={(e) => {
        if (e.key === 'Escape' || e.key === 'Tab') {
          e.preventDefault()
          onClose()
        }
      }}
    >
      <MenuList label={at.label} items={at.items} onDone={onClose} className="w-[240px]" />
    </div>,
    document.body,
  )
}

export type SelectOption<T extends string> = { value: T; label: string }

export type SelectProps<T extends string> = {
  label: string
  hideLabel?: boolean
  value: T
  options: SelectOption<T>[]
  onChange: (v: T) => void
  /** field＝輸入框樣式（40 高）；compact＝設定列裡的小下拉（32 高）。 */
  look?: 'field' | 'compact'
  className?: string
  /** 沒選到任何選項時顯示的灰字。 */
  placeholder?: string
  disabled?: boolean
}

/** 選項多到這個數量就在清單上方加搜尋框（例：OpenCode 列出的幾十個模型）。 */
const FILTER_AT = 12

type PopPos = { left: number; width: number; top?: number; bottom?: number; maxHeight: number }

/** 下拉清單的位置：用 fixed 貼著按鈕，不會被外面 overflow-hidden 的卡片裁掉；下面不夠就往上開。 */
function placeBelow(el: HTMLElement): PopPos {
  const r = el.getBoundingClientRect()
  const gap = 4
  const margin = 8
  const below = window.innerHeight - r.bottom - gap - margin
  const above = r.top - gap - margin
  const width = Math.max(r.width, 180)
  const left = Math.max(margin, Math.min(r.left, window.innerWidth - width - margin))
  if (below >= 240 || below >= above) return { left, width, top: r.bottom + gap, maxHeight: Math.min(360, below) }
  return { left, width, bottom: window.innerHeight - r.top + gap, maxHeight: Math.min(360, above) }
}

// 自畫下拉選單（不用原生 select）：按鈕＋listbox，方向鍵選、Enter 確定、Esc 取消。選項多時可以打字篩選。
export function Select<T extends string>({ label, hideLabel, value, options, onChange, look = 'field', className, placeholder, disabled }: SelectProps<T>) {
  const [open, setOpen] = useState(false)
  const [pos, setPos] = useState<PopPos | null>(null)
  const [filter, setFilter] = useState('')
  const btn = useRef<HTMLButtonElement>(null)
  const pop = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const id = useId()
  const close = () => {
    setOpen(false)
    setFilter('')
  }
  useDismiss(open, close, btn, pop)
  const filterable = options.length >= FILTER_AT
  useLayoutEffect(() => {
    if (!open || !btn.current) return
    const update = () => btn.current && setPos(placeBelow(btn.current))
    update()
    window.addEventListener('resize', update)
    window.addEventListener('scroll', update, true)
    return () => {
      window.removeEventListener('resize', update)
      window.removeEventListener('scroll', update, true)
    }
  }, [open])
  const shownPop = open && !!pos
  useEffect(() => {
    if (!shownPop) return
    if (filterable) {
      input.current?.focus()
      return
    }
    const els = [...(pop.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])]
    ;(els.find((el) => el.getAttribute('aria-selected') === 'true') ?? els[0])?.focus()
  }, [shownPop, filterable])
  useEffect(() => {
    if (!shownPop || !filterable) return
    pop.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [shownPop, filterable])
  const current = options.find((o) => o.value === value)
  const q = filter.trim().toLowerCase()
  const shown = q ? options.filter((o) => o.label.toLowerCase().includes(q) || o.value.toLowerCase().includes(q)) : options
  const pick = (v: T) => {
    onChange(v)
    close()
    btn.current?.focus()
  }
  const optionEls = () => [...(pop.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])]
  return (
    <div className={cx('relative flex flex-col gap-1', className)}>
      <span id={`${id}-l`} className={hideLabel ? 'sr-only-text' : 'text-meta text-ink-3'}>
        {label}
      </span>
      <button
        ref={btn}
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-labelledby={`${id}-l ${id}-v`}
        disabled={disabled}
        onClick={() => (open ? close() : setOpen(true))}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            e.preventDefault()
            setOpen(true)
          }
        }}
        className={cx(
          'flex items-center border border-line text-left text-ink disabled:opacity-50',
          look === 'field'
            ? 'h-10 gap-2 rounded-field bg-inset pl-3 pr-2.5 text-ui max-sm:h-11'
            : 'relative h-8 gap-1.5 rounded-field bg-card pl-3 pr-2 text-ui-sm touch-target',
        )}
      >
        <span id={`${id}-v`} className={cx('flex-1 truncate', !current && 'text-ink-3')}>
          {current?.label ?? placeholder}
        </span>
        <span className="flex text-ink-2">
          <Icon icon={ChevronDown} size={look === 'field' ? 18 : 16} />
        </span>
      </button>
      {open && pos && (
        <div
          ref={pop}
          style={{ left: pos.left, width: pos.width, top: pos.top, bottom: pos.bottom, maxHeight: pos.maxHeight }}
          className="fixed z-[70] flex flex-col overflow-hidden rounded-menu border border-line bg-panel shadow-pop"
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              e.preventDefault()
              close()
              btn.current?.focus()
            } else if (e.key === 'Tab') close()
          }}
        >
          {filterable && (
            <input
              ref={input}
              aria-label={`找${label}`}
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'ArrowDown') {
                  e.preventDefault()
                  optionEls()[0]?.focus()
                } else if (e.key === 'Enter' && !e.nativeEvent.isComposing && shown.length) {
                  e.preventDefault()
                  pick(shown[0].value)
                }
              }}
              placeholder="輸入文字篩選"
              className="m-1.5 mb-0 h-9 flex-none rounded-field border border-line bg-inset px-3 text-ui-sm text-ink outline-none focus-visible:border-focus"
            />
          )}
          <ul
            role="listbox"
            aria-labelledby={`${id}-l`}
            onKeyDown={(e) => {
              if (moveFocus(e, optionEls())) return
            }}
            className="m-0 min-h-0 flex-1 list-none overflow-y-auto p-1.5"
          >
            {shown.map((o) => {
              const on = o.value === value
              return (
                <li
                  key={o.value}
                  role="option"
                  aria-selected={on}
                  tabIndex={-1}
                  onClick={() => pick(o.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      pick(o.value)
                    }
                  }}
                  className="flex min-h-[34px] cursor-pointer items-center gap-2.5 rounded-seg px-2.5 py-1 text-ui outline-none hover:bg-hover focus:bg-hover max-sm:min-h-11"
                >
                  <span className="min-w-0 flex-1 break-words">{o.label}</span>
                  {on && <Icon icon={Check} size={16} color="var(--color-accent-ink)" />}
                </li>
              )
            })}
            {!shown.length && <li className="px-2.5 py-2 text-sub text-ink-3">沒有符合的</li>}
          </ul>
        </div>
      )}
    </div>
  )
}
