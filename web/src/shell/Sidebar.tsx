import { useState, type DragEvent, type ReactNode } from 'react'
import {
  Bot as BotIcon,
  CalendarClock,
  CalendarDays,
  ChevronDown,
  Ellipsis,
  FolderOpen,
  Inbox,
  KeyRound,
  LayoutGrid,
  MessagesSquare,
  Monitor,
  Network,
  OctagonPause,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Puzzle,
  Search,
  Settings,
  UsersRound,
  X,
} from 'lucide-react'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { CountBadge } from '../ui/Pill'
import { Icon } from '../ui/Icon'
import { MenuButton } from '../ui/Menu'
import { cx } from '../ui/cx'
import { linkClick, navigate } from '../router'
import { botsStore, look } from '../data/bots'
import { useLastThreads } from '../data/lastThreads'
import { inboxStore } from '../data/inbox'
import { openNewRoom, roomsStore, type Room } from '../data/rooms'
import { useStore } from '../data/store'
import { moveBot, openNewTeam, sectionsOf, teamsStore } from '../data/teams'
import { draggingBotStore, openNewBot, setRail } from '../data/ui'
import { BotListRow, ChiefCard, RoomListRow } from './BotList'
import { useRowMenu, type MenuBind, type MoreOpener } from './useRowMenu'
import { useToast } from '../ui/toast-context'
import { usePauseAll } from './PauseBanner'

type NavProps = { path: string; onNavigate?: () => void }

// 不常用的頁面收在「設定與更多」裡（連接應用程式在旁邊有自己的按鈕）。
const MORE = [
  { to: '/calendar', label: '定時任務', icon: CalendarDays },
  { to: '/agenda', label: '提案與討論', icon: CalendarClock },
  { to: '/files', label: '全部檔案', icon: FolderOpen },
  { to: '/apps/logins', label: '分享登入', icon: KeyRound },
  { to: '/teams', label: '團隊地圖', icon: LayoutGrid },
  { to: '/org', label: '組織架構', icon: Network },
]

