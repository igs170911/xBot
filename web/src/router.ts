import { useSyncExternalStore, type MouseEvent } from 'react'

// 很小的前端路由：History API＋popstate。頁面少，先不裝 react-router。
const listeners = new Set<() => void>()

function subscribe(fn: () => void) {
  listeners.add(fn)
  window.addEventListener('popstate', fn)
  return () => {
    listeners.delete(fn)
    window.removeEventListener('popstate', fn)
  }
}

export function navigate(to: string, opts: { replace?: boolean } = {}) {
  if (to === window.location.pathname + window.location.search) return
  if (opts.replace) window.history.replaceState(null, '', to)
  else window.history.pushState(null, '', to)
  listeners.forEach((fn) => fn())
}

export function usePath(): string {
  return useSyncExternalStore(subscribe, () => window.location.pathname, () => '/')
}

/** 網址的查詢字串（?a=b），跟著 navigate 與上一頁更新。 */
export function useSearch(): URLSearchParams {
  const raw = useSyncExternalStore(subscribe, () => window.location.search, () => '')
  return new URLSearchParams(raw)
}

// 給 <a href> 用：一般點擊交給前端路由，Ctrl／中鍵照瀏覽器原本的行為。
export function linkClick(to: string, after?: () => void) {
  return (e: MouseEvent<HTMLAnchorElement>) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    e.preventDefault()
    navigate(to)
    after?.()
  }
}
