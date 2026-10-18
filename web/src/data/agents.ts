import type { AgentsListOutput, AgentsSetDelegationInput, AgentsUpdateScopesInput, AuditListOutput } from '../api/generated'
import type { Tone } from '../ui/Pill'

// 「設定 → 已連線的代理」（design/J12）的資料整理：權限、資源範圍、上限、代為核准的草稿與輸入。

export type Agent = AgentsListOutput['agents'][number]
export type Scope = Agent['scopes'][number]
export type Category = Agent['delegation']['categories'][number]
export type ResourceScope = Agent['resource_scope']
export type AuditEntry = AuditListOutput['entries'][number]

export const SCOPES: { key: Scope; name: string; desc: string }[] = [
  { key: 'read', name: '看', desc: '看 Bot、對話、進度、用量與紀錄' },
  { key: 'operate', name: '交辦', desc: '交辦工作、開對話串、定時任務、檔案' },
  { key: 'manage', name: '管理', desc: '建立／修改 Bot、團隊、群組、技能、記憶' },
  { key: 'admin', name: '系統', desc: '主機、連接應用程式、模型金鑰、成員' },
]

export const CATEGORIES: { key: Category; name: string }[] = [
  { key: 'external_send', name: '對外發送' },
  { key: 'spend_small', name: '小額花費' },
  { key: 'publish', name: '上架' },
  { key: 'sign', name: '簽約' },
]

export const CATEGORY_NAME: Record<string, string> = Object.fromEntries(CATEGORIES.map((c) => [c.key, c.name]))

/** 永遠要你本人核准（後端 delegation.Never；清單還沒讀到時用這份）。 */
export const NEVER = ['刪除 Bot 或資料', '移除主機', '提高花費上限', '改代為核准授權']

export const EXPIRY_DAYS = [1, 7, 30] as const
export type ExpiryDays = (typeof EXPIRY_DAYS)[number]

/** 美元百萬分之一 → 畫面上的數字（不含 US$）：2500000 → "2.5"。 */
export function microsToUsd(n: number): string {
  if (!n) return ''
  return String(Math.round(n / 10_000) / 100)
}

/** 畫面上的數字 → 美元百萬分之一；空白是 0；不是數字回 null。 */
export function usdToMicros(s: string): number | null {
  const t = s.trim()
  if (!t) return 0
  if (!/^\d+(\.\d{1,2})?$/.test(t)) return null
  return Math.round(Number(t) * 1_000_000)
}

/** 整數欄位：空白是 0；不是非負整數回 null。 */
export function toCount(s: string): number | null {
  const t = s.trim()
  if (!t) return 0
  return /^\d+$/.test(t) ? Number(t) : null
}

export function usd(n: number): string {
  return `US$${microsToUsd(n) || '0'}`
}

export type Draft = {
  scopes: Scope[]
  resourceScope: ResourceScope
  bots: string[]
  dailyCalls: string
  dailySpend: string
  dg: { on: boolean; cats: Category[]; count: string; amount: string; days: ExpiryDays }
}

export const NEW_DRAFT: Draft = {
  scopes: ['read', 'operate'],
  resourceScope: 'own',
  bots: [],
  dailyCalls: '500',
  dailySpend: '',
  dg: { on: false, cats: ['external_send'], count: '5', amount: '', days: 7 },
}

function closestDays(expiresAt: number, now: number): ExpiryDays {
  const left = (expiresAt - now) / 86_400_000
  return EXPIRY_DAYS.find((d) => left <= d) ?? 30
}

export function draftOf(a: Agent, now = Date.now()): Draft {
  const d = a.delegation
  return {
    scopes: [...a.scopes],
    resourceScope: a.resource_scope,
    bots: a.resource_scope === 'custom' ? [...(a.resources.bots ?? [])] : [],
    dailyCalls: a.daily_calls ? String(a.daily_calls) : '',
    dailySpend: microsToUsd(a.daily_spend_micros),
    dg: d.enabled
      ? { on: true, cats: [...d.categories], count: String(d.daily_count), amount: microsToUsd(d.daily_amount_micros), days: closestDays(d.expires_at, now) }
      : { ...NEW_DRAFT.dg },
  }
}

