import { OctagonPause } from 'lucide-react'
import { collabStopAll } from '../api/generated'
import type { MenuItem } from '../ui/Menu'
import { useToast } from '../ui/toast-context'

// 「全部停下」是緊急停止按鈕（xbot.collab.stop_all）：馬上停掉所有 Bot 正在做與排隊的工作，停完就照常，
// 不用按繼續（2026-10-23 使用者：「全部停止就直接停止，不需要用恢復」）。

/** 「設定與更多」等選單裡的「全部停下」。 */
export function useStopAllMenuItem(): MenuItem {
  const toast = useToast()
  return {
    label: '全部停下（所有 Bot）',
    icon: OctagonPause,
    separator: true,
    onSelect: () =>
      void collabStopAll({}).then(
        (o) => toast.show({ kind: 'done', text: o.stopped ? `全部停下了（停掉 ${o.stopped} 件工作）` : '全部停下了（沒有正在做的工作）' }),
        (e: unknown) => toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) }),
      ),
  }
}
