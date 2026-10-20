import { Inbox, MessageCircle, Monitor } from 'lucide-react'

export { useNarrow as usePhone } from '../files/useNarrow'

// 手機版（W31）的導覽規則：哪些是分頁首頁、哪些頁自己畫頁首、返回去哪裡、從左邊緣滑動。

/**
 * 底部分頁列（L01）：對話／待我處理／辦公室。2026-10-20 依使用紀錄精簡：行事曆很少用，收進對話分頁右上的「設定與更多」；
 * 對話最常用，放第一個，從主畫面打開 App 也先到對話（launchPath）。
 */
export const TABS = [
  { to: '/chats', label: '對話', icon: MessageCircle },
  { to: '/', label: '待我處理', icon: Inbox },
  { to: '/office', label: '辦公室', icon: Monitor },
] as const

/** 手機從主畫面圖示打開 App（manifest 的 start_url 帶 source=pwa）時先到對話；點推播進來（/）照原本去待我處理。 */
export function launchPath(loc: { pathname: string; search: string }, phone: boolean): string | null {
  if (!phone || loc.pathname !== '/') return null
  return new URLSearchParams(loc.search).get('source') === 'pwa' ? '/chats' : null
}

/** 這個路徑是不是分頁的首頁（顯示分頁列、沒有返回鈕）。 */
export function isTabPath(path: string): boolean {
  return path === '/' || path === '/chats' || path === '/office'
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

