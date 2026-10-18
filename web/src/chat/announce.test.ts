import { describe, expect, it } from 'vitest'
import { nextAnnouncement } from './announce'
import type { ChatItem, ChatMsg } from './assemble'

const msg = (id: string, over: Partial<ChatMsg> = {}): ChatItem => ({
  type: 'message',
  msg: { id, seq: 1, sender: 'bot:b1', kind: 'text', content: '好的', streaming: false, createdAt: 1, updatedAt: 1, live: false, ...over } as ChatMsg,
})

describe('nextAnnouncement（v1.0：Bot 回覆講完才念）', () => {
  it('串流中不念，講完念一次，之後不重複', () => {
    const said = new Set<string>()
    expect(nextAnnouncement([msg('m1', { streaming: true, content: '好' })], said, '小秘')).toBeNull()
    expect(nextAnnouncement([msg('m1', { content: '好的，做完了' })], said, '小秘')).toBe('小秘回覆：好的，做完了')
    expect(nextAnnouncement([msg('m1', { content: '好的，做完了' })], said, '小秘')).toBeNull()
  })

  it('你自己說的話、沒有內容、靜默的不念；太長的截斷', () => {
    const said = new Set<string>()
    expect(nextAnnouncement([msg('o1', { sender: 'owner', content: '幫我查' }), msg('m2', { content: '' }), msg('m3', { silent: true } as Partial<ChatMsg>)], said, '小秘')).toBeNull()
    const long = 'a'.repeat(400)
    expect(nextAnnouncement([msg('m4', { content: long })], said, '小秘')).toBe('小秘回覆：' + 'a'.repeat(300) + '…')
  })
})
