import { useEffect, useState } from 'react'
import { createStore } from './store'

/** 「新增 Bot」對話框開著沒（側欄兩處都能打開，所以放全域）。 */
export const newBotDialog = createStore(false)
export const openNewBot = () => newBotDialog.set(true)

/** 每秒更新一次的現在時間（只在需要時開計時器）。 */
export function useNow(on: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!on) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [on])
  return now
}

/** 訊息之間的時間標籤：「今天 下午 2:02」「昨天 上午 9:00」「10 月 15 日 下午 5:40」。 */
export function stampLabel(ms: number, now = Date.now()): string {
  const d = new Date(ms)
  const today = new Date(now)
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const days = Math.round((startOf(today) - startOf(d)) / 86_400_000)
  const day = days === 0 ? '今天' : days === 1 ? '昨天' : `${d.getMonth() + 1} 月 ${d.getDate()} 日`
  const h = d.getHours()
  const part = h < 12 ? '上午' : '下午'
  const h12 = h % 12 === 0 ? 12 : h % 12
  return `${day} ${part} ${h12}:${String(d.getMinutes()).padStart(2, '0')}`
}
