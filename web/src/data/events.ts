import { subscribeEvents, type XBotEvent } from '../api/generated'
import { createStore } from './store'

// 全站只開一條 SSE（TECH-DESIGN §6）；各個資料來源用 onEvent 訂閱。
// 斷線重連時 subscribeEvents 會從最後收到的事件之後補送；即時的文字增量不會補，
// 所以重連後通知 onReconnect，讓畫面重讀一次資料。

export type Connection = 'connecting' | 'open' | 'reconnecting'

export const connection = createStore<Connection>('connecting')

const listeners = new Set<(e: XBotEvent) => void>()
const reconnectListeners = new Set<() => void>()
let stop: (() => void) | null = null

export function onEvent(fn: (e: XBotEvent) => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function onReconnect(fn: () => void): () => void {
  reconnectListeners.add(fn)
  return () => reconnectListeners.delete(fn)
}

/** 把一筆事件交給所有訂閱者（測試也用它模擬事件）。 */
export function dispatchEvent(e: XBotEvent) {
  listeners.forEach((fn) => fn(e))
}

export function startEvents(): () => void {
  if (stop) return stop
  if (typeof EventSource === 'undefined') return () => {}
  let dropped = false
  const s = subscribeEvents(dispatchEvent, {
    onStatus: (st) => {
      if (st === 'reconnecting') dropped = true
      if (st === 'open' && dropped) {
        dropped = false
        reconnectListeners.forEach((fn) => fn())
      }
      connection.set(st)
    },
  })
  stop = () => {
    s()
    stop = null
  }
  return stop
}
