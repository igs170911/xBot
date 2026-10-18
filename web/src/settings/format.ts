import { ApiError } from '../api/generated'

/** 操作錯誤的白話訊息。 */
export function errText(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'pending_approval') return '已送到「待我處理」，你核准後才會生效。'
    return err.message
  }
  return err instanceof Error ? err.message : String(err)
}

export function fieldError(err: unknown, field: string): string | undefined {
  return err instanceof ApiError ? err.fields?.find((f) => f.field === field)?.message : undefined
}

export function isPending(err: unknown): boolean {
  return err instanceof ApiError && err.code === 'pending_approval'
}

/** 美元：小額多留幾位。 */
export function usd(v: number): string {
  if (v === 0) return 'US$0'
  if (Math.abs(v) < 0.01) return 'US$' + v.toFixed(4)
  if (Math.abs(v) < 100) return 'US$' + v.toFixed(2)
  return 'US$' + Math.round(v).toLocaleString('en-US')
}

/** token 數：1.2k、3.4M。 */
export function tokens(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 0 : 1) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(n >= 1e4 ? 0 : 1) + 'k'
  return String(n)
}

export function pct(v: number): string {
  return Math.round(v * 100) + '%'
}
