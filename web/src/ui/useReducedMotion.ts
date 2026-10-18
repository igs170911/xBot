import { useSyncExternalStore } from 'react'

// 系統的「減少動態效果」（PRD §8）。CSS 動畫在 index.css 統一關掉；SVG 的 <animate…>（頭像眨眼）CSS 管不到，用這個判斷。
const QUERY = '(prefers-reduced-motion: reduce)'

function media(): MediaQueryList | null {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(QUERY) : null
}

function subscribe(fn: () => void) {
  const m = media()
  m?.addEventListener?.('change', fn)
  return () => m?.removeEventListener?.('change', fn)
}

export function useReducedMotion(): boolean {
  return useSyncExternalStore(subscribe, () => media()?.matches ?? false, () => false)
}
