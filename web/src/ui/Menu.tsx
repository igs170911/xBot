import { Fragment, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode, type Ref, type RefObject } from 'react'
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
  useEffect(() => {
    if (open) pop.current?.querySelector<HTMLElement>('[role="menuitem"]:not([disabled])')?.focus()
  }, [open])
  const close = () => {
    setOpen(false)
    btn.current?.focus()
  }
  return (
    <span className="relative inline-flex">
      {trigger({ ref: btn, onClick: () => setOpen((o) => !o), 'aria-haspopup': 'menu', 'aria-expanded': open })}
      {open && (
        <div
          ref={pop}
          className={cx('absolute top-full z-40 mt-1', align === 'end' ? 'right-0' : 'left-0')}
          onKeyDown={(e) => {
            if (e.key === 'Escape' || e.key === 'Tab') {
              if (e.key === 'Escape') e.preventDefault()
              close()
            }
          }}
        >
          <MenuList label={label} items={items} onDone={close} />
        </div>
      )}
    </span>
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
  useEffect(() => {
    if (!open) return
    if (filterable) {
      input.current?.focus()
      return
    }
    const els = [...(pop.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])]
    ;(els.find((el) => el.getAttribute('aria-selected') === 'true') ?? els[0])?.focus()
  }, [open, filterable])
  useEffect(() => {
    if (!open || !filterable) return
    pop.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [open, filterable])
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
