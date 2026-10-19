import { useCallback, useMemo, useRef, type RefObject } from 'react'

// 對話跟著捲到底：新內容進來時，本來就在最下面才跟著捲到底。
// 手指放在畫面上、或放開後還在慣性滑動時不去動它：iPhone 上一設定 scrollTop 就會打斷手指的捲動，
// Bot 一邊回覆一邊更新，手指往上滑不到 80px 就一直被拉回最底下，像被鎖住（2026-10-20 手機回報）。

/** 離底部這麼近，就算「在最下面」。 */
const NEAR = 80
/** 最後一次手指或慣性捲動後這麼久內，都算還在捲。 */
const SETTLE = 150

export function useFollowBottom(scrollerRef: RefObject<HTMLElement | null>) {
  const stickRef = useRef(true)
  const userRef = useRef({ touching: false, at: 0 })
  /** 新內容進來時呼叫：在最下面、手指也沒在捲，才捲到底。 */
  const follow = useCallback(() => {
    const el = scrollerRef.current
    const u = userRef.current
    if (el && stickRef.current && !u.touching && Date.now() - u.at >= SETTLE) el.scrollTop = el.scrollHeight
  }, [scrollerRef])
  const handlers = useMemo(() => {
    const release = () => {
      userRef.current = { touching: false, at: Date.now() }
    }
    return {
      onScroll() {
        const el = scrollerRef.current
        if (!el) return
        const u = userRef.current
        if (u.touching || Date.now() - u.at < SETTLE) u.at = Date.now() // 慣性還在滑
        stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR
      },
      onTouchStart() {
        userRef.current = { touching: true, at: Date.now() }
      },
      onTouchEnd: release,
      onTouchCancel: release,
      onWheel() {
        userRef.current.at = Date.now()
      },
    }
  }, [scrollerRef])
  return { stickRef, follow, handlers }
}
