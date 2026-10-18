import type { ChatItem } from './assemble'

// 對話的螢幕閱讀器播報（Announcer.tsx 用）：Bot 的回覆「講完」時念一次，最多 300 字。
const MAX = 300

/** 回傳這次要念的文字（沒有就是 null）；念過的訊息 id 記在 said，同一則只念一次。 */
export function nextAnnouncement(items: ChatItem[], said: Set<string>, name: string): string | null {
  let text: string | null = null
  for (const it of items) {
    if (it.type !== 'message') continue
    const m = it.msg
    if (m.sender === 'owner' || m.streaming || m.silent || !m.content || said.has(m.id)) continue
    said.add(m.id)
    const body = m.content.replace(/\s+/g, ' ').trim()
    text = `${name}回覆：${body.length > MAX ? body.slice(0, MAX) + '…' : body}`
  }
  return text
}
