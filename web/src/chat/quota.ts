// 引擎額度用完卡的資料（EN-7）：inbox payload 與訊息 meta.quota 的形狀、標題。

export type SwitchOption = { engine: string; model?: string; label: string; note?: string; needs_link?: boolean }

export type Quota = {
  inbox_id?: string
  engine: string
  provider?: string
  kind: string
  reset_at?: number
  message?: string
  label: string
  bot_name?: string
  thread_id?: string
  task_id?: string
  retry_text?: string
  options: SwitchOption[]
}

/** 從 inbox payload 或訊息 meta.quota 讀出額度卡；不是時回 null。 */
export function quotaFrom(x: unknown): Quota | null {
  if (!x || typeof x !== 'object') return null
  const q = x as Partial<Quota>
  if (typeof q.engine !== 'string' || typeof q.kind !== 'string') return null
  return { ...q, label: q.label || q.engine, options: Array.isArray(q.options) ? q.options : [] } as Quota
}

export function hhmm(ms: number, now = Date.now()): string {
  const d = new Date(ms)
  const t = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  return new Date(now).toDateString() === d.toDateString() ? t : `${d.getMonth() + 1}/${d.getDate()} ${t}`
}

/** 卡片標題（和服務端 runtime.quotaTitle 一樣）：「Claude Code 的額度用完了（約 14:20 恢復）」。 */
export function quotaTitle(q: Pick<Quota, 'label' | 'kind' | 'reset_at'>, now = Date.now()): string {
  if (q.kind === 'auth') return `${q.label} 的登入過期了`
  const head = q.kind === 'funds' ? `${q.label} 的餘額不足` : q.kind === 'rate_limit' ? `${q.label} 暫時用量太多，被限流了` : `${q.label} 的額度用完了`
  return q.reset_at ? `${head}（約 ${hhmm(q.reset_at, now)} 恢復）` : head
}
