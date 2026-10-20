import { createStore } from '../data/store'
import { navigate } from '../router'

// 「在對話中提到」：記下要放進輸入框的字；不在和這個 Bot 的對話就先過去（對話頁拿走一次）。
// 已經在那個對話（例如從旁邊的檔案面板按）就留在原本的對話串，直接放進輸入框（2026-10-20）。

export const mentionStore = createStore<{ botId: string; text: string } | null>(null)

/** 到和這個 Bot 的對話，把檔案路徑放進輸入框（L19「在對話中提到」）。 */
export function mentionInChat(botId: string, path: string) {
  mentionStore.set({ botId, text: `${path} ` })
  const chat = `/bots/${encodeURIComponent(botId)}`
  const here = window.location.pathname
  const inChat = here === chat || (here.startsWith(chat + '/threads/'))
  if (!inChat) navigate(chat)
}

/** 對話頁拿走要放進輸入框的字（一次）。 */
export function takeMention(botId: string): string | undefined {
  const m = mentionStore.get()
  if (m?.botId !== botId) return undefined
  mentionStore.set(null)
  return m.text
}
