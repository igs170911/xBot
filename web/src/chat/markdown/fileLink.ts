import { createContext, type KeyboardEvent, type MouseEvent } from 'react'
import { navigate } from '../../router'
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


/**
 * 檔案參照（回覆裡的檔案路徑）：點了在旁邊打開那個檔案（手機換到檔案頁）。
 * 不用 <a>：iPhone 上手指從連結上開始滑，整個對話滑不動，放開時還被當成點了連結、打開檔案
 * （2026-10-20 真機回報，每次滑不動都按在檔案連結上；拿掉拖曳也一樣）。換成可以點的文字（role=link，鍵盤也能用），
 * iPhone 就照一般文字處理捲動。
 */
export function fileRefProps(href: string) {
  const open = () => {
    if (!openFileHere(href)) navigate(href)
  }
  return {
    role: 'link' as const,
    tabIndex: 0,
    'data-href': href,
    title: '打開這個檔案',
    onClick: (e: MouseEvent<HTMLElement>) => {
      if (e.metaKey || e.ctrlKey || e.shiftKey) {
        window.open(href, '_blank', 'noopener')
        return
      }
      open()
    },
    onKeyDown: (e: KeyboardEvent<HTMLElement>) => {
      if (e.key !== 'Enter' && e.key !== ' ') return
      e.preventDefault()
      open()
    },
  }
}
