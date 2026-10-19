import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { CalendarClock, Crown, FolderOpen, LayoutGrid, Network, Plus, Puzzle, Search, Settings, UsersRound, WifiOff, X, KeyRound } from 'lucide-react'
import { chatListThreads } from '../api/generated'
import { botsStore, type Bot } from '../data/bots'
import { onEvent } from '../data/events'
import { roomsStore, type Room } from '../data/rooms'
import { useStore } from '../data/store'
import { sectionsOf, teamsStore } from '../data/teams'
import { drawerStore } from '../data/ui'
import { linkClick, navigate } from '../router'
import { Avatar, Mascot } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { TabHeader } from './Phone'
import { useOnline } from './pwa'
import { rowTime } from './format'

// L01 手機的對話分頁：Bot 與群組清單，每個 Bot 的臉就是狀態；列上有時間、一行預覽、未讀藍點。

type Last = { at: number; unread: number; title: string }

/** 每個 Bot 最近的對話串（時間、未讀）；有新訊息或工作狀態變了就重讀那個 Bot。 */
function useLastThreads(bots: Bot[] | null): Record<string, Last> {
  const [last, setLast] = useState<Record<string, Last>>({})
  const ids = (bots ?? []).filter((b) => !b.archived).map((b) => b.id)
  const key = ids.join(',')
  useEffect(() => {
    let alive = true
    const load = (id: string) =>
      chatListThreads({ bot_id: id, limit: 5 })
        .then((o) => {
          if (!alive) return
          const latest = o.threads.reduce((m, t) => Math.max(m, t.last_message_at), 0)
          const unread = o.threads.reduce((n, t) => n + t.unread, 0)
          const top = o.threads.find((t) => t.last_message_at === latest)
          setLast((s) => ({ ...s, [id]: { at: latest, unread, title: top?.title ?? '' } }))
        })
        .catch(() => {})
    key.split(',').filter(Boolean).forEach((id) => void load(id))
    const off = onEvent((e) => {
      if (e.bot_id && (e.kind === 'task.state' || e.kind === 'message.created' || e.kind === 'inbox.created')) void load(e.bot_id)
    })
    return () => {
      alive = false
      off()
    }
  }, [key])
  return last
}

function Row({ to, avatar, name, star, time, sub, warn, unread }: { to: string; avatar: ReactNode; name: string; star?: boolean; time?: string; sub: string; warn?: boolean; unread?: boolean }) {
  return (
    <a href={to} onClick={linkClick(to)} className="flex min-h-[56px] items-center gap-3 px-4 py-1.5 text-ink active:bg-control/60">
      <span className="relative flex size-10 flex-none items-center justify-center">{avatar}</span>
      <span className="flex min-w-0 flex-1 flex-col leading-[1.45]">
        <span className="flex items-baseline gap-1.5">
          <span className="truncate text-name">{name}</span>
          {star && (
            <span className="self-center text-ink-3">
              <Icon icon={Crown} size={14} strokeWidth={2} label="主要 Bot" />
            </span>
          )}
          {time && <span className="ml-auto flex-none text-meta text-ink-3">{time}</span>}
        </span>
        <span className="flex items-center gap-2">
          <span className={cx('min-w-0 flex-1 truncate text-sub', warn ? 'text-warning-ink' : 'text-ink-2')}>{sub}</span>
          {unread && <span aria-label="有新訊息" role="img" className="size-2 flex-none rounded-full bg-accent" />}
        </span>
      </span>
    </a>
  )
}

function botSub(bot: Bot, state: string | undefined, last?: Last): { text: string; warn: boolean } {
  const st = state ?? 'done'
  if (st === 'waiting_input') return { text: '等你回覆' + (last?.title ? '：' + last.title : ''), warn: true }
  if (st === 'waiting_approval') return { text: '等你允許' + (last?.title ? '：' + last.title : ''), warn: true }
  if (st === 'waiting_box') return { text: '電腦開機中…', warn: false }
  if (st === 'queued') return { text: '排隊中' + (last?.title ? ' · ' + last.title : ''), warn: false }
  if (st !== 'done') return { text: '工作中…' + (last?.title ?? ''), warn: false }
  return { text: last?.title ? `待命 · ${last.title}` : bot.title || bot.description || '待命', warn: false }
}