function botStatus(state?: string): {
  text: string
  waiting: boolean
  working: boolean
} {
  const st = state ?? 'done'
  const waiting = st === 'waiting_input' || st === 'waiting_approval'
  const working = st !== 'done' && !waiting
  return {
    text: waiting ? '等你' : st === 'waiting_box' ? '開機中' : working ? '工作中' : '',
    waiting,
    working,
  }
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

/**
 * 一個分區（團隊、未分區、群組）：標題可以收合；團隊的標題有右鍵選單和滑過出現的「⋯」。
 * drop：把 Bot 拖進來（換到這個團隊／移出團隊），拖著的時候框起來。
 */
function Section({ title, children, team, menu, more, drop }: { title: string; children: ReactNode; team?: string; menu?: MenuBind; more?: MoreOpener; drop?: (bot: { id: string; teamId: string }) => void }) {
  const [open, setOpen] = useState(true)
  const dragging = useStore(draggingBotStore)
  const [over, setOver] = useState(false)
  const accepts = !!drop && !!dragging && dragging.teamId !== (team ?? '')
  const dnd = {
    onDragOver: (e: DragEvent) => {
      if (!accepts) return
      e.preventDefault()
      e.dataTransfer.dropEffect = 'move'
      setOver(true)
    },
    onDragLeave: (e: DragEvent) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOver(false)
    },
    onDrop: (e: DragEvent) => {
      setOver(false)
      if (!accepts || !dragging) return
      e.preventDefault()
      drop?.(dragging)
    },
  }
  const ring = cx('rounded-menu', accepts && 'outline-1 -outline-offset-1 outline-dashed outline-line', over && accepts && 'bg-control/40 outline-2 outline-solid outline-accent')
  if (!title)
    return (
      <div className={cx('flex flex-col gap-px', ring)} {...dnd}>
        {children}
      </div>
    )
  return (
    <div className={cx('flex flex-col gap-px', ring)} role="group" aria-label={title} data-team={team} {...dnd}>
      <div className="group relative flex items-center justify-between pr-1">
        <button
          type="button"
          aria-expanded={open}
          data-section-toggle="1"
          {...menu}
          onClick={() => setOpen((o) => !o)}
          className="relative flex min-h-7 items-center gap-1 self-start rounded-seg bg-transparent pb-0 pl-2 pr-2 pt-2 text-[12px] text-ink-3 touch-target hover:text-ink-2"
        >
          {title}
          <span className={cx('transition-transform', !open && '-rotate-90')}>
            <Icon icon={ChevronDown} size={14} strokeWidth={2} />
          </span>
        </button>
        {more && (
          <button
            type="button"
            aria-label={`${title}的選單`}
            title="改名、換成員、刪除"
            onClick={(e) => more(e.currentTarget)}
            className="mt-1 flex size-6 items-center justify-center rounded-seg text-ink-3 opacity-0 hover:bg-control hover:text-ink focus-visible:opacity-100 group-hover:opacity-100 [@media(hover:none)]:opacity-100"
          >
            <Icon icon={Ellipsis} size={15} />
          </button>
        )}
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

// 側欄（2026-10-20 改版，design 稿 A「總管置頂」）：最上面搜尋、新增；大總管（主要 Bot）的大卡片，下面是「待我處理」「辦公室」；
// 其他 Bot 照團隊分區，一列一個（頭像、名字、職稱、最後一句話）；群組在最後。不常用的頁面收進左下角「設定與更多」。桌面可以縮成只有圖示。
export function Sidebar(props: SidebarProps) {
  return props.rail ? <Rail {...props} /> : <Full {...props} />
}

/** 小膠囊連結（待我處理、辦公室）。 */
function Chip({ to, path, icon, children, aside, onNavigate, tour }: NavProps & { to: string; icon: typeof Inbox; children: ReactNode; aside?: ReactNode; tour?: string }) {
  const active = path === to
  return (
    <a
      href={to}
      data-tour={tour}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      className={cx('flex h-8 items-center gap-1.5 rounded-full border px-3 text-[13px] max-sm:h-11', active ? 'border-accent/60 bg-control text-ink' : 'border-line bg-card/60 text-ink-2 hover:text-ink')}
    >
      <Icon icon={icon} size={15} />
      {children}
      {aside}
    </a>
  )
}

function Full({ path, onNavigate, onClose }: SidebarProps) {
  const pauseAll = usePauseAll()
  const { bots, error } = useStore(botsStore)
  const inbox = useStore(inboxStore).items
  const rooms = useStore(roomsStore).rooms
  const teams = useStore(teamsStore).teams
  const last = useLastThreads(bots)
  const [q, setQ] = useState<string | null>(null)
  const query = (q ?? '').trim().toLowerCase()
  const match = (name: string) => !query || name.toLowerCase().includes(query)
  const live = (bots ?? []).filter((b) => !b.archived)
  const chief = live.find((b) => b.primary)
  const sections = bots ? sectionsOf(live.filter((b) => b !== chief), teams) : []
  const count = inbox?.length ?? 0
  const go = (fn: () => void) => () => {
    onNavigate?.()
    fn()
  }
  const newBot = go(openNewBot)
  const rowMenu = useRowMenu(onNavigate)
  const shownRooms = (rooms ?? []).filter((r) => match(r.name))
  const toast = useToast()
  const dragging = useStore(draggingBotStore)
  // 拖 Bot 到分區＝換團隊（空白＝移出團隊）
  const moveTo = (to: string) => (b: { id: string; teamId: string }) => {
    draggingBotStore.set(null)
    const who = live.find((x) => x.id === b.id)
    const where = teams?.find((t) => t.id === to)?.name
    moveBot({ id: b.id, team_id: b.teamId }, to)
      .then(() => toast.show({ kind: 'done', text: where ? `已把 ${who?.name ?? 'Bot'} 移到「${where}」` : `已把 ${who?.name ?? 'Bot'} 移出團隊` }))
      .catch((e: unknown) => toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) }))
  }
  const hasLoose = sections.some((sec) => !sec.team)
  return (
    <nav aria-label="側欄" className="flex h-full flex-col gap-2 overflow-hidden bg-panel px-2.5 pb-2.5 pt-3.5 text-ui-sm leading-[1.45] text-ink">
      <div className="flex min-h-9 items-center gap-1 pl-0.5">
        {onClose ? <IconButton icon={X} label="關閉側欄" onClick={onClose} data-autofocus /> : <IconButton icon={PanelLeftClose} label="縮小側欄" onClick={() => setRail(true)} />}
        <span className="flex-1" />
        <IconButton icon={Search} label="搜尋 Bot 和群組" pressed={q !== null} onClick={() => setQ((v) => (v === null ? '' : null))} />
        <MenuButton
          label="新增"
          align="end"
          items={[
            { label: '新增 Bot', icon: BotIcon, onSelect: newBot },
            { label: '新增群組', icon: MessagesSquare, onSelect: go(openNewRoom) },
            { label: '建立團隊', icon: UsersRound, onSelect: go(openNewTeam) },
          ]}
          trigger={(t) => <IconButton {...t} icon={Plus} label="新增" />}
        />
      </div>
      {q !== null && (
        <label className="relative flex items-center">
          <span className="sr-only-text">搜尋 Bot 和群組</span>
          <span className="pointer-events-none absolute left-2.5 text-ink-3">
            <Icon icon={Search} size={16} />
          </span>
          <input
            autoFocus
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && setQ(null)}
            placeholder="搜尋 Bot 和群組"
            className="h-9 w-full rounded-field bg-control pl-8 pr-3 text-ui text-ink outline-none focus-visible:outline-2 focus-visible:outline-focus max-sm:h-11"
          />
        </label>
      )}

      <div className="flex flex-col items-center gap-2.5 pb-1">
        {chief && <ChiefCard bot={chief} last={last[chief.id]} active={path === `/bots/${chief.id}` || path.startsWith(`/bots/${chief.id}/`)} onNavigate={onNavigate} menu={rowMenu.bot(chief)} ring="#262626" />}
        <div className="flex flex-wrap justify-center gap-1.5">
          <Chip to="/" path={path} icon={Inbox} onNavigate={onNavigate} tour="inbox" aside={count > 0 ? <CountBadge count={count} label={`${count} 件等你`} /> : undefined}>
            待我處理
          </Chip>
          <Chip to="/office" path={path} icon={Monitor} onNavigate={onNavigate}>
            辦公室
          </Chip>
        </div>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto overscroll-y-contain">
        {!bots && error && <p className="m-0 px-3 py-2 text-sub text-danger">讀不到 Bot 清單：{error}</p>}
        {bots && live.length === 0 && (
          <div className="flex flex-col items-start gap-2 px-3 py-2">
            <span className="text-meta text-ink-3">還沒有 Bot</span>
            <Button icon={Plus} variant="outline" onClick={newBot}>
              建立 Bot
            </Button>
          </div>
        )}
        {sections.map((sec) => {
          const list = sec.bots.filter((b) => match(b.name))
          if (query && !list.length) return null
          // 只有一個分區（沒有團隊）時不用標題
          const title = sections.length === 1 && !sec.team ? '' : sec.title
          return (
            <Section
              key={sec.key}
              title={title}
              team={sec.team?.id}
              menu={sec.team ? rowMenu.team(sec.team) : undefined}
              more={sec.team ? rowMenu.more.team(sec.team) : undefined}
              drop={teams?.length ? moveTo(sec.team?.id ?? '') : undefined}
            >
              {list.map((b) => (
                <BotListRow key={b.id} bot={b} last={last[b.id]} crown={!!sec.team && sec.team.chief_id === b.id} path={path} onNavigate={onNavigate} menu={rowMenu.bot(b)} more={rowMenu.more.bot(b)} look="side" ring="#111111" />
              ))}
              {sec.team && sec.bots.length === 0 && (
                <p className="m-0 px-3 py-1 text-sub text-ink-3">{chief?.team_id === sec.team.id ? `成員只有大總管 ${chief.name}（在最上面）。` : '還沒有成員。'}</p>
              )}
            </Section>
          )
        })}
        {shownRooms.length > 0 && (
          // 拖著 Bot 時標題改成提示（不另外插一行，免得放下的位置被推走）
          <Section title={dragging ? '群組（放上去＝加進群組）' : '群組'}>
            {shownRooms.map((r) => (
              <RoomListRow key={r.id} room={r} path={path} onNavigate={onNavigate} menu={rowMenu.room(r)} more={rowMenu.more.room(r)} look="side" ring="#111111" />
            ))}
          </Section>
        )}
        {/* 沒有「未分區」時，拖著團隊裡的 Bot 才在最下面出現（放在最後，上面的列不會被推動） */}
        {dragging?.teamId && !hasLoose && (
          <Section title="" drop={moveTo('')}>
            <p className="m-0 px-3 py-3 text-center text-meta text-ink-2">拖到這裡＝移出團隊</p>
          </Section>
        )}
        {query && bots && !live.some((b) => match(b.name)) && !shownRooms.length && <p className="m-0 px-3 py-4 text-center text-sub text-ink-2">找不到「{q?.trim()}」</p>}
      </div>

      <div className="flex items-center gap-2">
        <MenuButton
          label="設定與更多"
          items={[
            { label: '設定', icon: Settings, onSelect: go(() => navigate('/settings')) },
            ...MORE.map((m, i) => ({ label: m.label, icon: m.icon, separator: i === 0, onSelect: go(() => navigate(m.to)) })),
            { label: '全部停下（所有 Bot）', icon: OctagonPause, separator: true, onSelect: () => void pauseAll() },
          ]}
          trigger={(t) => (
            <button
              {...t}
              type="button"
              aria-label="設定與更多"
              className={cx('relative flex size-10 flex-none items-center justify-center rounded-full border border-line bg-card/60 text-ink-2 touch-target hover:text-ink aria-expanded:bg-control max-sm:size-11', path.startsWith('/settings') && 'border-accent/60 text-ink')}
            >
              <Icon icon={Settings} size={18} />
            </button>
          )}
        />
        <a
          href="/apps"
          onClick={linkClick('/apps', onNavigate)}
          aria-current={path === '/apps' ? 'page' : undefined}
          className={cx('flex h-10 min-w-0 flex-1 items-center justify-center gap-2 rounded-full border border-line px-3 text-[13px] text-ink-2 hover:text-ink max-sm:h-11', path === '/apps' ? 'bg-control text-ink' : 'bg-card/60')}
        >
          <Icon icon={Puzzle} size={16} />
          連接應用程式
        </a>
      </div>
      {rowMenu.ui}
    </nav>
  )
}

