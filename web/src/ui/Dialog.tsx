import { useId, useRef, type CSSProperties, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { cx } from './cx'
import { Button } from './Button'
import { useFocusTrap } from './useFocusTrap'
import { slide, useDrag } from './useDrag'

export type DialogProps = {
  open: boolean
  onClose: () => void
  title: ReactNode
  description?: ReactNode
  children?: ReactNode
  /** 底部按鈕列（靠右）。 */
  actions?: ReactNode
  /** alertdialog：確認危險動作時用。 */
  role?: 'dialog' | 'alertdialog'
  /** 寬度，預設 480（N01：對話框 480–980）。 */
  width?: number
  /** 處理中時不能按 Esc 或點背景關閉。 */
  busy?: boolean
}

// 對話框：#111111、1px #262626 框、圓角 18、深陰影（B08）。
// 開啟時把焦點移進來、Tab 在裡面循環、Esc 關閉、關閉後焦點回到原本的按鈕。
export function Dialog({ open, onClose, title, description, children, actions, role = 'dialog', width = 480, busy }: DialogProps) {
  const ref = useRef<HTMLDivElement>(null)
  const titleId = useId()
  const descId = useId()
  const onKeyDown = useFocusTrap(ref, open, () => {
    if (!busy) onClose()
  })

  // 手機的 sheet：往下拖就關（內容捲在最上面時才接手，避免和捲動打架）
  const sheet = open && !busy && typeof window !== 'undefined' && window.matchMedia?.('(max-width: 767px)').matches
  useDrag(ref, {
    axis: 'y',
    dir: 1,
    enabled: !!sheet,
    size: () => ref.current?.offsetHeight ?? 400,
    canStart: () => (ref.current?.scrollTop ?? 0) <= 0,
    onMove: (d) => {
      const el = ref.current
      if (!el) return
      el.style.transition = 'none'
      el.style.transform = `translateY(${d}px)`
    },
    onEnd: (commit) => {
      if (commit) slide(ref.current, 'translateY(100%)', onClose)
      else slide(ref.current, 'translateY(0)')
    },
  })

  if (!open) return null

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 max-md:items-end max-md:p-0"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose()
      }}
    >
      <div
        ref={ref}
        role={role}
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        data-sheet="1"
        className="flex max-h-[calc(100dvh-32px)] w-full max-w-[var(--dialog-w)] flex-col gap-2 overflow-auto overscroll-y-contain rounded-card border border-divider bg-panel pb-[18px] pl-[22px] pr-5 pt-5 shadow-dialog outline-none max-md:max-h-[92dvh] max-md:max-w-none max-md:rounded-b-none max-md:border-x-0 max-md:border-b-0 max-md:px-4 max-md:pb-[max(18px,env(safe-area-inset-bottom))] max-md:pt-2.5"
        style={{ '--dialog-w': `${width}px` } as CSSProperties}
      >
        {/* 手機：從底部拉出的 sheet（L07、L11），上面一條把手 */}
        <span aria-hidden="true" className="mx-auto mb-1 hidden h-[5px] w-9 flex-none rounded-full bg-hover max-md:block" />
        <h2 id={titleId} className="m-0 text-title">
          {title}
        </h2>
        {description && (
          <p id={descId} className="m-0 text-ui-sm text-ink-2">
            {description}
          </p>
        )}
        {children}
        {actions && <div className={cx('mt-2.5 flex flex-wrap justify-end gap-2')}>{actions}</div>}
      </div>
    </div>,
    document.body,
  )
}

export type ConfirmDialogProps = {
  open: boolean
  title: ReactNode
  description: ReactNode
  confirmText: string
  cancelText?: string
  /** 危險動作：確認鈕用實心紅（只有這裡用）。 */
  danger?: boolean
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
}

// 確認對話框：說清楚什麼會保留、能不能復原；按下後顯示「處理中…」並停用取消（B08）。
export function ConfirmDialog({ open, title, description, confirmText, cancelText = '取消', danger, busy, onConfirm, onCancel }: ConfirmDialogProps) {
  return (
    <Dialog
      open={open}
      onClose={onCancel}
      title={title}
      description={description}
      role="alertdialog"
      busy={busy}
      actions={
        <>
          <Button variant="secondary" onClick={onCancel} disabled={busy} data-autofocus>
            {cancelText}
          </Button>
          <Button variant={danger ? 'danger-solid' : 'primary'} onClick={onConfirm} loading={busy}>
            {busy ? '處理中…' : confirmText}
          </Button>
        </>
      }
    />
  )
}
