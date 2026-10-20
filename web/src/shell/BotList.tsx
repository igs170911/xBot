import type { ReactNode } from 'react'
import { Crown, Star } from 'lucide-react'
import { botsStore, type Bot, look } from '../data/bots'
import { botLine, type LastThread } from '../data/lastThreads'
import type { Room } from '../data/rooms'
import { useStore } from '../data/store'
import { rowTime } from '../mobile/format'
import { linkClick } from '../router'
import { Avatar } from '../ui/Avatar'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import type { MenuBind } from './useRowMenu'

// 側欄與手機對話分頁共用的清單（2026-10-20 側欄改版，design 稿 A「總管置頂」）：
// 最上面是大總管的大卡片；其他一個 Bot 一列：頭像、名字、職稱、最後一句話（有事時改成「等你」「工作中」）、未讀點。

type Look = 'side' | 'phone'

/** 大總管（主要 Bot）的卡片：大頭像、星星徽章、名字；有事時名字下面一行狀態。 */
export function ChiefCard({ bot, last, active, onNavigate, menu, ring }: { bot: Bot; last?: LastThread; active?: boolean; onNavigate?: () => void; menu?: MenuBind; ring: string }) {
  const to = `/bots/${bot.id}`
  const line = botLine(useStore(botsStore).activity[bot.id]?.state, last)
  const unread = (last?.unread ?? 0) > 0
  return (
    <a
      href={to}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      aria-label={`${bot.name}（大總管）${line.warn || line.working ? '，' + line.text : ''}${unread ? '，有新訊息' : ''}`}
      data-testid="chief-card"
      {...menu}
      className={cx(
        'relative flex w-[112px] select-none flex-col items-center gap-1.5 rounded-card border px-2 pb-2.5 pt-3.5 text-ink [-webkit-touch-callout:none]',
        active ? 'border-accent/60 bg-control' : 'border-line bg-card hover:bg-control/60',
      )}
    >
      <span className="relative">
        <Avatar {...look(bot)} state={line.warn ? 'notifying' : line.working ? 'working' : 'idle'} dot="none" ring={ring} size={60} />
        <span aria-hidden="true" className="absolute -bottom-0.5 -right-1 flex size-5 items-center justify-center rounded-full border-2 border-card bg-warning text-warning-on">
          <Icon icon={Star} size={11} strokeWidth={2.5} />
        </span>
      </span>
      <span className="max-w-full truncate text-ui">{bot.name}</span>
      {(line.warn || line.working) && <span className={cx('-mt-1 max-w-full truncate text-meta', line.warn ? 'text-warning-ink' : 'text-accent-ink')}>{line.warn ? '等你' : '工作中'}</span>}
      {unread && <span aria-hidden="true" className="absolute right-2 top-2 size-2 rounded-full bg-accent" />}
    </a>
  )
}

