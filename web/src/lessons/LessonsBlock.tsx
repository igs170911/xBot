import { useCallback, useEffect, useState } from 'react'
import { Lightbulb, Trash2 } from 'lucide-react'
import { lessonsAdd, lessonsDelete, lessonsList, type LessonsListOutput } from '../api/generated'
import { Button, IconButton } from '../ui/Button'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'
import { Block, Group } from '../settings/parts'

// Bot 設定 › 能力 › 記憶 的「學到的事」：寫在 Bot 守則裡的做法（你同意過的老師提議，或你自己加的），每一條都可以刪。
// 所有引擎都會讀（Hermes 的 SOUL.md、ACP 引擎的系統提示）。

type Lesson = LessonsListOutput['bot'][number]

export function LessonsBlock({ botId, botName }: { botId: string; botName: string }) {
  const toast = useToast()
  const [data, setData] = useState<LessonsListOutput | null>(null)
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const load = useCallback(() => {
    lessonsList({ bot_id: botId })
      .then(setData)
      .catch(() => setData(null))
  }, [botId])
  useEffect(load, [load])
  const add = async () => {
    setBusy(true)
    try {
      await lessonsAdd({ bot_id: botId, text: text.trim() })
      setText('')
      toast.show({ kind: 'done', text: `已加給${botName}，下一段新對話開始照做` })
      load()
    } catch (e) {
      toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
    } finally {
      setBusy(false)
    }
  }
  const remove = async (l: Lesson, all: boolean) => {
    try {
      await lessonsDelete({ bot_id: all ? undefined : botId, id: l.id })
      toast.show({ kind: 'done', text: '已刪掉' })
      load()
    } catch (e) {
      toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
    }
  }
  const row = (l: Lesson, all: boolean) => (
    <div key={l.id} className="flex items-start gap-2.5 px-4 py-2.5" data-testid="lesson-row">
      <span className="pt-0.5 text-accent-ink">
        <Icon icon={Lightbulb} size={16} />
      </span>
      <span className="min-w-0 flex-1 text-ui-sm">
        {l.text}
        {all && <span className="ml-2 text-meta text-ink-3">所有 Bot</span>}
      </span>
      <IconButton icon={Trash2} label={`刪掉「${l.text}」`} onClick={() => void remove(l, all)} />
    </div>
  )
  const count = (data?.bot.length ?? 0) + (data?.all.length ?? 0)
  return (
    <Block title="學到的事" sub={`沒做完的工作，老師模型看過後提議、你同意的做法，寫在${botName}的守則裡。每條每次對話都會帶上，留真的有用的就好。`}>
      <Group>
        {count === 0 && <p className="m-0 px-4 py-3 text-ui-sm text-ink-3">還沒有。工作沒做完時，「待我處理」會出現「學到一件事」的提議。</p>}
        <div className="flex flex-col divide-y divide-line">
          {data?.all.map((l) => row(l, true))}
          {data?.bot.map((l) => row(l, false))}
        </div>
        <div className="flex items-end gap-2 border-t border-line px-4 py-3">
          <TextInput label="自己加一條" wrapperClassName="min-w-0 flex-1" maxLength={80} value={text} placeholder="例：查資料先用搜尋，要登入的頁面才開瀏覽器" onChange={(e) => setText(e.target.value)} />
          <Button disabled={!text.trim()} loading={busy} onClick={() => void add()}>
            加上
          </Button>
        </div>
      </Group>
    </Block>
  )
}
