import type { AppsListOutput, AppsCatalogOutput } from '../api/generated'
import type { Bot } from '../data/bots'

export type Conn = AppsListOutput['connections'][number]
export type CatalogApp = AppsCatalogOutput['apps'][number]

export type Tone = 'success' | 'warning' | 'danger' | 'neutral' | 'accent'

/** 連接狀態的白話說明（I01：可以使用／需要重新登入／授權已過期）。 */
export function statusOf(status: string): { text: string; tone: Tone } {
  switch (status) {
    case 'ok':
      return { text: '可以使用', tone: 'success' }
    case 'needs_signin':
      return { text: '需要登入', tone: 'warning' }
    case 'expired':
      return { text: '授權已過期', tone: 'danger' }
    case 'error':
      return { text: '連不上', tone: 'danger' }
    case 'disabled':
      return { text: '已停用', tone: 'neutral' }
    case 'signing_in':
      return { text: '登入中', tone: 'accent' }
    default:
      return { text: '還沒測試', tone: 'neutral' }
  }
}

export const toneText: Record<Tone, string> = {
  success: 'text-success',
  warning: 'text-warning-ink',
  danger: 'text-danger',
  neutral: 'text-ink-2',
  accent: 'text-accent-ink',
}

export function whoCanUse(c: Pick<Conn, 'all_bots' | 'bot_ids'>, bots: Bot[]): string {
  if (c.all_bots) return '所有 Bot'
  if (!c.bot_ids.length) return '還沒給任何 Bot'
  return c.bot_ids.map((id) => bots.find((b) => b.id === id)?.name ?? '已刪除的 Bot').join('、')
}

export function when(ms: number): string {
  if (!ms) return '還沒用過'
  const d = new Date(ms)
  const now = new Date()
  const hm = d.toLocaleTimeString('zh-TW', { hour: 'numeric', minute: '2-digit' })
  if (d.toDateString() === now.toDateString()) return `今天${hm}`
  const y = new Date(now)
  y.setDate(now.getDate() - 1)
  if (d.toDateString() === y.toDateString()) return `昨天${hm}`
  return `${d.getMonth() + 1}/${d.getDate()} ${hm}`
}
