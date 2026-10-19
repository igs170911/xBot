import { useEffect, useRef, type RefObject } from 'react'

// 跟著手指走的拖拉（手機的抽屜、返回、底部 sheet）：
// 按下後先判斷方向（橫的還是直的），是這個手勢的方向才接手；move 時回報位移（0 到 size），
// 放開時位移超過 size 的 1/3，或甩得夠快（> 0.45 px/ms），就算數。

export type DragOptions = {
  /** 'x'：左右；'y'：上下。 */
  axis: 'x' | 'y'
  /** 往哪個方向拖才算：1＝往右／往下，-1＝往左／往上。 */
  dir: 1 | -1
  enabled: boolean
  /** 拖到底的距離（例：抽屜寬度）。 */
  size: () => number
  /** 這次按下能不能開始（例：要從螢幕邊緣開始、內容捲到最上面才能往下拉）。 */
  canStart?: (x: number, y: number, target: EventTarget | null) => boolean
  onMove: (px: number) => void
  onEnd: (commit: boolean) => void
}

const SLOP = 8
const FLICK = 0.45

export function useDrag(target: RefObject<HTMLElement | null> | 'document', opts: DragOptions) {
  const o = useRef(opts)
  useEffect(() => {
    o.current = opts
  })
  useEffect(() => {
    if (!opts.enabled) return
    const el: HTMLElement | Document | null = target === 'document' ? document : target.current
    if (!el) return
    let start: { x: number; y: number; t: number } | null = null
    let locked = false
    let last = { d: 0, t: 0 }
    let prev = { d: 0, t: 0 }
    const delta = (t: Touch) => (o.current.axis === 'x' ? t.clientX - start!.x : t.clientY - start!.y) * o.current.dir
    const down = (ev: Event) => {
      const e = ev as TouchEvent
      if (e.touches.length !== 1) return
      const t = e.touches[0]
      if (o.current.canStart && !o.current.canStart(t.clientX, t.clientY, e.target)) return
      start = { x: t.clientX, y: t.clientY, t: e.timeStamp }
      locked = false
      last = prev = { d: 0, t: e.timeStamp }
    }
    const move = (ev: Event) => {
      const e = ev as TouchEvent
      if (!start) return
      const t = e.touches[0]
      const along = delta(t)
      const across = Math.abs(o.current.axis === 'x' ? t.clientY - start.y : t.clientX - start.x)
      if (!locked) {
        if (across > SLOP && across > Math.abs(along)) {
          start = null // 是另一個方向的捲動
          return
        }
        if (along > SLOP) locked = true
        else if (along < -SLOP) {
          start = null
          return
        }
      }
      if (!locked) return
      if (!passive && e.cancelable) e.preventDefault()
      const d = Math.max(0, Math.min(along, o.current.size()))
      prev = last
      last = { d, t: e.timeStamp }
      o.current.onMove(d)
    }
    const up = () => {
      if (!start) return
      const was = locked
      start = null
      locked = false
      if (!was) return
      const v = (last.d - prev.d) / Math.max(1, last.t - prev.t)
      o.current.onEnd(last.d > o.current.size() / 3 || v > FLICK)
    }
    const cancel = () => {
      if (start && locked) o.current.onEnd(false)
      start = null
      locked = false
    }
    el.addEventListener('touchstart', down, { passive: true })
    // 左右的拖拉（抽屜、返回）不擋捲動：passive 的 touchmove 不用等 JS，上下捲才會順（手機回報 2026-10-19：對話上下滑超不順）。
    // 頁面本來就不會左右捲，不 preventDefault 也不會一起動。上下的（底部 sheet）要擋住內容捲動，只掛在 sheet 本身。
    const passive = o.current.axis === 'x'
    el.addEventListener('touchmove', move, { passive })
    el.addEventListener('touchend', up)
    el.addEventListener('touchcancel', cancel)
    return () => {
      el.removeEventListener('touchstart', down)
      el.removeEventListener('touchmove', move)
      el.removeEventListener('touchend', up)
      el.removeEventListener('touchcancel', cancel)
    }
  }, [opts.enabled, target])
}

/** 動畫到某個位置（transform），結束後呼叫 done。 */
export function slide(el: HTMLElement | null, transform: string, done?: () => void, ms = 220) {
  if (!el) {
    done?.()
    return
  }
  el.style.transition = `transform ${ms}ms cubic-bezier(0.2, 0.8, 0.2, 1)`
  el.style.transform = transform
  if (!done) return
  let fired = false
  const finish = () => {
    if (fired) return
    fired = true
    el.removeEventListener('transitionend', finish)
    done()
  }
  el.addEventListener('transitionend', finish)
  setTimeout(finish, ms + 60)
}
