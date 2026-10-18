import { useId, useRef, type KeyboardEvent, type ReactNode } from 'react'
import { cx } from './cx'

export type SwitchProps = {
  checked: boolean
  onChange: (next: boolean) => void
  /** 沒有可見文字時一定要給。 */
  label: string
  /** 旁邊顯示的文字；不給就只畫開關。 */
  children?: ReactNode
  disabled?: boolean
  className?: string
}

// 開關：40×24，開＝#1084FE、關＝#3D3D3D，白色圓鈕 20（N03）。
export function Switch({ checked, onChange, label, children, disabled, className }: SwitchProps) {
  const btn = (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cx(
        'relative h-6 w-10 flex-none rounded-full p-0 transition-colors touch-target disabled:opacity-40',
        checked ? 'bg-accent' : 'bg-hover',
        !children && className,
      )}
    >
      <span
        aria-hidden="true"
        className={cx('absolute top-0.5 size-5 rounded-full bg-white transition-[left] duration-150', checked ? 'left-[18px]' : 'left-0.5')}
      />
    </button>
  )
  if (!children) return btn
  return (
    <span className={cx('inline-flex items-center gap-2.5', className)}>
      {btn}
      <span className="text-ui-sm" aria-hidden="true">
        {children}
      </span>
    </span>
  )
}

export type CheckboxProps = {
  checked: boolean
  onChange: (next: boolean) => void
  children: ReactNode
  /** 右邊的灰字。 */
  note?: ReactNode
  disabled?: boolean
  className?: string
}

// 勾選框：18、圓角 5；勾選＝藍底白勾，未勾＝1.5px #5A5A5A 框。底下是真的 checkbox。
export function Checkbox({ checked, onChange, children, note, disabled, className }: CheckboxProps) {
  return (
    <label className={cx('relative flex cursor-pointer items-center gap-2.5 text-ui-sm has-disabled:cursor-not-allowed has-disabled:opacity-50', className)}>
      <input
        type="checkbox"
        className="peer absolute size-px opacity-0"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span
        aria-hidden="true"
        className={cx(
          'flex size-[18px] flex-none items-center justify-center rounded-chk peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-focus',
          checked ? 'bg-accent' : 'border-[1.5px] border-check-off',
        )}
      >
        {checked && (
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#FFFFFF" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
            <path d="M20 6 9 17l-5-5" />
          </svg>
        )}
      </span>
      <span className="flex-1">{children}</span>
      {note && <span className="text-meta text-ink-3">{note}</span>}
    </label>
  )
}

export type SegmentOption<T extends string> = { value: T; label: ReactNode; weight?: number }

export type SegmentedControlProps<T extends string> = {
  options: SegmentOption<T>[]
  value: T
  onChange: (v: T) => void
  label: string
  className?: string
}

// 分段控制：外框圓角 10、#191919 底；選中 #2F2F2F、圓角 8。方向鍵切換（radiogroup）。
export function SegmentedControl<T extends string>({ options, value, onChange, label, className }: SegmentedControlProps<T>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const onKey = (e: KeyboardEvent, i: number) => {
    const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
    if (!step) return
    e.preventDefault()
    const j = (i + step + options.length) % options.length
    onChange(options[j].value)
    refs.current[j]?.focus()
  }
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className={cx('grid gap-0.5 rounded-field bg-inset p-[3px]', className)}
      style={{ gridTemplateColumns: options.map((o) => `${o.weight ?? 1}fr`).join(' ') }}
    >
      {options.map((o, i) => {
        const on = o.value === value
        return (
          <button
            key={o.value}
            ref={(el) => {
              refs.current[i] = el
            }}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={on ? 0 : -1}
            onClick={() => onChange(o.value)}
            onKeyDown={(e) => onKey(e, i)}
            className={cx(
              'h-8 whitespace-nowrap rounded-seg px-2 text-ui-sm transition-colors max-sm:h-10',
              on ? 'bg-control text-ink' : 'bg-transparent text-ink-2 hover:text-ink',
            )}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}

export type SliderProps = {
  label: string
  value: number
  min?: number
  max?: number
  step?: number
  onChange: (v: number) => void
  /** 右上角顯示的值。 */
  format?: (v: number) => string
  className?: string
}

// 滑桿：4px 軌道、藍色已選段、白色 16 圓鈕。底下是原生 range，鍵盤可用。
export function Slider({ label, value, min = 0, max = 100, step = 1, onChange, format, className }: SliderProps) {
  const id = useId()
  const pct = ((value - min) / (max - min)) * 100
  return (
    <div className={cx('flex flex-col gap-2', className)}>
      <div className="flex text-sub">
        <label htmlFor={id} className="flex-1">
          {label}
        </label>
        <span className="text-ink-2">{format ? format(value) : value}</span>
      </div>
      <input
        id={id}
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        aria-valuetext={format ? format(value) : undefined}
        onChange={(e) => onChange(Number(e.target.value))}
        className="xb-slider h-4 w-full max-sm:h-11"
        style={{ ['--pct' as string]: `${pct}%` }}
      />
    </div>
  )
}
