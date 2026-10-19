import { useState, type ReactNode } from 'react'
import {
  CalendarClock,
  CalendarDays,
  ChevronDown,
  Crown,
  Ellipsis,
  FolderOpen,
  Inbox,
  LayoutGrid,
  Monitor,
  Network,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Puzzle,
  Settings,
  UsersRound,
  X,
} from 'lucide-react'
import { Avatar, Mascot } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { CountBadge } from '../ui/Pill'
import { Icon } from '../ui/Icon'
import { MenuButton } from '../ui/Menu'
import { cx } from '../ui/cx'
import { linkClick, navigate } from '../router'
import { botsStore, type Bot, look } from '../data/bots'
import { inboxStore } from '../data/inbox'
import { openNewRoom, roomsStore, type Room } from '../data/rooms'
import { useStore } from '../data/store'
import { openNewTeam, sectionsOf, teamsStore } from '../data/teams'
import { openNewBot, setRail } from '../data/ui'

type NavProps = { path: string; onNavigate?: () => void }

// 不常用的頁面收在「更多」裡。
const MORE = [
  { to: '/calendar', label: '定時任務', icon: CalendarDays },
  { to: '/agenda', label: '提案與討論', icon: CalendarClock },
  { to: '/files', label: '全部檔案', icon: FolderOpen },
  { to: '/apps', label: '連接應用程式', icon: Puzzle },
  { to: '/teams', label: '團隊地圖', icon: LayoutGrid },
  { to: '/org', label: '組織架構', icon: Network },
]

function NavRow({ to, path, icon, children, aside, onNavigate, tour }: NavProps & { to: string; icon: typeof Inbox; children: ReactNode; aside?: ReactNode; tour?: string }) {
  const active = path === to
  return (
    <a
      href={to}
      data-tour={tour}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      className={cx('flex min-h-9 items-center gap-3 rounded-menu px-3 text-ui text-ink max-sm:min-h-11', active ? 'bg-control' : 'hover:bg-control/60')}
    >
      <Icon icon={icon} size={18} />
      <span className="flex-1 truncate">{children}</span>
      {aside}
    </a>
  )
}

function botStatus(state?: string): { text: string; waiting: boolean; working: boolean } {
  const st = state ?? 'done'
  const waiting = st === 'waiting_input' || st === 'waiting_approval'
  const working = st !== 'done' && !waiting
  return { text: waiting ? '等你' : st === 'waiting_box' ? '開機中' : working ? '工作中' : '', waiting, working }
}

// 一行一個 Bot：頭像＋名字，有事的時候才在右邊標「工作中」「等你」。
function BotRow({ bot, path, onNavigate, chief }: NavProps & { bot: Bot; chief?: boolean }) {
  const to = `/bots/${bot.id}`
  const active = path === to || path.startsWith(to + '/')
  const st = botStatus(useStore(botsStore).activity[bot.id]?.state)
  return (
    <a
      href={to}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      title={bot.title || undefined}
      className={cx('flex min-h-10 items-center gap-2.5 rounded-menu px-2.5 text-ink max-sm:min-h-11', active ? 'bg-control' : 'hover:bg-control/60')}
    >
      <Avatar {...look(bot)} state={st.waiting ? 'notifying' : st.working ? 'working' : 'idle'} dot={st.waiting ? 'waiting' : 'none'} ring={active ? '#2F2F2F' : '#111111'} size={26} />
      <span className="min-w-0 flex-1 truncate text-ui">{bot.name}</span>
      {chief && <Icon icon={Crown} size={13} strokeWidth={2} label="主管" className="text-ink-3" />}
      {st.text && <span className={cx('flex-none text-meta', st.waiting ? 'text-warning-ink' : 'text-accent-ink')}>{st.text}</span>}
    </a>
  )
}

