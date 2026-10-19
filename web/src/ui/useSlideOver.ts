import { useEffect, useState } from 'react'
import { useDrag } from './useDrag'

// 從左邊或右邊滑出來的抽屜（手機的側欄、對話串）：位置只由 React 決定，不會和動畫搶。
//   - 拖的時候：跟著手指（offset，0＝收起、size＝全開）
//   - 放開或按按鈕：動畫到全開或收起；收起動畫跑完才把它拿掉
// 高負載時（例：手機很忙）用命令式動畫會和重畫打架，抽屜停在半路（2026-10-19 e2e 偶發）。

const EDGE = 24
const MS = 220

export function useSlideOver({
  side,
  open,
  setOpen,
  enabled,
  size,
}: {
  side: 'left' | 'right'
  open: boolean
  setOpen: (open: boolean) => void
  /** 這一頁能不能用手勢拉出來／收回去。 */
  enabled: boolean
  /** 抽屜寬度。 */
  size: () => number
}) {
  // drag：拖的時候抽屜露出來多少（px）；null＝沒在拖
  const [drag, setDrag] = useState<number | null>(null)
  // 收起動畫還沒跑完前，抽屜要留著
  const [leaving, setLeaving] = useState(false)
  const [prev, setPrev] = useState(open)
  const [entered, setEntered] = useState(open)
  if (prev !== open) {
    setPrev(open)
    if (!open) {
      setLeaving(true)
      setEntered(false)
    }
  }
  useEffect(() => {
    if (!leaving) return
    const t = setTimeout(() => setLeaving(false), MS + 40)
    return () => clearTimeout(t)
  }, [leaving])

  const dirOpen = side === 'left' ? 1 : -1
  // 拉出來
  useDrag('document', {
    axis: 'x',
    dir: dirOpen,
    enabled: enabled && !open,
    size,
    canStart: (x) => (side === 'left' ? x <= EDGE : x >= window.innerWidth - EDGE),
    onMove: (d) => setDrag(d),
    onEnd: (commit) => {
      setDrag(null)
      if (commit) {
        setEntered(true) // 從手指放開的位置接著滑到底，不要先跳回收起
        setOpen(true)
      } else setLeaving(true)
    },
  })
  // 收回去
  useDrag('document', {
    axis: 'x',
    dir: (-dirOpen) as 1 | -1,
    enabled: enabled && open,
    size,
    onMove: (d) => setDrag(size() - d),
    onEnd: (commit) => {
      setDrag(null)
      if (commit) setOpen(false)
    },
  })

  // 按按鈕打開時先畫在收起的位置，下一格再滑進來（不然沒有動畫）
  useEffect(() => {
    if (!open) return
    const f = requestAnimationFrame(() => setEntered(true))
    return () => cancelAnimationFrame(f)
  }, [open])

  const mounted = open || drag !== null || leaving
  const w = size()
  const shown = drag !== null ? drag : open && entered ? w : 0 // 露出來多少
  const hidden = side === 'left' ? `translateX(${shown - w}px)` : `translateX(${w - shown}px)`
  return {
    mounted,
    /** 收起中、還在拉的時候：對點擊與螢幕閱讀器都當成不在（不會和頁面上的同名連結重複）。 */
    hidden: !open,
    panelStyle: { transform: hidden, transition: drag !== null ? 'none' : `transform ${MS}ms cubic-bezier(0.2, 0.8, 0.2, 1)` },
    shadeStyle: { opacity: w ? Math.max(0, Math.min(1, shown / w)) : 0, transition: drag !== null ? 'none' : `opacity ${MS}ms ease-out` },
  }
}
