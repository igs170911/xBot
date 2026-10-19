import { useState } from 'react'
import { ChevronRight, CircleAlert, CircleCheck, Check, CircleX, Loader } from 'lucide-react'
import { Avatar } from '../ui/Avatar'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import type { Bot } from '../data/bots'
import { useNow } from '../data/ui'
import { AnsweredChip, AskCard } from './AskCard'
import { formatElapsed, isActive, waitText, workSummary, type Activity, type Step } from './assemble'

const STATE_TITLE: Record<string, string> = {
  queued: '排隊中',
  waiting_box: '電腦開機中',
  waiting_input: '等你回覆',
  waiting_approval: '等你允許',
  stopping: '正在停止…',
}

function title(a: Activity): string {
  if (a.state === 'waiting_box' && a.wait) return waitText(a.wait) ?? STATE_TITLE.waiting_box
  if (STATE_TITLE[a.state]) return STATE_TITLE[a.state]
  // C01：做事中的標題一律是「思考中」（步驟清單已經說明在做什麼）
  return '思考中'
}

function StepIcon({ s }: { s: Step }) {
  if (s.status === 'running')
    return (
      <span className="flex text-accent-ink">
        <Icon icon={Loader} size={16} className="animate-spin [animation-duration:2s]" label="進行中" />
      </span>
    )
  if (s.status === 'fail')
    return (
      <span className="flex text-danger">
        <Icon icon={CircleX} size={16} label="失敗" />
      </span>
    )
  return (
    <span className="flex text-success">
      <Icon icon={CircleCheck} size={16} label="完成" />
    </span>
  )
}

function StepList({ steps, compact, now }: { steps: Step[]; compact?: boolean; now?: number }) {
  return (
    <ol
      className={cx(
        'm-0 flex list-none flex-col border-l border-line',
        compact ? '-mt-1 ml-3.5 gap-1 pl-[18px] text-sub text-ink/80' : 'ml-[15px] gap-2 py-0.5 pl-[19px]',
      )}
    >
      {steps.map((s) => (
        <li key={s.id} className={cx('flex flex-wrap items-center gap-x-2.5 gap-y-1.5', !compact && 'min-h-7')}>
          <StepIcon s={s} />
          {s.label && <span className={cx(!compact && 'text-ui-sm', s.status === 'running' ? 'text-ink' : 'text-ink/80')}>{s.label}</span>}
          <span className="inline-flex h-[26px] items-center gap-1.5 rounded-full border border-line px-2.5 font-mono text-[12px] text-ink/80">
            {s.status === 'ok' && <Icon icon={Check} size={14} strokeWidth={2.25} color="var(--color-success)" />}
            {s.name}
          </span>
          {now && s.status === 'running' && <span className="text-meta text-ink-3">{formatElapsed(Math.max(0, now - s.at))}</span>}
        </li>
      ))}
    </ol>
  )
}

// 工作進度（CH-1，design/C01、C02）：進行中是「思考中 · 1 分 12 秒 · 6 個步驟」，預設收合、只露出最新一步（點開看全部）；
// 做完摺疊成「工作了 N 分鐘 · M 步」，點開看步驟。沒有用到工具的回合不顯示。
export function WorkSteps({ a, bot }: { a: Activity; bot?: Bot }) {
  const active = isActive(a)
  const [open, setOpen] = useState(false)
  const [openDone, setOpenDone] = useState(false)
  const now = useNow(active)
  const name = bot?.name ?? 'Bot'

  const answered = a.answered?.length ? (
    <div className="flex flex-wrap gap-1.5">
      {a.answered.map((x) => (
        <AnsweredChip key={x.inboxId} a={x} />
      ))}
    </div>
  ) : null

  if (!active) {
    if (a.steps.length === 0) return answered
    return (
      <section aria-label={`${name}的工作步驟`} className="flex flex-col gap-1.5">
        {answered}
        <button
          type="button"
          aria-expanded={openDone}
          onClick={() => setOpenDone((o) => !o)}
          className="relative flex h-[30px] items-center gap-1.5 self-start rounded-full border border-line bg-transparent pl-3 pr-2.5 text-[13px] text-ink/80 touch-target hover:bg-control"
        >
          <Icon icon={a.verdict === 'failed' ? CircleAlert : Check} size={14} strokeWidth={2.25} color={a.verdict === 'failed' ? 'var(--color-danger)' : 'var(--color-success)'} />
          {workSummary(a)}
          <span className={cx('flex text-ink-2 transition-transform', openDone && 'rotate-90')}>
            <Icon icon={ChevronRight} size={16} />
          </span>
        </button>
        {openDone && <StepList steps={a.steps} compact />}
      </section>
    )
  }

  const elapsed = a.startedAt ? formatElapsed(Math.max(0, now - a.startedAt)) : ''
  const meta = [elapsed, a.steps.length ? `${a.steps.length} 個步驟` : ''].filter(Boolean).join(' · ')
  const stopping = a.state === 'stopping'
  const waiting = a.state === 'waiting_input' || a.state === 'waiting_approval'

  return (
    <section aria-label={`${name}的工作進度`} className="flex flex-col gap-1.5" data-state={a.state}>
      <button
        type="button"
        aria-expanded={a.steps.length ? open : undefined}
        onClick={() => setOpen((o) => !o)}
        className="flex h-9 items-center gap-2.5 self-start rounded-full bg-transparent pl-1 pr-3 text-ink"
      >
        <Avatar shape={bot?.shape} color={bot?.color} state={stopping ? 'drowsy' : waiting ? 'notifying' : a.thinking || !a.steps.length ? 'thinking' : 'working'} dot="none" ring="#070707" size={24} />
        <span className={cx('text-[15px]', stopping ? 'text-ink-2' : waiting ? 'text-warning-ink' : 'text-ink', !stopping && !waiting && 'animate-pulse [animation-duration:2.4s]')}>{title(a)}</span>
        {meta && <span className="text-sub text-ink-3">{meta}</span>}
        {a.steps.length > 0 && (
          <span className={cx('flex text-ink-2 transition-transform', open && 'rotate-90')}>
            <Icon icon={ChevronRight} size={16} />
          </span>
        )}
      </button>
      {open && a.steps.length > 0 && <StepList steps={a.steps} now={now} />}
      {!open && a.steps.length > 0 && <StepList steps={a.steps.slice(-1)} now={now} compact />}
      {answered}
      {a.request?.inboxId ? (
        <AskCard ask={a.request} bot={bot ? { name: bot.name, shape: bot.shape, color: bot.color } : undefined} />
      ) : (
        a.request && (
          <p className="m-0 ml-[34px] rounded-menu bg-card px-3.5 py-2.5 text-ui-sm">
            <span className="text-warning-ink">{name}在等你：</span>
            {a.request.prompt}
          </p>
        )
      )}
    </section>
  )
}
