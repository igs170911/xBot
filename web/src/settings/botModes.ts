import type { BotsGetOutput } from '../api/generated'

type Mode = BotsGetOutput['approval_mode']

// 引擎與權限等級的白話名稱（G08／G09、BT-5）。

export const ENGINE_LABEL: Record<string, string> = {
  hermes: 'Hermes',
  'claude-code': 'Claude Code',
  codex: 'Codex',
  gemini: 'Gemini CLI',
  opencode: 'OpenCode',
  grok: 'Grok',
}
/** 訂閱的白話名稱（標示用）。 */
export const PLAN_NAME: Record<string, string> = { 'claude-code': 'Claude', codex: 'ChatGPT', gemini: 'Google', opencode: 'OpenCode Go', grok: 'Grok' }

/** 引擎選擇器的順序與一行說明（G08；E31 加上 Gemini CLI、OpenCode、Grok）。 */
export const ENGINES: Array<{ value: string; desc: string }> = [
  { value: 'hermes', desc: '內建引擎，用「AI 模型」設定的模型與金鑰' },
  { value: 'claude-code', desc: '用你的 Claude 訂閱（Claude Code）' },
  { value: 'codex', desc: '用你的 ChatGPT 方案（Codex CLI）' },
  { value: 'gemini', desc: '用你的 Google 帳號（Gemini CLI）' },
  { value: 'opencode', desc: '用你的 OpenCode Go 或 Zen（OpenCode）' },
  { value: 'grok', desc: '用你的 Grok 帳號（Grok Build）' },
]

/** AI 帳號與模型（2026-10-20）：列出的順序、名稱與一行說明。訂閱帳號由每個 Bot 自己登入。 */
export const ACCOUNTS: Array<{ value: string; title: string; desc: string }> = [
  { value: 'hermes', title: '內建（Hermes）', desc: '用「AI 模型」設定的金鑰或 ChatGPT 方案' },
  { value: 'claude-code', title: 'Claude Code', desc: '用你的 Claude 帳號（Pro／Max）' },
  { value: 'codex', title: 'Codex', desc: '用你的 ChatGPT 帳號（Plus／Pro）' },
  { value: 'grok', title: 'Grok', desc: '用你的 Grok 帳號（SuperGrok）' },
  { value: 'opencode', title: 'OpenCode', desc: '用你的 OpenCode Go 或 Zen 金鑰' },
  { value: 'gemini', title: 'Gemini CLI', desc: '用你的 Google 帳號（要在這台電腦登入）' },
]

export const MODES: Array<{ value: Mode; label: string; desc: string }> = [
  { value: 'ask', label: '先問我', desc: '花錢、對外發送、改檔案、刪除前都先問你' },
  { value: 'edits', label: '一般的事自己做', desc: '讀檔、改檔自己做；其他會改東西的事先問你（預設）' },
  { value: 'auto', label: '放手做', desc: '大部分自己做；付款、簽約、上架仍然一定問你' },
  { value: 'full', label: '完全放手', desc: '幾乎都自己做，不再逐步問你' },
]

export const MODE_LABEL: Record<string, string> = Object.fromEntries(MODES.map((m) => [m.value, m.label]))

export function subscriptionMarker(engine: string, marker?: string): string {
  return marker || `這個 Bot 用你的 ${PLAN_NAME[engine] ?? ENGINE_LABEL[engine] ?? engine} 訂閱`
}

