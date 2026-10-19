import type { ModelsGetOutput } from '../api/generated'
import type { Bot } from '../data/bots'
import { stampLabel } from '../data/ui'
import type { ChatMsg, ChatState } from './assemble'

// 對話畫面用的小工具（純函式，好測）。

/** 「下午 2:05」 */
export function clockLabel(ms: number): string {
  const d = new Date(ms)
  const h = d.getHours()
  const h12 = h % 12 === 0 ? 12 : h % 12
  return `${h < 12 ? '上午' : '下午'} ${h12}:${String(d.getMinutes()).padStart(2, '0')}`
}

// 長訊息先收起（你貼的長文、Bot 的超長報告），按「顯示全部」展開。
const OWNER_LONG_CHARS = 800
const OWNER_LONG_LINES = 14
const BOT_LONG_CHARS = 6000

export function isLong(msg: ChatMsg): boolean {
  if (msg.streaming) return false
  if (msg.sender === 'owner') return msg.content.length > OWNER_LONG_CHARS || msg.content.split('\n').length > OWNER_LONG_LINES
  return msg.content.length > BOT_LONG_CHARS
}

/** 「剛剛」「12 分鐘前」，再早就寫日期時間。 */
export function relTime(ms: number, now = Date.now()): string {
  const d = now - ms
  if (d < 60_000) return '剛剛'
  if (d < 3_600_000) return `${Math.floor(d / 60_000)} 分鐘前`
  return stampLabel(ms, now)
}

/** 一件工作的標題：交代它的那句話（第一行，最多 40 字）。 */
export function taskTitle(s: ChatState, threadId: string, taskId: string): string {
  const m = s.threads[threadId]?.messages.find((x) => x.taskId === taskId && x.sender === 'owner')
  const line = (m?.content ?? '').split('\n')[0].trim()
  return line.length > 40 ? line.slice(0, 40) + '…' : line || '這件工作'
}

/** 開場的建議（點了放進輸入框，不會直接送出）。 */
export const STARTERS = ['先介紹你自己，說說你能幫我做什麼', '看看你的電腦，告訴我裡面有什麼', '幫我想想這週可以先交給你哪些事']

/** Bot 的 model 欄位：「<供應商>/<模型>」或只寫模型（用預設模型的供應商）；空白是預設模型。 */
export function parseModel(model: string, m: ModelsGetOutput | null): { provider?: string; model: string } {
  if (!model) return { provider: m?.default?.provider, model: m?.default?.model ?? '' }
  const i = model.indexOf('/')
  if (i > 0 && m?.providers.some((p) => p.id === model.slice(0, i))) return { provider: model.slice(0, i), model: model.slice(i + 1) }
  return { provider: m?.default?.provider, model }
}

/** 這個 Bot 用的是訂閱 CLI（不是內建的 Hermes＋API 金鑰）。 */
export function usesPlan(bot: Bot): boolean {
  return !!bot.engine && bot.engine !== 'hermes'
}

/** 標題列膠囊上的模型名稱。 */
export function modelLabel(bot: Bot, m: ModelsGetOutput | null): string {
  if (!m) return bot.model || ''
  const cur = parseModel(bot.model, m)
  return cur.model || '還沒設定模型'
}


/** 判斷的白話（和後端 judge.Verdict.Label 一樣）。 */
export const VERDICT_LABEL: Record<string, string> = {
  done: '已完成',
  needs_person: '需要你',
  handed_back: '交回給你',
  only_promised: '只說要做',
  stuck: '卡住了',
}

/** 誰判的：規則寫「規則」，快判模型寫機率（92%）。 */
export function judgeBy(by: string, confidence?: number): string {
  if (by === 'model' && confidence) return `${Math.round(confidence * 100)}%`
  return by === 'model' ? '快速判斷' : '規則'
}
