import { useEffect, useLayoutEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { useStore } from '../data/store'
import { Button } from '../ui/Button'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'
import { endTour, onboardStore, startTour, tourStep } from './state'

// 導覽聚光燈（A10、ON-4）：三站 —— 輸入框、電腦按鈕（看畫面與接手）、待我處理（核准卡）。
// 其他區域變暗，目標本身亮著並有藍色光圈；提示框貼在目標旁邊，視窗大小改變或捲動時跟著重新對齊。
// 寬度 ≤ 640 時提示框固定在底部成為 sheet。直接點目標也算下一步；Esc 跳過。看完或跳過會記住。

export const TOUR_STOPS = [
  { id: 'composer', title: '在這裡交代事情', body: '像跟同事說話一樣打字。Bot 會自己做，或分給別的 Bot。' },
  {
    id: 'computer',
    title: '看它的電腦，需要時接手',
    body: '每個 Bot 有自己的電腦。按這裡看它正在做什麼；要登入或輸入驗證碼時，按「接手」自己操作，做完再交還給它。',
  },
  {
    id: 'inbox',
    title: '要你決定的事都在這裡',
    body: '付款、對外發送、刪除這類事，Bot 會先送一張核准卡到「待我處理」。按「允許這次」或「退回」就好；隊友說可以不算數。',
  },
] as const

type Rect = { top: number; left: number; width: number; height: number }

function find(id: string): HTMLElement | null {
  const all = Array.from(document.querySelectorAll<HTMLElement>(`[data-tour="${id}"]`))
  return all.find((el) => el.getClientRects().length > 0 && el.offsetWidth > 0) ?? null
}

function useRect(id: string | null): Rect | null {
  const [rect, setRect] = useState<Rect | null>(null)
  useLayoutEffect(() => {
    if (!id) return
    let raf = 0
    const measure = () => {
      const el = find(id)
      const r = el?.getBoundingClientRect()
      setRect((old) => {
        if (!r) return null
        if (old && old.top === r.top && old.left === r.left && old.width === r.width && old.height === r.height) return old
        return { top: r.top, left: r.left, width: r.width, height: r.height }
      })
    }
    const loop = () => {
      measure()
      raf = requestAnimationFrame(loop)
    }
    loop()
    return () => cancelAnimationFrame(raf)
  }, [id])
  return id ? rect : null
}

function useNarrowTour() {
  const q = '(max-width: 640px)'
  const [narrow, setNarrow] = useState(() => typeof window.matchMedia === 'function' && window.matchMedia(q).matches)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const m = window.matchMedia(q)
    const on = () => setNarrow(m.matches)
    m.addEventListener?.('change', on)
    return () => m.removeEventListener?.('change', on)
  }, [])
  return narrow
}

const PAD = 6

export function Tour() {
  const toast = useToast()
  const step = useStore(onboardStore).tour
  const stop = step === null ? null : TOUR_STOPS[step]
  const rect = useRect(stop?.id ?? null)
  const narrow = useNarrowTour()

  const finish = (skipped: boolean) => {
    endTour()
    toast.show({ kind: 'done', text: skipped ? '已跳過導覽' : '導覽看完了', action: { label: '再看一次', onClick: startTour } })
  }
  const next = () => {
    if (step === null) return
    if (step + 1 >= TOUR_STOPS.length) finish(false)
    else tourStep(step + 1)
  }

  useEffect(() => {
    if (step === null) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') finish(true)
    }
    // 直接點目標：視為下一步
    const onClick = (e: MouseEvent) => {
      const el = stop && find(stop.id)
      if (el && e.target instanceof Node && el.contains(e.target)) next()
    }
    window.addEventListener('keydown', onKey)
    document.addEventListener('click', onClick, true)
    return () => {
      window.removeEventListener('keydown', onKey)
      document.removeEventListener('click', onClick, true)
    }
  })

  if (step === null || !stop) return null
  const last = step + 1 === TOUR_STOPS.length
  const hole = rect ? { x: rect.left - PAD, y: rect.top - PAD, w: rect.width + PAD * 2, h: rect.height + PAD * 2 } : null
  const vw = window.innerWidth
  const vh = window.innerHeight
  // 提示框：目標在下半部就放上面，否則放下面；左右貼齊目標，不超出視窗。
  const above = hole ? hole.y + hole.h / 2 > vh / 2 : false
  const width = 320
  const left = hole ? Math.min(Math.max(12, hole.x), vw - width - 12) : 12
  // 目標在左邊窄欄（側欄）：放在它右邊，不蓋住側欄
  const side = hole && hole.x + hole.w < vw / 3 && hole.x + hole.w + 12 + width < vw
  const box = narrow || !hole ? null : side ? { left: hole.x + hole.w + 12, top: Math.max(12, hole.y) } : above ? { left, bottom: vh - hole.y + 12 } : { left, top: hole.y + hole.h + 12 }

  return createPortal(
    <div className="pointer-events-none fixed-screen z-[70]" data-testid="tour">
      {/* 只有暗的地方擋點擊；洞裡的點擊交給底下的目標（直接點目標＝下一步） */}
      <svg className="pointer-events-none absolute inset-0 size-full" aria-hidden="true">
        <path
          pointerEvents="auto"
          fillRule="evenodd"
          fill="rgba(0,0,0,0.62)"
          d={`M0 0H${vw}V${vh}H0Z` + (hole ? ` M${hole.x + 14} ${hole.y}H${hole.x + hole.w - 14}Q${hole.x + hole.w} ${hole.y} ${hole.x + hole.w} ${hole.y + 14}V${hole.y + hole.h - 14}Q${hole.x + hole.w} ${hole.y + hole.h} ${hole.x + hole.w - 14} ${hole.y + hole.h}H${hole.x + 14}Q${hole.x} ${hole.y + hole.h} ${hole.x} ${hole.y + hole.h - 14}V${hole.y + 14}Q${hole.x} ${hole.y} ${hole.x + 14} ${hole.y}Z` : '')}
        />
      </svg>
      {hole && (
        <span
          aria-hidden="true"
          data-testid="tour-ring"
          className="pointer-events-none absolute rounded-[14px] border-2 border-accent shadow-[0_0_0_4px_rgba(16,132,254,0.3)]"
          style={{ left: hole.x, top: hole.y, width: hole.w, height: hole.h }}
        />
      )}
      <div
        role="dialog"
        aria-label={`導覽 ${step + 1} / ${TOUR_STOPS.length}：${stop.title}`}
        className={cx(
          'pointer-events-auto absolute flex flex-col gap-1.5 border border-line bg-card px-4 pb-3.5 pt-3 shadow-dialog',
          box ? 'w-[320px] rounded-group' : 'inset-x-0 bottom-0 rounded-t-card pb-[max(14px,env(safe-area-inset-bottom))]',
        )}
        style={box ?? undefined}
      >
        <span className="text-meta text-ink-2">
          {step + 1} / {TOUR_STOPS.length}
        </span>
        <h2 className="m-0 text-[16px]">{stop.title}</h2>
        <p className="m-0 text-ui-sm text-ink-2">{stop.body}</p>
        {!hole && stop.id === 'inbox' && <p className="m-0 text-meta text-ink-3">在左上角的選單裡。</p>}
        <div className="mt-1.5 flex items-center justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => finish(true)} className="text-ink-2">
            跳過導覽
          </Button>
          <Button variant="primary" size="sm" onClick={next} data-autofocus>
            {last ? '完成' : '下一步'}
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
