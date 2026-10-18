import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { CircleAlert, CircleCheck, Info, X } from 'lucide-react'
import { Avatar } from './Avatar'
import { Icon } from './Icon'
import { cx } from './cx'
import { MAX_VISIBLE_TOASTS, ToastContext, toastDuration, type ToastApi, type ToastInput, type ToastItem } from './toast-context'

const KIND_ICON = {
  done: { icon: CircleCheck, color: 'var(--color-success)' },
  undo: { icon: CircleCheck, color: 'var(--color-success)' },
  error: { icon: CircleAlert, color: 'var(--color-danger)' },
  info: { icon: Info, color: 'var(--color-accent-ink)' },
  decide: { icon: CircleAlert, color: 'var(--color-warning)' },
}

export type ToastViewProps = ToastInput & { onClose?: () => void; className?: string }

// 單則提示：#262626、圓角 14、陰影；左圖示或頭像、右邊一個動作與關閉（N03、B08）。
export function ToastView({ kind = 'info', text, action, bot, onClose, className }: ToastViewProps) {
  const k = KIND_ICON[kind]
  return (
    <div
      role={kind === 'error' ? 'alert' : 'status'}
      className={cx('flex min-h-12 w-full items-center gap-2.5 rounded-group bg-card py-2 pl-3.5 pr-2 text-ui shadow-pop', className)}
    >
      {bot ? (
        <Avatar {...bot} size={28} ring="#262626" />
      ) : (
        <Icon icon={k.icon} color={k.color} />
      )}
      <span className="min-w-0 flex-1">{text}</span>
      {action && (
        <button
          type="button"
          onClick={action.onClick}
          className={cx(
            'relative h-8 flex-none rounded-full px-2.5 text-ui-sm touch-target',
            kind === 'decide' ? 'bg-ink text-on-ink' : 'bg-transparent text-accent-ink hover:bg-control',
          )}
        >
          {action.label}
        </button>
      )}
      {onClose && (
        <button
          type="button"
          aria-label="關閉提示"
          onClick={onClose}
          className="relative flex size-[30px] flex-none items-center justify-center rounded-seg bg-transparent text-ink-3 touch-target hover:bg-control hover:text-ink"
        >
          <Icon icon={X} size={16} />
        </button>
      )}
    </div>
  )
}

function TimedToast({ item, onClose }: { item: ToastItem; onClose: (id: number) => void }) {
  const ms = toastDuration(item)
  useEffect(() => {
    if (ms === null) return
    const t = setTimeout(() => onClose(item.id), ms)
    return () => clearTimeout(t)
  }, [item.id, ms, onClose])
  return <ToastView {...item} onClose={() => onClose(item.id)} className="animate-toast-in" />
}

// 提示的容器：底部置中，最多同時 3 則，多的排隊。
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([])
  const next = useRef(1)
  const dismiss = useCallback((id: number) => setItems((xs) => xs.filter((x) => x.id !== id)), [])
  const show = useCallback((t: ToastInput) => {
    const id = next.current++
    setItems((xs) => [...xs, { ...t, id }])
    return id
  }, [])
  const api = useMemo<ToastApi>(() => ({ show, dismiss }), [show, dismiss])
  const visible = items.slice(0, MAX_VISIBLE_TOASTS)
  return (
    <ToastContext.Provider value={api}>
      {children}
      <div
        aria-live="polite"
        className="pointer-events-none fixed inset-x-0 bottom-4 z-[60] flex flex-col items-center gap-2 px-4 pb-[env(safe-area-inset-bottom)]"
      >
        {visible.map((t) => (
          <div key={t.id} className="pointer-events-auto w-full max-w-[430px]">
            <TimedToast item={t} onClose={dismiss} />
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}
