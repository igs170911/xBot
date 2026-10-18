// 畫面連線的常數、按鍵與狀態文字（VncScreen 用；分開放讓 React Fast Refresh 正常）。
// 畫面連線恢復的數字（E10 規格卡；LazyBoy web/src/computer-recovery.ts）：
// 斷線 8 秒後重試、再斷冷卻 15 秒、最多自動 2 次；連上 30 秒才算穩定；20 秒沒連上算失敗；分頁在背景 15 秒就暫停串流。
export const RETRY_DELAY = 8000
export const RETRY_COOLDOWN = 15000
export const STABLE_CONNECTION = 30000
export const CONNECT_TIMEOUT = 20000
export const BACKGROUND_PAUSE = 15000
export const AUTO_RETRIES = 2

export type VncStatus =
  | { kind: 'connecting' }
  | { kind: 'connected'; width?: number; height?: number }
  /** 自動重試：第 attempt 次，等 seconds 秒（8 秒，第二次冷卻 15 秒） */
  | { kind: 'retrying'; attempt: number; seconds: number }
  /** 自動試了 2 次都沒成功：請按重新連線 */
  | { kind: 'failed'; reason?: string }
  /** 分頁在背景：暫停串流，回來自動接上 */
  | { kind: 'paused' }
  | { kind: 'offline' }

export type VncHandle = {
  /** 手動重新連線（重新計算自動重試次數）。 */
  reconnect: () => void
  /** 送一個鍵（按下＋放開）；mods 是同時按住的修飾鍵（keysym）。 */
  key: (keysym: number, mods?: number[]) => void
  /** 把文字放進電腦的剪貼簿並按 Ctrl+V。 */
  paste: (text: string) => void
  /** 逐字打進去（手機鍵盤）。 */
  type: (text: string) => void
  focus: () => void
}

/** 畫面 WebSocket 的網址（同源；https 時用 wss）。 */
export function vncUrl(path: string, control: boolean): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}${path}${control ? '&mode=control' : ''}`
}

export const KEY = {
  Escape: 0xff1b,
  Tab: 0xff09,
  Return: 0xff0d,
  BackSpace: 0xff08,
  Left: 0xff51,
  Up: 0xff52,
  Right: 0xff53,
  Down: 0xff54,
  Control: 0xffe3,
  Alt: 0xffe9,
  v: 0x76,
} as const

/** 字元的 keysym：Latin-1 直接用碼位，其他用 0x01000000＋碼位（X11 的 Unicode keysym）。 */
export function keysymOf(ch: string): number {
  if (ch === '\n') return KEY.Return
  const cp = ch.codePointAt(0) ?? 0
  return cp < 0x100 ? cp : 0x01000000 + cp
}

/** 狀態的白話（工具列、提示列用）。 */
export function statusText(s: VncStatus): string {
  switch (s.kind) {
    case 'connecting':
      return '正在連線畫面…'
    case 'connected':
      return s.width && s.height ? `已連線 · ${s.width}×${s.height}` : '已連線'
    case 'retrying':
      return s.attempt === 1 ? `畫面中斷了，${s.seconds} 秒後重新連線…` : `還是連不上，先等 ${s.seconds} 秒再試…`
    case 'failed':
      return '畫面連不上，請按重新連線'
    case 'paused':
      return '分頁在背景，已暫停畫面'
    case 'offline':
      return '網路已離線，恢復後會自動重新連線'
  }
}
