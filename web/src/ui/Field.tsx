import { forwardRef, useId, type InputHTMLAttributes, type ReactNode, type TextareaHTMLAttributes } from 'react'
import type { LucideIcon } from 'lucide-react'
import { Icon } from './Icon'
import { cx } from './cx'

// 輸入框共用外觀（N03）：高 40、圓角 10、#191919 底、#333 框；聚焦藍框加 3px 光暈。
export const fieldClass =
  'w-full min-w-0 rounded-field border border-line bg-inset px-3 text-ui text-ink outline-none transition-[border-color,box-shadow] ' +
  'focus:border-focus focus:shadow-[0_0_0_3px_rgb(69_159_254/0.18)] focus-visible:outline-none ' +
  'disabled:opacity-50 aria-invalid:border-danger aria-invalid:focus:shadow-[0_0_0_3px_rgb(255_86_103/0.18)]'

type FieldShellProps = {
  id: string
  label: string
  /** 標籤只給螢幕閱讀器（畫面上用 placeholder 當提示時）。 */
  hideLabel?: boolean
  hint?: ReactNode
  error?: ReactNode
  children: ReactNode
  className?: string
}

function FieldShell({ id, label, hideLabel, hint, error, children, className }: FieldShellProps) {
  return (
    <div className={cx('flex flex-col gap-1', className)}>
      <label htmlFor={id} className={hideLabel ? 'sr-only-text' : 'text-meta text-ink-3'}>
        {label}
      </label>
      {children}
      {error ? (
        <span id={`${id}-msg`} className="text-meta text-danger">
          {error}
        </span>
      ) : hint ? (
        <span id={`${id}-msg`} className="text-meta text-ink-3">
          {hint}
        </span>
      ) : null}
    </div>
  )
}

export type TextInputProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'size'> & {
  label: string
  hideLabel?: boolean
  hint?: ReactNode
  error?: ReactNode
  /** 左邊的圖示（例：搜尋）。 */
  icon?: LucideIcon
  wrapperClassName?: string
}

export const TextInput = forwardRef<HTMLInputElement, TextInputProps>(function TextInput(
  { label, hideLabel, hint, error, icon, id, className, wrapperClassName, type = 'text', ...rest },
  ref,
) {
  const auto = useId()
  const fid = id ?? auto
  return (
    <FieldShell id={fid} label={label} hideLabel={hideLabel} hint={hint} error={error} className={wrapperClassName}>
      <span className="relative flex items-center">
        {icon && (
          <span className="pointer-events-none absolute left-3 text-ink-3">
            <Icon icon={icon} size={18} />
          </span>
        )}
        <input
          ref={ref}
          id={fid}
          type={type}
          aria-invalid={error ? true : undefined}
          aria-describedby={error || hint ? `${fid}-msg` : undefined}
          className={cx(fieldClass, 'h-10 max-sm:h-11', icon && 'pl-[38px]', className)}
          {...rest}
        />
      </span>
    </FieldShell>
  )
})

export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement> & {
  label: string
  hideLabel?: boolean
  hint?: ReactNode
  error?: ReactNode
  wrapperClassName?: string
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { label, hideLabel, hint, error, id, className, wrapperClassName, rows = 3, ...rest },
  ref,
) {
  const auto = useId()
  const fid = id ?? auto
  return (
    <FieldShell id={fid} label={label} hideLabel={hideLabel} hint={hint} error={error} className={wrapperClassName}>
      <textarea
        ref={ref}
        id={fid}
        rows={rows}
        aria-invalid={error ? true : undefined}
        aria-describedby={error || hint ? `${fid}-msg` : undefined}
        className={cx(fieldClass, 'resize-y py-2 leading-normal', className)}
        {...rest}
      />
    </FieldShell>
  )
})
