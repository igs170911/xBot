import { useCallback, useEffect, useState } from 'react'
import { ApiError, pushConfig, pushSubscribe, pushTest, pushUnsubscribe, type PushConfigOutput } from '../api/generated'
import { useToast } from '../ui/toast-context'
import { currentSubscription, deviceLabel, pushSupported, subscribePush, subscriptionKeys, unsubscribePush } from './pwa'

// 推播設定（L06）：瀏覽器訂閱＋xbot.push.subscribe／unsubscribe／test。

function errText(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}

const PUSH_ID = 'xbot.push.id'
/** 這支手機在服務那邊的訂閱 id（只是為了顯示開關狀態；拿不到也沒關係）。 */
function savedId(): string | null {
  try {
    return window.localStorage.getItem(PUSH_ID)
  } catch {
    return null
  }
}
function saveId(id: string | null) {
  try {
    if (id) window.localStorage.setItem(PUSH_ID, id)
    else window.localStorage.removeItem(PUSH_ID)
  } catch {
    // 無痕視窗：不記
  }
}

/** 推播的兩個開關（L06）：「等你的事推播到這支手機」「工作完成也通知」。 */
export function usePushSettings() {
  const toast = useToast()
  const [cfg, setCfg] = useState<PushConfigOutput | null>(null)
  const [endpoint, setEndpoint] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const supported = pushSupported()
  const load = useCallback(() => {
    pushConfig({})
      .then(setCfg)
      .catch(() => setCfg(null))
    void currentSubscription()
      .then((s) => setEndpoint(s?.endpoint ?? null))
      .catch(() => setEndpoint(null))
  }, [])
  useEffect(() => load(), [load])
  const mine = cfg && endpoint ? cfg.subscriptions.find((s) => s.id === savedId()) : undefined
  const on = !!endpoint && !!mine
  const notifyDone = !!mine?.notify_done

  const enable = async (done = notifyDone) => {
    if (!cfg) return
    setBusy(true)
    try {
      const sub = await subscribePush(cfg.public_key)
      const out = await pushSubscribe({ ...subscriptionKeys(sub), label: deviceLabel(), notify_done: done })
      saveId(out.id)
      setEndpoint(sub.endpoint)
      load()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }
  const disable = async () => {
    setBusy(true)
    try {
      const ep = await unsubscribePush()
      if (ep) await pushUnsubscribe({ endpoint: ep })
      saveId(null)
      setEndpoint(null)
      load()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }
  const test = async () => {
    try {
      const r = await pushTest({ endpoint: endpoint ?? undefined })
      toast.show({ kind: r.sent ? 'done' : 'error', text: r.sent ? '已送出測試推播' : '沒有送出去，請重新打開推播' })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }
  return {
    supported,
    on,
    notifyDone,
    busy,
    devices: cfg?.subscriptions.length ?? 0,
    setOn: (v: boolean) => void (v ? enable() : disable()),
    setNotifyDone: (v: boolean) => void enable(v),
    test,
  }
}

