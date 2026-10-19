import { useRef, type ReactNode } from 'react'
import { useFocusTrap } from '../ui/useFocusTrap'

// 桌面的設定：跳出來的大視窗（背景是你原本所在的頁面）。按 ✕、Esc 或點外面就回原本的頁面。
export function SettingsModal({ onClose, children }: { onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const onKeyDown = useFocusTrap(ref, true, onClose)
  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/60 p-6"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label="設定"
        tabIndex={-1}
        onKeyDown={onKeyDown}
        data-testid="settings-modal"
        className="flex h-[min(880px,calc(100dvh-48px))] w-[min(1180px,calc(100vw-48px))] overflow-hidden rounded-card border border-divider bg-sheet shadow-dialog outline-none"
      >
        {children}
      </div>
    </div>
  )
}
