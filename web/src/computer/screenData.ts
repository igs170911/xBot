import { useCallback, useEffect, useRef, useState } from 'react'
import { computersScreen, computersScreens, computersSnapshot, computersWake, hostsList, type ComputersScreenOutput, type HostsListOutput, type XBotEvent } from '../api/generated'
import { onEvent, onReconnect } from '../data/events'

export type Screen = ComputersScreenOutput
export type Host = HostsListOutput['hosts'][number]

/** 每台電腦的設定（PRD BX-11、BX-12；介面只顯示）。 */
export const BOX_SPEC = '2 CPU · 3 GB'
export const IDLE_SLEEP_MIN = 30
/** 第一次開機桌面就緒的上限（秒，limits.BoxReadyTimeout）。 */
export const BOOT_LIMIT_S = 75

/** 狀態會變的事件：接手、交還、有人在看、Bot 開始或做完工作、待我處理的卡。 */
function touches(e: XBotEvent, botId?: string): boolean {
  if (e.kind === 'inbox.created' || e.kind === 'inbox.updated') return true
  if (e.kind !== 'computer.screen' && e.kind !== 'computer.status' && e.kind !== 'task.state') return false
  return !botId || e.bot_id === botId
}

/** 不在看的分頁不輪詢。 */
function useVisiblePoll(fn: () => void, ms: number) {
  const ref = useRef(fn)
  useEffect(() => {
    ref.current = fn
  }, [fn])
  useEffect(() => {
    const t = window.setInterval(() => {
      if (!document.hidden) ref.current()
    }, ms)
    return () => window.clearInterval(t)
  }, [ms])
}

/** 一台電腦的畫面狀態（xbot.computers.screen），事件來就重讀，另外每 15 秒對一次（開機、休眠沒有事件）。 */
export function useScreen(botId: string) {
  const [screen, setScreen] = useState<Screen | null>(null)
  const [error, setError] = useState<string | null>(null)
  const reload = useCallback(
    () =>
      computersScreen({ bot_id: botId })
        .then((s) => {
          setScreen(s)
          setError(null)
        })
        .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err))),
    [botId],
  )
  useEffect(() => {
    void reload()
    const offs = [onEvent((e) => touches(e, botId) && void reload()), onReconnect(() => void reload())]
    return () => offs.forEach((f) => f())
  }, [botId, reload])
  useVisiblePoll(() => void reload(), 15000)
  return { screen, error, reload, setScreen }
}

/** 辦公室：所有電腦＋主機（分組、開機數）。 */
export function useOffice() {
  const [screens, setScreens] = useState<Screen[] | null>(null)
  const [hosts, setHosts] = useState<Host[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const reload = useCallback(() => {
    void computersScreens({})
      .then((o) => {
        setScreens(o.screens)
        setError(null)
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
    void hostsList({})
      .then((o) => setHosts(o.hosts))
      .catch(() => setHosts((h) => h ?? []))
  }, [])
  useEffect(() => {
    reload()
    const offs = [onEvent((e) => touches(e) && reload()), onReconnect(reload)]
    return () => offs.forEach((f) => f())
  }, [reload])
  useVisiblePoll(reload, 20000)
  return { screens, hosts, error, reload }
}

/** 辦公室的縮圖：每 10 秒（看得到時）擷取一張；電腦沒開機時是 null。 */
export function useSnapshot(botId: string, enabled: boolean, every = 10000) {
  const [src, setSrc] = useState<string | null>(null)
  const [stale, setStale] = useState(false)
  const load = useCallback(() => {
    if (!enabled) return
    computersSnapshot({ bot_id: botId })
      .then((o) => {
        setSrc(o.available && o.image ? `data:${o.media_type || 'image/jpeg'};base64,${o.image}` : null)
        setStale(false)
      })
      // 縮圖斷線：留著最後一張，標「重新連線中」（E03）。
      .catch(() => setStale(true))
  }, [botId, enabled])
  useEffect(() => {
    if (enabled) load()
  }, [enabled, load])
  useVisiblePoll(load, every)
  return { src: enabled ? src : null, stale }
}

/** 叫醒（開機）一台電腦（B13 xbot.computers.wake）；狀態經 computer.status 事件回來。 */
export function useWake(botId: string, done?: () => void) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const wake = useCallback(() => {
    setBusy(true)
    setError(null)
    computersWake({ bot_id: botId })
      .then(() => done?.())
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false))
  }, [botId, done])
  return { wake, busy, error }
}

/** 「3 分鐘」「1 小時」：從某個時間到現在。 */
export function since(ms: number, now = Date.now()): string {
  const m = Math.max(0, Math.round((now - ms) / 60000))
  if (m < 1) return '剛剛'
  if (m < 60) return `${m} 分鐘`
  const h = Math.round(m / 60)
  if (h < 24) return `${h} 小時`
  return `${Math.round(h / 24)} 天`
}