function BotItem({ bot, last, star }: { bot: Bot; last?: Last; star?: boolean }) {
  const act = useStore(botsStore).activity[bot.id]
  const sub = botSub(bot, act?.state, last)
  const working = act && act.state !== 'done' && !sub.warn
  return (
    <Row
      to={`/bots/${bot.id}`}
      avatar={<Avatar shape={bot.shape} color={bot.color} state={sub.warn ? 'notifying' : working ? 'working' : 'idle'} dot={sub.warn ? 'waiting' : 'none'} ring="#070707" size={40} />}
      name={bot.name}
      star={star}
      time={rowTime(last?.at ?? 0)}
      sub={sub.text}
      warn={sub.warn}
      unread={(last?.unread ?? 0) > 0}
    />
  )
}

function RoomAvatars({ room }: { room: Room }) {
  const [a, b] = room.members
  return (
    <span aria-hidden="true" className="relative size-10">
      {a && (
        <span className="absolute left-0 top-0.5">
          <Avatar shape={a.shape} color={a.color} dot="none" size={24} blink={false} ring="#070707" />
        </span>
      )}
      {b && (
        <span className="absolute left-[16px] top-0.5">
          <Avatar shape={b.shape} color={b.color} dot="none" size={24} blink={false} ring="#070707" />
        </span>
      )}
      {room.members.length > 2 && <span className="absolute bottom-0 left-[14px] rounded-full bg-control px-1 text-[10.5px] leading-[15px] text-ink-2">+{room.members.length - 2}</span>}
    </span>
  )
}

function RoomItem({ room }: { room: Room }) {
  const working = room.members.filter((m) => m.seat === 'working')
  const waiting = room.members.some((m) => m.seat === 'waiting')
  const sub = working.length ? `${working[0].name} 正在回覆…` : waiting ? '有成員在等你' : room.preview || `${room.members.length} 位成員`
  return <Row to={`/rooms/${room.id}`} avatar={<RoomAvatars room={room} />} name={room.name} time={rowTime(room.last_message_at)} sub={sub} warn={waiting} />
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section aria-label={title} className="flex flex-col">
      <h2 className="m-0 px-5 pb-1 pt-3 text-meta text-ink-2">{title}</h2>
      {children}
    </section>
  )
}

const MORE = [
  { to: '/agenda', label: '提案與討論', icon: CalendarClock },
  { to: '/files', label: '全部檔案', icon: FolderOpen },
  { to: '/apps/logins', label: '分享登入', icon: KeyRound },
  { to: '/apps', label: '連接應用程式', icon: Puzzle },
  { to: '/teams', label: '團隊地圖', icon: LayoutGrid },
  { to: '/org', label: '組織架構', icon: Network },
]

