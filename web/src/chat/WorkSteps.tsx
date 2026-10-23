import { useState } from 'react'
import { ChevronRight, CircleAlert, CircleCheck, CircleX, Check, Loader } from 'lucide-react'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import type { Bot } from '../data/bots'
import { useNow } from '../data/ui'
import { linkClick } from '../router'
import { AnsweredChip, AskCard } from './AskCard'
import { formatElapsed, isActive, waitText, type Activity, type Step } from './assemble'

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

function StepList({ steps, compact, now, botId }: { steps: Step[]; compact?: boolean; now?: number; botId?: string }) {
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
          {s.memory && botId && <MemoryLink botId={botId} />}
        </li>
      ))}
    </ol>
  )
}

// Bot 用記憶工具改了記憶：到「Bot 設定 → 能力 → 記憶」看它記了什麼、改掉記錯的。
function MemoryLink({ botId }: { botId: string }) {
  const to = `/settings/bots/${botId}/abilities/memory`
  return (
    <a href={to} onClick={linkClick(to)} className="text-meta text-accent-ink underline-offset-2 hover:underline">
      看記憶
    </a>
  )
}

// 工作進度（簡化版）：進行中顯示「工作中…」單行可展開，做完摺疊成「工作了 N 分鐘」，點開看步驟。
export function WorkSteps({ a, bot }: { a: Activity; bot?: Bot }) {
  const active = isActive(a)
  const [open, setOpen] = useState(false)
  const now = useNow(active)
  const name = bot?.name ?? 'Bot'
  const botId = a.botId ?? bot?.id

  const answered = a.answered?.length ? (
    <div className="flex flex-wrap gap-1.5">
      {a.answered.map((x) => (
        <AnsweredChip key={x.inboxId} a={x} />
      ))}
    </div>
  ) : null

  if (!active) {
    if (a.steps.length === 0) return answered
    // 工作完成：摺疊成一行「工作了 N 分鐘」（簡化，不顯示步數）
    const dur = (a.endedAt ?? now) - (a.startedAt ?? 0)
    const elapsed = Math.floor(dur / (60 * 1000))
    const m = elapsed === 0 ? '不到 1 分鐘' : elapsed === 1 ? '1 分鐘' : `${elapsed} 分鐘`
    return (
      <details className="group flex flex-col" open={open} onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}>
        <summary className="flex cursor-pointer list-none items-center gap-2 py-1 text-meta text-ink-3 marker:hidden hover:text-ink-2 [&::-webkit-details-marker]:hidden">
          <Icon icon={a.verdict === 'failed' ? CircleAlert : CircleCheck} size={14} color={a.verdict === 'failed' ? 'var(--color-danger)' : 'var(--color-success)'} className="flex-none" />
          <span className="flex-1">工作了 {m}</span>
          <ChevronRight size={14} className="flex-none transition-transform group-open:rotate-90" />
        </summary>
        {open && (
          <div className="ml-6 mt-1 space-y-1">
            <StepList steps={a.steps} compact now={now} botId={botId} />
          </div>
        )}
        {answered}
      </details>
    )
  }

  // 進行中：單行「工作中…」可展開查看詳細步驟（簡化）
  const elapsed = a.startedAt ? formatElapsed(Math.max(0, now - a.startedAt)) : ''
  const stepCount = a.steps.length
  const stopping = a.state === 'stopping'
  const waiting = a.state === 'waiting_input' || a.state === 'waiting_approval'

  return (
    <section aria-label={`${name}的工作進度`} className="flex flex-col gap-1" data-state={a.state}>
      <details className="group" open={open} onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}>
        <summary className="flex cursor-pointer list-none items-center gap-2 py-1 text-meta text-ink-2 marker:hidden hover:text-ink [&::-webkit-details-marker]:hidden">
          <Loader size={14} className="flex-none animate-spin text-accent-ink [animation-duration:2s]" />
          <span className="flex-1">
            {stopping ? '正在停止…' : waiting ? title(a) : '工作中…'}
            {elapsed && ` ${elapsed}`}
          </span>
          {stepCount > 0 && <span className="text-ink-3">{stepCount} 步</span>}
          <ChevronRight size={14} className="flex-none transition-transform group-open:rotate-90" />
        </summary>
        {open && stepCount > 0 && (
          <div className="ml-6 mt-1 space-y-1">
            <StepList steps={a.steps} now={now} botId={botId} />
          </div>
        )}
      </details>
      {answered}
      {a.request?.inboxId ? (
        <AskCard ask={a.request} bot={bot ? { name: bot.name, shape: bot.shape, color: bot.color } : undefined} />
      ) : (
        a.request && (
          <p className="m-0 ml-6 rounded-menu bg-card px-3.5 py-2.5 text-ui-sm">
            <span className="text-warning-ink">{name}在等你：</span>
            {a.request.prompt}
          </p>
        )
      )}
    </section>
  )
}
