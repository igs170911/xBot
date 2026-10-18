import { publicBase } from '../data/network'
import { routinesList, type RoutinesListOutput, type RoutinesLogsOutput, type RoutinesProposeOutput, type XBotEvent } from '../api/generated'
import { onEvent, onReconnect } from '../data/events'
import { createStore } from '../data/store'

export type Routine = RoutinesListOutput['routines'][number]
export type Run = RoutinesLogsOutput['runs'][number]
export type Proposal = RoutinesProposeOutput
export type Draft = Proposal['draft']

/** 「新增定時任務」對話框（H02）；text 是預先帶入的句子（行事曆點空格時）。 */
export const newRoutineDialog = createStore<{ open: boolean; text?: string }>({ open: false })
export const openNewRoutine = (text?: string) => newRoutineDialog.set({ open: true, text })

export type RoutinesState = { routines: Routine[] | null; error?: string }

// 定時任務清單（C32）。排程器在背景跑，沒有專屬事件：工作狀態變了、或有人改了定時任務就重讀，另外每 30 秒對一次。
export const routinesStore = createStore<RoutinesState>({ routines: null })

let inflight: Promise<void> | null = null

export function loadRoutines(): Promise<void> {
  if (inflight) return inflight
  inflight = routinesList({})
    .then((out) => routinesStore.set({ routines: out.routines }))
    .catch((err: unknown) => routinesStore.set((s) => ({ ...s, error: err instanceof Error ? err.message : String(err) })))
    .finally(() => {
      inflight = null
    })
  return inflight
}

function touches(e: XBotEvent): boolean {
  if (e.kind === 'task.state') return true
  if (e.kind !== 'op') return false
  const op = (e.payload as { op?: string } | undefined)?.op
  return typeof op === 'string' && op.startsWith('xbot.routines.')
}

let users = 0
let stop: (() => void) | null = null

/** 畫面打開時呼叫；回傳的函式在離開時呼叫。 */
export function startRoutinesFeed(): () => void {
  users++
  if (!stop) {
    void loadRoutines()
    const timer = window.setInterval(() => void loadRoutines(), 30_000)
    let pending: number | undefined
    const offs = [
      onEvent((e) => {
        if (!touches(e)) return
        window.clearTimeout(pending)
        pending = window.setTimeout(() => void loadRoutines(), 300)
      }),
      onReconnect(() => void loadRoutines()),
    ]
    stop = () => {
      window.clearInterval(timer)
      window.clearTimeout(pending)
      offs.forEach((f) => f())
      stop = null
    }
  }
  return () => {
    users--
    if (users === 0) stop?.()
  }
}

// ── 時間的寫法（不出現 AM/PM，H02）─────────────────────

const WD = ['日', '一', '二', '三', '四', '五', '六']

export function clockText(d: Date): string {
  const h = d.getHours()
  const m = String(d.getMinutes()).padStart(2, '0')
  if (h < 5) return `凌晨 ${h === 0 ? 12 : h}:${m}`
  if (h < 12) return `上午 ${h}:${m}`
  if (h === 12) return `中午 12:${m}`
  if (h < 18) return `下午 ${h - 12}:${m}`
  return `晚上 ${h - 12}:${m}`
}

export function dateText(d: Date): string {
  return `${d.getMonth() + 1} 月 ${d.getDate()} 日（${WD[d.getDay()]}）`
}

function dayDiff(d: Date, now: Date): number {
  const a = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  const b = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  return Math.round((a - b) / 86_400_000)
}

/** 「今天上午 9:00」「明天上午 9:00」「10 月 20 日（二）上午 9:00」。 */
export function whenText(ms: number, now = new Date()): string {
  const d = new Date(ms)
  const diff = dayDiff(d, now)
  const day = diff === 0 ? '今天' : diff === 1 ? '明天' : diff === -1 ? '昨天' : dateText(d) + ' '
  return day + clockText(d)
}

export function durationText(fromMs: number, toMs: number): string {
  const s = Math.max(0, Math.round((toMs - fromMs) / 1000))
  if (s < 60) return `${s} 秒`
  const m = Math.round(s / 60)
  if (m < 60) return `${m} 分鐘`
  return `${Math.floor(m / 60)} 小時 ${m % 60} 分`
}

export const TRIGGER_LABEL: Record<string, string> = { schedule: '排程', catchup: '補跑', manual: '手動', webhook: '網址觸發', test: '測試' }

export const RUN_STATUS: Record<string, { label: string; tone: 'success' | 'danger' | 'warning' | 'accent' | 'neutral' }> = {
  starting: { label: '開始中', tone: 'accent' },
  running: { label: '執行中', tone: 'accent' },
  done: { label: '已完成', tone: 'success' },
  failed: { label: '失敗', tone: 'danger' },
  cancelled: { label: '已停止', tone: 'neutral' },
  skipped: { label: '已略過', tone: 'neutral' },
  retrying: { label: '等重試', tone: 'warning' },
  test: { label: '測試', tone: 'neutral' },
  scheduled: { label: '排程中', tone: 'neutral' },
}

/** 定時任務的狀態膠囊（H03）：啟用中、第 N 輪執行中…、執行中…、有問題、已暫停、已結束。 */
export function routineStatus(r: Routine): { label: string; tone: 'success' | 'danger' | 'warning' | 'accent' | 'neutral'; key: 'running' | 'problem' | 'paused' | 'active' | 'done' } {
  if (r.running) return { label: r.goal ? `第 ${r.goal.rounds} 輪執行中…` : '執行中…', tone: 'accent', key: 'running' }
  if ((r.failures ?? 0) > 0 || r.last_error) return { label: '有問題', tone: 'danger', key: 'problem' }
  if (r.status === 'paused') return { label: '已暫停', tone: 'neutral', key: 'paused' }
  if (r.status === 'done') return { label: r.goal?.completed_at ? '目標完成' : '已結束', tone: 'neutral', key: 'done' }
  return { label: '啟用中', tone: 'success', key: 'active' }
}

export const JITTER_CHOICES = [0, 15, 30, 60, 90, 120]

export function jitterText(m: number): string {
  return m === 0 ? '不飄移' : `隨機延後 ${m} 分鐘內`
}

/** 網址觸發的完整網址：有外部入口（Funnel／反向代理）時接在對外網址後面，否則接在目前這個網站後面（C35）。 */
export function hookURL(path: string): string {
  return publicBase() + path
}
