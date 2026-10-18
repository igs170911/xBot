import { AgentsPage } from '../pages/AgentsPage'
import { useEffect, useState, type ReactNode } from 'react'
import { ChevronLeft, ChevronRight, Construction } from 'lucide-react'
import { linkClick, navigate } from '../router'
import { Icon } from '../ui/Icon'
import { SimplifiedTag } from '../ui/Pill'
import { cx } from '../ui/cx'
import { GeneralSection } from './GeneralSection'
import { ModelsSection } from './ModelsSection'
import { UsageSection } from './UsageSection'
import { ComputersSection } from './ComputersSection'
import { AddHostPage, HostDetailPage } from './HostPages'
import { BotSettingsPage } from './BotSettings'
import { SettingsOverview } from './SettingsOverview'
import { SectionHeader } from './parts'
import { SETTINGS_NAV, activeNav } from './nav'

// 設定（J01～J04、L06）：15 個區塊整併成 5 個 —— 一般 · AI 模型 · 電腦與裝置 · 用量與花費 · 進階。
// 桌面：左邊 220 的分類導覽＋右邊內容；手機：/settings 是 L06 的總覽清單，點進去一頁一頁看，左上角返回。

const TITLES: Record<string, string> = Object.fromEntries(SETTINGS_NAV.map((n) => [n.to, n.label]))

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

function SettingsNav({ path }: { path: string }) {
  const current = activeNav(path)
  return (
    <nav aria-label="設定分類" className="flex w-[220px] flex-none flex-col gap-0.5 border-r border-divider p-3 max-md:hidden">
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

function Placeholder({ title, sub, note }: { title: string; sub: string; note: string }) {
  return (
    <>
      <SectionHeader title={title} sub={sub} />
      <div className="flex flex-col items-center gap-2 rounded-group bg-card px-6 py-10 text-center">
        <span className="text-ink-3">
          <Icon icon={Construction} size={28} />
        </span>
        <p className="m-0 text-ui">這一頁還在做</p>
        <p className="m-0 text-sub text-ink-2">{note}</p>
      </div>
    </>
  )
}

function Content({ path }: { path: string }) {
  const bot = path.match(/^\/settings\/bots\/([^/]+)\/?$/)
  if (bot) return <BotSettingsPage botId={decodeURIComponent(bot[1])} />
  if (path === '/settings/general') return <GeneralSection />
  if (path.startsWith('/settings/models')) return <ModelsSection path={path} />
  if (path === '/settings/usage') return <UsageSection />
  if (path === '/settings/computers') return <ComputersSection />
  if (path === '/settings/computers/add') return <AddHostPage />
  const host = path.match(/^\/settings\/computers\/hosts\/([^/]+)\/?$/)
  if (host) return <HostDetailPage hostId={decodeURIComponent(host[1])} />
  if (path === '/settings/advanced/agents') return <AgentsPage />
  if (path === '/settings/advanced/backup') return <Placeholder title="備份與更新" sub="備份資料、還原、更新 XBot。" note="目前請用 make backup／make update。" />
  if (path === '/settings/advanced/people') return <Placeholder title="成員與紀錄" sub="組織成員和操作紀錄。" note="之後的任務卡會做。" />
  if (path === '/settings/advanced/lab') return <Placeholder title="技能庫與實驗" sub="技能庫和還在試的功能。" note="之後的任務卡會做。" />
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

function backTarget(path: string): string {
  if (path.startsWith('/settings/bots/')) return '/settings/models/bots'
  if (path.startsWith('/settings/advanced/')) return '/settings/advanced'
  if (path.startsWith('/settings/computers/')) return '/settings/computers'
  return '/settings'
}

function simplified(path: string): string | null {
  if (path === '/settings/general' || path === '/settings') return '簡化：15 個區塊 → 5 個'
  if (path === '/settings/models') return '簡化：5 個欄位 → 1 個'
  if (path === '/settings/computers/add') return '簡化：手動設定 Docker／SSH／防火牆 → 貼一行指令'
  return null
}

/** /settings 與底下的每一頁。 */
export function SettingsPage({ path }: { path: string }) {
  const narrow = useNarrow()
  // 桌面打開 /settings 直接進「一般」
  useEffect(() => {
    if (!narrow && (path === '/settings' || path === '/settings/')) navigate('/settings/general', { replace: true })
  }, [narrow, path])

  const tag = simplified(path)
  const overview = narrow && (path === '/settings' || path === '/settings/')
  const title = path.startsWith('/settings/bots/') ? 'Bot 的引擎與權限' : path === '/settings/computers/add' ? '新增主機' : path.startsWith('/settings/computers/hosts/') ? '主機' : (TITLES[path] ?? (path.startsWith('/settings/models') ? 'AI 模型' : '設定'))

  let body: ReactNode
  if (overview) body = <SettingsOverview />
  else if (path === '/settings' || path === '/settings/') body = null
  else body = <Content path={path} />

  return (
    <main className="flex min-h-0 flex-1 flex-col bg-sheet md:h-dvh md:flex-none">
      <header className={cx('flex h-16 flex-none items-center gap-3 border-b border-divider pl-[22px] pr-3.5 max-md:h-14 max-md:pl-2', overview && 'hidden')}>
        {narrow && !overview && (
          <a
            href={backTarget(path)}
            onClick={linkClick(backTarget(path))}
            aria-label="返回"
            className="flex size-11 items-center justify-center rounded-full bg-card text-ink"
          >
            <Icon icon={ChevronLeft} />
          </a>
        )}
        <h1 className="m-0 flex-1 text-title max-md:text-center">{narrow && !overview ? title : '設定'}</h1>
        {tag && (
          <span className="max-sm:hidden">
            <SimplifiedTag>{tag}</SimplifiedTag>
          </span>
        )}
        {narrow && !overview && <span className="size-11" aria-hidden="true" />}
      </header>
      <div className="flex min-h-0 flex-1">
        <SettingsNav path={path} />
        <div className="flex min-w-0 flex-1 flex-col gap-5 overflow-y-auto px-7 pb-6 pt-[22px] max-md:px-4 max-md:pt-4">
          <div className="mx-auto flex w-full max-w-[880px] flex-col gap-5">{body}</div>
        </div>
      </div>
    </main>
  )
}
