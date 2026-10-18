import { ChevronDown, Captions, Hand, Mic, PhoneOff } from 'lucide-react'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { goBack, navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'

// L18 和 Bot 通話：語音在 E32 做；這裡先放全螢幕的通話畫面與說明，按鈕停用，掛斷回到對話。
export function CallPage({ botId }: { botId: string }) {
  const bot = useStore(botsStore).bots?.find((b) => b.id === botId)
  const back = () => goBack(`/bots/${botId}`)
  const btn = 'flex size-16 items-center justify-center rounded-full'
  return (
    <main className="flex min-h-dvh flex-1 flex-col items-center bg-app px-4 pb-[max(24px,env(safe-area-inset-bottom))] pt-[max(8px,env(safe-area-inset-top))]" aria-label="通話">
      <div className="flex w-full items-center">
        <button type="button" aria-label="收起" onClick={back} className="flex size-11 items-center justify-center rounded-full border border-divider bg-panel text-ink">
          <Icon icon={ChevronDown} size={22} />
        </button>
        <div className="flex flex-1 flex-col items-center">
          <span className="text-title">{bot?.name ?? 'Bot'}</span>
          <span className="text-meta text-ink-3">語音通話還在做</span>
        </div>
        <span className="flex size-11 items-center justify-center rounded-full border border-divider bg-panel text-ink-3" aria-hidden="true">
          <Icon icon={Captions} size={20} />
        </span>
      </div>
      <div className="mt-10 flex size-[196px] items-center justify-center rounded-full border border-line bg-panel">
        <Avatar shape={bot?.shape} color={bot?.color} state="sleeping" dot="none" size={128} ring="#111111" />
      </div>
      <p className="m-0 mt-6 max-w-[300px] text-center text-ui text-ink-2">用說的交代{bot?.name ?? 'Bot'}、聽它回報、說「允許」核准，會在之後的版本開放。現在請先用文字對話。</p>
      <span className="flex-1" />
      <div className="flex w-full max-w-[340px] justify-between">
        {[
          { icon: Mic, label: '靜音' },
          { icon: Hand, label: '打斷' },
        ].map((b) => (
          <span key={b.label} className="flex flex-col items-center gap-1.5 text-sub text-ink-3">
            <button type="button" disabled aria-label={b.label} className={cx(btn, 'bg-control text-ink-3')}>
              <Icon icon={b.icon} size={24} />
            </button>
            {b.label}
          </span>
        ))}
        <span className="flex flex-col items-center gap-1.5 text-sub text-ink-2">
          <button type="button" aria-label="掛斷" onClick={() => navigate(`/bots/${botId}`, { replace: true })} className={cx(btn, 'bg-danger text-white')}>
            <Icon icon={PhoneOff} size={24} />
          </button>
          掛斷
        </span>
      </div>
    </main>
  )
}
