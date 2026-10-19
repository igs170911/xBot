import { useEffect } from 'react'

// 開著蓋住整頁的東西（手機的 sheet、抽屜）時，後面的頁面不能捲：
// iPhone 在 sheet 裡滑到頂或從標題列開始滑時，會改捲後面的頁面，看起來就是 sheet「滑不動」（2026-10-20 實機）。
let locks = 0
let saved = ''

export function useLockBody(on: boolean) {
  useEffect(() => {
    if (!on) return
    if (locks++ === 0) {
      saved = document.documentElement.style.overflow
      document.documentElement.style.overflow = 'hidden'
    }
    return () => {
      if (--locks === 0) document.documentElement.style.overflow = saved
    }
  }, [on])
}
