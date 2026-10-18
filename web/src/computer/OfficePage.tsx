import { useMemo, useState } from 'react'
import { Cloud, Hourglass, House, LoaderCircle, Moon, Server, WifiOff } from 'lucide-react'
import { botsStore, type Bot } from '../data/bots'
import { inboxStore } from '../data/inbox'
import { useStore } from '../data/store'
import { linkClick, navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { useNarrow } from './hooks'
import { BOX_SPEC, IDLE_SLEEP_MIN, since, useOffice, useSnapshot, useWake, type Host, type Screen } from './screenData'

type Filter = 'all' | 'working' | 'waiting' | 'queued' | 'sleeping'
type Status = Exclude<Filter, 'all'> | 'idle' | 'booting' | 'offline' | 'yours'

const FILTERS: { key: Filter; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'working', label: '工作中' },
  { key: 'waiting', label: '等你' },
  { key: 'queued', label: '排隊中' },
  { key: 'sleeping', label: '休眠中' },
]

type Row = { screen: Screen; bot?: Bot; status: Status; waitText?: string }

/** 一台電腦的狀態（辦公室的篩選、卡片的字）。 */
function statusOf(s: Screen, waiting: string | undefined, activity?: string): Status {
  if (s.host_status === 'offline' || s.host_status === 'failed') return 'offline'
  if (s.takeover) return 'yours'
  if (waiting) return 'waiting'
  if (activity === 'queued' || activity === 'waiting_box') return 'queued'
  if (s.box === 'creating' || s.box === 'updating') return 'booting'
  if (s.working) return 'working'
  if (s.box === 'running') return 'idle'
  return 'sleeping'
}

const STATUS_TEXT: Record<Status, string> = {
  working: '工作中',
  waiting: '等你',
  queued: '排隊中',
  sleeping: '休眠中',
  idle: '待命',
  booting: '開機中',
  offline: '主機離線',
  yours: '你正在操作',
}

/** /office：所有 Bot 的電腦（design/E03；手機 L15），依主機分組。 */
export function OfficePage() {
  const { screens, hosts, error } = useOffice()
  const { bots, activity } = useStore(botsStore)
  const inbox = useStore(inboxStore).items
  const [filter, setFilter] = useState<Filter>('all')
  const narrow = useNarrow()

  const rows = useMemo<Row[]>(() => {
    if (!screens) return []
    return screens.map((s) => {
      const open = (inbox ?? []).find((i) => i.bot_id === s.bot_id && i.group === 'decide')
      const waitText = s.request ? `等你接手 · ${s.request.reason}` : open ? `等你${open.kind === 'approval' ? '允許' : '回覆'} · ${open.title}` : undefined
      return { screen: s, bot: bots?.find((b) => b.id === s.bot_id), status: statusOf(s, waitText, activity[s.bot_id]?.state), waitText }
    })
  }, [screens, bots, inbox, activity])

  const groups = useMemo(() => {
    const byHost = new Map<string, Row[]>()
    const fallback = hosts?.find((h) => h.default)?.id ?? hosts?.[0]?.id ?? ''
    for (const r of rows) {
      const id = r.screen.host_id || fallback
      byHost.set(id, [...(byHost.get(id) ?? []), r])
    }
    const known = (hosts ?? []).map((h) => ({ host: h as Host | undefined, id: h.id, rows: byHost.get(h.id) ?? [] }))
    for (const [id, list] of byHost) if (!known.some((k) => k.id === id)) known.push({ host: undefined, id, rows: list })
    return known
  }, [rows, hosts])

  const count = (f: Filter) => (f === 'all' ? rows.length : rows.filter((r) => match(r.status, f)).length)
  const running = rows.filter((r) => r.screen.box === 'running').length
  const waiting = rows.filter((r) => r.status === 'waiting').length
  const offline = (hosts ?? []).filter((h) => h.status === 'offline').length

  return (
    <main className="flex min-w-0 flex-1 flex-col gap-5 overflow-y-auto px-7 pb-8 pt-6 max-md:gap-4 max-md:px-3 max-md:pt-4" data-testid="office">
      <header className="flex flex-wrap items-start gap-3">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <h1 className="m-0 text-page">辦公室</h1>
          <p className="m-0 text-sub text-ink-2">
            {narrow
              ? `${running} 台開機 · ${waiting} 台等你接手${offline ? ` · ${offline} 台主機離線` : ''}`
              : '每個 Bot 都有自己的電腦，依主機分組。點「接手」就能直接操作。'}
          </p>
        </div>
        <Button variant="outline" icon={Server} onClick={() => navigate('/settings/computers')} className="max-md:hidden">
          管理主機
        </Button>
      </header>

      <div className="flex flex-wrap items-center gap-3">
        <div role="tablist" aria-label="篩選" className="flex gap-0.5 overflow-x-auto rounded-field bg-inset p-[3px]">
          {FILTERS.map((f) => (
            <button
              key={f.key}
              type="button"
              role="tab"
              aria-selected={filter === f.key}
              onClick={() => setFilter(f.key)}
              className={cx('h-8 whitespace-nowrap rounded-seg px-3 text-ui-sm max-sm:h-10', filter === f.key ? 'bg-control text-ink' : 'text-ink-2 hover:text-ink')}
            >
              {f.label} <span className="text-ink-3">{count(f.key)}</span>
            </button>
          ))}
        </div>
        <span className="flex-1" />
        <span className="text-meta text-ink-3 max-md:hidden">
          每台電腦 {BOX_SPEC} · 閒置 {IDLE_SLEEP_MIN} 分鐘休眠
        </span>
      </div>

      {error && <p className="m-0 text-sub text-danger">{error}</p>}
      {!screens && !error && <p className="m-0 text-sub text-ink-3">讀取中…</p>}
      {screens && rows.length === 0 && <p className="m-0 text-sub text-ink-2">還沒有 Bot。</p>}
      {screens && rows.length > 0 && groups.every((g) => !g.rows.some((r) => match(r.status, filter))) && (
        <p className="m-0 text-sub text-ink-2" data-testid="office-empty">
          沒有符合的電腦。
        </p>
      )}

      {groups.map((g) => {
        const shown = g.rows.filter((r) => match(r.status, filter))
        if (!shown.length && (filter !== 'all' || !g.host)) return null
        return <HostGroup key={g.id} host={g.host} rows={shown} narrow={narrow} />
      })}
    </main>
  )
}

