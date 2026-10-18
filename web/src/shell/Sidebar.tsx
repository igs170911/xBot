import { useState, type ReactNode } from 'react'
import { Bell, CalendarClock, CalendarDays, ChevronDown, Crown, Inbox, LayoutGrid, Monitor, Network, Plus, Puzzle, Search, Settings, UsersRound, X } from 'lucide-react'
import { Avatar, Mascot } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { CountBadge } from '../ui/Pill'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { linkClick } from '../router'
import { botsStore, type Bot } from '../data/bots'
import { inboxStore } from '../data/inbox'
import { openNewRoom, roomsStore, type Room } from '../data/rooms'
import { useStore } from '../data/store'
import { openNewTeam, sectionsOf, teamsStore } from '../data/teams'
import { openNewBot } from '../data/ui'

type NavProps = { path: string; onNavigate?: () => void }

function NavRow({ to, path, icon, children, aside, onNavigate }: NavProps & { to: string; icon: typeof Inbox; children: ReactNode; aside?: ReactNode }) {
  const active = path === to
  return (
    <a
      href={to}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      className={cx('flex min-h-10 items-center gap-3 rounded-menu px-3 text-ink max-sm:min-h-11', active ? 'bg-control' : 'hover:bg-control/60')}
    >
      <Icon icon={icon} />
      <span className="flex-1">{children}</span>
      {aside}
    </a>
  )
}

function BotRow({ bot, path, onNavigate, chief }: NavProps & { bot: Bot; chief?: boolean }) {
  const to = `/bots/${bot.id}`
  const active = path === to || path.startsWith(to + '/')
  const ring = active ? '#2F2F2F' : '#111111'
  const act = useStore(botsStore).activity[bot.id]
  const st = act?.state ?? 'done'
  const waiting = st === 'waiting_input' || st === 'waiting_approval'
  const working = st !== 'done' && !waiting
  const sub = waiting ? '等你回覆' : st === 'waiting_box' ? '電腦開機中…' : working ? '工作中…' : bot.title || bot.description || '待命'
  return (
    <a
      href={to}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      className={cx('flex min-h-[58px] items-center gap-2.5 rounded-menu px-2.5 py-[3px] text-ink', active ? 'bg-control' : 'hover:bg-control/60')}
    >
      <Avatar shape={bot.shape} color={bot.color} state={waiting ? 'notifying' : working ? 'working' : 'idle'} dot={waiting ? 'waiting' : 'none'} ring={ring} size={40} />
      <RowText label={bot.name} star={chief} starLabel="主管" sub={sub} subClass={waiting ? 'text-warning-ink' : 'text-ink-2'} />
    </a>
  )
}

function RoomRow({ room, path, onNavigate }: NavProps & { room: Room }) {
  const to = `/rooms/${room.id}`
  const active = path === to
  const working = room.members.filter((m) => m.seat === 'working' || m.seat === 'queued')
  const waiting = room.members.some((m) => m.seat === 'waiting')
  const sub = working.length ? `${working[0].name} 正在回覆…` : waiting ? '有成員在等你' : room.preview || `${room.members.length} 位成員`
  const shown = room.members.slice(0, 3)
  return (
    <a
      href={to}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      className={cx('flex min-h-[58px] items-center gap-2.5 rounded-menu px-2.5 py-[3px] text-ink', active ? 'bg-control' : 'hover:bg-control/60')}
    >
      <span aria-hidden="true" className="relative size-10 flex-none">
        {shown.map((m, i) => (
          <span key={m.bot_id} className="absolute" style={{ left: i === 1 ? 18 : i === 2 ? 9 : 0, top: i === 2 ? 18 : 0 }}>
            <Avatar shape={m.shape} color={m.color} dot="none" size={22} blink={false} ring={active ? '#2F2F2F' : '#111111'} />
          </span>
        ))}
      </span>
      <RowText label={room.name} sub={sub} subClass={waiting ? 'text-warning-ink' : 'text-ink-2'} />
    </a>
  )
}

function RowText({ label, star, starLabel = '幕僚長', time, sub, subClass, unread }: { label: string; star?: boolean; starLabel?: string; time?: string; sub: string; subClass: string; unread?: boolean }) {
  return (
    <span className="flex min-w-0 flex-1 flex-col leading-[1.45]">
      <span className="flex items-baseline gap-1.5">
        <span className="truncate text-body">{label}</span>
        {star && (
          <span className="self-center text-ink-3">
            <Icon icon={Crown} size={13} strokeWidth={2} label={starLabel} />
          </span>
        )}
        {time && <span className="ml-auto flex-none text-meta text-ink-3">{time}</span>}
      </span>
      <span className="flex items-center gap-1.5">
        <span className={cx('min-w-0 flex-1 truncate text-sub', subClass)}>{sub}</span>
        {unread && <span aria-label="有新訊息" role="img" className="size-2 flex-none rounded-full bg-accent" />}
      </span>
    </span>
  )
}

function Section({ title, children, action, team }: { title: string; children: ReactNode; action?: ReactNode; team?: string }) {
  const [open, setOpen] = useState(true)
  return (
    <div className="flex flex-col gap-px" role="group" aria-label={title} data-team={team}>
      <div className="flex items-center justify-between pr-1">
      <button
        type="button"
        aria-expanded={open}
        data-section-toggle="1"
        onClick={() => setOpen((o) => !o)}
        className="relative flex min-h-7 items-center gap-1.5 self-start rounded-seg bg-transparent pb-0 pl-3 pr-2 pt-1 text-[13px] text-ink-3 touch-target hover:text-ink-2"
      >
        {title}
        <span className={cx('transition-transform', !open && '-rotate-90')}>
          <Icon icon={ChevronDown} size={14} strokeWidth={2} />
        </span>
      </button>
      {action}
      </div>
      {open && children}
    </div>
  )
}

