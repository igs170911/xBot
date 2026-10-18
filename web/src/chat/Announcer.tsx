import { useEffect, useRef, useState } from 'react'
import { nextAnnouncement } from './announce'
import type { ChatItem } from './assemble'

// 給螢幕閱讀器的即時播報（v1.0 無障礙，WCAG 4.1.3）：Bot 的回覆串流時一個字一個字地變，整個訊息列表如果是
// aria-live，會一直念到一半的句子。所以訊息列表本身不播報，回覆「講完」時才在這裡念一次。
// 打開對話時已經在的回覆不念。
export function ChatAnnouncer({ items, name, ready }: { items: ChatItem[]; name: string; ready: boolean }) {
  const said = useRef<Set<string> | null>(null)
  const [text, setText] = useState('')
  useEffect(() => {
    if (!ready) return
    if (said.current === null) {
      // 第一次：把已經在畫面上的回覆記成念過
      said.current = new Set()
      nextAnnouncement(items, said.current, name)
      return
    }
    const t = nextAnnouncement(items, said.current, name)
    if (t) setText(t)
  }, [items, name, ready])
  return (
    <div role="status" aria-live="polite" aria-atomic="true" className="sr-only-text" data-testid="chat-announcer">
      {text}
    </div>
  )
}
