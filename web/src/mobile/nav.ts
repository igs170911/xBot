import { CalendarDays, Inbox, MessageCircle, Monitor } from 'lucide-react'

export { useNarrow as usePhone } from '../files/useNarrow'

// 手機版（W31）的導覽規則：哪些是分頁首頁、哪些頁自己畫頁首、返回去哪裡、從左邊緣滑動。

/** 底部分頁列的四個分頁（L01）：待我處理／對話／辦公室／行事曆。 */
export const TABS = [
  { to: '/', label: '待我處理', icon: Inbox },
  { to: '/chats', label: '對話', icon: MessageCircle },
  { to: '/office', label: '辦公室', icon: Monitor },
  { to: '/calendar', label: '行事曆', icon: CalendarDays },
] as const

/** 這個路徑是不是分頁的首頁（顯示分頁列、沒有返回鈕）。 */
export function isTabPath(path: string): boolean {
  return path === '/' || path === '/chats' || path === '/office' || path === '/calendar'
}

/** 手機上自己畫頁首的頁面（L03 對話、L05 群組、L06 設定、L10 Bot 資料、L14 通知、L19 檔案…）；其他頁用共用的返回列。 */
export function ownsPhoneHeader(path: string): boolean {
  return (
    isTabPath(path) ||
    /^\/bots\/[^/]+(\/threads\/[^/]+|\/files|\/profile|\/call|\/voice|\/computer)?\/?$/.test(path) ||
    /^\/rooms\/[^/]+\/?$/.test(path) ||
    path.startsWith('/settings') ||
    path.startsWith('/calendar') ||
    path === '/notifications' ||
    path === '/pair' ||
    path === '/share' ||
    path === '/new/bot' ||
    path === '/new/room' ||
    path === '/welcome'
  )
}

/** 手機上按返回（或從左邊緣滑）要去哪裡：直接打開這一頁時用。 */
export function backFallback(path: string): string {
  const bot = path.match(/^\/bots\/([^/]+)\/(files|computer|profile|call|voice)/)
  if (bot) return `/bots/${bot[1]}`
  if (path.startsWith('/settings/')) return '/settings'
  if (path.startsWith('/calendar/')) return '/calendar'
  return '/chats'
}