function match(s: Status, f: Filter): boolean {
  if (f === 'all') return true
  if (f === 'working') return s === 'working' || s === 'yours'
  if (f === 'sleeping') return s === 'sleeping'
  if (f === 'queued') return s === 'queued' || s === 'booting'
  return s === f
}

function HostGroup({ host, rows, narrow }: { host?: Host; rows: Row[]; narrow: boolean }) {
  const local = host?.kind === 'local'
  const max = host?.max_running ?? 0
  const on = host?.running ?? rows.filter((r) => r.screen.box === 'running').length
  const offline = host?.status === 'offline' || host?.status === 'failed'
  const full = max > 0 && on >= max
  return (
    <section className="flex flex-col gap-3" aria-label={host?.name ?? '主機'} data-testid="host-group">
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
        <Icon icon={offline ? WifiOff : local ? House : Cloud} size={18} className={offline ? 'text-danger' : 'text-ink-2'} />
        <h2 className="m-0 text-title">{host?.name ?? '主機'}</h2>
        <span className="text-sub text-ink-3">
          {offline ? '離線' : local ? '控制中心' : '雲端'}
          {host?.resources?.cpus ? ` · ${host.resources.cpus} 核 ${Math.round(host.resources.mem_bytes / 2 ** 30)} GB` : ''}
        </span>
        {!local && host && !offline && <span className="rounded-full bg-success-soft px-2 py-px text-[12px] text-success">在線</span>}
        <span className="flex-1" />
        {max > 0 && (
          <span className="flex items-center gap-2 text-sub text-ink-2">
            開機 {on} / {max}
            <span className="h-1.5 w-28 overflow-hidden rounded-full bg-control max-sm:w-16">
              <span className={cx('block h-full rounded-full', full ? 'bg-warning' : 'bg-ink')} style={{ width: `${Math.min(100, (on / max) * 100)}%` }} />
            </span>
          </span>
        )}
      </div>
      {rows.length === 0 ? (
        <p className="m-0 rounded-card border border-dashed border-line px-4 py-3 text-sub text-ink-3">{offline ? '主機離線，上面的 Bot 工作先排隊，回來會自動重新連線。' : '這台主機上還沒有 Bot。'}</p>
      ) : narrow ? (
        <ul className="m-0 flex list-none flex-col divide-y divide-divider overflow-hidden rounded-card bg-panel p-0">
          {rows.map((r) => (
            <PhoneRow key={r.screen.bot_id} row={r} />
          ))}
        </ul>
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-3">
          {rows.map((r) => (
            <ComputerCard key={r.screen.bot_id} row={r} />
          ))}
        </div>
      )}
    </section>
  )
}

/** 縮圖：開著的電腦每 10 秒擷取一張；休眠顯示月亮，開機中轉圈，離線顯示斷線。 */
function Thumb({ row, className }: { row: Row; className?: string }) {
  const live = row.screen.box === 'running' && row.status !== 'offline'
  const { src, stale } = useSnapshot(row.screen.bot_id, live)
  const waiting = row.status === 'waiting'
  const icon = row.status === 'offline' ? WifiOff : row.status === 'booting' || row.status === 'queued' ? (row.status === 'booting' ? LoaderCircle : Hourglass) : Moon
  return (
    <div className={cx('relative overflow-hidden rounded-[10px] bg-[#0d0d0d]', className)} data-testid="thumb">
      {live && src ? (
        <img src={src} alt={`${row.screen.name}的電腦畫面`} className="size-full object-cover object-top" />
      ) : (
        <div className="flex size-full flex-col items-center justify-center gap-1 text-center text-meta text-ink-2">
          <Icon icon={live ? LoaderCircle : icon} size={20} className={live || row.status === 'booting' ? 'animate-spin' : undefined} />
          {!live && <span>{STATUS_TEXT[row.status]}</span>}
        </div>
      )}
      {live && src && (
        <span className="absolute right-1.5 top-1.5 inline-flex items-center gap-1 rounded-full bg-black/75 px-2 py-0.5 text-[11.5px] text-ink">
          <span className={cx('size-1.5 rounded-full', stale ? 'bg-warning' : waiting ? 'bg-warning' : 'bg-success')} />
          {stale ? '重新連線中' : waiting ? '等你' : '即時'}
        </span>
      )}
    </div>
  )
}

