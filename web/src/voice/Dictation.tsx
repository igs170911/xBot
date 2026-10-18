import { Check, LoaderCircle, Mic, X } from 'lucide-react'
import { IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import type { Dictation } from './useDictation'
import { clock } from './voice'
import { LevelMeter } from './Wave'

// 語音輸入的按鈕與聽寫列（C05 #9）；狀態在 useDictation。

/** 麥克風鈕（C05：輸入框右邊）。 */
export function MicButton({ d }: { d: Dictation }) {
  return <IconButton icon={d.phase.kind === 'starting' ? LoaderCircle : Mic} label="語音輸入" shape="round" onClick={() => void d.start()} disabled={d.phase.kind === 'starting'} className={d.phase.kind === 'starting' ? '[&_svg]:animate-spin' : undefined} />
}

/** 聽寫列（C05 #9）：✕ 取消、紅點＋聲波、時間、✓ 完成。上面一行是瀏覽器辨識的即時文字。 */
export function DictationBar({ d }: { d: Dictation }) {
  const busy = d.phase.kind === 'transcribing'
  return (
    <div className="flex flex-col gap-2" data-testid="dictation">
      {d.interim && <p className="m-0 px-4 text-sub text-ink-2">「{d.interim}」</p>}
      <div className="flex min-h-[52px] items-center gap-2 rounded-[26px] bg-control py-[8px] pl-2 pr-[9px]" role="group" aria-label="語音輸入">
        <IconButton icon={X} label="取消語音輸入" shape="round" onClick={d.cancel} disabled={busy} />
        {busy ? (
          <span className="flex flex-1 items-center gap-2 text-sub text-ink-2" role="status">
            <Icon icon={LoaderCircle} size={16} className="animate-spin" />
            聽寫中…
          </span>
        ) : (
          <>
            <span className="size-2 flex-none animate-pulse rounded-full bg-danger" />
            <LevelMeter level={d.level} className="flex-1" bars={40} />
          </>
        )}
        <span className="flex-none text-meta text-ink-2 tabular-nums" aria-label="錄音長度">
          {clock(d.elapsed)}
        </span>
        <IconButton icon={busy ? LoaderCircle : Check} label="說完了" variant="send" onClick={() => void d.finish()} disabled={busy} className={busy ? '[&_svg]:animate-spin' : undefined} />
      </div>
    </div>
  )
}
