import { onboardStatus, onboardUpdate, type OnboardStatusOutput, type OnboardUpdateInput } from '../api/generated'
import { createStore } from '../data/store'

// 首次設定（A03～A10、ON-3、ON-4）的全域狀態：服務記的狀態（做完了沒、導覽、關掉的卡片）＋導覽進行到哪一站。

export type OnboardState = OnboardStatusOutput['state']

export type Onboard = {
  /** null＝還在讀；'off'＝讀不到（舊服務或連不上），不顯示首次設定。 */
  status: OnboardStatusOutput | null | 'off'
  /** 這次開著的網頁裡跳過了首次設定（服務記不起來時也不要再擋路）。 */
  skipped: boolean
  /** 導覽目前在第幾站（0 起算）；null 表示沒在導覽。 */
  tour: number | null
}

export const onboardStore = createStore<Onboard>({ status: null, skipped: false, tour: null })

const TOUR_KEY = 'xbot.tour.done'

function localTourDone(): boolean {
  try {
    return window.localStorage.getItem(TOUR_KEY) === '1'
  } catch {
    return false
  }
}

function rememberTour(done: boolean) {
  try {
    if (done) window.localStorage.setItem(TOUR_KEY, '1')
    else window.localStorage.removeItem(TOUR_KEY)
  } catch {
    // 瀏覽器不讓存：服務那邊有記
  }
}

/** 讀首次設定狀態（不偵測本機模型，比較快）；讀不到就當成不用首次設定。 */
export function loadOnboard(): Promise<void> {
  return onboardStatus({ skip_detect: true })
    .then((s) => onboardStore.set((o) => ({ ...o, status: s })))
    .catch(() => onboardStore.set((o) => ({ ...o, status: 'off' })))
}

function patchState(next: OnboardState) {
  onboardStore.set((o) => (o.status && o.status !== 'off' ? { ...o, status: { ...o.status, state: next, first_run: o.status.first_run && !next.completed } } : o))
}

/** 記下首次設定的狀態；服務記不起來時畫面照樣往下走。 */
export async function updateOnboard(input: OnboardUpdateInput): Promise<void> {
  if (input.completed) onboardStore.set((o) => ({ ...o, skipped: true }))
  try {
    patchState(await onboardUpdate(input))
  } catch {
    // 舊服務沒有這個操作：只在這個瀏覽器記
  }
}

export function tourDone(): boolean {
  const s = onboardStore.get().status
  return (s && s !== 'off' && s.state.tour_done) || localTourDone()
}

export function startTour() {
  rememberTour(false)
  onboardStore.set((o) => ({ ...o, tour: 0 }))
}

export function tourStep(step: number) {
  onboardStore.set((o) => ({ ...o, tour: step }))
}

/** 看完或跳過導覽：記住，之後不再自己跳出來。 */
export function endTour() {
  rememberTour(true)
  onboardStore.set((o) => ({ ...o, tour: null }))
  void updateOnboard({ tour_done: true })
}

export function dismissCard(card: 'import' | 'phone' | 'tour') {
  const s = onboardStore.get().status
  if (s && s !== 'off') patchState({ ...s.state, dismissed: [...s.state.dismissed, card] })
  void updateOnboard({ dismiss: card })
}

/** 測試用：回到還沒讀的狀態。 */
export function resetOnboard() {
  onboardStore.set({ status: null, skipped: false, tour: null })
}

export function restoreCards() {
  const s = onboardStore.get().status
  if (s && s !== 'off') patchState({ ...s.state, dismissed: [] })
  void updateOnboard({ restore: true })
}