/** L01 對話分頁（/chats）。 */
export function ChatListPage() {
  const { bots, error } = useStore(botsStore)
  const rooms = useStore(roomsStore).rooms
  const teams = useStore(teamsStore).teams
  const last = useLastThreads(bots)
  const online = useOnline()
  const [q, setQ] = useState<string | null>(null)
  const query = (q ?? '').trim()

  const live = useMemo(() => (bots ?? []).filter((b) => !b.archived), [bots])
  const pinned = live.filter((b) => b.primary)
  const sections = bots ? sectionsOf(live.filter((b) => !b.primary), teams) : []
  const match = (s: string) => !query || s.toLowerCase().includes(query.toLowerCase())

  return (
    <main className="flex min-h-0 flex-1 flex-col" aria-label="對話">
      <TabHeader
        lead={
          <button type="button" aria-label="打開側欄" onClick={() => drawerStore.set(true)} className="-ml-2 flex size-11 items-center justify-center rounded-full active:bg-control">
            <Mascot size={26} />
          </button>
        }
        title="對話"
        right={
          <span className="-mr-2 flex items-center">
            <IconButton icon={Search} label="搜尋" className="size-11" onClick={() => setQ((v) => (v === null ? '' : null))} />
            <IconButton icon={Plus} label="新增 Bot" className="size-11" onClick={() => navigate('/new/bot')} />
            <IconButton icon={Settings} label="設定" className="size-11" onClick={() => navigate('/settings')} />
          </span>
        }
      />
      {q !== null && (
        <div className="flex items-center gap-2 px-4 pb-2">
          <label className="relative flex flex-1 items-center">
            <span className="sr-only-text">搜尋 Bot 與群組</span>
            <span className="pointer-events-none absolute left-3 text-ink-3">
              <Icon icon={Search} size={18} />
            </span>
            <input autoFocus type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜尋 Bot 與群組" className="h-11 w-full rounded-field bg-control pl-[38px] pr-3 text-ui text-ink outline-none focus-visible:outline-2 focus-visible:outline-focus" />
          </label>
          <IconButton icon={X} label="關閉搜尋" className="size-11" onClick={() => setQ(null)} />
        </div>
      )}
      {!online && (
        <p role="status" className="mx-4 mb-1 flex items-center gap-2 rounded-field bg-warning-soft px-3 py-2 text-sub text-warning-ink">
          <Icon icon={WifiOff} size={16} />
          網路已離線，恢復後會自動重新連線
        </p>
      )}
      <div className="flex flex-col pb-4">
        {!bots && error && <p className="m-0 px-5 py-2 text-sub text-danger">讀不到 Bot 清單：{error}</p>}
        {bots && live.length === 0 && (
          <div className="mx-4 mt-6 flex flex-col items-center gap-3 rounded-card bg-card px-5 py-8 text-center">
            <Mascot size={48} pose="happy" />
            <p className="m-0 text-body">還沒有 Bot</p>
            <p className="m-0 text-sub text-ink-2">建一個，交代它第一件事。</p>
            <Button variant="primary" icon={Plus} onClick={() => navigate('/new/bot')}>
              建立第一個 Bot
            </Button>
          </div>
        )}
        {pinned.filter((b) => match(b.name)).length > 0 && (
          <Group title="已釘選">
            {pinned.filter((b) => match(b.name)).map((b) => (
              <BotItem key={b.id} bot={b} last={last[b.id]} star />
            ))}
          </Group>
        )}
        {rooms && rooms.filter((r) => match(r.name)).length > 0 && (
          <Group title="群組對話">
            {rooms.filter((r) => match(r.name)).map((r) => (
              <RoomItem key={r.id} room={r} />
            ))}
          </Group>
        )}
        {sections.map((sec) => {
          const list = sec.bots.filter((b) => match(b.name))
          if (!list.length) return null
          return (
            <Group key={sec.key} title={sec.title}>
              {list.map((b) => (
                <BotItem key={b.id} bot={b} last={last[b.id]} star={!!sec.team && sec.team.chief_id === b.id} />
              ))}
            </Group>
          )
        })}
        {query && bots && ![...live, ...(rooms ?? [])].some((x) => match(x.name)) && <p className="m-0 px-5 py-6 text-center text-sub text-ink-2">找不到「{query}」</p>}
        {!query && bots && (
          <Group title="更多">
            <button type="button" onClick={() => navigate('/new/room')} className="flex min-h-12 items-center gap-3 px-5 text-left text-ui text-ink active:bg-control/60">
              <Icon icon={UsersRound} />
              新增群組
            </button>
            {MORE.map((m) => (
              <a key={m.to} href={m.to} onClick={linkClick(m.to)} className="flex min-h-12 items-center gap-3 px-5 text-ui text-ink active:bg-control/60">
                <Icon icon={m.icon} />
                {m.label}
              </a>
            ))}
          </Group>
        )}
      </div>
    </main>
  )
}
