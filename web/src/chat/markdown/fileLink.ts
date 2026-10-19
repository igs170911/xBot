import { createContext } from 'react'

// 回覆裡的檔案路徑（`/workspace/…`）變成可以點的連結：點了打開那個 Bot 的檔案頁、直接預覽那個檔案。
// 由對話頁提供（知道是哪個 Bot）；沒有提供時照舊只是程式碼文字。

/** 路徑 → 檔案頁的網址；不是 Bot 電腦裡的檔案時回 undefined。 */
export type FileLinker = (path: string) => string | undefined

export const FileLinkContext = createContext<FileLinker | null>(null)

/** 看起來是 Bot 電腦裡的一個檔案（不是資料夾、沒有空白）。 */
export function isBoxFile(text: string): boolean {
  return /^\/workspace\/\S*[^/\s]$/.test(text.trim())
}

export function botFileLinker(botId: string): FileLinker {
  return (path) => (isBoxFile(path) ? `/bots/${encodeURIComponent(botId)}/files?path=${encodeURIComponent(path.trim())}` : undefined)
}
