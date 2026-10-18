import { useState, type ReactNode } from 'react'
import { Bell, CalendarDays, ChevronDown, Crown, Inbox, Monitor, Network, Plus, Puzzle, Search, Settings, X } from 'lucide-react'
import { Avatar, Mascot } from '../ui/Avatar'
import { IconButton } from '../ui/Button'
import { CountBadge } from '../ui/Pill'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { linkClick } from '../router'
import { BOTS, INBOX_COUNT, SECTIONS, type SampleRoom } from './sample'

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

function BotRow({ id, path, onNavigate }: NavProps & { id: string }) {
  const b = BOTS[id]
  const to = `/bots/${id}`
  const active = path === to
  const ring = active ? '#2F2F2F' : '#111111'
  return (
    <a
      href={to}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      className={cx('flex min-h-[58px] items-center gap-2.5 rounded-menu px-2.5 py-[3px] text-ink', active ? 'bg-control' : 'hover:bg-control/60')}
    >
      <Avatar shape={b.shape} color={b.color} state={b.state} dot={b.dot} ring={ring} size={40} />
      <RowText
        label={b.name}
        star={b.chief}
        time={b.time}
        sub={b.sub}
        subClass={b.dot === 'waiting' ? 'text-warning-ink' : 'text-ink-2'}
        unread={b.unread}
      />
    </a>
  )
}

function RoomRow({ room, path, onNavigate }: NavProps & { room: SampleRoom }) {
  const to = `/rooms/${room.id}`
  const active = path === to
  const [m1, m2] = room.members.map((m) => BOTS[m])
  return (
    <a
      href={to}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      className={cx('flex min-h-[58px] items-center gap-2.5 rounded-menu px-2.5 py-[3px] text-ink', active ? 'bg-control' : 'hover:bg-control/60')}
    >
      <span className="relative size-10 flex-none" aria-hidden="true">
        <span className="absolute left-0 top-1">
          <Avatar shape={m1.shape} color={m1.color} state="idle" dot="none" size={22} />
        </span>
        <span className="absolute left-3 top-1">
          <Avatar shape={m2.shape} color={m2.color} state="idle" dot="none" size={22} />
        </span>
        <span className="absolute left-[18px] top-[18px] flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-control px-1 text-[10.5px] text-ink-2">
          {room.more}
        </span>
      </span>
      <RowText label={room.name} time={room.time} sub={room.sub} subClass="text-ink-2" />
    </a>
  )
}

function RowText({ label, star, time, sub, subClass, unread }: { label: string; star?: boolean; time?: string; sub: string; subClass: string; unread?: boolean }) {
  return (
    <span className="flex min-w-0 flex-1 flex-col leading-[1.45]">
      <span className="flex items-baseline gap-1.5">
        <span className="truncate text-body">{label}</span>
        {star && (
          <span className="self-center text-ink-3">
            <Icon icon={Crown} size={13} strokeWidth={2} label="幕僚長" />
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

function Section({ title, children }: { title: string; children: ReactNode }) {
  const [open, setOpen] = useState(true)
  return (
    <div className="flex flex-col gap-px">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="relative flex min-h-7 items-center gap-1.5 self-start rounded-seg bg-transparent pb-0 pl-3 pr-2 pt-1 text-[13px] text-ink-3 touch-target hover:text-ink-2"
      >
        {title}
        <span className={cx('transition-transform', !open && '-rotate-90')}>
          <Icon icon={ChevronDown} size={14} strokeWidth={2} />
        </span>
      </button>
      {open && children}
    </div>
  )
}

const FOOT = [
  { to: '/calendar', label: '定時任務與行事曆', icon: CalendarDays },
  { to: '/apps', label: '連接應用程式', icon: Puzzle },
  { to: '/org', label: '組織架構', icon: Network },
]

export type SidebarProps = NavProps & {
  /** 抽屜模式：右上角多一個關閉鈕。 */
  onClose?: () => void
}

// 側欄（design/Sidebar.dc.html）：品牌列、搜尋、待我處理／辦公室、分組的 Bot 與群組、底部連結與你。
export function Sidebar({ path, onNavigate, onClose }: SidebarProps) {
  return (
    <nav aria-label="側欄" className="flex h-full flex-col gap-2 overflow-hidden bg-panel px-2.5 pb-2.5 pt-3.5 text-ui-sm leading-[1.45] text-ink">
      <div className="flex min-h-9 items-center gap-1 pl-1.5 pr-0.5">
        <Mascot size={26} />
        <span className="flex-1 pl-1.5 text-title">XBot</span>
        <IconButton icon={Bell} label="通知" />
        <IconButton icon={Plus} label="新增 Bot 或群組對話" />
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
        <NavRow to="/" path={path} icon={Inbox} onNavigate={onNavigate} aside={<CountBadge count={INBOX_COUNT} label={`${INBOX_COUNT} 件等你`} />}>
          待我處理
        </NavRow>
        <NavRow to="/office" path={path} icon={Monitor} onNavigate={onNavigate} aside={<span className="text-meta text-ink-3">5 台開機</span>}>
          辦公室
        </NavRow>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-px overflow-y-auto">
        {SECTIONS.map((sec) => (
          <Section key={sec.title} title={sec.title}>
            {sec.rows.map((r) =>
              'bot' in r ? (
                <BotRow key={r.bot} id={r.bot} path={path} onNavigate={onNavigate} />
              ) : (
                <RoomRow key={r.room.id} room={r.room} path={path} onNavigate={onNavigate} />
              ),
            )}
          </Section>
        ))}
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
