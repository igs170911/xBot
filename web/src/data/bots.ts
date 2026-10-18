import { botsList, type BotsListOutput, type XBotEvent } from '../api/generated'
import { onEvent, onReconnect } from './events'
import { createStore } from './store'

export type Bot = BotsListOutput['bots'][number]

/** Bot 目前在做什麼（從 task.state 事件來；重新整理後要等下一個事件才知道）。 */
export type BotActivity = { state: string; threadId?: string; taskId?: string }

export type BotsState = {
  /** null 表示還在讀。 */
  bots: Bot[] | null
  error?: string
  activity: Record<string, BotActivity>
}

export const botsStore = createStore<BotsState>({ bots: null, activity: {} })

let inflight: Promise<void> | null = null
let again = false

/** 重讀 Bot 清單；讀的途中又被叫，就在讀完後再讀一次（不會漏掉剛建立的 Bot）。 */
export function loadBots(): Promise<void> {
  if (inflight) {
    again = true
    return inflight
  }
  inflight = botsList({})
    .then((out) => botsStore.set((s) => ({ ...s, bots: out.bots, error: undefined })))
    .catch((err: unknown) => botsStore.set((s) => ({ ...s, error: err instanceof Error ? err.message : String(err) })))
    .finally(() => {
      inflight = null
      if (again) {
        again = false
        void loadBots()
      }
    })
  return inflight
}

/** 建立後先放進清單（不用等事件），之後的重讀會再對齊。 */
export function upsertBot(b: Bot) {
  botsStore.set((s) => {
    const list = s.bots ?? []
    const i = list.findIndex((x) => x.id === b.id)
    const next = i < 0 ? [...list, b] : list.map((x) => (x.id === b.id ? b : x))
    return { ...s, bots: next }
  })
}

export function botActivityFromEvent(prev: Record<string, BotActivity>, e: XBotEvent): Record<string, BotActivity> {
  if (e.kind !== 'task.state' || !e.bot_id) return prev
  const p = (e.payload ?? {}) as { state?: string }
  if (!p.state) return prev
  return { ...prev, [e.bot_id]: { state: p.state, threadId: e.thread_id ?? prev[e.bot_id]?.threadId, taskId: e.task_id } }
}

function isBotOp(e: XBotEvent): boolean {
  if (e.kind !== 'op') return false
  const p = (e.payload ?? {}) as { op?: string; status?: string }
  return typeof p.op === 'string' && p.op.startsWith('xbot.bots.') && p.status === 'ok'
}

let unsubs: Array<() => void> | null = null

/** 開始追蹤 Bot 清單：先讀一次，之後有人（網頁、MCP、指令列）改了 Bot 就重讀。 */
export function startBots() {
  if (unsubs) return
  void loadBots()
  unsubs = [
    onEvent((e) => {
      if (isBotOp(e)) void loadBots()
      if (e.kind === 'task.state') botsStore.set((s) => ({ ...s, activity: botActivityFromEvent(s.activity, e) }))
    }),
    onReconnect(() => void loadBots()),
  ]
}

/** 測試用：回到初始狀態。 */
export function resetBots() {
  unsubs?.forEach((u) => u())
  unsubs = null
  botsStore.set({ bots: null, activity: {} })
}
