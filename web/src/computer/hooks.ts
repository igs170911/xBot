import { useCallback, useEffect, useState } from 'react'
import { CloudOff, LoaderCircle, MonitorOff, Moon, TriangleAlert } from 'lucide-react'
import { computersHandback, computersTakeover } from '../api/generated'
import { useToast } from '../ui/toast-context'
import { BOOT_LIMIT_S, type Screen } from './screenData'

export function useNarrow(q = '(max-width: 767px)'): boolean {
  const [narrow, setNarrow] = useState(() => typeof window.matchMedia === 'function' && window.matchMedia(q).matches)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const m = window.matchMedia(q)
    const on = () => setNarrow(m.matches)
    m.addEventListener?.('change', on)
    return () => m.removeEventListener?.('change', on)
  }, [q])
  return narrow
}

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** 接手與交還（E01、E02、L04）：按下去就呼叫操作，畫面狀態用回傳值更新。 */
export function useTakeover(botId: string, botName: string, setScreen: (s: Screen) => void) {
  const toast = useToast()
  const [busy, setBusy] = useState<'take' | 'back' | null>(null)
  const take = useCallback(async () => {
    setBusy('take')
    try {
      setScreen(await computersTakeover({ bot_id: botId }))
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(null)
    }
  }, [botId, setScreen, toast])
  const back = useCallback(
    async (note: string) => {
      setBusy('back')
      try {
        const out = await computersHandback({ bot_id: botId, note: note.trim() || undefined })
        setScreen(out.screen)
        toast.show({ kind: 'done', text: `已交還給${botName}` })
        return out
      } catch (err) {
        toast.show({ kind: 'error', text: errText(err) })
        return null
      } finally {
        setBusy(null)
      }
    },
    [botId, botName, setScreen, toast],
  )
  return { take, back, busy }
}

/** 誰在用電腦。 */
export function controlOf(s: Screen | null): 'you' | 'other' | 'bot' {
  if (s?.takeover) return s.you ? 'you' : 'other'
  return 'bot'
}

/** 電腦本身的狀態（E10）：還沒開機、開機中、休眠、主機離線、出錯；開著時回 null（顯示畫面）。 */
export function boxState(s: Screen): { icon: typeof Moon; title: string; sub: string; tone?: 'warning' | 'danger'; spin?: boolean } | null {
  if (s.host_status === 'offline' || s.host_status === 'failed')
    return { icon: CloudOff, title: '主機離線', sub: '工作先排隊，主機回來會自動重新連線；結果不明的操作不會自動重做。', tone: 'danger' }
  switch (s.box) {
    case 'running':
      return null
    case 'creating':
    case 'updating':
      return { icon: LoaderCircle, title: s.box === 'updating' ? '正在更新電腦…' : '正在開機…', sub: `準備桌面與瀏覽器，最長等 ${BOOT_LIMIT_S} 秒`, spin: true }
    case 'sleeping':
      return { icon: Moon, title: '休眠中', sub: '有新工作會自動喚醒' }
    case 'error':
      return { icon: TriangleAlert, title: '電腦出了問題', sub: s.error || '請到設定重新開機或重建電腦', tone: 'danger' }
  }
  return { icon: MonitorOff, title: `${s.name}的電腦還沒開機`, sub: '第一次收到工作時會自己開機' }
}