function rowSub(r: Row): string {
  if (r.status === 'waiting' && r.waitText) return r.waitText
  if (r.status === 'yours') return `${r.screen.takeover?.by_name ?? '你'}正在操作`
  if (r.status === 'sleeping') return r.screen.box === 'none' ? '還沒開機 · 有工作自動開機' : `休眠中 · 閒置 ${IDLE_SLEEP_MIN} 分鐘`
  if (r.status === 'queued') return '排隊中 · 等電腦空出來'
  if (r.status === 'booting') return '開機中 · 桌面就緒上限 75 秒'
  return STATUS_TEXT[r.status]
}

function ComputerCard({ row }: { row: Row }) {
  const { screen, bot } = row
  const waiting = row.status === 'waiting'
  const to = `/bots/${screen.bot_id}/computer`
  return (
    <article
      className={cx('flex flex-col gap-2.5 rounded-card border bg-panel p-2', waiting ? 'border-warning' : 'border-line')}
      data-testid="office-card"
      data-status={row.status}
      aria-label={`${screen.name}的電腦`}
    >
      <Thumb row={row} className="aspect-[16/10] w-full" />
      <div className="flex items-center gap-2.5 px-1.5">
        {bot && <Avatar shape={bot.shape} color={bot.color} state={row.status === 'working' ? 'working' : row.status === 'sleeping' ? 'sleeping' : 'idle'} dot={waiting ? 'waiting' : 'none'} size={28} ring="#111111" />}
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-ui">{screen.name}的電腦</span>
          <span className={cx('truncate text-meta', waiting ? 'text-warning-ink' : 'text-ink-2')}>{rowSub(row)}</span>
        </div>
      </div>
      <div className="flex items-center gap-2 px-1.5 pb-1">
        <span className="min-w-0 flex-1 truncate text-meta text-ink-3">{screen.last_active_at ? `${since(screen.last_active_at)}前用過` : ''}</span>
        {screen.box === 'running' && row.status !== 'offline' ? (
          <a href={to} onClick={linkClick(to)} className={cx('inline-flex h-[30px] items-center rounded-full px-3.5 text-sub', waiting ? 'bg-ink text-on-ink' : 'bg-control text-ink hover:bg-hover')}>
            接手
          </a>
        ) : (
          <SecondAction row={row} size="sm" />
        )}
      </div>
    </article>
  )
}

function PhoneRow({ row }: { row: Row }) {
  const { screen, bot } = row
  const waiting = row.status === 'waiting'
  const to = `/bots/${screen.bot_id}/computer`
  return (
    <li className="flex items-center gap-3 px-3 py-2.5" data-testid="office-row" data-status={row.status}>
      <Thumb row={row} className="h-[50px] w-[88px] flex-none" />
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="flex items-center gap-1.5 text-ui">
          {bot && <Avatar shape={bot.shape} color={bot.color} state="idle" dot={waiting ? 'waiting' : 'none'} size={20} ring="#111111" />}
          {screen.name}
        </span>
        <span className={cx('truncate text-meta', waiting ? 'text-warning-ink' : 'text-ink-2')}>{rowSub(row)}</span>
      </div>
      {screen.box === 'running' ? (
        <a href={to} onClick={linkClick(to)} className={cx('inline-flex h-11 flex-none items-center rounded-full px-4 text-ui-sm', waiting ? 'bg-ink text-on-ink' : 'bg-control')}>
          接手
        </a>
      ) : (
        <SecondAction row={row} size="md" />
      )}
    </li>
  )
}

/** 休眠或還沒開機：叫醒（B13 xbot.computers.wake，主機滿了會排隊）；其他（開機中、離線）：到對話。 */
function SecondAction({ row, size }: { row: Row; size: 'sm' | 'md' }) {
  const { screen } = row
  const { wake, busy, error } = useWake(screen.bot_id)
  if (row.status === 'sleeping') {
    return (
      <Button size={size === 'sm' ? 'sm' : 'md'} variant="outline" loading={busy} onClick={wake} title={error ?? undefined} className={size === 'md' ? 'h-11' : undefined}>
        叫醒
      </Button>
    )
  }
  const to = `/bots/${screen.bot_id}`
  return (
    <a href={to} onClick={linkClick(to)} className={cx('inline-flex flex-none items-center rounded-full border border-line', size === 'sm' ? 'h-[30px] px-3.5 text-sub' : 'h-11 px-4 text-ui-sm')}>
      對話
    </a>
  )
}
