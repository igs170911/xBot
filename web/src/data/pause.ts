import { collabPauseAll, collabPauseState } from '../api/generated'
import { onReconnect } from './events'
import { createStore } from './store'

// 總控「全部停下」（xbot.collab.pause_all）：暫停中畫面最上方有一條提示和「繼續」。

export const pauseStore = createStore<boolean>(false)

// 提示按 ✕ 收起（只是不顯示，Bot 還是停著）：記在這台裝置；恢復往來或再按一次「全部停下」時重新顯示。
const HIDDEN_KEY = 'xbot.pauseBannerHidden'
function readHidden(): boolean {
  try {
    return window.localStorage.getItem(HIDDEN_KEY) === '1'
  } catch {
    return false
  }
}
export const pauseHiddenStore = createStore<boolean>(readHidden())
function setHidden(v: boolean) {
  pauseHiddenStore.set(v)
  try {
    if (v) window.localStorage.setItem(HIDDEN_KEY, '1')
    else window.localStorage.removeItem(HIDDEN_KEY)
  } catch {
    // 存不了（私密瀏覽）：這次開著的期間還是收起
  }
}
/** 收起「全部停下中」的提示（不恢復往來）。 */
export const hidePauseBanner = () => setHidden(true)
pauseStore.subscribe(() => {
  if (!pauseStore.get()) setHidden(false)
})

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
  if (out.paused) setHidden(false) // 再按一次全部停下：提示重新出現
  pauseStore.set(out.paused)
  return out.stopped ?? 0
}
