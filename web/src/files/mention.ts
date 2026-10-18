import { navigate } from '../router'

// 「在對話中提到」：先記下要放進輸入框的字，再到和這個 Bot 的對話（對話頁拿走一次）。

let pendingMention: { botId: string; text: string } | null = null

/** 到和這個 Bot 的對話，把檔案路徑放進輸入框（L19「在對話中提到」）。 */
export function mentionInChat(botId: string, path: string) {
  pendingMention = { botId, text: `${path} ` }
  navigate(`/bots/${encodeURIComponent(botId)}`)
}

/** 對話頁拿走要放進輸入框的字（一次）。 */
export function takeMention(botId: string): string | undefined {
  if (pendingMention?.botId !== botId) return undefined
  const t = pendingMention.text
  pendingMention = null
  return t
}
