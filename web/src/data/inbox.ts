import { inboxList, type InboxListOutput, type XBotEvent } from '../api/generated'
import { onEvent, onReconnect } from './events'
import { createStore } from './store'

export type InboxItem = InboxListOutput['items'][number]

export type InboxState = {
  /** 還沒處理的（xbot.inbox.list status=open）；null 表示還在讀。 */
  items: InboxItem[] | null
  /** 最近過期、還沒請它再問的提問（L14「請它再問一次」）。 */
  expired?: InboxItem[]
  error?: string
}

// 「待我處理」。
export const inboxStore = createStore<InboxState>({ items: null })

export function loadInbox(): Promise<void> {
  const open = inboxList({ status: 'open' })
  const expired = inboxList({ status: 'expired', limit: 10 }).catch(() => ({ items: [] as InboxItem[] }))
  return Promise.all([open, expired])
    .then(([o, x]) => inboxStore.set({ items: o.items, expired: x.items.filter((i) => i.resolution !== 'asked_again') }))
    .catch((err: unknown) => inboxStore.set((s) => ({ ...s, error: err instanceof Error ? err.message : String(err) })))
}

// 卡片新增或被處理（inbox.created／inbox.updated，C12）、有核准進來、或任何和 inbox 有關的操作，就重讀。
function touchesInbox(e: XBotEvent): boolean {
  if (e.kind === 'inbox.created' || e.kind === 'inbox.updated') return true
  if (e.kind !== 'op') return false
  const p = (e.payload ?? {}) as { op?: string; status?: string; inbox_id?: string }
  return p.status === 'pending_approval' || !!p.inbox_id || (typeof p.op === 'string' && (p.op.startsWith('xbot.approvals.') || p.op.startsWith('xbot.inbox.')))
}

let unsubs: Array<() => void> | null = null

export function startInbox() {
  if (unsubs) return
  void loadInbox()
  unsubs = [
    onEvent((e) => {
      if (touchesInbox(e)) void loadInbox()
    }),
    onReconnect(() => void loadInbox()),
  ]
}

export function resetInbox() {
  unsubs?.forEach((u) => u())
  unsubs = null
  inboxStore.set({ items: null })
}
