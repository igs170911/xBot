import { createContext, useContext, type ReactNode } from 'react'
import type { AvatarProps } from './Avatar'

export type ToastKind = 'done' | 'undo' | 'error' | 'decide' | 'info'

export type ToastInput = {
  kind?: ToastKind
  text: ReactNode
  /** 右邊的動作按鈕。 */
  action?: { label: string; onClick: () => void }
  /** 有 Bot 時左邊放它的頭像。 */
  bot?: Pick<AvatarProps, 'shape' | 'color' | 'state' | 'dot'>
  /** 毫秒；不給就照種類（一般 4 秒、出錯 7 秒、要你決定不消失）。 */
  duration?: number
}

export type ToastItem = ToastInput & { id: number }

export type ToastApi = {
  show: (t: ToastInput) => number
  dismiss: (id: number) => void
}

export const ToastContext = createContext<ToastApi | null>(null)

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast 要放在 <ToastProvider> 裡')
  return ctx
}

// 一般 4 秒、出錯 7 秒；要你決定的不會自己消失（B08）。
export function toastDuration(t: ToastInput): number | null {
  if (t.duration !== undefined) return t.duration
  if (t.kind === 'decide') return null
  if (t.kind === 'error') return 7000
  return 4000
}

// 同時最多 3 則，多的排隊。
export const MAX_VISIBLE_TOASTS = 3
