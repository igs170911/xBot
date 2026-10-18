import type { ReactNode } from 'react'
import { cx } from '../ui/cx'

// 設定頁共用的版面（J01～J04）：頁首（18／13.5）、小節標題（15）、#262626 圓角 14 的群組、列（14.5／13，中間 #333 細線）。

export function SectionHeader({ title, sub, aside }: { title: string; sub?: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex items-start gap-4">
      <div className="min-w-0 flex-1">
        {/* 手機的返回列已經有標題，這裡只留給螢幕閱讀器 */}
        <h2 className="m-0 text-[18px] leading-[1.4] max-md:sr-only-text">{title}</h2>
        {sub && <p className="m-0 mt-0.5 text-sub text-ink-2">{sub}</p>}
      </div>
      {aside}
    </div>
  )
}

export function Block({ title, sub, aside, children, className }: { title?: string; sub?: ReactNode; aside?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section aria-label={title} className={cx('flex flex-col gap-2.5', className)}>
      {(title || aside) && (
        <div className="flex min-h-[22px] items-center gap-3">
          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
            {title && <h3 className="m-0 text-[15px] leading-[1.4]">{title}</h3>}
            {sub && <span className="text-[13px] text-ink-2">{sub}</span>}
          </span>
          {aside}
        </div>
      )}
      {children}
    </section>
  )
}

export function Group({ children, className, label }: { children: ReactNode; className?: string; label?: string }) {
  return (
    <div role={label ? 'group' : undefined} aria-label={label} className={cx('flex flex-col overflow-hidden rounded-group bg-card', className)}>
      {children}
    </div>
  )
}

/** 一列：左邊標題＋說明，右邊控制項。手機寬度控制項換到下一行。 */
export function Row({ title, sub, children, wrap, className }: { title: ReactNode; sub?: ReactNode; children?: ReactNode; wrap?: boolean; className?: string }) {
  return (
    <div className={cx('flex items-center gap-4 border-line px-4 py-3 not-first:border-t', wrap && 'max-sm:flex-col max-sm:items-stretch max-sm:gap-2.5', className)}>
      <span className="min-w-0 flex-1">
        <span className="block text-ui leading-[1.45]">{title}</span>
        {sub && <span className="mt-0.5 block text-[13px] leading-normal text-ink-2">{sub}</span>}
      </span>
      {children}
    </div>
  )
}

/** 說明條（J03 頁首的「一般人不用動」、實驗性功能的提醒）。 */
export function Note({ tone = 'accent', icon, children, action }: { tone?: 'accent' | 'warning'; icon: ReactNode; children: ReactNode; action?: ReactNode }) {
  return (
    <div
      className={cx(
        'flex items-center gap-3 rounded-group border px-4 py-3 text-ui-sm max-sm:flex-wrap',
        tone === 'accent' ? 'border-accent/40 bg-accent-soft/50' : 'border-warning/40 bg-warning-soft',
      )}
    >
      <span className={tone === 'accent' ? 'text-accent-ink' : 'text-warning-ink'}>{icon}</span>
      <span className="min-w-0 flex-1">{children}</span>
      {action}
    </div>
  )
}
