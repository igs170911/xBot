import { collabPauseAll, collabPauseState } from '../api/generated'
import { onReconnect } from './events'
import { createStore } from './store'

// 總控「全部停下」（xbot.collab.pause_all）：暫停中畫面最上方有一條提示和「繼續」。

export const pauseStore = createStore<boolean>(false)

const load = () =>
  collabPauseState({})
    .then((o) => pauseStore.set(o.paused))
    .catch(() => {})

let subscribed = false
/** 讀一次目前狀態（每次 App 啟動都讀）；斷線重連時也重讀。 */
export function startPause() {
  void load()
  if (subscribed) return
  subscribed = true
  onReconnect(() => void load())
}

/** 全部停下（true）或繼續（false）；回傳這次停掉幾件工作。 */
export async function setPauseAll(paused: boolean): Promise<number> {
  const out = await collabPauseAll({ paused })
  pauseStore.set(out.paused)
  return out.stopped ?? 0
}
