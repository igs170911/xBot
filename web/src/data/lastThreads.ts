import { useEffect } from 'react'
import { chatListThreads } from '../api/generated'
import type { Bot } from './bots'
import { onEvent } from './events'
import { createStore, useStore } from './store'

// 每個 Bot 最近的對話：時間、未讀、最後一句話（側欄、手機的對話分頁共用；2026-10-20 側欄改版）。
// 有新訊息、工作狀態變了，或你看過了（chat.mark_read），就重讀那個 Bot。

export type LastThread = { at: number; unread: number; title: string; preview: string }

export const lastThreadsStore = createStore<Record<string, LastThread>>({})

/** 每個 Bot 看最近幾條對話串（算未讀、找最新的那條）。 */
const RECENT = 5

export function loadLastThread(botId: string): Promise<void> {
  return chatListThreads({ bot_id: botId, limit: RECENT, preview: true })
    .then((o) => {
      const latest = o.threads.reduce((m, t) => Math.max(m, t.last_message_at), 0)
      const unread = o.threads.reduce((n, t) => n + t.unread, 0)
      const top = o.threads.find((t) => t.last_message_at === latest)
      lastThreadsStore.set((s) => ({ ...s, [botId]: { at: latest, unread, title: top?.title ?? '', preview: top?.preview ?? '' } }))
    })
    .catch(() => {})
}

let watchers = 0
let stopWatching: (() => void) | null = null

/** 讀這些 Bot 最近的對話，之後有事件就更新；回傳目前知道的。 */
export function useLastThreads(bots: Bot[] | null): Record<string, LastThread> {
  const key = (bots ?? [])
    .filter((b) => !b.archived)
    .map((b) => b.id)
    .join(',')
  useEffect(() => {
    key
      .split(',')
      .filter(Boolean)
      .forEach((id) => void loadLastThread(id))
  }, [key])
  useEffect(() => {
    if (watchers++ === 0) {
      stopWatching = onEvent((e) => {
        if (e.bot_id && (e.kind === 'task.state' || e.kind === 'message.created' || e.kind === 'inbox.created')) void loadLastThread(e.bot_id)
      })
    }
    return () => {
      if (--watchers === 0) {
        stopWatching?.()
        stopWatching = null
      }
    }
  }, [])
  return useStore(lastThreadsStore)
}

/** 清單上那一行：有事時講狀態（等你、工作中），沒事時是最後一句話。 */
export function botLine(state: string | undefined, last?: LastThread): { text: string; warn: boolean; working: boolean } {
  const st = state ?? 'done'
  if (st === 'waiting_input') return { text: '等你回覆' + (last?.title ? '：' + last.title : ''), warn: true, working: false }
  if (st === 'waiting_approval') return { text: '等你允許' + (last?.title ? '：' + last.title : ''), warn: true, working: false }
  if (st === 'waiting_box') return { text: '電腦開機中…', warn: false, working: true }
  if (st === 'queued') return { text: '排隊中', warn: false, working: true }
  if (st !== 'done') return { text: '工作中…', warn: false, working: true }
  return { text: last?.preview || last?.title || '', warn: false, working: false }
}
