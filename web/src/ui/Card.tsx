import { useRef, type HTMLAttributes, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { Icon } from './Icon'
import { cx } from './cx'

const CARD_LAYOUT = 'flex flex-col gap-2.5 px-[18px] pt-3 pb-3.5'

// 卡片：#262626、圓角 18（待我處理卡片、提問卡）。
// layout 換掉預設的排版與內距（不用 className 疊加，避免同一屬性兩個 class 打架）。
export function Card({ className, layout = CARD_LAYOUT, children, ...rest }: HTMLAttributes<HTMLElement> & { layout?: string }) {
  return (
    <article className={cx('rounded-card bg-card', layout, className)} {...rest}>
      {children}
    </article>
  )
}

// 面板：#111111、圓角 18（設計系統頁、右欄區塊）。
export function Panel({ title, aside, className, children }: { title?: ReactNode; aside?: ReactNode; className?: string; children: ReactNode }) {
  return (
    <section className={cx('flex flex-col gap-3 rounded-card bg-panel px-[22px] py-5', className)}>
      {(title || aside) && (
        <div className="flex items-baseline gap-3">
          {title && <h2 className="m-0 text-[16px]">{title}</h2>}
          {aside && <span className="text-[13px] text-ink-2">{aside}</span>}
        </div>
      )}
      {children}
    </section>
  )
}

export type OptionItem = { key: string; label: string }

// A/B/C 選項列（提問卡）：點一列就送出；方向鍵上下移動。
export function OptionList({
  options,
  value,
  onPick,
  label = '選一個回答',
}: {
  options: OptionItem[]
  value?: string
  onPick: (key: string) => void
  label?: string
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const onKey = (e: KeyboardEvent, i: number) => {
    const step = e.key === 'ArrowDown' ? 1 : e.key === 'ArrowUp' ? -1 : 0
    if (!step) return
    e.preventDefault()
    refs.current[(i + step + options.length) % options.length]?.focus()
  }
  return (
    <div role="group" aria-label={label} className="-mx-2 flex flex-col">
      {options.map((o, i) => {
        const on = o.key === value
        return (
          <button
            key={o.key}
            ref={(el) => {
              refs.current[i] = el
            }}
            type="button"
            aria-pressed={on}
            onClick={() => onPick(o.key)}
            onKeyDown={(e) => onKey(e, i)}
            className={cx(
              'flex h-10 items-center gap-3 rounded-none border-t border-line px-2 text-left text-ui text-ink max-sm:h-11',
              on ? 'bg-control' : 'bg-transparent hover:bg-control/60',
            )}
          >
            <span
              aria-hidden="true"
              className={cx('size-5 flex-none rounded-chk text-center text-[12px] leading-5', on ? 'bg-ink text-on-ink' : 'bg-line text-ink-2')}
            >
              {o.key}
            </span>
            {o.label}
          </button>
        )
      })}
    </div>
  )
}

// 設定列群組：#191919、圓角 14，列高 48，中間細線。
export function SettingsGroup({ children, label }: { children: ReactNode; label?: string }) {
  return (
    <div role="group" aria-label={label} className="flex flex-col rounded-group bg-inset">
      {children}
    </div>
  )
}

// 列與列之間的細線從左邊 14 px 開始（N03）。
const ROW =
  'relative flex min-h-12 items-center gap-2 not-first:before:absolute not-first:before:left-3.5 not-first:before:right-0 not-first:before:top-0 not-first:before:h-px not-first:before:bg-line'

export function SettingsRow({ title, sub, children }: { title: ReactNode; sub?: ReactNode; children?: ReactNode }) {
  return (
    <div className={cx(ROW, 'pl-3.5 pr-2 py-1')}>
      <span className="min-w-0 flex-1">
        <span className="block text-ui leading-[1.3]">{title}</span>
        {sub && <span className="block text-meta leading-[1.3] text-ink-3">{sub}</span>}
      </span>
      {children}
    </div>
  )
}

export function SettingsLink({ title, note, href, onClick }: { title: ReactNode; note?: ReactNode; href?: string; onClick?: (e: MouseEvent<HTMLAnchorElement>) => void }) {
  return (
    <a href={href ?? '#'} onClick={onClick} className={cx(ROW, 'pl-3.5 pr-3 text-ink')}>
      <span className="flex-1 text-ui">{title}</span>
      {note && <span className="text-[13px] text-ink-2">{note}</span>}
      <span className="text-ink-3">
        <Icon icon={ChevronRight} size={18} />
      </span>
    </a>
  )
}
