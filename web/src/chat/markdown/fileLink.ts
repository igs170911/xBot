import { createContext, type MouseEvent } from 'react'
import { linkClick } from '../../router'
import { openFileHere } from '../../shell/panes'

// 回覆裡的檔案路徑（`/workspace/…`）變成可以點的連結：點了打開那個 Bot 的檔案頁、直接預覽那個檔案。
// 由對話頁提供（知道是哪個 Bot）；沒有提供時照舊只是程式碼文字。

/** 路徑 → 檔案頁的網址；不是 Bot 電腦裡的檔案時回 undefined。 */
export type FileLinker = (path: string) => string | undefined

export const FileLinkContext = createContext<FileLinker | null>(null)

/** 看起來是 Bot 電腦裡的一個檔案（不是資料夾、沒有空白）。 */
export function isBoxFile(text: string): boolean {
  return /^\/workspace\/\S*[^/\s]$/.test(text.trim())
}

/**
 * 連結網址指到 Bot 電腦裡的檔案時回傳那個路徑：Bot 寫的連結有 `/workspace/…`、`file:///workspace/…`、
 * `sandbox:/workspace/…` 幾種（2026-10-20 基準測試看到三種都有）；不是的話回 undefined。
 */
export function boxPath(href: string): string | undefined {
  let p = href.trim().replace(/^sandbox:/i, '').replace(/^file:\/\//i, '')
  try {
    p = decodeURI(p)
  } catch {
    return undefined
  }
  return isBoxFile(p) ? p : undefined
}

export function botFileLinker(botId: string): FileLinker {
  return (path) => (isBoxFile(path) ? `/bots/${encodeURIComponent(botId)}/files?path=${encodeURIComponent(path.trim())}` : undefined)
}

/** 點檔案連結：對話頁旁邊能開檔案面板就在那裡預覽（和電腦面板一樣，2026-10-20），不然照常換到檔案頁。 */
export function fileClick(href: string) {
  const go = linkClick(href)
  return (e: MouseEvent<HTMLAnchorElement>) => {
    if (!e.metaKey && !e.ctrlKey && !e.shiftKey && e.button === 0 && openFileHere(href)) {
      e.preventDefault()
      return
    }
    go(e)
  }
}
