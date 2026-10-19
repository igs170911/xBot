import type { MemoryHistoryOutput } from '../api/generated'

// 記憶的「最近的變更」與每一條的來歷（G06）：純函式，好測。

export type MemChange = MemoryHistoryOutput['changes'][number]

export type ChangeKind = '新增' | '刪除' | '修改' | '整理'

/** 一次變更是哪一種。新增、拿掉都沒有的是只調整了順序或拿掉重複的條目（整理），不是刪除。 */
export function changeKind(c: Pick<MemChange, 'added' | 'removed'>): ChangeKind {
  if (c.added.length && c.removed.length) return '修改'
  if (c.added.length) return '新增'
  if (c.removed.length) return '刪除'
  return '整理'
}

/** 誰改的：你、Bot 自己、代理。 */
export function actorName(actor: string, botName: string): string {
  if (actor === 'owner') return '你'
  return actor.startsWith('bot:') ? botName : '代理'
}

/** 一條記憶是哪一次變更加進來的（最新的那次；changes 新的在前）。最近的變更裡找不到時是 undefined。 */
export function entryOrigin(entry: string, target: string, changes: MemChange[]): MemChange | undefined {
  return changes.find((c) => c.target === target && c.added.includes(entry))
}

/** 在哪裡記的：對話串或群組的網址；不知道（或對話串刪了）時是 undefined。 */
export function originLink(c: Pick<MemChange, 'thread_id' | 'room_id'>, botId: string): string | undefined {
  if (c.room_id) return `/rooms/${c.room_id}`
  if (c.thread_id) return `/bots/${botId}/threads/${c.thread_id}`
  return undefined
}

/** 月／日，例：10/18。 */
export function shortDate(ms: number): string {
  const d = new Date(ms)
  return `${d.getMonth() + 1}/${d.getDate()}`
}
