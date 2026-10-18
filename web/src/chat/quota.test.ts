import { describe, expect, it } from 'vitest'
import { quotaFrom, quotaTitle } from './quota'

describe('額度用完卡（EN-7）', () => {
  const now = new Date(2026, 9, 18, 10, 0).getTime()
  const at = new Date(2026, 9, 18, 14, 20).getTime()

  it('標題和服務端一樣', () => {
    expect(quotaTitle({ label: 'Claude Code', kind: 'quota', reset_at: at }, now)).toBe('Claude Code 的額度用完了（約 14:20 恢復）')
    expect(quotaTitle({ label: 'Claude Code', kind: 'quota', reset_at: at + 86_400_000 }, now)).toBe('Claude Code 的額度用完了（約 10/19 14:20 恢復）')
    expect(quotaTitle({ label: 'OpenCode Go', kind: 'funds' }, now)).toBe('OpenCode Go 的餘額不足')
    expect(quotaTitle({ label: 'Grok', kind: 'auth', reset_at: at }, now)).toBe('Grok 的登入過期了')
    expect(quotaTitle({ label: 'Grok', kind: 'rate_limit' }, now)).toBe('Grok 暫時用量太多，被限流了')
  })

  it('讀 payload：不是額度卡回 null，選項缺少時是空的', () => {
    expect(quotaFrom(null)).toBeNull()
    expect(quotaFrom({ engine: 'codex' })).toBeNull()
    const q = quotaFrom({ engine: 'codex', kind: 'quota' })
    expect(q?.label).toBe('codex')
    expect(q?.options).toEqual([])
  })
})
