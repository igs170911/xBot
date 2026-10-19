import { AgentsPage } from '../pages/AgentsPage'
import { useEffect, useState, type ReactNode } from 'react'
import { ChevronRight, X } from 'lucide-react'
import { IconButton } from '../ui/Button'
import { linkClick, navigate, previousPath } from '../router'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { GeneralSection } from './GeneralSection'
import { ModelsSection } from './ModelsSection'
import { UsageSection } from './UsageSection'
import { ComputersSection } from './ComputersSection'
import { AddHostPage, HostDetailPage } from './HostPages'
import { BotSettingsPage, type BotTab } from './BotSettings'
import { SkillsLabPage } from './SkillsLab'
import { NetworkPage } from './NetworkPage'
import { BackupPage } from './BackupPage'
import { LimitsPage } from './LimitsPage'
import { PeoplePage } from './PeoplePage'
import { SettingsOverview } from './SettingsOverview'
import { SectionHeader } from './parts'
import { SETTINGS_NAV, activeNav, botBack, botOrigin, settingsTitle, type BotBack } from './nav'
import { PhoneBar } from '../mobile/Phone'

// 設定（J01～J04、L06）：15 個區塊整併成 5 個 —— 一般 · AI 模型 · 電腦與裝置 · 用量與花費 · 進階。
// 桌面：左邊 220 的分類導覽＋右邊內容；平板寬度（768～1023）導覽收成上面一排，內容才放得下；
// 手機：/settings 是 L06 的總覽清單，點進去一頁一頁看，左上角返回。

function useNarrow(): boolean {
  const q = '(max-width: 767px)'
  const [narrow, setNarrow] = useState(() => typeof window.matchMedia === 'function' && window.matchMedia(q).matches)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const m = window.matchMedia(q)
    const on = () => setNarrow(m.matches)
    m.addEventListener?.('change', on)
    return () => m.removeEventListener?.('change', on)
  }, [])
  return narrow
}

function SettingsNav({ current }: { current: string }) {
  return (
    <nav aria-label="設定分類" className="flex w-[220px] flex-none flex-col gap-0.5 border-r border-divider p-3 max-lg:hidden">
      {SETTINGS_NAV.map((n) => (
        <a
          key={n.to}
          href={n.to}
          onClick={linkClick(n.to)}
          aria-current={current === n.to ? 'page' : undefined}
          className={cx(
            'flex items-center gap-2.5 rounded-field text-ink',
            n.sub ? 'h-[34px] pl-[38px] pr-2.5 text-ui-sm text-ink-2 hover:text-ink' : 'h-[38px] px-2.5 text-ui',
            current === n.to ? 'bg-control text-ink' : 'hover:bg-control/60',
          )}
        >
          {n.icon && <Icon icon={n.icon} size={18} />}
          {n.label}
        </a>
      ))}
      <span className="flex-1" />
      <p className="m-0 px-2.5 pt-3 text-[12px] text-ink-3">變更會自動儲存</p>
    </nav>
  )
}

/** 平板寬度（768～1023）：左邊的分類導覽收成上面一排（只放 5 個大分類；進階的子頁在「進階」裡）。 */
function SettingsTabs({ current }: { current: string }) {
  return (
    <nav aria-label="設定分類" className="flex flex-none gap-1 overflow-x-auto border-b border-divider px-3.5 py-2 max-md:hidden lg:hidden">
      {SETTINGS_NAV.filter((n) => !n.sub).map((n) => {
        const on = current === n.to || (n.to === '/settings/advanced' && current.startsWith('/settings/advanced/'))
        return (
          <a
            key={n.to}
            href={n.to}
            onClick={linkClick(n.to)}
            aria-current={on ? 'page' : undefined}
            className={cx('flex h-9 flex-none items-center gap-2 whitespace-nowrap rounded-field px-3 text-ui-sm', on ? 'bg-control text-ink' : 'text-ink-2 hover:bg-control/60 hover:text-ink')}
          >
            {n.icon && <Icon icon={n.icon} size={16} />}
            {n.label}
          </a>
        )
      })}
    </nav>
  )
}