// 窄版：只有圖示，滑過顯示名字；不常用的頁面在「更多」選單裡。
function Rail({ path }: SidebarProps) {
  const pauseAll = usePauseAll()
  const rowMenu = useRowMenu()
  const { bots, activity } = useStore(botsStore)
  const inbox = useStore(inboxStore).items
  const rooms = useStore(roomsStore).rooms
  const count = inbox?.length ?? 0
  const item = (active: boolean) => cx('relative flex size-10 flex-none items-center justify-center rounded-menu text-ink', active ? 'bg-control' : 'hover:bg-control/60')
  const link = (to: string, label: string, body: ReactNode, active = path === to, extra?: ReactNode, menu?: MenuBind) => (
    <a key={to} href={to} onClick={linkClick(to)} aria-label={label} title={label} aria-current={active ? 'page' : undefined} {...menu} className={cx(item(active), 'select-none [-webkit-touch-callout:none]')}>
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
      {link(
        '/',
        '待我處理',
        <Icon icon={Inbox} size={18} />,
        path === '/',
        count > 0 ? <span className="absolute right-1.5 top-1.5 size-2 rounded-full bg-warning" aria-label={`${count} 件等你`} role="img" /> : undefined,
      )}
      {link('/office', '辦公室', <Icon icon={Monitor} size={18} />)}
      <span aria-hidden="true" className="my-1 h-px w-6 flex-none bg-line" />
      <div className="flex min-h-0 flex-1 flex-col items-center gap-1 overflow-y-auto overscroll-y-contain">
        {bots?.map((b) => {
          const to = `/bots/${b.id}`
          const active = path === to || path.startsWith(to + '/')
          const st = botStatus(activity[b.id]?.state)
          return link(
            to,
            st.text ? `${b.name}（${st.text}）` : b.name,
            <Avatar {...look(b)} state={st.waiting ? 'notifying' : st.working ? 'working' : 'idle'} dot={st.waiting ? 'waiting' : 'none'} size={26} ring={active ? '#2F2F2F' : '#111111'} />,
            active,
            undefined,
            rowMenu.bot(b),
          )
        })}
        {rooms?.map((r) =>
          link(`/rooms/${r.id}`, r.name, <RoomFace room={r} active={path === `/rooms/${r.id}`} />, undefined, undefined, rowMenu.room(r)),
        )}
      </div>
      <MenuButton
        label="更多"
        items={[
          ...MORE.map((m) => ({ label: m.label, icon: m.icon, onSelect: () => navigate(m.to) })),
          { label: '全部停下（所有 Bot）', icon: OctagonPause, separator: true, onSelect: () => void pauseAll() },
        ]}
        trigger={(t) => <IconButton {...t} icon={Ellipsis} label="更多" />}
      />
      {link('/settings', '設定', <Icon icon={Settings} size={18} />, path.startsWith('/settings'))}
      {rowMenu.ui}
    </nav>
  )
}
