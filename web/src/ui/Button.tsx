import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { LoaderCircle, type LucideIcon } from 'lucide-react'
import { Icon } from './Icon'
import { cx } from './cx'

export type ButtonVariant = 'primary' | 'secondary' | 'outline' | 'ghost' | 'danger' | 'danger-solid'
export type ButtonSize = 'md' | 'sm'

const VARIANT: Record<ButtonVariant, string> = {
  primary: 'bg-ink text-on-ink',
  secondary: 'bg-control text-ink hover:bg-hover',
  outline: 'border border-line bg-transparent text-ink hover:bg-control',
  ghost: 'bg-transparent text-accent-ink hover:bg-control',
  // 危險：#2F2F2F 底紅字；實心紅只在確認對話框（N03、B08）。
  danger: 'bg-control text-danger hover:bg-hover',
  'danger-solid': 'bg-danger-solid text-white',
}

const SIZE: Record<ButtonSize, string> = {
  md: 'h-9 text-ui gap-2 max-sm:h-11',
  sm: 'h-[30px] px-3 text-sub gap-1.5 touch-target',
}

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant
  size?: ButtonSize
  /** 左邊的 Lucide 圖示。 */
  icon?: LucideIcon
  /** 處理中：顯示轉圈並停用。 */
  loading?: boolean
  children?: ReactNode
}

// 膠囊按鈕（N03）：主要、次要、外框、文字、危險；中 36、小 30。
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'secondary', size = 'md', icon, loading, disabled, className, children, type = 'button', ...rest },
  ref,
) {
  const iconSize = size === 'sm' ? 14 : 16
  return (
    <button
      ref={ref}
      type={type}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={cx(
        'relative inline-flex flex-none items-center justify-center whitespace-nowrap rounded-full leading-none transition-[background-color,filter] hover:brightness-[1.08] disabled:opacity-40 disabled:hover:brightness-100',
        size === 'md' && (icon ? 'pl-3 pr-3.5' : 'px-4'),
        SIZE[size],
        VARIANT[variant],
        className,
      )}
      {...rest}
    >
      {loading ? (
        <Icon icon={LoaderCircle} size={iconSize} strokeWidth={2} className="animate-spin" />
      ) : (
        icon && <Icon icon={icon} size={iconSize} />
      )}
      {children}
    </button>
  )
})

export type IconButtonProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'aria-label' | 'children'> & {
  icon: LucideIcon
  /** 一定要有：螢幕閱讀器讀這個。 */
  label: string
  /** ghost＝透明底灰圖示（hover 變 #2F2F2F）；filled＝#2F2F2F 底；send＝白底黑箭頭（送出鈕）。 */
  variant?: 'ghost' | 'filled' | 'send'
  /** square＝圓角 10；round＝圓形。 */
  shape?: 'square' | 'round'
  size?: 'md' | 'sm'
  iconSize?: number
  pressed?: boolean
  /** ghost 圖示用主要文字色（預設是次要灰）。 */
  strong?: boolean
}

// 圖示按鈕：36 方形圓角 10；手機 44。aria-label 必填。
export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { icon, label, variant = 'ghost', shape = 'square', size = 'md', iconSize, pressed, strong, className, type = 'button', ...rest },
  ref,
) {
  const send = variant === 'send'
  return (
    <button
      ref={ref}
      type={type}
      aria-label={label}
      aria-pressed={pressed}
      className={cx(
        'relative inline-flex flex-none items-center justify-center p-0 transition-colors disabled:opacity-40',
        send ? (size === 'md' ? 'size-[34px] touch-target' : 'size-8 touch-target') : size === 'md' ? 'size-9 max-sm:size-11' : 'size-[30px] touch-target',
        shape === 'round' || send ? 'rounded-full' : 'rounded-field',
        variant === 'ghost' && cx('bg-transparent hover:bg-control hover:text-ink aria-pressed:bg-control aria-pressed:text-ink', strong ? 'text-ink' : 'text-ink-2'),
        variant === 'filled' && 'bg-control text-ink hover:bg-hover',
        send && 'bg-ink text-on-ink hover:brightness-[1.08]',
        className,
      )}
      {...rest}
    >
      <Icon icon={icon} size={iconSize ?? (send ? 18 : size === 'sm' ? 16 : 20)} strokeWidth={send ? 2.25 : 1.75} />
    </button>
  )
})
