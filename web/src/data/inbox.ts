import { inboxList, type InboxListOutput, type XBotEvent } from '../api/generated'
import { onEvent, onReconnect } from './events'
import { createStore } from './store'

export type InboxItem = InboxListOutput['items'][number]

export type InboxState = {
  /** 還沒處理的（xbot.inbox.list status=open）；null 表示還在讀。 */
  items: InboxItem[] | null
  /** 最近過期、還沒請它再問的提問（L14「請它再問一次」）。 */
  expired?: InboxItem[]
  /** 最近的決定：代理代為核准的紀錄卡（MCP-10，F08「最近的決定」、C16），一天內的。 */
  delegated?: InboxItem[]
  error?: string
}

/** 「最近的決定」顯示多久內的代為核准紀錄。 */
const RECENT_MS = 24 * 60 * 60_000

// 「待我處理」。
export const inboxStore = createStore<InboxState>({ items: null })

export function loadInbox(): Promise<void> {
  const open = inboxList({ status: 'open' })
  const expired = inboxList({ status: 'expired', limit: 10 }).catch(() => ({ items: [] as InboxItem[] }))
  const delegated = inboxList({ status: 'all', kind: 'delegated', limit: 10 }).catch(() => ({ items: [] as InboxItem[] }))
  return Promise.all([open, expired, delegated])
    .then(([o, x, d]) => {
      const since = Date.now() - RECENT_MS
      inboxStore.set({ items: o.items, expired: x.items.filter((i) => i.resolution !== 'asked_again'), delegated: d.items.filter((i) => i.kind === 'delegated' && i.created_at >= since) })
    })
    .catch((err: unknown) => inboxStore.set((s) => ({ ...s, error: err instanceof Error ? err.message : String(err) })))
}

// 卡片新增或被處理（inbox.created／inbox.updated，C12）、有核准進來、或任何和 inbox 有關的操作，就重讀。
function touchesInbox(e: XBotEvent): boolean {
  if (e.kind === 'inbox.created' || e.kind === 'inbox.updated') return true
  if (e.kind !== 'op') return false
  const p = (e.payload ?? {}) as { op?: string; status?: string; inbox_id?: string }
  return p.status === 'pending_approval' || !!p.inbox_id || (typeof p.op === 'string' && (p.op.startsWith('xbot.approvals.') || p.op.startsWith('xbot.inbox.')))
}

/** 這筆事件說某張卡已經處理掉了（回覆了、結掉了）：回傳那張卡的 id。 */
export function settledCard(e: XBotEvent): string | undefined {
  const p = (e.payload ?? {}) as { id?: string; status?: string; inbox_id?: string }
  if (e.kind === 'chat.answered') return p.inbox_id
  if (e.kind === 'inbox.updated' && p.id && p.status && p.status !== 'open') return p.id
  return undefined
}

let unsubs: Array<() => void> | null = null

export function startInbox() {
  if (unsubs) return
  void loadInbox()
  unsubs = [
    onEvent((e) => {
      // 已經處理掉的卡先拿掉，不等重讀（2026-10-21 審查：對話裡按了允許，卡片又在最下面出現、還能按）
      const gone = settledCard(e)
      if (gone) inboxStore.set((s) => (s.items?.some((i) => i.id === gone) ? { ...s, items: s.items.filter((i) => i.id !== gone) } : s))
      if (touchesInbox(e) || gone) void loadInbox()
    }),
    onReconnect(() => void loadInbox()),
  ]
}

export function resetInbox() {
  unsubs?.forEach((u) => u())
  unsubs = null
  inboxStore.set({ items: null })
}
