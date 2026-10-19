import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { ArrowUp, AtSign, Check, ChevronDown, ChevronLeft, CirclePause, Ellipsis, Phone, CirclePlay, Info, OctagonPause, Pin, TriangleAlert, Users } from 'lucide-react'
import { ApiError, collabRoomRead, collabStopRoom, roomsPauseMember, roomsPost, roomsUpdate, type CollabRoomReadOutput } from '../api/generated'
import { onEvent, onReconnect } from '../data/events'
import { loadRooms } from '../data/rooms'
import { linkClick } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { cx } from '../ui/cx'
import { Dialog } from '../ui/Dialog'
import { MenuButton } from '../ui/Menu'
import { goBack } from '../router'
import { usePhone } from '../mobile/nav'
import { Markdown } from '../chat/markdown/Markdown'
import { clockLabel } from '../chat/format'
import { ROOM_ROUNDS as ROUNDS, RULES, type Rule } from './rules'

// 群組對話（design/D01；空白時 D04）：你和 2–10 個 Bot。@ 指定負責的 Bot；Bot 之間交棒每次最多 4 輪。



type Read = CollabRoomReadOutput
type RoomMsg = Read['messages'][number]
type Member = Read['room']['members'][number]

const SEAT: Record<string, string> = { idle: '待命', queued: '排隊中', working: '正在回覆…', waiting: '在自己的對話等你' }

function GroupAvatar({ members, size = 34 }: { members: Member[]; size?: number }) {
  const shown = members.slice(0, 3)
  const s = Math.round(size * 0.56)
  return (
    <span aria-hidden="true" className="relative flex-none" style={{ width: size, height: size }}>
      {shown.map((m, i) => (
        <span key={m.bot_id} className="absolute" style={{ left: i === 1 ? size - s : i === 2 ? (size - s) / 2 : 0, top: i === 2 ? size - s : 0 }}>
          <Avatar shape={m.shape} color={m.color} dot="none" size={s} blink={false} />
        </span>
      ))}
    </span>
  )
}

function statusLine(members: Member[]): { text: string; tone: 'working' | 'waiting' | 'idle' } {
  const working = members.filter((m) => m.seat === 'working')
  const waiting = members.filter((m) => m.seat === 'waiting')
  const queued = members.filter((m) => m.seat === 'queued')
  if (working.length) return { text: `${working.map((m) => m.name).join('、')} 正在回覆…`, tone: 'working' }
  if (waiting.length) return { text: `${waiting.map((m) => m.name).join('、')} 在自己的對話等你`, tone: 'waiting' }
  if (queued.length) return { text: `${queued.map((m) => m.name).join('、')} 排隊中`, tone: 'working' }
  return { text: '全部待命', tone: 'idle' }
}

function names(ids: string[] | undefined, members: Member[]): string {
  return (ids ?? []).map((id) => members.find((m) => m.bot_id === id)?.name ?? '已離開的成員').join('、')
}

function Divider({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center gap-3 text-meta text-ink-3" role="separator">
      <span className="h-px flex-1 bg-line" />
      <span>{children}</span>
      <span className="h-px flex-1 bg-line" />
    </div>
  )
}

