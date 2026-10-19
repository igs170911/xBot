import { chatRead, chatSteps } from '../api/generated'
import { onEvent } from '../data/events'
import { createStore } from '../data/store'
import { applyEvent, applyRead, applySteps, emptyChat, type ChatState } from './assemble'

// 全站一份對話狀態：從開網頁起就收所有事件（剛送出、還不知道 thread_id 的新對話串也不會漏），
// 打開某條對話串時再用 chat.read 補上歷史。
export const chatStore = createStore<ChatState>(emptyChat)

let unsub: (() => void) | null = null

export function startChat() {
  if (unsub) return
  unsub = onEvent((e) => chatStore.set((s) => applyEvent(s, e)))
}

export async function loadThread(threadId: string): Promise<void> {
  const out = await chatRead({ thread_id: threadId, limit: 100 })
  chatStore.set((s) => applyRead(s, out))
  await loadSteps(threadId)
}

/** 補回工作步驟（重新整理後 chat.read 只有訊息）。讀不到不影響對話。 */
async function loadSteps(threadId: string): Promise<void> {
  let after = 0
  for (let page = 0; page < 20; page++) {
    const out = await chatSteps({ thread_id: threadId, after: after || undefined }).catch(() => null)
    if (!out) return
    chatStore.set((s) => applySteps(s, out.events))
    if (!out.has_more || !out.next_after) return
    after = out.next_after
  }
}

/** 往前多讀一頁（比目前最舊的那則更早的訊息）。 */
export async function loadOlder(threadId: string): Promise<void> {
  const first = chatStore.get().threads[threadId]?.messages.find((m) => m.seq !== Number.MAX_SAFE_INTEGER)
  if (!first) return
  const out = await chatRead({ thread_id: threadId, before: first.seq, limit: 100 })
  chatStore.set((s) => applyRead(s, out))
}

export function resetChat() {
  unsub?.()
  unsub = null
  chatStore.set(emptyChat)
}
