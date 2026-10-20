import { describe, expect, it } from 'vitest'
import { settledCard } from './inbox'

describe('待我處理', () => {
  it('回覆了、結掉了的卡馬上認得出來（不等重讀）', () => {
    expect(settledCard({ kind: 'chat.answered', at: 1, payload: { inbox_id: 'i1' } })).toBe('i1')
    expect(settledCard({ kind: 'inbox.updated', at: 1, payload: { id: 'i2', status: 'resolved' } })).toBe('i2')
    expect(settledCard({ kind: 'inbox.updated', at: 1, payload: { id: 'i3', status: 'open' } })).toBeUndefined()
    expect(settledCard({ kind: 'chat.text', at: 1, payload: {} })).toBeUndefined()
  })
})