function Timeline({ messages, members }: { messages: RoomMsg[]; members: Member[] }) {
  const out: ReactNode[] = []
  for (const m of messages) {
    if (m.sender === 'system') {
      out.push(
        <div key={m.id} data-testid="room-notice" className="flex items-center gap-2 self-center rounded-full bg-warning-soft px-3 py-1 text-meta text-warning-ink">
          <Icon icon={TriangleAlert} size={14} />
          {m.content}
        </div>,
      )
      continue
    }
    if (m.sender === 'owner') {
      out.push(
        <div key={m.id} className="flex flex-col items-end gap-1">
          <div data-testid="room-owner" className="max-w-[75%] whitespace-pre-wrap break-words rounded-bubble bg-bubble-user px-4 py-2.5 text-[15px] leading-[1.6] text-ink">
            {m.content}
          </div>
          <span className="text-meta text-ink-3">{clockLabel(m.created_at)}</span>
        </div>,
      )
      const woke = names(m.picked, members)
      if (m.picked_by === 'lead' && woke) out.push(<Divider key={m.id + 'p'}>沒有點名 · 交給主責 {woke}</Divider>)
      else if (m.picked_by === 'judge' && woke) out.push(<Divider key={m.id + 'p'}>系統挑選：{woke}</Divider>)
      else if (m.picked_by === 'everyone' && woke) out.push(<Divider key={m.id + 'p'}>沒有點名 · 叫醒所有成員</Divider>)
      else if (woke) out.push(<Divider key={m.id + 'p'}>叫醒 {woke}</Divider>)
      if (m.paused?.length) out.push(<Divider key={m.id + 'z'}>{m.paused.join('、')} 已暫停，沒有叫醒</Divider>)
      continue
    }
    const who = members.find((x) => 'bot:' + x.bot_id === m.sender)
    out.push(
      <div key={m.id} data-testid="room-bot" className="flex gap-2.5">
        {who ? <Avatar shape={who.shape} color={who.color} dot="none" size={30} /> : <span className="size-[30px] flex-none rounded-full bg-control" />}
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="flex flex-wrap items-center gap-1.5 text-meta text-ink-2">
            <span className="text-ui-sm text-ink">{m.speaker_name}</span>
            {who?.title && <span className="text-ink-3">{who.title}</span>}
            {m.failed && <StatusChip size="sm" tone="danger">沒有完成</StatusChip>}
            <span className="text-ink-3">{clockLabel(m.created_at)}</span>
          </span>
          <div className="min-w-0 self-start rounded-bubble bg-bubble-bot px-4 py-2.5 text-[15px] leading-[1.6]">
            <Markdown text={m.content} />
          </div>
        </div>
      </div>,
    )
    if (m.handoff) out.push(<Divider key={m.id + 'h'}>{`第 ${m.handoff}/${ROUNDS} 輪 · ${m.speaker_name} → ${names(m.mentions, members)}`}</Divider>)
  }
  return <>{out}</>
}

function LiveCard({ members }: { members: Member[] }) {
  const busy = members.filter((m) => m.seat !== 'idle')
  if (!busy.length) return null
  return (
    <section aria-label="成員狀態" className="flex flex-col gap-2 rounded-card border border-line px-4 py-3">
      {busy.map((m) => (
        <div key={m.bot_id} className="flex items-center gap-2 text-ui-sm">
          <Avatar shape={m.shape} color={m.color} state={m.seat === 'working' ? 'working' : 'idle'} dot="none" size={20} />
          <span className={m.seat === 'waiting' ? 'text-warning-ink' : 'text-ink'}>
            {m.name} {SEAT[m.seat]}
          </span>
          {m.seat === 'waiting' && m.seat_thread_id && (
            <a href={`/bots/${m.bot_id}/threads/${m.seat_thread_id}`} onClick={linkClick(`/bots/${m.bot_id}/threads/${m.seat_thread_id}`)} className="ml-auto text-accent-ink">
              前往
            </a>
          )}
        </div>
      ))}
    </section>
  )
}

function Suggestions({ members, onPick }: { members: Member[]; onPick: (t: string) => void }) {
  const [a, b, c] = members
  const ideas = [
    `@${a?.name} 先列出這件事要做哪些步驟，再分給 @${b?.name}${c ? ` 和 @${c.name}` : ''}`,
    `@所有人 各自用一句話說你可以負責哪一塊`,
    `@${b?.name} 先查資料，查完交給 @${a?.name} 整理成報告`,
  ]
  return (
    <section aria-label="開場建議" className="mx-auto flex w-full max-w-[560px] flex-col items-center gap-4 py-6 text-center">
      <span className="flex gap-3">
        {members.slice(0, 5).map((m) => (
          <Avatar key={m.bot_id} shape={m.shape} color={m.color} state="happy" dot="none" size={52} />
        ))}
      </span>
      <h1 className="m-0 text-page">{members.map((m) => m.name).join('、')} 都在這裡</h1>
      <ul className="m-0 flex list-none flex-col gap-1 p-0 text-ui-sm text-ink-2">
        <li>用 @名字 指定誰負責</li>
        <li>沒有點名時，照回應規則決定誰回答</li>
        <li>你每發言一次，Bot 之間最多來回 {ROUNDS} 輪</li>
      </ul>
      <div className="flex w-full flex-col gap-2">
        {ideas.map((t, i) => (
          <button key={t} type="button" onClick={() => onPick(t)} className="flex items-start gap-3 rounded-group border border-line bg-transparent px-3.5 py-2.5 text-left text-ui-sm text-ink hover:bg-control/50">
            <span className="flex size-6 flex-none items-center justify-center rounded-full bg-control text-meta text-ink-2">{'ABC'[i]}</span>
            {t}
          </button>
        ))}
      </div>
    </section>
  )
}