const FOOT = [
  { to: '/calendar', label: '定時任務與行事曆', icon: CalendarDays },
  { to: '/agenda', label: '等你的事（提案與討論）', icon: CalendarClock },
  { to: '/apps', label: '連接應用程式', icon: Puzzle },
  { to: '/teams', label: '團隊地圖', icon: LayoutGrid },
  { to: '/org', label: '組織架構', icon: Network },
]

export type SidebarProps = NavProps & {
  /** 抽屜模式：右上角多一個關閉鈕。 */
  onClose?: () => void
}

// 側欄（design/Sidebar.dc.html）：品牌列、搜尋、待我處理／辦公室、分組的 Bot 與群組、底部連結與你。
export function Sidebar({ path, onNavigate, onClose }: SidebarProps) {
  const { bots, error } = useStore(botsStore)
  const inbox = useStore(inboxStore).items
  const rooms = useStore(roomsStore).rooms
  const teams = useStore(teamsStore).teams
  const sections = bots ? sectionsOf(bots, teams) : []
  const count = inbox?.length ?? 0
  const newBot = () => {
    onNavigate?.()
    openNewBot()
  }
  return (
    <nav aria-label="側欄" className="flex h-full flex-col gap-2 overflow-hidden bg-panel px-2.5 pb-2.5 pt-3.5 text-ui-sm leading-[1.45] text-ink">
      <div className="flex min-h-9 items-center gap-1 pl-1.5 pr-0.5">
        <Mascot size={26} />
        <span className="flex-1 pl-1.5 text-title">XBot</span>
        <IconButton icon={Bell} label="通知" />
        <IconButton icon={Plus} label="新增 Bot" onClick={newBot} />
        {onClose && <IconButton icon={X} label="關閉側欄" onClick={onClose} data-autofocus />}
      </div>

      <label className="relative flex items-center">
        <span className="sr-only-text">搜尋</span>
        <span className="pointer-events-none absolute left-3 text-ink-3">
          <Icon icon={Search} size={18} />
        </span>
        <input
          type="search"
          placeholder="搜尋"
          className="h-10 w-full rounded-field bg-control pl-[38px] pr-3 text-ui text-ink outline-none focus-visible:outline-2 focus-visible:outline-focus max-sm:h-11"
        />
      </label>

      <div className="flex flex-col gap-0.5">
        <NavRow to="/" path={path} icon={Inbox} onNavigate={onNavigate} aside={count > 0 ? <CountBadge count={count} label={`${count} 件等你`} /> : undefined}>
          待我處理
        </NavRow>
        <NavRow to="/office" path={path} icon={Monitor} onNavigate={onNavigate}>
          辦公室
        </NavRow>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-px overflow-y-auto">
        {!bots && error && <p className="m-0 px-3 py-2 text-sub text-danger">讀不到 Bot 清單：{error}</p>}
        {sections.map((sec, i) => (
          <Section
            key={sec.key}
            title={sec.title}
            team={sec.team?.id}
            action={
              i === 0 ? (
                <IconButton
                  icon={UsersRound}
                  label="建立團隊"
                  className="size-[30px] max-sm:size-11"
                  onClick={() => {
                    onNavigate?.()
                    openNewTeam()
                  }}
                />
              ) : undefined
            }
          >
            {sec.bots.map((b) => (
              <BotRow key={b.id} bot={b} chief={!!sec.team && sec.team.chief_id === b.id} path={path} onNavigate={onNavigate} />
            ))}
            {sec.team && sec.bots.length === 0 && <p className="m-0 px-3 py-1 text-sub text-ink-3">這個團隊還沒有成員。</p>}
            {!sec.team && bots && bots.length === 0 && (
              <div className="flex flex-col items-start gap-2 px-3 py-2 text-sub text-ink-2">
                還沒有 Bot。建一個，交代它第一件事。
                <Button icon={Plus} onClick={newBot}>
                  新增 Bot
                </Button>
              </div>
            )}
          </Section>
        ))}
        <Section
          title="群組對話"
          action={
            <IconButton
              icon={Plus}
              label="新增群組"
              className="size-[30px] max-sm:size-11"
              onClick={() => {
                onNavigate?.()
                openNewRoom()
              }}
            />
          }
        >
          {rooms?.map((r) => <RoomRow key={r.id} room={r} path={path} onNavigate={onNavigate} />)}
          {rooms && rooms.length === 0 && <p className="m-0 px-3 py-1 text-sub text-ink-3">還沒有群組。按 + 拉 2 個以上的 Bot 一起做事。</p>}
        </Section>
      </div>

      <div className="flex flex-col pt-1">
        {FOOT.map((f) => (
          <a
            key={f.to}
            href={f.to}
            onClick={linkClick(f.to, onNavigate)}
            aria-current={path === f.to ? 'page' : undefined}
            className="flex min-h-[38px] items-center gap-3 rounded-field px-3 text-ui text-ink hover:bg-control/60 max-sm:min-h-11"
          >
            <Icon icon={f.icon} />
            {f.label}
          </a>
        ))}
        <div className="flex min-h-11 items-center gap-2.5 pl-2.5 pr-1.5">
          <span aria-hidden="true" className="flex size-[30px] items-center justify-center rounded-full bg-control text-meta text-ink-2">
            王
          </span>
          <span className="flex-1 text-ui">你</span>
          <a
            href="/settings"
            onClick={linkClick('/settings', onNavigate)}
            aria-label="設定"
            className="flex size-9 items-center justify-center rounded-field text-ink-2 hover:bg-control hover:text-ink max-sm:size-11"
          >
            <Icon icon={Settings} />
          </a>
        </div>
      </div>
    </nav>
  )
}
