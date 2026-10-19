import { useCallback, useEffect, useMemo, useRef, type RefObject } from 'react'

// 對話跟著捲到底：新內容進來時，本來就在最下面才跟著捲到底。
// 手指放在畫面上、或放開後還在慣性滑動時不去動它：iPhone 上一設定 scrollTop 就會打斷手指的捲動，
// Bot 一邊回覆一邊更新，手指往上滑不到 80px 就一直被拉回最底下，像被鎖住（2026-10-20 手機回報）。
// 內容或對話框的高度變了（訊息晚一點才排好、程式碼區塊載入、鍵盤彈出）也跟著捲：
// 只在訊息數變了時捲，打開對話時常停在最新一則上面一點。

/** 離底部這麼近，就算「在最下面」。 */
const NEAR = 80
/** 手指或滾輪捲的時候，離底部超過這麼多就不跟了：往上看一點點也算，不然一放開又被下一段字拉回去。 */
const USER_NEAR = 24
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
  const observerRef = useRef<ResizeObserver | null>(null)
  useEffect(() => {
    const el = scrollerRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    observerRef.current ??= new ResizeObserver(() => follow())
    observerRef.current.observe(el)
    for (const c of el.children) observerRef.current.observe(c) // 重複 observe 同一個沒關係
  })
  useEffect(() => () => observerRef.current?.disconnect(), [])
  const handlers = useMemo(() => {
    const release = () => {
      userRef.current = { touching: false, at: Date.now() }
    }
    return {
      onScroll() {
        const el = scrollerRef.current
        if (!el) return
        const u = userRef.current
        const byUser = u.touching || Date.now() - u.at < SETTLE
        if (byUser) u.at = Date.now() // 慣性還在滑
        // 捲回最下面就再跟；其他捲動（拖捲軸、鍵盤）照舊用寬一點的範圍
        stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < (byUser ? USER_NEAR : NEAR)
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
