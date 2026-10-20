import { useCallback, useEffect, useRef, useState } from 'react'
import { CheckCheck, CircleAlert, Clock } from 'lucide-react'
import { ApiError, approvalsDecide, chatAskAgain, chatRespond, inboxCounts, inboxList, inboxMarkRead, type InboxCountsOutput } from '../api/generated'
import { botsStore, type Bot } from '../data/bots'
import { onEvent, onReconnect } from '../data/events'
import { loadInbox, type InboxItem } from '../data/inbox'
import { isOpApproval } from '../chat/ask'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { PhoneBar } from './Phone'
import { rowTime } from './format'

// L14 通知中心（/notifications）：篩選全部／要你決定／完成／提到你／出錯；核准就地按、接手與查看一鍵前往，
// 過期的提問可以請它再問。資料來自 xbot.inbox.list（status=all）與 xbot.inbox.counts。

type Group = 'all' | 'decide' | 'done' | 'mention' | 'error'
const FILTERS: Array<{ id: Group; label: string }> = [
  { id: 'all', label: '全部' },
  { id: 'decide', label: '要你決定' },
  { id: 'done', label: '完成' },
  { id: 'mention', label: '提到你' },
  { id: 'error', label: '出錯' },
]
const GROUP_LABEL: Record<string, { text: string; cls: string }> = {
  decide: { text: '要你決定', cls: 'text-warning-ink' },
  done: { text: '完成', cls: 'text-success' },
  mention: { text: '提到你', cls: 'text-accent-ink' },
  error: { text: '出錯', cls: 'text-danger' },
}

function errText(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}

function label(it: InboxItem): { text: string; cls: string } {
  if (it.kind === 'question') return { text: '提問', cls: 'text-ink-2' }
  return GROUP_LABEL[it.group] ?? { text: '', cls: 'text-ink-2' }
}

/** 點了要去哪裡：對話串、群組、電腦、待我處理。 */
function targetOf(it: InboxItem): string {
  if (it.kind === 'takeover' && it.bot_id) return `/bots/${it.bot_id}/computer`
  const p = (it.payload ?? {}) as { room_id?: string }
  if (p.room_id) return `/rooms/${p.room_id}`
  if (it.bot_id && it.thread_id) return `/bots/${it.bot_id}/threads/${it.thread_id}`
  if (it.bot_id) return `/bots/${it.bot_id}`
  return '/'
}

