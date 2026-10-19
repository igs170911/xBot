import { useState } from 'react'
import { Cpu, Gauge, ListChecks, Monitor, type LucideIcon } from 'lucide-react'
import { judgeFeedback } from '../api/generated'
import { useStore } from '../data/store'
import { chipsByTask, fixedByTask, type Chip } from './precheckData'
import { Icon } from '../ui/Icon'
import { JudgeTag } from '../ui/Pill'

// 先判斷列（C15、L21，PRD §20 用途 11–14）：你交辦後，快判做的四個選擇題（夠清楚、要不要開電腦、交給誰、預估花費）。
// 只是建議：不會擋住工作。只顯示生效、模型有把握（≥ 0.6）的項目；影子模式、逾時、沒設定時整列不出現。
// 每一項都有「不對？」：選正確的那一個，記下來校正門檻（xbot.judge.feedback）。

const ICONS: Record<string, LucideIcon> = {
  precheck_clarity: ListChecks,
  precheck_computer: Monitor,
  precheck_engine: Cpu,
  precheck_cost: Gauge,
}

function ChipTag({ chip, taskId }: { chip: Chip; taskId: string }) {
  const fixed = useStore(fixedByTask)[taskId]?.[chip.use]
  const [error, setError] = useState<string | null>(null)
  const pick = (key: string) => {
    setError(null)
    judgeFeedback({ use: chip.use, task_id: taskId, judged: chip.label, judged_by: 'model', corrected: key })
      .then(() => fixedByTask.set((s) => ({ ...s, [taskId]: { ...s[taskId], [chip.use]: key } })))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }
  const fixedShown = fixed ? (chip.options.find((o) => o.key === fixed)?.shown ?? fixed) : ''
  return (
    <span className="inline-flex items-center gap-1" data-testid={`precheck-${chip.use}`}>
      <JudgeTag menu={chip.options.map((o) => ({ label: o.shown, onSelect: () => pick(o.key) }))} menuLabel="應該是哪一個？" prefix="">
        <span className="inline-flex items-center gap-1 text-ink" title={chip.why}>
          <Icon icon={ICONS[chip.use] ?? ListChecks} size={13} />
          {chip.shown}
        </span>{' '}
        · {Math.round(chip.p * 100)}%{fixedShown && <span className="text-ink"> · 已記下：{fixedShown}</span>}
      </JudgeTag>
      {error && (
        <span role="alert" className="text-meta text-danger">
          {error}
        </span>
      )}
    </span>
  )
}

/** 你那則訊息下方的先判斷列；沒有結果時什麼都不畫。 */
export function PreCheckRow({ taskId }: { taskId?: string }) {
  const chips = useStore(chipsByTask)[taskId ?? '']
  if (!taskId || !chips?.length) return null
  return (
    <div className="flex flex-wrap items-center justify-end gap-1.5" data-testid="precheck-row" aria-label="先判斷">
      {chips.map((c) => (
        <ChipTag key={c.use} chip={c} taskId={taskId} />
      ))}
    </div>
  )
}
