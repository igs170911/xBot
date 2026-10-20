import { useState } from 'react'
import { Lightbulb } from 'lucide-react'
import { lessonsDecide, type InboxListOutput } from '../api/generated'
import { loadInbox } from '../data/inbox'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Card } from '../ui/Card'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'

// 「學到一件事」的待我處理卡（internal/lessons）：老師模型看了沒做完的工作，提議一條守則（或技能的注意事項）。
// 可以先改字，再選加給這個 Bot、加給所有 Bot（或加進那個技能），或不要。

type InboxItem = InboxListOutput['items'][number]
type Payload = { lesson?: string; why?: string; skill?: string; teacher?: string; bot_name?: string; asked?: string }

export function LessonCard({ item, bot }: { item: InboxItem; bot: { name: string; shape?: string; color?: string } }) {
  const toast = useToast()
  const p = (item.payload ?? {}) as Payload
  const [text, setText] = useState(p.lesson ?? item.body)
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState('')
  const decide = async (apply: 'bot' | 'all' | 'skill' | 'no') => {
    setBusy(apply)
    try {
      await lessonsDecide({ inbox_id: item.id, apply, text: text.trim() !== (p.lesson ?? '') ? text.trim() : undefined })
      toast.show({
        kind: 'done',
        text: apply === 'no' ? '已略過' : apply === 'skill' ? `已加進技能「${p.skill}」` : apply === 'all' ? '已加給所有 Bot' : `已加給${bot.name}`,
      })
      await loadInbox()
    } catch (e) {
      toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
    } finally {
      setBusy('')
    }
  }
  return (
    <Card data-testid="inbox-item" data-kind="lesson" layout="flex flex-col gap-2.5 py-3 pl-[18px] pr-3.5">
      <div className="flex items-center gap-3">
        <Avatar shape={bot.shape as never} color={bot.color as never} size={32} dot="none" />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="flex items-center gap-2 text-ui">
            {bot.name}
            <StatusChip tone="accent" size="sm" icon={Lightbulb}>
              {p.skill ? '技能可以改' : '學到一件事'}
            </StatusChip>
          </span>
          <span className="truncate text-meta text-ink-3">{p.asked ? `那次的工作：${p.asked}` : item.title}</span>
        </div>
      </div>
      {editing ? (
        <TextInput label="守則" hideLabel value={text} maxLength={80} onChange={(e) => setText(e.target.value)} />
      ) : (
        <p className="m-0 flex items-start gap-2 pl-11 text-ui">
          <span className="pt-0.5 text-accent-ink">
            <Icon icon={Lightbulb} size={16} />
          </span>
          <span>{text}</span>
        </p>
      )}
      {p.why && <p className="m-0 pl-11 text-sub text-ink-2">為什麼：{p.why}</p>}
      <div className="flex flex-wrap gap-2 pl-11" data-card-actions="1">
        {p.skill ? (
          <Button variant="primary" loading={busy === 'skill'} onClick={() => void decide('skill')}>
            加進技能「{p.skill}」
          </Button>
        ) : (
          <Button variant="primary" loading={busy === 'bot'} onClick={() => void decide('bot')}>
            加給{bot.name}
          </Button>
        )}
        <Button loading={busy === 'all'} onClick={() => void decide('all')}>
          加給所有 Bot
        </Button>
        <Button variant="ghost" onClick={() => setEditing((x) => !x)}>
          {editing ? '改好了' : '改字'}
        </Button>
        <Button variant="ghost" loading={busy === 'no'} onClick={() => void decide('no')}>
          不要
        </Button>
      </div>
    </Card>
  )
}