function MemberPanel({ room, onChanged, inSheet }: { room: Read['room']; onChanged: () => void; inSheet?: boolean }) {
  const toggle = async (m: Member) => {
    await roomsPauseMember({ room_id: room.id, bot_id: m.bot_id, paused: !m.paused }).catch(() => undefined)
    onChanged()
  }
  return (
    <aside aria-label="成員" className={inSheet ? 'flex flex-col gap-4' : 'flex w-[300px] flex-none flex-col gap-4 overflow-y-auto border-l border-divider px-5 py-4 max-lg:hidden'}>
      <div className="flex flex-col gap-1">
        <span className="text-ui-sm text-ink">接力規則</span>
        <span className="text-meta text-ink-2">每次你發言後，Bot 之間最多交棒 {ROUNDS} 輪，避免互相丟來丟去。你的發言永遠優先。</span>
      </div>
      <div className="flex flex-col gap-2">
        <span className="text-ui-sm text-ink">成員 · {room.members.length}</span>
        {room.members.map((m) => (
          <div key={m.bot_id} className="flex items-center gap-2.5" data-testid="room-member">
            <Avatar shape={m.shape} color={m.color} state={m.seat === 'working' ? 'working' : m.paused ? 'sleepy' : 'idle'} dot={m.seat === 'waiting' ? 'waiting' : 'none'} size={30} />
            <span className="flex min-w-0 flex-1 flex-col leading-[1.35]">
              <span className="truncate text-ui-sm text-ink">
                {m.name}
                {m.lead && <span className="text-ink-3"> · 主責</span>}
              </span>
              <span className={cx('truncate text-meta', m.seat === 'waiting' ? 'text-warning-ink' : 'text-ink-3')}>{m.paused ? '已暫停' : SEAT[m.seat]}</span>
            </span>
            <IconButton icon={m.paused ? CirclePlay : CirclePause} label={m.paused ? `恢復 ${m.name}` : `先停 ${m.name}`} size="sm" onClick={() => void toggle(m)} />
          </div>
        ))}
      </div>
    </aside>
  )
}

