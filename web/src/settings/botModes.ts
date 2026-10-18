import type { BotsGetOutput } from '../api/generated'

type Mode = BotsGetOutput['approval_mode']

// 引擎與權限等級的白話名稱（G08／G09、BT-5）。

export const ENGINE_LABEL: Record<string, string> = { hermes: 'Hermes', 'claude-code': 'Claude Code', codex: 'Codex' }
/** 訂閱的白話名稱（標示用）。 */
export const PLAN_NAME: Record<string, string> = { 'claude-code': 'Claude', codex: 'ChatGPT' }

export const MODES: Array<{ value: Mode; label: string; desc: string }> = [
  { value: 'ask', label: '先問我', desc: '花錢、對外發送、改檔案、刪除前都先問你（預設）' },
  { value: 'edits', label: '一般的事自己做', desc: '讀檔、改檔自己做；其他會改東西的事先問你' },
  { value: 'auto', label: '放手做', desc: '大部分自己做；付款、簽約、上架仍然一定問你' },
  { value: 'full', label: '完全放手', desc: '幾乎都自己做，不再逐步問你' },
]

export const MODE_LABEL: Record<string, string> = Object.fromEntries(MODES.map((m) => [m.value, m.label]))

export function subscriptionMarker(engine: string, marker?: string): string {
  return marker || `這個 Bot 用你的 ${PLAN_NAME[engine] ?? ENGINE_LABEL[engine] ?? engine} 訂閱`
}

