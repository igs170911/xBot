import type { ReactNode } from 'react'
import { botsStore } from '../data/bots'
import { roomsStore } from '../data/rooms'
import { useStore } from '../data/store'
import { Avatar } from '../ui/Avatar'
import { cx } from '../ui/cx'

// @ 提到選單（L20）：輸入框打「@」叫出，分 Bot／群組兩區（應用程式等 C34 接上再加），點一列把 @名字 填進輸入框。

const SEAT: Record<string, string> = { waiting_input: '等你回覆', waiting_approval: '等你允許', queued: '排隊中', waiting_box: '開機中' }

function Row({ onPick, avatar, label, sub, warn }: { onPick: () => void; avatar: ReactNode; label: string; sub: string; warn?: boolean }) {
  return (
    <button
      type="button"
      role="option"
      aria-selected="false"
      onMouseDown={(e) => e.preventDefault()}
      onClick={onPick}
      className="flex min-h-11 w-full items-center gap-3 rounded-seg px-2.5 text-left hover:bg-hover focus-visible:bg-hover"
    >
      <span className="flex size-8 flex-none items-center justify-center">{avatar}</span>
      <span className="flex-none text-ui text-ink">@{label}</span>
      <span className={cx('min-w-0 truncate text-meta', warn ? 'text-warning-ink' : 'text-ink-3')}>{sub}</span>
    </button>
  )
}

export function MentionMenu({ query, exclude, onPick }: { query: string; exclude?: string; onPick: (name: string) => void }) {
  const { bots, activity } = useStore(botsStore)
  const rooms = useStore(roomsStore).rooms
  const q = query.toLowerCase()
  const list = (bots ?? []).filter((b) => !b.archived && b.name !== exclude && b.name.toLowerCase().includes(q))
  const groups = (rooms ?? []).filter((r) => r.name.toLowerCase().includes(q))
  if (!list.length && !groups.length) return null
  return (
    <div role="listbox" aria-label="提到誰" className="absolute inset-x-0 bottom-full z-20 mb-2 flex max-h-[min(420px,55dvh)] flex-col overflow-y-auto rounded-menu border border-divider bg-panel p-1.5 shadow-pop">
      {list.length > 0 && <span className="px-2.5 pb-1 pt-1 text-meta text-ink-3">Bot</span>}
      {list.map((b) => {
        const st = activity[b.id]?.state
        const seat = st && st !== 'done' ? (SEAT[st] ?? '工作中') : '待命'
        const warn = st === 'waiting_input' || st === 'waiting_approval'
        return (
          <Row
            key={b.id}
            onPick={() => onPick(b.name)}
            avatar={<Avatar shape={b.shape} color={b.color} state={warn ? 'notifying' : 'idle'} dot={warn ? 'waiting' : 'none'} size={28} blink={false} ring="#111111" />}
            label={b.name}
            sub={[b.title, seat].filter(Boolean).join(' · ')}
            warn={warn}
          />
        )
      })}
      {groups.length > 0 && <span className="px-2.5 pb-1 pt-2 text-meta text-ink-3">群組</span>}
      {groups.map((r) => (
        <Row
          key={r.id}
          onPick={() => onPick(r.name)}
          avatar={
            <span className="relative size-8">
              {r.members.slice(0, 2).map((m, i) => (
                <span key={m.bot_id} className="absolute" style={{ left: i * 10, top: i * 8 }}>
                  <Avatar shape={m.shape} color={m.color} dot="none" size={20} blink={false} ring="#111111" />
                </span>
              ))}
            </span>
          }
          label={r.name}
          sub={`${r.members.length} 個 Bot`}
        />
      ))}
    </div>
  )
}
