import { useState } from 'react'
import { Check, Clock, MessageCircle } from 'lucide-react'
import { ApiError, agendaDecide } from '../api/generated'
import { daysLeft, localInput, whenInput, whenLabel } from '../data/agenda'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Card } from '../ui/Card'
import { TextInput, Textarea } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'

// 一件提案或討論（design/H04；待我處理也用同一張）：
//   提案：「N 天後生效」，否決（可以寫原因）／現在就同意。
//   討論：直接寫回覆、不用討論了、改時間。按了就地變成結果。

export type AgendaCardItem = {
  id: string
  kind: string
  subject: string
  details: string
  due_at: number
  state?: string
  bot_name: string
  from_name?: string
}

type Decision = 'accept' | 'veto' | 'answer' | 'decline'

function errText(err: unknown): string {
  if (err instanceof ApiError) return err.message
  return err instanceof Error ? err.message : String(err)
}

const DONE: Record<string, string> = {
  accepted: '你同意了，Bot 會開始做。',
  vetoed: '已否決。14 天內不會再提同樣的事。',
  answered: '已回覆，Bot 會在對話裡接著處理。',
  declined: '不用討論了，Bot 會照自己的判斷做。',
}

export function AgendaCard({
  item,
  bot,
  sub,
  onDone,
}: {
  item: AgendaCardItem
  bot?: { shape?: string; color?: string }
  sub?: string
  onDone?: (state: string) => void
}) {
  const [busy, setBusy] = useState<Decision | 'move' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<string | null>(null)
  const [reply, setReply] = useState('')
  const [vetoing, setVetoing] = useState(false)
  const [reason, setReason] = useState('')
  const [moving, setMoving] = useState(false)
  const [when, setWhen] = useState(() => localInput(item.due_at))
  const [due, setDue] = useState(item.due_at)
  const propose = item.kind === 'propose'

  const decide = async (decision: Decision, note?: string, at?: string) => {
    setBusy(at ? 'move' : decision)
    setError(null)
    try {
      const out = await agendaDecide({ id: item.id, decision, note: note || undefined, when: at })
      if (at) {
        setDue(out.due_at)
        setMoving(false)
      } else {
        setResult(out.state)
        onDone?.(out.state)
      }
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(null)
    }
  }

  const left = daysLeft(due)
  return (
    <Card data-testid="agenda-item" data-kind={item.kind}>
      <div className="flex items-center gap-2.5">
        <Avatar shape={bot?.shape} color={bot?.color} state="notifying" dot="none" size={26} ring="#262626" />
        <span className="min-w-0 flex-1 truncate text-sub text-ink-2">
          <span className="text-ui-sm text-ink">{item.bot_name}</span> · {propose ? '提案' : '想找你討論'}
          {sub ? ` · ${sub}` : ''}
        </span>
        {propose ? (
          <StatusChip size="sm" tone="warning" icon={Clock}>
            {left > 0 ? `${left} 天後生效` : '今天生效'}
          </StatusChip>
        ) : (
          <StatusChip size="sm" tone="neutral" icon={MessageCircle}>
            {whenLabel(due)}
          </StatusChip>
        )}
      </div>
      <p className="m-0 text-body">{item.subject}</p>
      {item.details && <p className="m-0 whitespace-pre-wrap text-ui-sm text-ink-2">{item.details}</p>}
      {propose && <p className="m-0 text-meta text-ink-3">你沒意見的話，{whenLabel(due)}自動生效。{item.from_name ? `原本是 ${item.from_name} 交辦的。` : ''}</p>}
      {item.state === 'held' && <p className="m-0 text-meta text-warning-ink">討論時間到了，{item.bot_name} 在對話裡等你。</p>}

      {result ? (
        <p role="status" className="m-0 flex items-center gap-1.5 border-t border-line pt-2.5 text-ui-sm text-success">
          <Icon icon={Check} size={16} />
          {DONE[result] ?? '已處理'}
        </p>
      ) : (
        <>
          {propose && vetoing && (
            <TextInput label="否決的原因（選填）" value={reason} maxLength={2000} onChange={(e) => setReason(e.target.value)} placeholder="例：先不要，等月底再說" />
          )}
          {!propose && (
            <Textarea label="你的回覆" hideLabel value={reply} rows={2} maxLength={2000} onChange={(e) => setReply(e.target.value)} placeholder="有想法直接寫在這裡，不用開會" />
          )}
          {!propose && moving && (
            <div className="flex flex-wrap items-end gap-2">
              <TextInput label="改到什麼時候（台北時間）" type="datetime-local" value={when} onChange={(e) => setWhen(e.target.value)} wrapperClassName="flex-1" />
              <Button size="sm" loading={busy === 'move'} onClick={() => void decide('accept', undefined, whenInput(when))}>
                確定改時間
              </Button>
            </div>
          )}
          {error && (
            <p role="alert" className="m-0 text-sub text-danger">
              {error}
            </p>
          )}
          <div className="flex flex-wrap justify-end gap-2">
            {propose ? (
              <>
                <Button variant="secondary" loading={busy === 'veto'} disabled={!!busy} onClick={() => (vetoing ? void decide('veto', reason.trim()) : setVetoing(true))}>
                  {vetoing ? '確定否決' : '否決'}
                </Button>
                <Button variant="primary" loading={busy === 'accept'} disabled={!!busy} onClick={() => void decide('accept')}>
                  現在就同意
                </Button>
              </>
            ) : (
              <>
                <Button variant="outline" loading={busy === 'decline'} disabled={!!busy} onClick={() => void decide('decline')}>
                  不用討論了
                </Button>
                <Button variant="secondary" disabled={!!busy} onClick={() => setMoving((m) => !m)}>
                  改時間
                </Button>
                <Button variant="primary" loading={busy === 'answer'} disabled={!!busy || !reply.trim()} onClick={() => void decide('answer', reply.trim())}>
                  送出回覆
                </Button>
              </>
            )}
          </div>
        </>
      )}
    </Card>
  )
}
