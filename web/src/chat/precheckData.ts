import { judgePrecheck, type JudgePrecheckOutput } from '../api/generated'
import { createStore } from '../data/store'

// 先判斷列的資料（C15）：每件工作的快判結果與你改正過的項目，只放在這個分頁的記憶體裡。

export type Chip = JudgePrecheckOutput['chips'][number]

/** 每件工作（task id）的先判斷結果；只放在這個分頁的記憶體裡。 */
export const chipsByTask = createStore<Record<string, Chip[]>>({})
/** 你改正過的項目：task id → 用途 → 你選的。 */
export const fixedByTask = createStore<Record<string, Record<string, string>>>({})

/** 送出後在背景問先判斷；不等它、失敗也不影響送出。 */
export function runPreCheck(botId: string, text: string, taskId: string) {
  judgePrecheck({ bot_id: botId, text, task_id: taskId })
    .then((out) => {
      if (out.chips.length) chipsByTask.set((s) => ({ ...s, [taskId]: out.chips }))
    })
    .catch(() => {})
}
