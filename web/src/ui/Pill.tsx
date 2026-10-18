import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Check, ChevronDown, LoaderCircle, BadgeCheck, Sparkles, type LucideIcon } from 'lucide-react'
import { Icon } from './Icon'
import { cx } from './cx'
import { MenuButton, type MenuItem } from './Menu'

export type Tone = 'accent' | 'warning' | 'success' | 'danger' | 'neutral'

const TONE: Record<Tone, string> = {
  accent: 'bg-accent-soft text-accent-ink',
  warning: 'bg-warning-soft text-warning-ink',
  success: 'bg-success-soft text-success',
  danger: 'bg-danger-soft text-danger',
  neutral: 'bg-control text-ink-2',
}

// 狀態標籤：高 24、膠囊、12.5（工作中／等你回覆／排隊中…）；sm＝卡片名字旁的小標（12 px）。
export function StatusChip({
  tone = 'neutral',
  size = 'md',
  icon,
  children,
  className,
}: {
  tone?: Tone
  size?: 'md' | 'sm'
  icon?: LucideIcon
  children: ReactNode
  className?: string
}) {
  return (
    <span
      className={cx(
        'inline-flex flex-none items-center gap-[5px] whitespace-nowrap rounded-full',
        size === 'md' ? 'h-6 px-2.5 text-meta' : 'px-2 py-px text-[12px] leading-[1.55]',
        tone === 'neutral' && size === 'sm' ? 'bg-line text-ink-2' : TONE[tone],
        className,
      )}
    >
      {icon && <Icon icon={icon} size={14} />}
      {children}
    </span>
  )
}

// 簡化標註：放在被簡化畫面的右上角。
export function SimplifiedTag({ children }: { children: ReactNode }) {
  return (
    <StatusChip tone="accent" icon={Sparkles}>
      {children}
    </StatusChip>
  )
}

// 「系統判斷 · 不對？」：快判模型判斷過的地方都帶這個。給 menu 時「不對？」打開選單（選正確的那一種）。
export function JudgeTag({ children, onDispute, menu, menuLabel = '應該是哪一種？' }: { children: ReactNode; onDispute?: () => void; menu?: MenuItem[]; menuLabel?: string }) {
  const cls = 'relative h-[26px] rounded-full bg-transparent px-1.5 text-meta text-accent-ink touch-target hover:bg-control'
  return (
    <span className="inline-flex h-7 items-center gap-1.5 rounded-full border border-line pl-2.5 pr-1 text-meta text-ink-2" data-testid="judge-tag">
      <Icon icon={BadgeCheck} size={14} color="var(--color-success)" />
      系統判斷：{children} ·
      {menu ? (
        <MenuButton
          label={menuLabel}
          items={menu}
          trigger={(p) => (
            <button type="button" {...p} className={cls}>
              不對？
            </button>
          )}
        />
      ) : (
        <button type="button" onClick={onDispute} className={cls}>
          不對？
        </button>
      )}
    </span>
  )
}

export type ToolPillStatus = 'done' | 'running'

// 工具膠囊：mono 字、28 高、細框；完成綠勾、進行中藍色轉圈。
export function ToolPill({ name, status = 'done' }: { name: string; status?: ToolPillStatus }) {
  return (
    <span className={cx('inline-flex h-7 items-center gap-1.5 rounded-full border border-line px-2.5 font-mono text-meta', status === 'running' ? 'text-ink' : 'text-ink-2')}>
      {status === 'running' ? (
        <Icon icon={LoaderCircle} size={14} color="var(--color-accent-ink)" className="animate-spin" />
      ) : (
        <Icon icon={Check} size={14} strokeWidth={2.25} color="var(--color-success)" />
      )}
      {name}
      {status === 'running' && <span className="sr-only-text">進行中</span>}
    </span>
  )
}

// 事件膠囊：Bot 之間往來（第 N/6 棒）、群組輪數（第 N/4 輪）。
export function EventPill({ icon = Check, done, children }: { icon?: LucideIcon; done?: boolean; children: ReactNode }) {
  return (
    <span className={cx('inline-flex h-[30px] items-center gap-1.5 rounded-full border border-line px-3 text-[13px]', done ? 'text-ink' : 'text-ink-2')}>
      <Icon icon={done ? Check : icon} size={14} strokeWidth={done ? 2.25 : 1.75} color={done ? 'var(--color-success)' : undefined} />
      {children}
    </span>
  )
}

// 可切換的膠囊（篩選、工作列「工作了 2 分鐘 · 6 個步驟」）。
export function ChipButton({
  children,
  expanded,
  selected,
  className,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { expanded?: boolean; selected?: boolean }) {
  return (
    <button
      type="button"
      aria-expanded={expanded}
      aria-pressed={selected}
      className={cx(
        'relative inline-flex h-[34px] items-center gap-2 rounded-full border border-line px-3 text-sub text-ink touch-target hover:bg-control',
        selected ? 'bg-control' : 'bg-transparent',
        className,
      )}
      {...rest}
    >
      {children}
      {expanded !== undefined && (
        <span className={cx('text-ink-3 transition-transform', expanded && 'rotate-180')}>
          <Icon icon={ChevronDown} size={16} />
        </span>
      )}
    </button>
  )
}

// 數字徽章（側欄「待我處理 5」）：等你的事用橘底。
export function CountBadge({ count, tone = 'warning', label }: { count: number; tone?: 'warning' | 'neutral'; label?: string }) {
  return (
    <span
      aria-label={label}
      className={cx(
        'flex h-5 min-w-5 items-center justify-center rounded-full px-1.5 text-[12px] leading-none',
        tone === 'warning' ? 'bg-warning text-warning-on' : 'bg-control text-ink-2',
      )}
    >
      {count}
    </span>
  )
}

// 鍵盤快捷鍵。
export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="h-[22px] rounded-[6px] border border-line px-1.5 font-mono text-tab leading-[22px] text-ink-2">{children}</kbd>
}