function Row({
  to,
  active,
  onNavigate,
  menu,
  avatar,
  name,
  tag,
  crown,
  line,
  warn,
  unread,
  time,
  look: kind,
}: {
  to: string
  active?: boolean
  onNavigate?: () => void
  menu?: MenuBind
  avatar: ReactNode
  name: string
  tag?: string
  crown?: boolean
  line: string
  warn?: boolean
  unread?: boolean
  time?: string
  look: Look
}) {
  return (
    <a
      href={to}
      onClick={linkClick(to, onNavigate)}
      aria-current={active ? 'page' : undefined}
      {...menu}
      className={cx(
        'flex select-none items-center gap-2.5 text-ink [-webkit-touch-callout:none]',
        kind === 'side' ? 'min-h-[52px] rounded-menu px-2 py-1.5' : 'min-h-[60px] gap-3 px-4 py-1.5 active:bg-control/60',
        kind === 'side' && (active ? 'bg-control' : 'hover:bg-control/60'),
      )}
    >
      <span className="relative flex flex-none items-center justify-center">{avatar}</span>
      <span className="flex min-w-0 flex-1 flex-col leading-[1.45]">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className={cx('min-w-0 truncate', kind === 'side' ? 'text-ui' : 'text-name')}>{name}</span>
          {crown && <Icon icon={Crown} size={13} strokeWidth={2} label="主管" className="flex-none text-ink-3" />}
          {tag && <span className="flex-none truncate rounded-[6px] border border-line px-1.5 text-[11px] leading-[17px] text-ink-2 max-w-[96px]">{tag}</span>}
          {time && <span className="ml-auto flex-none pl-1 text-meta text-ink-3">{time}</span>}
        </span>
        {(line || unread) && (
          <span className="flex min-w-0 items-center gap-2">
            <span className={cx('min-w-0 flex-1 truncate', kind === 'side' ? 'text-[12.5px]' : 'text-sub', warn ? 'text-warning-ink' : 'text-ink-2')}>{line}</span>
            {unread && <span aria-label="有新訊息" role="img" className="size-2 flex-none rounded-full bg-accent" />}
          </span>
        )}
      </span>
    </a>
  )
}

export function BotListRow({ bot, last, path, onNavigate, menu, crown, look: kind, ring }: { bot: Bot; last?: LastThread; path?: string; onNavigate?: () => void; menu?: MenuBind; crown?: boolean; look: Look; ring: string }) {
  const to = `/bots/${bot.id}`
  const active = !!path && (path === to || path.startsWith(to + '/'))
  const line = botLine(useStore(botsStore).activity[bot.id]?.state, last)
  return (
    <Row
      to={to}
      active={active}
      onNavigate={onNavigate}
      menu={menu}
      avatar={<Avatar {...look(bot)} state={line.warn ? 'notifying' : line.working ? 'working' : 'idle'} dot={line.warn ? 'waiting' : 'none'} ring={active ? '#2F2F2F' : ring} size={kind === 'side' ? 36 : 40} />}
      name={bot.name}
      tag={bot.title || undefined}
      crown={crown}
      line={line.text || (kind === 'phone' ? '還沒聊過' : '')}
      warn={line.warn}
      unread={(last?.unread ?? 0) > 0}
      time={kind === 'phone' ? rowTime(last?.at ?? 0) : undefined}
      look={kind}
    />
  )
}

function RoomFace({ room, size, ring }: { room: Room; size: number; ring: string }) {
  const [a, b] = room.members
  const small = Math.round(size * 0.62)
  return (
    <span aria-hidden="true" className="relative block flex-none" style={{ width: size, height: size }}>
      {a && (
        <span className="absolute left-0 top-0">
          <Avatar shape={a.shape} color={a.color} dot="none" size={small} blink={false} ring={ring} />
        </span>
      )}
      {b && (
        <span className="absolute bottom-0 right-0">
          <Avatar shape={b.shape} color={b.color} dot="none" size={small} blink={false} ring={ring} />
        </span>
      )}
    </span>
  )
}

export function RoomListRow({ room, path, onNavigate, menu, look: kind, ring }: { room: Room; path?: string; onNavigate?: () => void; menu?: MenuBind; look: Look; ring: string }) {
  const to = `/rooms/${room.id}`
  const working = room.members.find((m) => m.seat === 'working' || m.seat === 'queued')
  const waiting = room.members.some((m) => m.seat === 'waiting')
  const line = working ? `${working.name} 正在回覆…` : waiting ? '有成員在等你' : room.preview.replace(/\*\*|`/g, '') || `${room.members.length} 位成員`
  return (
    <Row
      to={to}
      active={path === to}
      onNavigate={onNavigate}
      menu={menu}
      avatar={<RoomFace room={room} size={kind === 'side' ? 36 : 40} ring={ring} />}
      name={room.name}
      line={line}
      warn={waiting}
      time={kind === 'phone' ? rowTime(room.last_message_at) : undefined}
      look={kind}
    />
  )
}
