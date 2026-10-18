import type { ReactNode } from 'react'
import { Check, CircleX, OctagonPause } from 'lucide-react'
import type { Bot, BotActivity } from '../data/bots'
import { useNow } from '../data/ui'
import { navigate } from '../router'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { formatElapsed, isActive, waitText, type Activity, type ChatState } from './assemble'
import { clockLabel, taskTitle } from './format'

function Line({ label, tone, children }: { label: string; tone?: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[88px_minmax(0,1fr)] items-start gap-x-3 py-1.5 max-sm:grid-cols-1 max-sm:gap-y-1">
      <span className={cx('pt-px text-sub', tone ?? 'text-ink-2')}>{label}</span>
      <div className="min-w-0 text-ui">{children}</div>
    </div>
  )
}

const STATE_TEXT: Record<string, string> = {
  queued: '排隊中',
  waiting_box: '電腦開機中',
  running: '有工作正在進行',
  waiting_input: '在等你回覆',
  waiting_approval: '在等你允許',
  stopping: '正在停止',
}

// 目前進度（design/C12 左）：這條對話串現在在做什麼、做到第幾步、是不是卡在等你、最近完成了什麼。
// 資料來自即時事件（不用另外輪詢）。Context 用量（C12 右）還沒有資料來源，先不做。
export function ProgressDialog({ open, onClose, bot, chat, threadId, act, botAct }: { open: boolean; onClose: () => void; bot: Bot; chat: ChatState; threadId?: string; act?: Activity; botAct?: BotActivity }) {
  const now = useNow(open && !!act)
  const tasks = Object.entries(chat.taskThread)
    .filter(([, tid]) => tid === threadId)
    .map(([id]) => chat.tasks[id])
    .filter(Boolean)
  const recent = tasks.filter((a) => !isActive(a) && a.state === 'done' && a.endedAt).sort((a, b) => (b.endedAt ?? 0) - (a.endedAt ?? 0))[0]
  const elsewhere = !act && botAct && botAct.state !== 'done' && botAct.threadId && botAct.threadId !== threadId ? botAct : undefined
  const waiting = act && (act.state === 'waiting_input' || act.state === 'waiting_approval')
  const running = act?.steps.findLast((s) => s.status === 'running')

  return (
    <Dialog open={open} onClose={onClose} title={`${bot.name}的目前進度`} width={560} actions={<Button onClick={onClose}>關閉</Button>}>
      <div className="flex flex-col" data-testid="progress">
        {act ? (
          <div className={cx('my-2 flex h-11 items-center gap-2.5 rounded-menu px-3.5', waiting ? 'bg-warning-soft' : 'bg-success-soft')}>
            <span className={cx('size-2 rounded-full', waiting ? 'bg-warning' : 'bg-success')} />
            <span className={cx('flex-1 text-ui', waiting && 'text-warning-ink')}>{(act.state === 'waiting_box' && waitText(act.wait)) || (STATE_TEXT[act.state] ?? '有工作正在進行')}</span>
            {act.startedAt && <span className="text-meta text-ink-2">已工作 {formatElapsed(now - act.startedAt)}</span>}
          </div>
        ) : (
          <div className="my-2 flex min-h-11 flex-wrap items-center gap-2.5 rounded-menu bg-control px-3.5 py-2">
            <span className="size-2 rounded-full bg-ink-3" />
            <span className="flex-1 text-ui">{elsewhere ? `${bot.name}在另一個對話串工作` : '目前沒有任何人在工作'}</span>
            {elsewhere && (
              <Button
                size="sm"
                onClick={() => {
                  onClose()
                  navigate(`/bots/${bot.id}/threads/${elsewhere.threadId}`)
                }}
              >
                前往
              </Button>
            )}
          </div>
        )}
        {act && threadId && (
          <Line label="正在做">
            {taskTitle(chat, threadId, act.taskId)}
            <span className="text-sub text-ink-3">
              {act.startedAt ? ` · ${clockLabel(act.startedAt)} 開始` : ''}
              {act.steps.length ? ` · 第 ${act.steps.length} 步` : ''}
            </span>
          </Line>
        )}
        {running && (
          <Line label="這一步">
            {running.label ?? running.name}
            <span className="ml-2 inline-flex h-[22px] items-center rounded-full border border-line px-2 font-mono text-[11.5px] text-ink/80">{running.name}</span>
          </Line>
        )}
        {waiting && act?.request && (
          <Line label="卡在等你" tone="text-warning-ink">
            <span className="block rounded-menu bg-warning-soft px-3 py-2 text-warning-ink">{act.request.prompt || '需要你回覆'}</span>
          </Line>
        )}
        {act?.error && (
          <Line label="出了問題" tone="text-danger">
            {act.error}
          </Line>
        )}
        {recent && threadId && (
          <Line label="最近完成">
            <span className="inline-flex items-center gap-1.5">
              <Icon
                icon={recent.verdict === 'failed' ? CircleX : recent.verdict === 'cancelled' ? OctagonPause : Check}
                size={15}
                color={recent.verdict === 'failed' ? 'var(--color-danger)' : recent.verdict === 'cancelled' ? 'var(--color-ink-2)' : 'var(--color-success)'}
              />
              {taskTitle(chat, threadId, recent.taskId)}
            </span>
            <span className="text-sub text-ink-3"> · {clockLabel(recent.endedAt ?? 0)}</span>
          </Line>
        )}
        {!act && !recent && !elsewhere && <p className="m-0 py-1.5 text-sub text-ink-2">交代一件事，這裡會即時顯示它做到哪。</p>}
        <span className="pt-2 text-meta text-ink-3">即時更新 · 只看這個對話串</span>
      </div>
    </Dialog>
  )
}
