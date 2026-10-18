import { Fragment, useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode, type Ref, type RefObject } from 'react'
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
}

// 自畫下拉選單（不用原生 select）：按鈕＋listbox，方向鍵選、Enter 確定、Esc 取消。
export function Select<T extends string>({ label, hideLabel, value, options, onChange, look = 'field', className }: SelectProps<T>) {
  const [open, setOpen] = useState(false)
  const btn = useRef<HTMLButtonElement>(null)
  const pop = useRef<HTMLUListElement>(null)
  const id = useId()
  useDismiss(open, () => setOpen(false), btn, pop)
  useEffect(() => {
    if (!open) return
    const els = [...(pop.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])]
    ;(els.find((el) => el.getAttribute('aria-selected') === 'true') ?? els[0])?.focus()
  }, [open])
  const current = options.find((o) => o.value === value)
  const pick = (v: T) => {
    onChange(v)
    setOpen(false)
    btn.current?.focus()
  }
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
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            e.preventDefault()
            setOpen(true)
          }
        }}
        className={cx(
          'flex items-center border border-line text-left text-ink',
          look === 'field'
            ? 'h-10 gap-2 rounded-field bg-inset pl-3 pr-2.5 text-ui max-sm:h-11'
            : 'relative h-8 gap-1.5 rounded-field bg-card pl-3 pr-2 text-ui-sm touch-target',
        )}
      >
        <span id={`${id}-v`} className="flex-1 truncate">
          {current?.label}
        </span>
        <span className="flex text-ink-2">
          <Icon icon={ChevronDown} size={look === 'field' ? 18 : 16} />
        </span>
      </button>
      {open && (
        <ul
          ref={pop}
          role="listbox"
          aria-labelledby={`${id}-l`}
          onKeyDown={(e) => {
            const els = [...(pop.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])]
            if (moveFocus(e, els)) return
            if (e.key === 'Escape') {
              e.preventDefault()
              setOpen(false)
              btn.current?.focus()
            } else if (e.key === 'Tab') setOpen(false)
          }}
          className="absolute left-0 right-0 top-full z-40 m-0 mt-1 min-w-[180px] list-none rounded-menu bg-card p-1.5 shadow-pop"
        >
          {options.map((o) => {
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
                className="flex h-[34px] cursor-pointer items-center gap-2.5 rounded-seg px-2.5 text-ui outline-none hover:bg-hover focus:bg-hover max-sm:h-11"
              >
                <span className="flex-1">{o.label}</span>
                {on && <Icon icon={Check} size={16} color="var(--color-accent-ink)" />}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
