import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react'
import type RFB from '@novnc/novnc'
import { cx } from '../ui/cx'
import { AUTO_RETRIES, BACKGROUND_PAUSE, CONNECT_TIMEOUT, KEY, keysymOf, RETRY_COOLDOWN, RETRY_DELAY, STABLE_CONNECTION, vncUrl, type VncHandle, type VncStatus } from './vnc'

type Props = {
  /** xbot.computers.screen 的 vnc 欄位（/api/v1/computers/vnc?bot=…）。 */
  path: string
  /** 接手中：可以操作（服務那端也只讓接手的人連 control）。 */
  control: boolean
  /** true：縮放到符合框框；false：原始大小可捲動。 */
  fit?: boolean
  label: string
  className?: string
  onStatus?: (s: VncStatus) => void
}

/** Bot 電腦的即時畫面（noVNC，經 XBot 服務代理）。斷線自動恢復，規則見上面的常數。 */
export const VncScreen = forwardRef<VncHandle, Props>(function VncScreen({ path, control, fit = true, label, className, onStatus }, ref) {
  const box = useRef<HTMLDivElement>(null)
  const rfb = useRef<RFB | null>(null)
  const [generation, setGeneration] = useState(0)
  const status = useRef<VncStatus>({ kind: 'connecting' })
  const report = useRef(onStatus)
  useEffect(() => {
    report.current = onStatus
  }, [onStatus])
  const attempts = useRef(0)
  const timers = useRef<number[]>([])
  const paused = useRef(false)

  const set = useCallback((s: VncStatus) => {
    status.current = s
    report.current?.(s)
  }, [])
  const clearTimers = () => {
    timers.current.forEach((t) => window.clearTimeout(t))
    timers.current = []
  }
  const later = (fn: () => void, ms: number) => {
    timers.current.push(window.setTimeout(fn, ms))
  }
  const reconnect = useCallback(() => {
    attempts.current = 0
    paused.current = false
    setGeneration((g) => g + 1)
  }, [])

  useImperativeHandle(
    ref,
    () => ({
      reconnect,
      key: (keysym, mods = []) => {
        const r = rfb.current
        if (!r) return
        mods.forEach((m) => r.sendKey(m, null, true))
        r.sendKey(keysym, null, true)
        r.sendKey(keysym, null, false)
        for (const m of mods.slice().reverse()) r.sendKey(m, null, false)
      },
      paste: (text) => {
        const r = rfb.current
        if (!r) return
        r.clipboardPasteFrom(text)
        r.sendKey(KEY.Control, 'ControlLeft', true)
        r.sendKey(KEY.v, 'KeyV', true)
        r.sendKey(KEY.v, 'KeyV', false)
        r.sendKey(KEY.Control, 'ControlLeft', false)
      },
      type: (text) => {
        const r = rfb.current
        if (!r) return
        for (const ch of text) {
          const k = keysymOf(ch)
          r.sendKey(k, null, true)
          r.sendKey(k, null, false)
        }
      },
      focus: () => rfb.current?.focus({ preventScroll: true }),
    }),
    [reconnect],
  )

  // 連線：網址、模式改了或要重新連線時重來。
  useEffect(() => {
    const target = box.current
    if (!target) return
    let alive = true
    let intentional = false
    let current: RFB | null = null
    clearTimers()
    if (!navigator.onLine) {
      set({ kind: 'offline' })
      return
    }
    set({ kind: 'connecting' })
    const fail = (reason?: string) => {
      if (!alive) return
      if (attempts.current >= AUTO_RETRIES) {
        set({ kind: 'failed', reason })
        return
      }
      const seconds = (attempts.current === 0 ? RETRY_DELAY : RETRY_COOLDOWN) / 1000
      attempts.current += 1
      set({ kind: 'retrying', attempt: attempts.current, seconds })
      later(() => alive && setGeneration((g) => g + 1), seconds * 1000)
    }
    void import('@novnc/novnc')
      .then(({ default: RFBClass }) => {
        if (!alive) return
        const r = new RFBClass(target, vncUrl(path, control), { shared: true, wsProtocols: ['binary'] })
        current = r
        rfb.current = r
        r.viewOnly = !control
        r.scaleViewport = fit
        r.clipViewport = !fit
        r.resizeSession = false
        r.focusOnClick = control
        r.background = '#111111'
        r.qualityLevel = 6
        later(() => {
          if (status.current.kind === 'connecting') {
            intentional = false
            r.disconnect()
          }
        }, CONNECT_TIMEOUT)
        r.addEventListener('connect', () => {
          if (!alive) return
          const fb = r as unknown as { _fbWidth?: number; _fbHeight?: number }
          set({ kind: 'connected', width: fb._fbWidth, height: fb._fbHeight })
          later(() => {
            if (status.current.kind === 'connected') attempts.current = 0
          }, STABLE_CONNECTION)
        })
        r.addEventListener('disconnect', () => {
          if (rfb.current === r) rfb.current = null
          if (!alive || intentional) return
          fail()
        })
      })
      .catch((err: unknown) => {
        fail(err instanceof Error ? err.message : String(err))
      })
    return () => {
      alive = false
      intentional = true
      clearTimers()
      current?.disconnect()
      if (rfb.current === current) rfb.current = null
    }
  }, [path, control, generation, set]) // eslint-disable-line react-hooks/exhaustive-deps

  // 縮放方式可以即時切換，不用重連。
  useEffect(() => {
    const r = rfb.current
    if (!r) return
    r.scaleViewport = fit
    r.clipViewport = !fit
  }, [fit])

  // 分頁在背景 15 秒就暫停串流；回來立刻接上。網路斷了等恢復再連。
  useEffect(() => {
    let pauseTimer: number | undefined
    const onVisibility = () => {
      if (document.hidden) {
        pauseTimer = window.setTimeout(() => {
          paused.current = true
          rfb.current?.disconnect()
          set({ kind: 'paused' })
        }, BACKGROUND_PAUSE)
        return
      }
      window.clearTimeout(pauseTimer)
      if (paused.current) {
        paused.current = false
        setGeneration((g) => g + 1)
      }
    }
    const onOffline = () => set({ kind: 'offline' })
    const onOnline = () => {
      attempts.current = 0
      setGeneration((g) => g + 1)
    }
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('offline', onOffline)
    window.addEventListener('online', onOnline)
    return () => {
      window.clearTimeout(pauseTimer)
      document.removeEventListener('visibilitychange', onVisibility)
      window.removeEventListener('offline', onOffline)
      window.removeEventListener('online', onOnline)
    }
  }, [set])

  return (
    <div
      ref={box}
      role="img"
      aria-label={label}
      data-testid="vnc-screen"
      data-control={control || undefined}
      className={cx('surface-dark overflow-hidden bg-[#111111] [&_canvas]:!outline-none', control ? 'cursor-default' : 'cursor-not-allowed', className)}
    />
  )
})