function Content({ path, back }: { path: string; back: BotBack | null }) {
  const bot = path.match(/^\/settings\/bots\/([^/]+)(?:\/(soul|skills|memory|abilities|permissions|advanced)(?:\/([a-z]+))?)?\/?$/)
  if (bot) return <BotSettingsPage key={bot[1]} botId={decodeURIComponent(bot[1])} tab={bot[2] as BotTab | undefined} sub={bot[3]} back={back ?? undefined} />
  if (path === '/settings/general') return <GeneralSection />
  if (path.startsWith('/settings/models')) return <ModelsSection path={path} />
  if (path === '/settings/usage') return <UsageSection />
  if (path === '/settings/computers') return <ComputersSection />
  if (path === '/settings/computers/add') return <AddHostPage />
  const host = path.match(/^\/settings\/computers\/hosts\/([^/]+)\/?$/)
  if (host) return <HostDetailPage hostId={decodeURIComponent(host[1])} />
  if (path === '/settings/advanced/agents') return <AgentsPage />
  if (path === '/settings/advanced/network') return <NetworkPage />
  if (path === '/settings/advanced/limits') return <LimitsPage />
  if (path === '/settings/advanced/backup') return <BackupPage />
  if (path === '/settings/advanced/people') return <PeoplePage />
  if (path === '/settings/advanced/lab') return <SkillsLabPage />
  return <AdvancedIndex />
}

function AdvancedIndex() {
  return (
    <>
      <SectionHeader title="進階" sub="少用的設定收在這裡。" />
      <div className="flex flex-col overflow-hidden rounded-group bg-card">
        {SETTINGS_NAV.filter((n) => n.sub).map((n) => (
          <a key={n.to} href={n.to} onClick={linkClick(n.to)} className="flex min-h-12 items-center gap-2 border-line px-4 text-ui text-ink not-first:border-t hover:bg-control/50">
            <span className="flex-1">{n.label}</span>
            <span className="text-ink-3">
              <Icon icon={ChevronRight} size={18} />
            </span>
          </a>
        ))}
      </div>
    </>
  )
}

function backTarget(path: string, back: BotBack | null): string {
  if (back) return back.to
  if (path.startsWith('/settings/advanced/')) return '/settings/advanced'
  if (path.startsWith('/settings/computers/')) return '/settings/computers'
  return '/settings'
}

/** /settings 與底下的每一頁。 */
export function SettingsPage({ path, onClose }: { path: string; /** 桌面：設定是跳出來的視窗，按 ✕ 回原本的頁面。 */ onClose?: () => void }) {
  const narrow = useNarrow()
  // 桌面打開 /settings 直接進「一般」
  useEffect(() => {
    if (!narrow && (path === '/settings' || path === '/settings/')) navigate('/settings/general', { replace: true })
  }, [narrow, path])

  const overview = narrow && (path === '/settings' || path === '/settings/')
  const title = settingsTitle(path)
  // Bot 設定：從哪裡打開的就回哪裡；導覽只在從設定的某一頁點進來時亮那一項
  const back = path.startsWith('/settings/bots/') ? botBack(botOrigin(path, previousPath())) : null
  const current = back ? back.nav : activeNav(path)

  let body: ReactNode
  if (overview) body = <SettingsOverview />
  else if (path === '/settings' || path === '/settings/') body = null
  else body = <Content path={path} back={back} />

  return (
    <main className={cx('flex min-h-0 min-w-0 flex-1 flex-col bg-sheet max-md:min-h-dvh max-md:bg-app', onClose ? 'h-full' : 'md:h-dvh md:flex-none')}>
      {narrow && <PhoneBar title={overview ? '設定' : title} back={overview ? '/chats' : backTarget(path, back)} center />}
      <header className={cx('flex h-16 flex-none items-center gap-3 border-b border-divider pl-[22px] pr-3.5', narrow && 'hidden')}>
        <h1 className="m-0 flex-1 text-title">設定</h1>
        {onClose && <IconButton icon={X} label="關閉設定" onClick={onClose} />}
      </header>
      {!narrow && <SettingsTabs current={current} />}
      <div className="flex min-h-0 flex-1">
        <SettingsNav current={current} />
        <div className="flex min-w-0 flex-1 flex-col gap-5 overflow-y-auto px-7 pb-6 pt-[22px] max-lg:px-5 max-md:px-4 max-md:pt-4">
          <div className="mx-auto flex w-full max-w-[880px] flex-col gap-5">{body}</div>
        </div>
      </div>
    </main>
  )
}
