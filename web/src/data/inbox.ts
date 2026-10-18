import { inboxList, type InboxListOutput, type XBotEvent } from '../api/generated'
import { onEvent, onReconnect } from './events'
import { createStore } from './store'

export type InboxItem = InboxListOutput['items'][number]

export type InboxState = { items: InboxItem[] | null; error?: string }

// 「待我處理」：還沒處理的項目（xbot.inbox.list status=open）。
export const inboxStore = createStore<InboxState>({ items: null })

export function loadInbox(): Promise<void> {
  return inboxList({ status: 'open' })
    .then((out) => inboxStore.set({ items: out.items }))
    .catch((err: unknown) => inboxStore.set((s) => ({ ...s, error: err instanceof Error ? err.message : String(err) })))
}

// 有核准進來、被決定、或任何和 inbox 有關的操作，就重讀。
function touchesInbox(e: XBotEvent): boolean {
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