export type DraftErrors = Partial<Record<'dailyCalls' | 'dailySpend' | 'dgCount' | 'dgAmount' | 'dgCats' | 'scopes', string>>

export function validate(d: Draft): DraftErrors {
  const e: DraftErrors = {}
  if (d.scopes.length === 0) e.scopes = '至少勾一個'
  if (toCount(d.dailyCalls) === null) e.dailyCalls = '要是整數'
  if (usdToMicros(d.dailySpend) === null) e.dailySpend = '要是金額，例：20'
  if (d.dg.on) {
    const n = toCount(d.dg.count)
    if (n === null || n < 1 || n > 100) e.dgCount = '1～100 次'
    if (usdToMicros(d.dg.amount) === null) e.dgAmount = '要是金額，例：20'
    if (d.dg.cats.length === 0) e.dgCats = '至少勾一個類別'
  }
  return e
}

export function updateInput(id: string, d: Draft): AgentsUpdateScopesInput {
  return {
    id,
    scopes: d.scopes,
    resource_scope: d.resourceScope,
    ...(d.resourceScope === 'custom' ? { resources: { bots: d.bots } } : {}),
    daily_calls: toCount(d.dailyCalls) ?? 0,
    daily_spend_micros: usdToMicros(d.dailySpend) ?? 0,
  }
}

export function delegationInput(id: string, d: Draft): AgentsSetDelegationInput {
  if (!d.dg.on) return { id, enabled: false }
  return {
    id,
    enabled: true,
    categories: d.dg.cats,
    daily_count: toCount(d.dg.count) ?? 0,
    daily_amount_micros: usdToMicros(d.dg.amount) ?? 0,
    days: d.dg.days,
  }
}

/** 「到 10/25」 */
export function untilLabel(days: number, now = Date.now()): string {
  const t = new Date(now + days * 86_400_000)
  return `到 ${t.getMonth() + 1}/${t.getDate()}`
}

export function resourceText(a: Pick<Agent, 'resource_scope' | 'resources'>, botName: (id: string) => string | undefined): string {
  if (a.resource_scope === 'all') return '全部 Bot'
  if (a.resource_scope === 'own') return '只能碰它建立的'
  const names = (a.resources.bots ?? []).map((id) => botName(id) ?? 'Bot')
  return names.length ? `只有這些 Bot：${names.join('、')}` : '只能碰它建立的'
}

export function ago(ms: number, now = Date.now()): string {
  if (!ms) return '還沒用過'
  const min = Math.round((now - ms) / 60_000)
  if (min < 1) return '剛剛'
  if (min < 60) return `${min} 分鐘前`
  const h = Math.round(min / 60)
  return h < 24 ? `${h} 小時前` : `${Math.round(h / 24)} 天前`
}

/** 最近的呼叫的結果欄（J12）。 */
export function auditResult(e: Pick<AuditEntry, 'status' | 'error_code'>): { label: string; tone: Tone } {
  switch (e.status) {
    case 'ok':
      return { label: '完成', tone: 'success' }
    case 'pending_approval':
      return { label: '等你核准', tone: 'warning' }
    case 'reverted':
      return { label: '已復原', tone: 'neutral' }
  }
  switch (e.error_code) {
    case 'forbidden':
      return { label: '被拒：權限不足', tone: 'danger' }
    case 'limit':
      return { label: '被擋：超過上限', tone: 'danger' }
    case 'budget':
      return { label: '被擋：超過預算', tone: 'danger' }
  }
  return { label: '失敗', tone: 'danger' }
}

/** 撤銷後 10 分鐘內可以復原（後端 limits.AgentRestoreWindow）。 */
export const RESTORE_WINDOW_MS = 10 * 60_000

export function canRestore(a: Pick<Agent, 'revoked_at'>, now = Date.now()): boolean {
  return a.revoked_at > 0 && now - a.revoked_at <= RESTORE_WINDOW_MS
}
