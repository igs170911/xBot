import { useState } from 'react'
import { Laptop, Lock, Pencil, Play, Sparkles, X } from 'lucide-react'
import { teachingStart } from '../api/generated'
import { computerPanelStore } from '../computer/screenData'
import { useNarrow } from '../computer/hooks'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { Button, IconButton } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { setLesson, teachStartStore, type Lesson } from './teachingData'

/**
 * 教它一件事（design/E05）：只有一顆「開始示範」；要教什麼做完再補，試做預設開。
 * 按下後 XBot 叫醒電腦（最多 75 秒）、接手畫面、開始記錄，然後打開電腦面板（E04）。
 */
export function TeachStartDialog() {
  const target = useStore(teachStartStore)
  const bots = useStore(botsStore).bots
  const bot = bots?.find((b) => b.id === target?.botId)
  const name = bot?.name ?? 'Bot'
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const phone = useNarrow()
  const wide = !useNarrow('(max-width: 1179px)')
  const close = () => {
    if (busy) return
    setError(null)
    teachStartStore.set(null)
  }
  const start = async () => {
    if (!target) return
    setBusy(true)
    setError(null)
    try {
      const l = await teachingStart({ bot_id: target.botId, thread_id: target.threadId })
      setLesson(target.botId, l as Lesson)
      teachStartStore.set(null)
      // 寬螢幕：對話旁的電腦面板；窄的螢幕：全視窗的電腦頁。
      if (wide) computerPanelStore.set(true)
      else navigate(`/bots/${target.botId}/computer`)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open={!!target}
      onClose={close}
      busy={busy}
      width={560}
      title={
        <span className="flex items-center gap-2">
          <span className="min-w-0 flex-1">教{name}一件事</span>
          <IconButton icon={X} label="關閉" size="sm" onClick={close} disabled={busy} />
        </span>
      }
    >
      <div className="flex flex-col gap-4 pt-1" data-testid="teach-start">
        <p className="m-0 px-4 text-ui leading-relaxed text-ink">
          在{name}的電腦上照你平常的方式做一次。它會記下每一步，做完整理成技能，之後交代一句話就會照做。
        </p>
        {phone && (
          <p className="m-0 flex items-start gap-2 rounded-group bg-warning-soft px-3.5 py-2.5 text-sub text-warning-ink" role="note">
            <Icon icon={Laptop} size={16} className="mt-0.5 flex-none" />
            在電腦上教比較方便（畫面比較大）；用手機也可以開始。
          </p>
        )}
        <div className="flex flex-col gap-2">
          <Button variant="primary" icon={Play} loading={busy} onClick={() => void start()} className="h-14 w-full text-[17px] max-sm:h-14" data-autofocus>
            {busy ? '電腦開機中，準備好就開始…' : '開始示範'}
          </Button>
          <p className="m-0 text-center text-sub text-ink-2">要教什麼不用先寫，做完再補一句就好。</p>
          {error && (
            <p className="m-0 text-center text-sub text-danger" role="alert">
              {error}
            </p>
          )}
        </div>
        <ul className="m-0 flex list-none flex-col gap-3 rounded-group bg-inset px-4 py-3.5 text-ui-sm">
          <li className="flex items-center gap-2.5">
            <Icon icon={Lock} size={17} className="flex-none text-ink-2" />
            密碼欄位只會記成 ••••，不會存下來。
          </li>
          <li className="flex items-center gap-2.5">
            <Icon icon={Pencil} size={17} className="flex-none text-ink-2" />
            途中可以補充說明、暫停，或刪掉做錯的步驟。
          </li>
          <li className="flex items-center gap-2.5">
            <Icon icon={Sparkles} size={17} className="flex-none text-ink-2" />
            學完它會自己試做一次給你看，不合意可以再教。
          </li>
        </ul>
      </div>
    </Dialog>
  )
}
