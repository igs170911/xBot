import { agendaList, type AgendaListOutput, type XBotEvent } from '../api/generated'
import { onEvent, onReconnect } from './events'
import { createStore } from './store'

// 提案與討論（X03，TM-5、H04）：Bot 送來等你的事。
export type AgendaData = AgendaListOutput
export type AgendaItem = AgendaListOutput['pending'][number]

export type AgendaState = { data: AgendaData | null; error?: string }

export const agendaStore = createStore<AgendaState>({ data: null })

export function loadAgenda(): Promise<void> {
  return agendaList({ days: 7 })
    .then((data) => agendaStore.set({ data }))
    .catch((err: unknown) => agendaStore.set((s) => ({ ...s, error: err instanceof Error ? err.message : String(err) })))
}

/** 提案或討論有變（agenda.updated）、待我處理的卡有變、或回覆了一件。 */
export function isAgendaEvent(e: XBotEvent): boolean {
  if (e.kind === 'agenda.updated' || e.kind === 'inbox.created' || e.kind === 'inbox.updated') return true
  if (e.kind !== 'op') return false
  const p = (e.payload ?? {}) as { op?: string; status?: string }
  return typeof p.op === 'string' && (p.op.startsWith('xbot.agenda.') || p.op.startsWith('xbot.approvals.')) && p.status === 'ok'
}

let unsubs: Array<() => void> | null = null

export function startAgenda() {
  if (unsubs) return
  void loadAgenda()
  unsubs = [onEvent((e) => isAgendaEvent(e) && void loadAgenda()), onReconnect(() => void loadAgenda())]
}

export function resetAgenda() {
  unsubs?.forEach((u) => u())
  unsubs = null
  agendaStore.set({ data: null })
}

const DAY = 86_400_000

/** 提案還有幾天生效（不足一天算 1 天；已過是 0）。 */
export function daysLeft(dueAt: number, now = Date.now()): number {
  if (dueAt <= now) return 0
  return Math.max(1, Math.ceil((dueAt - now) / DAY))
}

const WEEK = '日一二三四五六'

/** 「今天下午 4:00」「明天上午 9:30」「10 月 20 日（二）下午 6:00」。 */
export function whenLabel(ms: number, now = Date.now()): string {
  const d = new Date(ms)
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const days = Math.round((startOf(d) - startOf(new Date(now))) / DAY)
  const day = days === 0 ? '今天' : days === 1 ? '明天' : days === -1 ? '昨天' : `${d.getMonth() + 1} 月 ${d.getDate()} 日（${WEEK[d.getDay()]}）`
  const h = d.getHours()
  const h12 = h % 12 === 0 ? 12 : h % 12
  return `${day}${h < 12 ? '上午' : '下午'} ${h12}:${String(d.getMinutes()).padStart(2, '0')}`
}

/** 處理過的結果怎麼說。 */
export function resultLabel(it: Pick<AgendaItem, 'state' | 'decided_at'>): string {
  switch (it.state) {
    case 'accepted':
      return '你同意了'
    case 'vetoed':
      return it.decided_at ? `你否決了 · ${new Date(it.decided_at + 14 * DAY).getMonth() + 1}/${new Date(it.decided_at + 14 * DAY).getDate()} 前不再提` : '你否決了'
    case 'auto':
      return '沒意見，已生效'
    case 'answered':
      return '已回覆'
    case 'declined':
      return '不用討論了'
    case 'cancelled':
      return '已取消'
    case 'held':
      return '時間到了，在對話裡等你'
  }
  return '等你'
}

/** 「改時間」的輸入（datetime-local 的 2026-10-20T15:00）換成服務要的「2026-10-20 15:00」。 */
export function whenInput(local: string): string {
  return local.replace('T', ' ').slice(0, 16)
}

/** datetime-local 的預設值：原本的時間。 */
export function localInput(ms: number): string {
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}
