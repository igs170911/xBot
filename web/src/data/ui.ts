import { useEffect, useState } from 'react'
import { createStore } from './store'

/** 「新增 Bot」對話框開著沒（側欄兩處都能打開，所以放全域）。 */
export const newBotDialog = createStore(false)
export const openNewBot = () => newBotDialog.set(true)

/** 手機的側欄抽屜開著沒（L01 的吉祥物、分頁首頁從左邊緣滑都能打開）。 */
export const drawerStore = createStore(false)

// 桌面側欄縮成只有圖示（記在這個瀏覽器；讀不到 localStorage 時就是展開）。
const RAIL_KEY = 'xbot.sidebar.rail'
function readRail(): boolean {
  try {
    return window.localStorage.getItem(RAIL_KEY) === '1'
  } catch {
    return false
  }
}
export const railStore = createStore(readRail())
export function setRail(on: boolean) {
  railStore.set(on)
  try {
    window.localStorage.setItem(RAIL_KEY, on ? '1' : '0')
  } catch {
    // 私密視窗等：只在這次有效
  }
}

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

/** 側欄正在拖的 Bot（拖到團隊分區＝換團隊，拖到群組＝加進群組；2026-10-20）。 */
export const draggingBotStore = createStore<{ id: string; teamId: string } | null>(null)
