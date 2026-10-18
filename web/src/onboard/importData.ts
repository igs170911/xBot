import { useEffect } from 'react'
import { importStatus, type ImportDetectOutput, type ImportPlanOutput, type ImportRunOutput } from '../api/generated'
import { onEvent, onReconnect } from '../data/events'
import { createStore, useStore } from '../data/store'

// 從 LazyBoy、OpenMausBot 帶入舊設定（V10，A05、F10）：xbot.import.detect／plan／run／status ＋ import.progress 事件。

export type Found = ImportDetectOutput['sources'][number]
export type Plan = ImportPlanOutput
export type PlanItem = Plan['items'][number]
export type ImportRun = ImportRunOutput

export const importRunStore = createStore<ImportRun | null>(null)

let users = 0
let unsub: (() => void) | null = null

function loadRun(): Promise<void> {
  return importStatus({})
    .then((s) => importRunStore.set((cur) => (cur && s.runs[0]?.id !== cur.id ? cur : (s.runs[0] ?? cur))))
    .catch(() => {})
}

/** 目前（或最近一次）匯入的進度；元件掛著時跟著 import.progress 事件更新。 */
export function useImportRun(): ImportRun | null {
  useEffect(() => {
    users++
    if (users === 1) {
      const off = onEvent((e) => {
        if (e.kind === 'import.progress' && e.payload) importRunStore.set(e.payload as ImportRun)
      })
      const offRe = onReconnect(() => void loadRun())
      unsub = () => {
        off()
        offRe()
      }
    }
    return () => {
      users--
      if (users === 0 && unsub) {
        unsub()
        unsub = null
      }
    }
  }, [])
  return useStore(importRunStore)
}

/** 預設勾選：照服務的建議（金鑰、瀏覽器登入要你自己勾）。 */
export function defaultSelection(plan: Plan): Set<string> {
  return new Set(plan.items.filter((it) => it.status === 'ready' && it.checked).map((it) => it.key))
}

/** 這一項能不能勾：要是 ready；跟著 Bot 的東西，那個 Bot 要一起帶或已經帶過。 */
export function selectable(plan: Plan, sel: Set<string>, it: PlanItem): boolean {
  if (it.status !== 'ready') return false
  if (!it.bot) return true
  const owner = plan.items.find((x) => x.key === it.bot)
  return !!owner && (owner.status === 'done' || sel.has(owner.key))
}

/** 實際會送出的項目（Bot 沒勾的，它的東西也不送）。 */
export function effective(plan: Plan, sel: Set<string>): string[] {
  return plan.items.filter((it) => sel.has(it.key) && selectable(plan, sel, it)).map((it) => it.key)
}

/** 勾或不勾一項；取消一個 Bot 時它底下的東西一起取消。 */
export function toggle(plan: Plan, sel: Set<string>, key: string, on: boolean): Set<string> {
  const next = new Set(sel)
  if (on) next.add(key)
  else next.delete(key)
  if (!on && key.startsWith('bot:')) plan.items.filter((it) => it.bot === key).forEach((it) => next.delete(it.key))
  if (on && key.startsWith('bot:'))
    plan.items.filter((it) => it.bot === key && it.status === 'ready' && it.checked).forEach((it) => next.add(it.key))
  return next
}

export function countNew(plan: Plan, keys: string[]) {
  const set = new Set(keys)
  const of = (kind: string) => plan.items.filter((it) => it.kind === kind && set.has(it.key))
  return {
    bots: of('bot').length,
    skills: of('skills').reduce((n, it) => n + (it.count ?? 0), 0),
    routines: of('routine').length,
    keys: of('key').length,
    logins: of('login').length,
  }
}

export function human(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GB`
  if (n >= 1 << 20) return `${Math.round(n / (1 << 20))} MB`
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`
  return `${n} B`
}