function Actions({ it, onChanged }: { it: InboxItem; onChanged: () => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState<string | null>(null)
  const run = async (key: string, fn: () => Promise<unknown>, done: string) => {
    setBusy(key)
    try {
      await fn()
      toast.show({ kind: 'done', text: done })
      onChanged()
      void loadInbox()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(null)
    }
  }
  const open = it.status === 'open'
  if (it.status === 'expired' && it.kind === 'question' && it.resolution !== 'asked_again')
    return (
      <div className="flex flex-col gap-2 rounded-card bg-card p-3.5">
        <p className="m-0 text-ui text-ink-2">{it.body || it.title}</p>
        <p className="m-0 flex items-center gap-1.5 text-sub text-warning-ink">
          <Icon icon={CircleAlert} size={16} />
          10 分鐘沒回，先跳過了
        </p>
        <div className="flex items-center gap-2">
          <span className="inline-flex flex-1 items-center gap-1.5 text-sub text-ink-3">
            <Icon icon={Clock} size={16} />
            已過期
          </span>
          <Button className="min-h-11" loading={busy === 'again'} onClick={() => void run('again', () => chatAskAgain({ inbox_id: it.id }), '已請它再問一次')}>
            請它再問一次
          </Button>
        </div>
      </div>
    )
  if (!open) return null
  if (it.kind === 'approval') {
    const decide = (d: 'allow' | 'deny') =>
      it.task_id && !isOpApproval(it) ? chatRespond({ inbox_id: it.id, choice: d === 'allow' ? 'once' : 'deny' }) : approvalsDecide({ approval_id: it.ref ?? '', decision: d })
    return (
      <div className="flex gap-2">
        <Button variant="primary" className="min-h-11 px-5" loading={busy === 'allow'} disabled={!!busy} onClick={() => void run('allow', () => decide('allow'), '已允許這次')}>
          允許這次
        </Button>
        <Button className="min-h-11 px-5" loading={busy === 'deny'} disabled={!!busy} onClick={() => void run('deny', () => decide('deny'), '已退回')}>
          退回
        </Button>
      </div>
    )
  }
  return null
}

/** L07 從通知打開的核定 sheet：內容、只有你能決定、允許這次／退回為全寬按鈕。 */
function ApprovalSheet({ it, bot, open, onClose, onChanged }: { it: InboxItem; bot?: Bot; open: boolean; onClose: () => void; onChanged: () => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState<'allow' | 'deny' | null>(null)
  const p = (it.payload ?? {}) as { description?: string; command?: string; thread_title?: string; op?: string }
  const decide = async (d: 'allow' | 'deny') => {
    setBusy(d)
    try {
      if (it.task_id && !isOpApproval(it)) await chatRespond({ inbox_id: it.id, choice: d === 'allow' ? 'once' : 'deny' })
      else await approvalsDecide({ approval_id: it.ref ?? '', decision: d })
      toast.show({ kind: 'done', text: d === 'allow' ? '已允許這次 · 以你的名義送回' : '已退回' })
      onClose()
      onChanged()
      void loadInbox()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(null)
    }
  }
  const rows: Array<[string, string]> = [
    ['要做什麼', p.description || it.body || it.title],
    ...(p.command ? ([['指令', p.command]] as Array<[string, string]>) : []),
    ...(p.op ? ([['操作', p.op]] as Array<[string, string]>) : []),
    ...(p.thread_title ? ([['在哪個對話', p.thread_title]] as Array<[string, string]>) : []),
  ]
  return (
    <Dialog open={open} onClose={onClose} title={it.title} description={`${bot?.name ?? 'Bot'} · 只有你能決定；隊友或主管說可以不算數`}>
      <div className="flex flex-col overflow-hidden rounded-card bg-card">
        {rows.map(([k, v]) => (
          <div key={k} className="flex gap-3 border-line px-4 py-3 not-first:border-t">
            <span className="w-[72px] flex-none text-sub text-ink-2">{k}</span>
            <span className="min-w-0 flex-1 whitespace-pre-wrap break-words text-ui">{v}</span>
          </div>
        ))}
      </div>
      <div className="flex flex-col gap-2 pt-2">
        <Button variant="primary" className="min-h-12 w-full" loading={busy === 'allow'} disabled={!!busy} onClick={() => void decide('allow')}>
          允許這次
        </Button>
        <Button className="min-h-12 w-full" loading={busy === 'deny'} disabled={!!busy} onClick={() => void decide('deny')}>
          退回
        </Button>
      </div>
    </Dialog>
  )
}

function aside(it: InboxItem): string | null {
  if (it.status !== 'open' && !(it.kind === 'mention' || it.group === 'error' || it.group === 'done')) return null
  if (it.kind === 'approval' || (it.status === 'expired' && it.kind === 'question')) return null
  if (it.kind === 'takeover' || it.kind === 'signin') return '接手'
  if (it.group === 'error') return '查看'
  return '打開'
}

function Row({ it, bot, onChanged }: { it: InboxItem; bot?: Bot; onChanged: () => void }) {
  const l = label(it)
  const go = aside(it)
  const waiting = it.group === 'decide' && it.status === 'open'
  const error = it.group === 'error'
  const sub = (it.payload as { thread_title?: string; room_name?: string } | null)?.room_name ?? (it.kind === 'approval' && it.body ? it.body : '')
  const [sheet, setSheet] = useState(false)
  const canSheet = it.kind === 'approval' && it.status === 'open'
  return (
    <article data-testid="notification" data-group={it.group} className="flex gap-3 border-b border-divider px-4 py-3.5">
      <span className="pt-0.5">
        <Avatar shape={bot?.shape} color={bot?.color} state={waiting ? 'notifying' : error ? 'alerting' : 'idle'} dot={waiting ? 'waiting' : error ? 'error' : 'none'} size={36} ring="#070707" />
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex items-baseline gap-2">
          <span className="text-name">{bot?.name ?? 'XBot'}</span>
          <span className={cx('text-meta', l.cls)}>{l.text}</span>
          <span className="ml-auto flex-none text-meta text-ink-3">{rowTime(it.created_at)}</span>
          {!it.read && <span role="img" aria-label="未讀" className="size-2 flex-none self-center rounded-full bg-accent" />}
        </div>
        <div className="flex items-center gap-3">
          <div className="flex min-w-0 flex-1 flex-col">
            {canSheet ? (
              <button type="button" onClick={() => setSheet(true)} className="min-h-11 text-left text-ui text-ink underline-offset-4 active:underline">
                {it.title}
              </button>
            ) : (
              <span className="text-ui text-ink">{it.title}</span>
            )}
            {sub && <span className="truncate text-sub text-ink-2">{sub}</span>}
          </div>
          {go && (
            <Button className="min-h-11 flex-none rounded-full px-5" onClick={() => navigate(targetOf(it))}>
              {go}
            </Button>
          )}
        </div>
        <Actions it={it} onChanged={onChanged} />
      </div>
      {canSheet && <ApprovalSheet it={it} bot={bot} open={sheet} onClose={() => setSheet(false)} onChanged={onChanged} />}
    </article>
  )
}

export function NotificationsPage() {
  const bots = useStore(botsStore).bots
  const [group, setGroup] = useState<Group>('all')
  const [items, setItems] = useState<InboxItem[] | null>(null)
  const [counts, setCounts] = useState<InboxCountsOutput | null>(null)
  const [error, setError] = useState<string | null>(null)
  const latest = useRef(0)
  const load = useCallback(() => {
    // 只用最後一次的回應：快速切分頁時，先送的晚回來不能蓋掉現在這個分頁的清單（2026-10-21 審查）
    const n = ++latest.current
    inboxList({ status: 'all', group: group === 'all' ? undefined : group, limit: 50 })
      .then((o) => {
        if (n !== latest.current) return
        setItems(o.items.filter((i) => i.kind !== 'delegated'))
        setError(null)
      })
      .catch((e: unknown) => n === latest.current && setError(errText(e)))
    inboxCounts({})
      .then(setCounts)
      .catch(() => {})
  }, [group])
  useEffect(() => {
    load()
    const offs = [
      onEvent((e) => {
        if (e.kind === 'inbox.created' || e.kind === 'inbox.updated') load()
      }),
      onReconnect(load),
    ]
    return () => offs.forEach((o) => o())
  }, [load])
  const readAll = async () => {
    await inboxMarkRead({ all: true }).catch(() => {})
    load()
  }
  const count = (g: Group) => (g === 'all' ? null : (counts?.groups[g]?.unread ?? 0) || null)
  const bot = (id?: string) => bots?.find((b) => b.id === id)
  const today = new Date().setHours(0, 0, 0, 0)
  const todayItems = (items ?? []).filter((i) => i.created_at >= today)
  const older = (items ?? []).filter((i) => i.created_at < today)

  return (
    <main className="flex min-h-dvh flex-1 flex-col bg-app" aria-label="通知">
      <PhoneBar
        title="通知"
        back="/"
        right={
          <button type="button" onClick={() => void readAll()} className="flex h-11 items-center gap-1.5 rounded-full px-2 text-ui text-accent-ink active:bg-control">
            <Icon icon={CheckCheck} size={18} />
            全部標為已讀
          </button>
        }
      />
      <div role="tablist" aria-label="篩選" className="flex flex-none gap-2 overflow-x-auto px-3 pb-2 [scrollbar-width:none]">
        {FILTERS.map((f) => {
          const on = f.id === group
          const n = count(f.id)
          return (
            <button
              key={f.id}
              type="button"
              role="tab"
              aria-selected={on}
              onClick={() => setGroup(f.id)}
              className={cx('flex h-11 flex-none items-center gap-1.5 rounded-full border px-4 text-ui', on ? 'border-ink bg-ink text-on-ink' : 'border-divider bg-transparent text-ink')}
            >
              {f.label}
              {n !== null && <span className={on ? 'text-on-ink/70' : 'text-ink-3'}>{n}</span>}
            </button>
          )
        })}
      </div>
      {error && <p className="m-0 px-4 py-2 text-sub text-danger">{error}</p>}
      {items && items.length === 0 && <p className="m-0 px-4 py-10 text-center text-sub text-ink-2">這裡沒有通知</p>}
      {todayItems.length > 0 && <h2 className="m-0 px-4 pb-1 pt-2 text-sub text-ink-2">今天</h2>}
      {todayItems.map((it) => (
        <Row key={it.id} it={it} bot={bot(it.bot_id)} onChanged={load} />
      ))}
      {older.length > 0 && <h2 className="m-0 px-4 pb-1 pt-3 text-sub text-ink-2">更早</h2>}
      {older.map((it) => (
        <Row key={it.id} it={it} bot={bot(it.bot_id)} onChanged={load} />
      ))}
      <div className="h-[env(safe-area-inset-bottom)]" />
    </main>
  )
}
