import { useState } from 'react'
import { OctagonPause, Play } from 'lucide-react'
import { pauseStore, setPauseAll } from '../data/pause'
import { useStore } from '../data/store'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'

// 暫停中（全部停下）：每一頁最上方的一條提示，按「繼續」恢復 Bot 之間的往來。
export function PauseBanner() {
  const paused = useStore(pauseStore)
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  if (!paused) return null
  return (
    <div role="status" data-testid="pause-banner" className="flex flex-none items-center gap-2.5 border-b border-warning/40 bg-warning-soft px-4 py-2 text-ui-sm text-warning-ink max-md:pt-[max(8px,env(safe-area-inset-top))]">
      <Icon icon={OctagonPause} size={16} />
      <span className="min-w-0 flex-1">全部停下中：Bot 之間不會互相交辦。你直接對 Bot 說話照常可以。</span>
      <Button
        size="sm"
        variant="secondary"
        icon={Play}
        loading={busy}
        onClick={async () => {
          setBusy(true)
          try {
            await setPauseAll(false)
            toast.show({ kind: 'done', text: 'Bot 之間的往來恢復了' })
          } catch (e) {
            toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
          } finally {
            setBusy(false)
          }
        }}
      >
        繼續
      </Button>
    </div>
  )
}

/** 「全部停下」按下去：停掉所有工作、暫停往來，提示停了幾件。 */
export function usePauseAll() {
  const toast = useToast()
  return async () => {
    try {
      const n = await setPauseAll(true)
      toast.show({ kind: 'done', text: n ? `全部停下了（停掉 ${n} 件工作）` : '全部停下了' })
    } catch (e) {
      toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
    }
  }
}