function RoomComposer({ room, text, setText, onSend }: { room: Read['room']; text: string; setText: (t: string) => void; onSend: () => Promise<void> }) {
  const phone = usePhone()
  const input = useRef<HTMLTextAreaElement>(null)
  const [sending, setSending] = useState(false)
  const submit = async (e?: FormEvent) => {
    e?.preventDefault()
    if (!text.trim() || sending) return
    setSending(true)
    try {
      await onSend()
    } finally {
      setSending(false)
      input.current?.focus()
    }
  }
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      void submit()
    }
  }
  const mention = (name: string) => {
    setText(`${text}${text && !text.endsWith(' ') ? ' ' : ''}@${name} `)
    input.current?.focus()
  }
  const lead = room.members.find((m) => m.lead)?.name ?? room.members[0]?.name
  const hint =
    room.responder_rule === 'mentioned'
      ? '沒有點名時沒有人回 · Enter 送出'
      : room.responder_rule === 'everyone'
        ? '沒有點名時叫醒所有成員 · Enter 送出'
        : room.responder_rule === 'lead'
          ? `沒有點名時由 ${lead} 回答 · Enter 送出`
          : `沒有點名時自動挑一位（目前交給主責 ${lead}）· Enter 送出`
  return (
    <div className="flex flex-col gap-2">
      {phone ? (
        <div className="-mx-3 flex gap-2 overflow-x-auto px-3 pb-0.5 [scrollbar-width:none]" aria-label="點名">
          {room.members.map((m) => (
            <button key={m.bot_id} type="button" onClick={() => mention(m.name)} className="flex h-11 flex-none items-center gap-1.5 rounded-full border border-divider bg-panel pl-2.5 pr-3.5 text-ui text-ink active:bg-control">
              <Avatar shape={m.shape} color={m.color} state="idle" dot="none" size={22} blink={false} ring="#111111" />@{m.name}
            </button>
          ))}
          <button type="button" onClick={() => mention('所有人')} className="flex h-11 flex-none items-center gap-1.5 rounded-full border border-divider bg-panel pl-3 pr-3.5 text-ui text-ink active:bg-control">
            <Icon icon={Users} size={18} />
            @所有人
          </button>
        </div>
      ) : (
      <div className="flex flex-wrap gap-1.5" aria-label="點名">
        {room.members.map((m) => (
          <button key={m.bot_id} type="button" onClick={() => mention(m.name)} className="relative flex h-7 items-center gap-1 rounded-full bg-inset px-2.5 text-meta text-ink-2 touch-target hover:text-ink">
            <Icon icon={AtSign} size={12} />
            {m.name}
          </button>
        ))}
      </div>
      )}
      <form onSubmit={submit} className="flex min-h-[52px] items-center gap-0.5 rounded-[26px] bg-control py-[8px] pl-4 pr-[9px]">
        {/* label 撐滿輸入列的高度（含上下留白），點輸入列任何地方都會聚焦到輸入框（手機至少 44 高） */}
        <label className="-my-[8px] flex min-w-0 flex-1 items-center self-stretch py-[8px]">
          <span className="sr-only-text">傳訊息到群組</span>
          <textarea
            ref={input}
            rows={1}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={phone ? '傳到群組…' : `傳訊息到「${room.name}」，用 @ 指定負責的 Bot…`}
            className="field-sizing-content max-h-40 min-h-9 min-w-0 flex-1 resize-none bg-transparent px-1.5 py-[6px] text-[15px] leading-6 text-ink outline-none focus-visible:outline-none"
          />
        </label>
        <IconButton icon={ArrowUp} label="送出" variant="send" type="submit" disabled={!text.trim() || sending} />
      </form>
      {!phone && <span className="self-center text-center text-meta text-ink-3">{hint}</span>}
    </div>
  )
}

/** 手機的群組頁首（L05）：返回、通話｜成員頭像｜成員、更多；下面一顆「群組名 · 成員」膠囊。 */
function PhoneRoomHeader({ room, status, anyBusy, lead, onRule, onStop, onMembers }: { room: Read['room']; status: { text: string; tone: string }; anyBusy: boolean; lead: string; onRule: (r: Rule) => void; onStop: () => void; onMembers: () => void }) {
  const round = 'flex size-11 flex-none items-center justify-center rounded-full border border-divider bg-panel text-ink active:bg-control'
  const [a, b] = room.members
  return (
    <header className="flex flex-none flex-col items-center gap-2 px-3 pb-2 pt-[max(8px,env(safe-area-inset-top))]">
      <div className="flex w-full items-center gap-2">
        <button type="button" aria-label="返回" className={round} onClick={() => goBack('/chats')}>
          <Icon icon={ChevronLeft} size={22} />
        </button>
        <button type="button" aria-label="群組通話（還在做）" className={round} disabled>
          <Icon icon={Phone} size={20} />
        </button>
        <span className="relative flex flex-1 justify-center" aria-hidden="true">
          <span className="relative h-10 w-12">
            {a && (
              <span className="absolute left-0 top-1">
                <Avatar shape={a.shape} color={a.color} state={a.seat === 'working' ? 'working' : 'idle'} dot="none" size={26} ring="#070707" />
              </span>
            )}
            {b && (
              <span className="absolute left-[20px] top-1">
                <Avatar shape={b.shape} color={b.color} state={b.seat === 'working' ? 'working' : 'idle'} dot="none" size={26} ring="#070707" />
              </span>
            )}
            {room.members.length > 2 && <span className="absolute bottom-0 left-[22px] rounded-full bg-control px-1 text-[10.5px] leading-[15px] text-ink-2">+{room.members.length - 2}</span>}
          </span>
        </span>
        <button type="button" aria-label="成員" className={round} onClick={onMembers}>
          <Icon icon={Users} size={20} />
        </button>
        <MenuButton
          label="更多"
          align="end"
          items={[
            ...RULES.map((r) => ({ label: `沒點名時：${r.label(lead)}`, icon: r.value === room.responder_rule ? Check : undefined, onSelect: () => onRule(r.value) })),
            { label: '停止所有成員', icon: OctagonPause, separator: true, danger: true, disabled: !anyBusy, onSelect: onStop },
          ]}
          trigger={(p) => (
            <button {...p} type="button" aria-label="更多" className={round}>
              <Icon icon={Ellipsis} size={20} />
            </button>
          )}
        />
      </div>
      <button type="button" onClick={onMembers} className="flex h-11 max-w-full items-center gap-2 rounded-full border border-divider bg-panel px-4 text-ink active:bg-control">
        <span className="flex-none text-ui">{room.name}</span>
        <span data-testid="room-status" className={cx('min-w-0 truncate text-sub', status.tone === 'waiting' ? 'text-warning-ink' : 'text-ink-2')}>
          {status.tone === 'idle' ? room.members.map((m) => m.name).join('、') : status.text}
        </span>
        <Icon icon={ChevronDown} size={16} className="flex-none text-ink-2" />
      </button>
    </header>
  )
}