function RoomRow({ room, path, onNavigate }: NavProps & { room: Room }) {
  const to = `/rooms/${room.id}`
  const active = path === to
  const working = room.members.some((m) => m.seat === 'working' || m.seat === 'queued')
  const waiting = room.members.some((m) => m.seat === 'waiting')
  return (
    <a
      href={to}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      className={cx('flex min-h-10 items-center gap-2.5 rounded-menu px-2.5 text-ink max-sm:min-h-11', active ? 'bg-control' : 'hover:bg-control/60')}
    >
      <RoomFace room={room} active={active} />
      <span className="min-w-0 flex-1 truncate text-ui">{room.name}</span>
      {(working || waiting) && <span className={cx('flex-none text-meta', waiting ? 'text-warning-ink' : 'text-accent-ink')}>{waiting ? '等你' : '回覆中'}</span>}
    </a>
  )
}

function RoomFace({ room, active }: { room: Room; active: boolean }) {
  const shown = room.members.slice(0, 2)
  return (
    <span aria-hidden="true" className="relative size-[26px] flex-none">
      {shown.map((m, i) => (
        <span key={m.bot_id} className="absolute" style={{ left: i * 10, top: i * 10 }}>
          <Avatar shape={m.shape} color={m.color} dot="none" size={16} blink={false} ring={active ? '#2F2F2F' : '#111111'} />
        </span>
      ))}
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

export type SidebarProps = NavProps & {
  /** 抽屜模式：右上角多一個關閉鈕。 */
  onClose?: () => void
  /** 縮成只有圖示的窄版（桌面）。 */
  rail?: boolean
}

// 側欄：品牌列、待我處理／辦公室、Bot 與群組；其他頁面收在「更多」。桌面可以縮成只有圖示。
export function Sidebar(props: SidebarProps) {
  return props.rail ? <Rail {...props} /> : <Full {...props} />
}

function Full({ path, onNavigate, onClose }: SidebarProps) {
  const { bots, error } = useStore(botsStore)
  const inbox = useStore(inboxStore).items
  const rooms = useStore(roomsStore).rooms
  const teams = useStore(teamsStore).teams
  const sections = bots ? sectionsOf(bots, teams) : []
  const count = inbox?.length ?? 0
  const inMore = MORE.some((m) => path === m.to || path.startsWith(m.to + '/'))
  const [more, setMore] = useState(inMore)
  const newBot = () => {
    onNavigate?.()
    openNewBot()
  }
  return (
    <nav aria-label="側欄" className="flex h-full flex-col gap-2 overflow-hidden bg-panel px-2.5 pb-2.5 pt-3.5 text-ui-sm leading-[1.45] text-ink">
      <div className="flex min-h-9 items-center gap-1 pl-1.5 pr-0.5">
        <Mascot size={24} />
        <span className="flex-1 pl-1.5 text-title">XBot</span>
        {onClose ? <IconButton icon={X} label="關閉側欄" onClick={onClose} data-autofocus /> : <IconButton icon={PanelLeftClose} label="縮小側欄" onClick={() => setRail(true)} />}
      </div>

      <div className="flex flex-col gap-0.5">
        <button type="button" onClick={newBot} className="flex min-h-9 items-center gap-3 rounded-menu bg-transparent px-3 text-left text-ui text-ink hover:bg-control/60 max-sm:min-h-11" aria-label="新增 Bot">
          <Icon icon={Plus} size={18} />
          <span className="flex-1">新增 Bot</span>
        </button>
        <NavRow to="/" path={path} icon={Inbox} onNavigate={onNavigate} tour="inbox" aside={count > 0 ? <CountBadge count={count} label={`${count} 件等你`} /> : undefined}>
          待我處理
        </NavRow>
        <NavRow to="/office" path={path} icon={Monitor} onNavigate={onNavigate}>
          辦公室
        </NavRow>
        <button
          type="button"
          aria-expanded={more}
          onClick={() => setMore((m) => !m)}
          className="flex min-h-9 items-center gap-3 rounded-menu bg-transparent px-3 text-left text-ui text-ink-2 hover:bg-control/60 hover:text-ink max-sm:min-h-11"
        >
          <Icon icon={Ellipsis} size={18} />
          <span className="flex-1">更多</span>
          <Icon icon={ChevronDown} size={14} className={cx('transition-transform', more && 'rotate-180')} />
        </button>
        {more &&
          MORE.map((m) => (
            <NavRow key={m.to} to={m.to} path={path} icon={m.icon} onNavigate={onNavigate}>
              {m.label}
            </NavRow>
          ))}
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-px overflow-y-auto pt-1">
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
            {sec.team && sec.bots.length === 0 && <p className="m-0 px-3 py-1 text-sub text-ink-3">還沒有成員。</p>}
            {!sec.team && bots && bots.length === 0 && (
              <div className="flex flex-col items-start gap-2 px-3 py-2">
                <span className="text-meta text-ink-3">還沒有 Bot</span>
                <Button icon={Plus} variant="outline" onClick={newBot}>
                  建立 Bot
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
          {rooms && rooms.length === 0 && <p className="m-0 px-3 py-1 text-sub text-ink-3">還沒有群組。</p>}
        </Section>
      </div>

      <a
        href="/settings"
        onClick={linkClick('/settings', onNavigate)}
        aria-label="設定"
        aria-current={path.startsWith('/settings') ? 'page' : undefined}
        className={cx('flex min-h-10 items-center gap-3 rounded-menu px-3 text-ui text-ink max-sm:min-h-11', path.startsWith('/settings') ? 'bg-control' : 'hover:bg-control/60')}
      >
        <Icon icon={Settings} size={18} />
        <span className="flex-1">設定</span>
      </a>
    </nav>
  )
}

// 窄版：只有圖示，滑過顯示名字；不常用的頁面在「更多」選單裡。
function Rail({ path }: SidebarProps) {
  const { bots, activity } = useStore(botsStore)
  const inbox = useStore(inboxStore).items
  const rooms = useStore(roomsStore).rooms
  const count = inbox?.length ?? 0
  const item = (active: boolean) =>
    cx('relative flex size-10 flex-none items-center justify-center rounded-menu text-ink', active ? 'bg-control' : 'hover:bg-control/60')
  const link = (to: string, label: string, body: ReactNode, active = path === to, extra?: ReactNode) => (
    <a key={to} href={to} onClick={linkClick(to)} aria-label={label} title={label} aria-current={active ? 'page' : undefined} className={item(active)}>
      {body}
      {extra}
    </a>
  )
  return (
    <nav aria-label="側欄" className="flex h-full flex-col items-center gap-1 overflow-hidden bg-panel px-1.5 pb-2.5 pt-3.5 text-ink">
      <IconButton icon={PanelLeftOpen} label="展開側欄" onClick={() => setRail(false)} />
      <button type="button" aria-label="新增 Bot" title="新增 Bot" onClick={openNewBot} className={cx(item(false), 'bg-transparent')}>
        <Icon icon={Plus} size={18} />
      </button>
      {link('/', '待我處理', <Icon icon={Inbox} size={18} />, path === '/', count > 0 ? <span className="absolute right-1.5 top-1.5 size-2 rounded-full bg-warning" aria-label={`${count} 件等你`} role="img" /> : undefined)}
      {link('/office', '辦公室', <Icon icon={Monitor} size={18} />)}
      <span aria-hidden="true" className="my-1 h-px w-6 flex-none bg-line" />
      <div className="flex min-h-0 flex-1 flex-col items-center gap-1 overflow-y-auto">
        {bots?.map((b) => {
          const to = `/bots/${b.id}`
          const active = path === to || path.startsWith(to + '/')
          const st = botStatus(activity[b.id]?.state)
          return link(to, st.text ? `${b.name}（${st.text}）` : b.name, <Avatar {...look(b)} state={st.waiting ? 'notifying' : st.working ? 'working' : 'idle'} dot={st.waiting ? 'waiting' : 'none'} size={26} ring={active ? '#2F2F2F' : '#111111'} />, active)
        })}
        {rooms?.map((r) => link(`/rooms/${r.id}`, r.name, <RoomFace room={r} active={path === `/rooms/${r.id}`} />))}
      </div>
      <MenuButton
        label="更多"
        items={MORE.map((m) => ({ label: m.label, icon: m.icon, onSelect: () => navigate(m.to) }))}
        trigger={(t) => <IconButton {...t} icon={Ellipsis} label="更多" />}
      />
      {link('/settings', '設定', <Icon icon={Settings} size={18} />, path.startsWith('/settings'))}
    </nav>
  )
}
