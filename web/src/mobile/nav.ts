import { useEffect, useRef, type RefObject } from 'react'
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

/**
 * 從螢幕左邊緣往右滑：返回上一頁（MB-1）。分頁首頁沒有上一頁，改成打開側欄。
 * 手指跟著移動時，畫面一起往右移，放開超過門檻才算數。
 */
export function useEdgeSwipe(target: RefObject<HTMLElement | null>, enabled: boolean, onSwipe: () => void) {
  const cb = useRef(onSwipe)
  useEffect(() => {
    cb.current = onSwipe
  }, [onSwipe])
  useEffect(() => {
    if (!enabled) return
    let start: { x: number; y: number } | null = null
    let dx = 0
    let locked = false
    const el = () => target.current
    const reset = () => {
      const e = el()
      if (e) {
        e.style.transition = 'transform 180ms ease-out'
        e.style.transform = ''
      }
      start = null
      dx = 0
      locked = false
    }
    const down = (ev: TouchEvent) => {
      const t = ev.touches[0]
      if (ev.touches.length !== 1 || t.clientX > EDGE) return
      start = { x: t.clientX, y: t.clientY }
      dx = 0
      locked = false
    }
    const move = (ev: TouchEvent) => {
      if (!start) return
      const t = ev.touches[0]
      const x = t.clientX - start.x
      const y = Math.abs(t.clientY - start.y)
      if (!locked) {
        if (y > 12 && y > x) {
          start = null // 是往上下捲，不是返回
          return
        }
        if (x > 10) locked = true
      }
      if (!locked) return
      dx = Math.max(0, x)
      const e = el()
      if (e) {
        e.style.transition = 'none'
        e.style.transform = `translateX(${Math.min(dx, window.innerWidth) * 0.6}px)`
      }
    }
    const up = () => {
      if (!start) return
      const done = locked && dx > THRESHOLD
      reset()
      if (done) cb.current()
    }
    document.addEventListener('touchstart', down, { passive: true })
    document.addEventListener('touchmove', move, { passive: true })
    document.addEventListener('touchend', up)
    document.addEventListener('touchcancel', reset)
    return () => {
      document.removeEventListener('touchstart', down)
      document.removeEventListener('touchmove', move)
      document.removeEventListener('touchend', up)
      document.removeEventListener('touchcancel', reset)
    }
  }, [enabled, target])
}

/** 從左邊緣幾 px 內開始滑才算（避開頁面本身的左右滑動）。 */
const EDGE = 24
/** 滑多遠放開才算返回。 */
const THRESHOLD = 80
