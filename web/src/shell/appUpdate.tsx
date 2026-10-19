import { RotateCw } from 'lucide-react'
import { onReconnect } from '../data/events'
import { createStore, useStore } from '../data/store'
import { fetchHealth } from '../health'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'

// 服務更新後，開著的網頁（特別是 iPhone 主畫面的 App：切回來不會重新載入）還在跑舊版。
// 使用者回報 2026-10-19：修好的東西手機上一直沒變。這裡在重新連上服務、或回到前景時比對版本：
// 換版了就重新載入——頁面在背景、或沒有打到一半的字、沒有開著的對話框時直接載入；不然顯示「重新載入」。

export const updateStore = createStore(false)

let booted: string | null = null
let started = false

/** 沒有打到一半的字、沒有開著的對話框：重新載入不會弄丟東西。 */
function safeToReload(): boolean {
  if (document.querySelector('[role="dialog"], [role="alertdialog"]')) return false
  for (const el of document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>('textarea, input:not([type]), input[type="text"], input[type="search"]')) {
    if (el.value.trim() && el.offsetParent) return false
  }
  return true
}

/** 重新載入（測試換成假的）。 */
export const reloader = { reload: () => window.location.reload() }

function reload() {
  reloader.reload()
}

/** 比對版本；換版了就重新載入或顯示提示（重新連上服務、回到前景時呼叫）。 */
export async function checkForUpdate() {
  const h = await fetchHealth()
  if (!h) return
  if (!booted) {
    booted = h.version
    return
  }
  if (h.version === booted) return
  if (document.visibilityState === 'hidden' || safeToReload()) reload()
  else updateStore.set(true)
}

export function startAppUpdate() {
  if (started) return
  started = true
  void checkForUpdate()
  onReconnect(() => void checkForUpdate())
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') void checkForUpdate()
    else if (updateStore.get()) reload() // 有新版、切到背景：趁現在換
  })
}

/** 換版了但現在不方便自動載入（有字打到一半、對話框開著）：右下角小提示。 */
export function UpdateBanner() {
  const ready = useStore(updateStore)
  if (!ready) return null
  return (
    <div role="status" className="fixed bottom-[max(16px,env(safe-area-inset-bottom))] right-4 z-[80] flex items-center gap-2.5 rounded-full border border-line bg-card py-1.5 pl-4 pr-1.5 text-ui-sm text-ink shadow-pop max-md:bottom-[calc(72px+env(safe-area-inset-bottom))] max-md:left-4 max-md:right-4 max-md:justify-between">
      <span className="flex items-center gap-2">
        <Icon icon={RotateCw} size={16} />
        XBot 更新好了
      </span>
      <Button size="sm" variant="primary" onClick={reload}>
        重新載入
      </Button>
    </div>
  )
}
