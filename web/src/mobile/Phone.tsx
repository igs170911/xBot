import type { ReactNode } from 'react'
import { ChevronLeft } from 'lucide-react'
import { inboxStore } from '../data/inbox'
import { useStore } from '../data/store'
import { goBack, linkClick } from '../router'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { TABS } from './nav'


// 手機版（W31、L01～L21）的共用零件：底部分頁列、頁首、滑動返回。

export function TabBar({ path }: { path: string }) {
  const count = useStore(inboxStore).items?.length ?? 0
  return (
    // 放在滿版高度的固定圖層底部，不用 bottom: 0：iPhone 主畫面 App 剛打開時高度少算狀態列，分頁列會先浮在上面（index.css 的 --app-full）
    <div className="pointer-events-none fixed-screen z-30 md:hidden">
      <nav aria-label="分頁" className="pointer-events-auto absolute inset-x-0 bottom-0 flex border-t border-edge bg-app pb-[env(safe-area-inset-bottom)]">
        {TABS.map((t) => {
          const on = t.to === path || (t.to === '/calendar' && path.startsWith('/calendar'))
          return (
            <a
              key={t.to}
              href={t.to}
              onClick={linkClick(t.to)}
              aria-current={on ? 'page' : undefined}
              className={cx('relative flex min-h-[56px] flex-1 flex-col items-center justify-center gap-1 pb-1 pt-2 text-tab', on ? 'text-ink' : 'text-ink-2')}
            >
              <span className="relative">
                <Icon icon={t.icon} size={22} strokeWidth={on ? 2 : 1.75} />
                {t.to === '/' && count > 0 && (
                  <span aria-label={`${count} 件等你`} className="absolute -right-3 -top-1.5 flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-warning px-1 text-[11px] leading-none text-warning-on">
                    {count > 99 ? '99+' : count}
                  </span>
                )}
              </span>
              {t.label}
            </a>
          )
        })}
      </nav>
    </div>
  )
}

/** 圓形返回鈕（L06、L09、L10…）：從 XBot 裡面走過來的就回上一頁，否則到 fallback。 */
export function BackButton({ fallback, label = '返回', onBack }: { fallback: string; label?: string; onBack?: () => void }) {
  return (
    <button
      type="button"
      aria-label={label}
      onClick={() => (onBack ? onBack() : goBack(fallback))}
      className="flex size-11 flex-none items-center justify-center rounded-full border border-divider bg-panel text-ink active:bg-control"
    >
      <Icon icon={ChevronLeft} size={22} />
    </button>
  )
}

/** 手機的頁首：返回鈕＋標題（靠左或置中）＋右邊的動作。 */
export function PhoneBar({
  title,
  sub,
  back,
  onBack,
  right,
  center,
  className,
  heading = true,
}: {
  title: ReactNode
  sub?: ReactNode
  /** 返回的預設去處；不填就不顯示返回鈕。 */
  back?: string
  onBack?: () => void
  right?: ReactNode
  center?: boolean
  className?: string
  /** false：標題不是 h1（頁面自己還有 h1 時，例：共用的返回列）。 */
  heading?: boolean
}) {
  return (
    <header className={cx('sticky top-0 z-20 flex min-h-[60px] flex-none items-center gap-3 bg-app px-3 pb-2 pt-[max(8px,env(safe-area-inset-top))]', className)}>
      {back !== undefined && <BackButton fallback={back} onBack={onBack} />}
      <div className={cx('flex min-w-0 flex-1 flex-col', center && 'items-center text-center')}>
        {heading ? <h1 className="m-0 truncate text-title">{title}</h1> : <span className="truncate text-title">{title}</span>}
        {sub && <span className="truncate text-meta text-ink-2">{sub}</span>}
      </div>
      {right ?? (center && back !== undefined ? <span className="size-11 flex-none" aria-hidden="true" /> : null)}
    </header>
  )
}

/** 分頁首頁的大標題列（L01「對話」、L02「待我處理」、L15「辦公室」）。 */
export function TabHeader({ title, sub, lead, right }: { title: ReactNode; sub?: ReactNode; lead?: ReactNode; right?: ReactNode }) {
  return (
    <header className="flex flex-none items-center gap-2 px-5 pb-2 pt-[max(18px,calc(env(safe-area-inset-top)+10px))]">
      {lead}
      <div className="flex min-w-0 flex-1 flex-col">
        <h1 className="m-0 truncate text-[22px] leading-[1.3]">{title}</h1>
        {sub && <span className="text-sub text-ink-2">{sub}</span>}
      </div>
      {right}
    </header>
  )
}

