import { useState } from 'react'
import { OctagonPause, Play, X } from 'lucide-react'
import { hidePauseBanner, pauseHiddenStore, pauseStore, setPauseAll } from '../data/pause'
import { useStore } from '../data/store'
import { Button, IconButton } from '../ui/Button'
import { cx } from '../ui/cx'
import { Icon } from '../ui/Icon'
import type { MenuItem } from '../ui/Menu'
import { useToast } from '../ui/toast-context'

// 暫停中（全部停下）的提示，按「繼續」恢復 Bot 之間的往來，按 ✕ 只收起提示（Bot 還是停著）。
// place：top＝電腦版每一頁最上方；inline＝手機對話頁的輸入框上面；dock＝手機其他頁浮在底部（分頁頁在分頁列上面）。
// 手機不放最上面：頁首自己已經留了狀態列的高度，提示放上面會多出一大段空白（2026-10-20 使用者回報「提示關不掉」）。
export function PauseBanner({ place = 'top', aboveTabs = false }: { place?: 'top' | 'inline' | 'dock'; aboveTabs?: boolean }) {
  const paused = useStore(pauseStore)
  const hidden = useStore(pauseHiddenStore)
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  if (!paused || hidden) return null
  const resume = async () => {
    setBusy(true)
    try {
      await setPauseAll(false)
      toast.show({ kind: 'done', text: 'Bot 之間的往來恢復了' })
    } catch (e) {
      toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
    } finally {
      setBusy(false)
    }
  }
  const compact = place !== 'top'
  return (
    <div
      role="status"
      data-testid="pause-banner"
      data-place={place}
      className={cx(
        'flex items-center gap-2.5 text-ui-sm text-warning-ink',
        place === 'top' && 'flex-none border-b border-warning/40 bg-warning-soft px-4 py-2',
        compact && 'rounded-card border border-warning/40 bg-panel py-1.5 pl-3 pr-1.5 shadow-dialog',
        place === 'dock' && 'fixed inset-x-3 z-30 md:hidden',
        place === 'dock' && (aboveTabs ? 'bottom-[calc(57px+env(safe-area-inset-bottom)+8px)]' : 'bottom-[calc(env(safe-area-inset-bottom)+8px)]'),
      )}
    >
      <Icon icon={OctagonPause} size={16} />
      <span className="min-w-0 flex-1">{compact ? '全部停下中' : '全部停下中：Bot 之間不會互相交辦。你直接對 Bot 說話照常可以。'}</span>
      <Button size="sm" variant="secondary" icon={Play} loading={busy} onClick={() => void resume()}>
        繼續
      </Button>
      <IconButton icon={X} label="收起提示（Bot 還是停著）" onClick={hidePauseBanner} />
    </div>
  )
}

/** 「設定與更多」選單裡的那一項：平常是「全部停下」，停下中換成「繼續」（提示按 ✕ 收起後還找得到）。 */
export function usePauseMenuItem(): MenuItem {
  const paused = useStore(pauseStore)
  const pauseAll = usePauseAll()
  const toast = useToast()
  if (!paused) return { label: '全部停下（所有 Bot）', icon: OctagonPause, separator: true, onSelect: () => void pauseAll() }
  return {
    label: '繼續（恢復 Bot 之間的往來）',
    icon: Play,
    separator: true,
    onSelect: () =>
      void setPauseAll(false).then(
        () => toast.show({ kind: 'done', text: 'Bot 之間的往來恢復了' }),
        (e: unknown) => toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) }),
      ),
  }
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