export function RoomPage({ roomId }: { roomId: string }) {
  const [data, setData] = useState<Read | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [text, setText] = useState('')
  const [notice, setNotice] = useState<string | null>(null)
  const [members, setMembers] = useState(false)
  const phone = usePhone()
  const scroller = useRef<HTMLDivElement>(null)
  const memberIds = useRef<string[]>([])
  useEffect(() => {
    memberIds.current = data?.room.members.map((m) => m.bot_id) ?? []
  }, [data])

  const load = useCallback(
    () =>
      collabRoomRead({ room: roomId, limit: 100 })
        .then((out) => {
          setData(out)
          setErr(null)
        })
        .catch((e: unknown) => setErr(e instanceof ApiError && e.code === 'not_found' ? '找不到這個群組' : e instanceof Error ? e.message : String(e))),
    [roomId],
  )
  useEffect(() => {
    void load()
    let timer: ReturnType<typeof setTimeout> | null = null
    const offs = [
      onEvent((e) => {
        const room = (e.payload as { room_id?: string; scope_id?: string } | undefined) ?? {}
        const mine = e.thread_id === roomId || room.room_id === roomId || room.scope_id === roomId
        const member = e.kind === 'task.state' && !!e.bot_id && memberIds.current.includes(e.bot_id)
        if (!(mine && e.kind.startsWith('room.')) && !member) return
        if (timer) return
        timer = setTimeout(() => {
          timer = null
          void load()
        }, 120)
      }),
      onReconnect(() => void load()),
    ]
    return () => {
      offs.forEach((o) => o())
      if (timer) clearTimeout(timer)
    }
  }, [roomId, load])

  const count = data?.messages.length ?? 0
  useEffect(() => {
    const el = scroller.current
    if (el) el.scrollTop = el.scrollHeight
  }, [count])

  if (err && !data)
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
        <Icon icon={Info} size={28} className="text-ink-3" />
        <p className="m-0 text-ui text-ink-2">{err}</p>
      </main>
    )
  if (!data) return <main className="flex-1" aria-busy="true" />

  const { room, messages } = data
  const st = statusLine(room.members)
  const anyBusy = room.members.some((m) => m.seat !== 'idle')
  const send = async () => {
    setNotice(null)
    try {
      const res = await roomsPost({ room_id: room.id, text: text.trim() })
      setText('')
      if (res.paused?.length) setNotice(`${res.paused.join('、')} 已暫停，沒有叫醒；要恢復的話到右邊成員清單按恢復。`)
      void load()
      void loadRooms()
    } catch (e) {
      setNotice(e instanceof Error ? e.message : String(e))
    }
  }
  const stopAll = async () => {
    await collabStopRoom({ room: room.id }).catch((e: unknown) => setNotice(e instanceof Error ? e.message : String(e)))
    void load()
  }
  const setRule = async (rule: Rule) => {
    await roomsUpdate({ room_id: room.id, responder_rule: rule }).catch((e: unknown) => setNotice(e instanceof Error ? e.message : String(e)))
    void load()
  }
  const lead = room.members.find((m) => m.lead)?.name ?? ''

  return (
    <div className="flex h-[var(--app-h,100dvh)] min-w-0 flex-1 max-md:fixed max-md:inset-x-0 max-md:top-[var(--app-top,0px)] max-md:z-10">
      <main className="flex min-w-0 flex-1 flex-col bg-app" aria-label={`群組：${room.name}`}>
        {messages.length > 0 && <h1 className="sr-only-text">群組：{room.name}</h1>}
        {phone && (
          <PhoneRoomHeader room={room} status={st} anyBusy={anyBusy} lead={lead} onRule={(r) => void setRule(r)} onStop={() => void stopAll()} onMembers={() => setMembers(true)} />
        )}
        {!phone && (
        <header>
        <div className="flex min-h-14 flex-none flex-wrap items-center gap-x-3 gap-y-1 py-2 pl-5 pr-3 max-md:pl-3">
          <GroupAvatar members={room.members} />
          <span className="flex min-w-0 flex-col leading-[1.3]">
            <span className="truncate text-[16px]">{room.name}</span>
            <span data-testid="room-status" className={cx('truncate text-[13px]', st.tone === 'waiting' ? 'text-warning-ink' : 'text-ink-3')}>
              {st.text}
            </span>
          </span>
          <span className="flex-1" />
          <label className="flex items-center gap-1.5 text-meta text-ink-3">
            <span className="max-sm:hidden">沒點名時</span>
            <select
              aria-label="回應規則"
              value={room.responder_rule}
              onChange={(e) => void setRule(e.target.value as Rule)}
              className="h-8 rounded-field border-0 bg-control px-2 text-ui-sm text-ink outline-none focus-visible:outline-2 focus-visible:outline-focus"
            >
              {RULES.map((r) => (
                <option key={r.value} value={r.value}>
                  {r.label(lead)}
                </option>
              ))}
            </select>
          </label>
          <Button size="sm" variant="danger" icon={OctagonPause} onClick={() => void stopAll()} disabled={!anyBusy}>
            停止所有成員
          </Button>
        </div>
        </header>
        )}
        {room.bulletin && (
          <div className="flex flex-none items-center gap-2 border-y border-divider px-5 py-2 text-meta text-ink-2">
            <Icon icon={Pin} size={14} />
            <span className="min-w-0 flex-1 truncate">群組指示：{room.bulletin}</span>
          </div>
        )}
        <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto">
          <div className={cx('mx-auto box-border flex min-h-full max-w-[828px] flex-col gap-3.5 px-6 pb-2 pt-3 max-sm:px-3', messages.length ? 'justify-end' : 'justify-center')} aria-live="polite">
            {messages.length === 0 ? <Suggestions members={room.members} onPick={setText} /> : <Timeline messages={messages} members={room.members} />}
            <LiveCard members={room.members} />
          </div>
        </div>
        <div className="flex-none px-6 pb-4 pt-2 max-sm:px-3 max-sm:pb-[max(12px,env(safe-area-inset-bottom))]">
          <div className="mx-auto flex max-w-[780px] flex-col gap-2">
            {notice && (
              <div role="alert" className="flex items-center gap-2 rounded-menu bg-card px-3.5 py-2 text-ui-sm text-warning-ink">
                <Icon icon={Users} size={16} />
                {notice}
              </div>
            )}
            <RoomComposer room={room} text={text} setText={setText} onSend={send} />
          </div>
        </div>
      </main>
      <MemberPanel room={room} onChanged={() => void load()} />
      <Dialog open={members} onClose={() => setMembers(false)} title={`成員 · ${room.members.length}`}>
        <MemberPanel inSheet room={room} onChanged={() => void load()} />
      </Dialog>
    </div>
  )
}
