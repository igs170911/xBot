import { useEffect, useRef, useState } from 'react'
import { MessageSquare, Plus, Trash2, X } from 'lucide-react'
import { Icon } from '../ui/Icon'
import { IconButton } from '../ui/Button'
import { cx } from '../ui/cx'
import { useFocusTrap } from '../ui/useFocusTrap'
import { slide, useDrag } from '../ui/useDrag'
import { relTime } from './format'
import type { Thread } from './ThreadMenu'

/** 從右邊緣幾 px 內開始拖才算。 */
const EDGE = 24

// 手機的對話串抽屜：從右邊滑出來（按「對話串」或從右邊緣往左拖，跟著手指走），往右拖收回去。
export function ThreadDrawer({
  botName,
  threads,
  threadId,
  open,
  onOpenChange,
  onPick,
  onNew,
  onDelete,
}: {
  botName: string
  threads: Thread[] | null
  threadId?: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onPick: (id: string) => void
  onNew: () => void
  onDelete?: () => void
}) {
  const panel = useRef<HTMLDivElement>(null)
  const shade = useRef<HTMLDivElement>(null)
  const [peek, setPeek] = useState(false)
  const close = () => onOpenChange(false)
  const onKeyDown = useFocusTrap(panel, open, close)
  const width = () => panel.current?.offsetWidth || Math.min(320, window.innerWidth * 0.85)
  const fade = (r: number, animate = true) => {
    if (!shade.current) return
    shade.current.style.transition = animate ? 'opacity 220ms ease-out' : 'none'
    shade.current.style.opacity = String(Math.max(0, Math.min(1, r)))
  }
  const closeSlide = () => {
    fade(0)
    slide(panel.current, 'translateX(100%)', close)
  }

  useEffect(() => {
    const p = panel.current
    if (!open || !p) return
    if (!p.style.transform) {
      p.style.transition = 'none'
      p.style.transform = 'translateX(100%)'
      void p.offsetWidth
    }
    slide(p, 'translateX(0)')
    fade(1)
  }, [open])

  // 從右邊緣往左拖：拉出來
  useDrag('document', {
    axis: 'x',
    dir: -1,
    enabled: !open,
    size: width,
    canStart: (x) => x >= window.innerWidth - EDGE,
    onMove: (d) => {
      setPeek(true)
      const p = panel.current
      if (p) {
        p.style.transition = 'none'
        p.style.transform = `translateX(${width() - d}px)`
      }
      fade(d / width(), false)
    },
    onEnd: (commit) => {
      if (commit) {
        onOpenChange(true)
        setPeek(false)
        return
      }
      fade(0)
      slide(panel.current, 'translateX(100%)', () => setPeek(false))
    },
  })

  // 打開時往右拖：收回去
  useDrag('document', {
    axis: 'x',
    dir: 1,
    enabled: open,
    size: width,
    onMove: (d) => {
      const p = panel.current
      if (p) {
        p.style.transition = 'none'
        p.style.transform = `translateX(${d}px)`
      }
      fade(1 - d / width(), false)
    },
    onEnd: (commit) => {
      if (commit) closeSlide()
      else {
        slide(panel.current, 'translateX(0)')
        fade(1)
      }
    },
  })

  if (!open && !peek) return null
  const pick = (fn: () => void) => () => {
    close()
    fn()
  }
  return (
    <div className="fixed inset-0 z-50" data-testid="thread-drawer">
      <div ref={shade} className="absolute inset-0 bg-black/60" style={{ opacity: 0 }} onClick={closeSlide} aria-hidden="true" />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label={`${botName}的對話串`}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        style={{ transform: open ? undefined : 'translateX(100%)' }}
        className="absolute inset-y-0 right-0 flex w-[min(320px,85vw)] flex-col border-l border-edge bg-panel pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)] shadow-dialog outline-none"
      >
        <div className="flex min-h-14 items-center gap-2 pl-4 pr-2">
          <span className="flex-1 text-title">對話串</span>
          <IconButton icon={X} label="關閉對話串" onClick={closeSlide} data-autofocus />
        </div>
        <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2">
          {(threads ?? []).map((t) => {
            const on = t.id === threadId
            return (
              <button
                key={t.id}
                type="button"
                aria-current={on ? 'true' : undefined}
                onClick={pick(() => !on && onPick(t.id))}
                className={cx('flex min-h-14 w-full items-center gap-3 rounded-menu px-3 text-left', on ? 'bg-control' : 'bg-transparent active:bg-control')}
              >
                <Icon icon={MessageSquare} size={18} className="text-ink-2" />
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-ui text-ink">{t.title || '新的對話'}</span>
                  <span className="truncate text-meta text-ink-3">
                    {relTime(t.last_message_at || t.created_at)}
                    {t.unread > 0 && ` · ${t.unread} 則未讀`}
                  </span>
                </span>
              </button>
            )
          })}
          {threads && threads.length === 0 && <p className="m-0 px-3 py-2 text-sub text-ink-3">還沒有對話串。</p>}
        </div>
        <div className="flex flex-col gap-1 border-t border-line p-2">
          <button type="button" onClick={pick(onNew)} className="flex h-12 items-center gap-3 rounded-menu bg-transparent px-3 text-left text-ui text-ink active:bg-control">
            <Icon icon={Plus} size={18} />
            新對話串
          </button>
          {threadId && onDelete && (
            <button type="button" onClick={pick(onDelete)} className="flex h-12 items-center gap-3 rounded-menu bg-transparent px-3 text-left text-ui text-danger active:bg-control">
              <Icon icon={Trash2} size={18} />
              刪除這個對話串
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
