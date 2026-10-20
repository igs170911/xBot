import { createStore } from '../data/store'

// 對話頁右邊的面板與左右兩欄的寬度（2026-10-20）。
// 右邊一次放一個：電腦或檔案（檔案原本是整頁，和電腦的行為不一致）；在對話裡點檔案連結也在這裡打開。
// 側欄和右邊面板都能拖邊緣改寬度，記在這個瀏覽器。

export type RightPanel = { kind: 'computer' | 'files' | null; /** 檔案面板一打開就預覽的檔案 */ path?: string; /** 每次打開加一（同一個檔案再點一次也會重新選它） */ seq: number }

export const rightPanelStore = createStore<RightPanel>({ kind: null, seq: 0 })

export function openRightPanel(kind: 'computer' | 'files', path?: string) {
  rightPanelStore.set((p) => ({ kind, path, seq: p.seq + 1 }))
}

export function closeRightPanel() {
  rightPanelStore.set((p) => ({ ...p, kind: null, path: undefined }))
}

/** 開著就關、關著就開（頁首的電腦、檔案鈕）。 */
export function toggleRightPanel(kind: 'computer' | 'files') {
  if (rightPanelStore.get().kind === kind) closeRightPanel()
  else openRightPanel(kind)
}

// ── 檔案連結：對話頁開著右邊面板時，點回覆裡的檔案就在面板預覽，不整頁跳走 ──

type Opener = (botId: string, path: string) => boolean
let opener: Opener | null = null

/** 對話頁（寬螢幕）登記：哪個 Bot 的檔案可以在旁邊打開。回傳取消登記的函式。 */
export function registerFileOpener(fn: Opener): () => void {
  opener = fn
  return () => {
    if (opener === fn) opener = null
  }
}

/** 檔案頁的網址（/bots/<id>/files?path=…）能在旁邊的面板打開就打開，回傳有沒有打開。 */
export function openFileHere(href: string): boolean {
  const m = href.match(/^\/bots\/([^/?]+)\/files\?(.*)$/)
  if (!m || !opener) return false
  const path = new URLSearchParams(m[2]).get('path')
  return !!path && opener(decodeURIComponent(m[1]), path)
}

// ── 寬度 ──

export type PaneSize = { key: string; min: number; max: number; def: number }

export const SIDEBAR: PaneSize = { key: 'xbot.sidebar.width', min: 220, max: 420, def: 260 }
export const RIGHT: PaneSize = { key: 'xbot.panel.width', min: 360, max: 960, def: 540 }

function read(s: PaneSize): number {
  try {
    const v = Number(window.localStorage.getItem(s.key))
    return v >= s.min && v <= s.max ? v : s.def
  } catch {
    return s.def
  }
}

export const sidebarWidthStore = createStore(read(SIDEBAR))
export const rightWidthStore = createStore(read(RIGHT))

export function saveWidth(s: PaneSize, w: number) {
  try {
    if (w === s.def) window.localStorage.removeItem(s.key)
    else window.localStorage.setItem(s.key, String(w))
  } catch {
    // 私密瀏覽、儲存被擋：這次照樣能拖，只是不記
  }
}
