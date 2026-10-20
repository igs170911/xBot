import { useRef } from 'react'
import { Activity, MessageSquare, Plus, Trash2, X } from 'lucide-react'
import { Icon } from '../ui/Icon'
import { IconButton } from '../ui/Button'
import { cx } from '../ui/cx'
import { useFocusTrap } from '../ui/useFocusTrap'
import { useSlideOver } from '../ui/useSlideOver'
import { relTime } from './format'
import type { Thread } from './ThreadMenu'

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
  onProgress,
}: {
  botName: string
  threads: Thread[] | null
  threadId?: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onPick: (id: string) => void
  onNew: () => void
  /** 刪除某一條對話串（每一條右邊的垃圾桶；最下面的「刪除這個對話串」刪目前這條）。 */
  onDelete?: (t: Thread) => void
  /** 目前進度（手機頁首的頭像改成打開 Bot 資料，進度放在這裡）。 */
  onProgress?: () => void
}) {
  const panel = useRef<HTMLDivElement>(null)
  const close = () => onOpenChange(false)
  const onKeyDown = useFocusTrap(panel, open, close)
  // 從右邊緣往左拉出來、打開時往右拖收回；位置由 useSlideOver 決定
  const slide = useSlideOver({ side: 'right', open, setOpen: onOpenChange, enabled: true, size: () => Math.min(320, window.innerWidth * 0.85) })

  const current = threads?.find((t) => t.id === threadId)
  if (!slide.mounted) return null
  const pick = (fn: () => void) => () => {
    close()
    fn()
  }
  return (
    <div className="fixed-screen z-50" data-testid="thread-drawer" aria-hidden={slide.hidden || undefined} inert={slide.hidden || undefined}>
      <div className="absolute inset-0 bg-black/60" style={slide.shadeStyle} onClick={close} aria-hidden="true" />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label={`${botName}的對話串`}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        style={slide.panelStyle}
        className="absolute inset-y-0 right-0 flex w-[min(320px,85vw)] flex-col border-l border-edge bg-panel pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)] shadow-dialog outline-none"
      >
        <div className="flex min-h-14 items-center gap-2 pl-4 pr-2">
          <span className="flex-1 text-title">對話串</span>
          <IconButton icon={X} label="關閉對話串" onClick={close} data-autofocus />
        </div>
        <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto overscroll-y-contain px-2">
          {(threads ?? []).map((t) => {
            const on = t.id === threadId
            return (
              <div key={t.id} className={cx('flex items-center rounded-menu', on && 'bg-control')}>
                <button
                  type="button"
                  aria-current={on ? 'true' : undefined}
                  onClick={pick(() => !on && onPick(t.id))}
                  className="flex min-h-14 min-w-0 flex-1 items-center gap-3 rounded-menu bg-transparent px-3 text-left active:bg-control"
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
                {onDelete && (
                  <button
                    type="button"
                    aria-label={`刪除「${t.title || '新的對話'}」`}
                    onClick={pick(() => onDelete(t))}
                    className="flex size-11 flex-none items-center justify-center rounded-menu bg-transparent text-ink-3 active:bg-control active:text-danger"
                  >
                    <Icon icon={Trash2} size={18} />
                  </button>
                )}
              </div>
            )
          })}
          {threads && threads.length === 0 && <p className="m-0 px-3 py-2 text-sub text-ink-3">還沒有對話串。</p>}
        </div>
        <div className="flex flex-col gap-1 border-t border-line p-2">
          {onProgress && (
            <button type="button" onClick={pick(onProgress)} className="flex h-12 items-center gap-3 rounded-menu bg-transparent px-3 text-left text-ui text-ink active:bg-control">
              <Icon icon={Activity} size={18} />
              目前進度
            </button>
          )}
          <button type="button" onClick={pick(onNew)} className="flex h-12 items-center gap-3 rounded-menu bg-transparent px-3 text-left text-ui text-ink active:bg-control">
            <Icon icon={Plus} size={18} />
            新對話串
          </button>
          {current && onDelete && (
            <button type="button" onClick={pick(() => onDelete(current))} className="flex h-12 items-center gap-3 rounded-menu bg-transparent px-3 text-left text-ui text-danger active:bg-control">
              <Icon icon={Trash2} size={18} />
              刪除這個對話串
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
