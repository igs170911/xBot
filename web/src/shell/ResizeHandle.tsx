import type { KeyboardEvent, PointerEvent } from 'react'
import type { Store } from '../data/store'
import { useStore } from '../data/store'
import { cx } from '../ui/cx'
import { saveWidth, type PaneSize } from './panes'

/** 方向鍵一次改多少。 */
const STEP = 24

/**
 * 拖邊緣改寬度（側欄的右邊、右邊面板的左邊）。放在那一欄裡（那一欄要是 relative／sticky）。
 * 拖、方向鍵（Home／End 最窄最寬）都可以；雙擊回到預設寬度。寬度記在這個瀏覽器（panes.saveWidth）。
 * edge：把手在這一欄的哪一邊；往外拖變寬。最寬不超過視窗的一半（側欄）或 60%（右邊面板），對話留得下來。
 */
export function ResizeHandle({ size, store, edge, label }: { size: PaneSize; store: Store<number>; edge: 'left' | 'right'; label: string }) {
  const width = useStore(store)
  const dir = edge === 'right' ? 1 : -1
  const max = () => Math.max(size.min, Math.min(size.max, Math.round(window.innerWidth * (edge === 'right' ? 0.5 : 0.6))))
  const clamp = (w: number) => Math.round(Math.min(max(), Math.max(size.min, w)))

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    e.preventDefault()
    const el = e.currentTarget
    const startX = e.clientX
    const startW = store.get()
    el.setPointerCapture(e.pointerId)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    const move = (ev: globalThis.PointerEvent) => store.set(clamp(startW + dir * (ev.clientX - startX)))
    const up = () => {
      el.removeEventListener('pointermove', move)
      el.removeEventListener('pointerup', up)
      el.removeEventListener('pointercancel', up)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      saveWidth(size, store.get())
    }
    el.addEventListener('pointermove', move)
    el.addEventListener('pointerup', up)
    el.addEventListener('pointercancel', up)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const next =
      e.key === 'ArrowRight' ? width + dir * STEP : e.key === 'ArrowLeft' ? width - dir * STEP : e.key === 'Home' ? size.min : e.key === 'End' ? max() : null
    if (next === null) return
    e.preventDefault()
    const w = clamp(next)
    store.set(w)
    saveWidth(size, w)
  }

  const reset = () => {
    store.set(clamp(size.def))
    saveWidth(size, size.def)
  }

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={width}
      aria-valuemin={size.min}
      aria-valuemax={size.max}
      tabIndex={0}
      title="拖這裡改寬度，雙擊回到預設"
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      onDoubleClick={reset}
      className={cx(
        'group/handle absolute inset-y-0 z-30 flex w-2 cursor-col-resize touch-none justify-center outline-none max-md:hidden',
        edge === 'right' ? '-right-1' : '-left-1',
      )}
    >
      <span aria-hidden="true" className="h-full w-px bg-transparent transition-colors group-hover/handle:w-0.5 group-hover/handle:bg-accent/70 group-focus-visible/handle:w-0.5 group-focus-visible/handle:bg-accent" />
    </div>
  )
}
