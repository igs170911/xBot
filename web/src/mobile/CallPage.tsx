import { useEffect, useState } from 'react'
import { ChevronDown, Captions, Hand, Mic, PhoneOff } from 'lucide-react'
import { ApiError, computersStatus, computersWake, type ComputersStatusOutput } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { goBack, navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { PhoneBar } from './Phone'

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

// L04 手機看 Bot 的電腦：即時畫面與接手在 B14 做；這裡先顯示電腦狀態，可以叫醒。
export function ComputerPage({ botId }: { botId: string }) {
  const bot = useStore(botsStore).bots?.find((b) => b.id === botId)
  const [c, setC] = useState<ComputersStatusOutput | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    computersStatus({ bot_id: botId })
      .then(setC)
      .catch((e: unknown) => setError(e instanceof ApiError || e instanceof Error ? e.message : String(e)))
  }, [botId])
  const wake = async () => {
    setBusy(true)
    try {
      setC(await computersWake({ bot_id: botId }))
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  const running = !!c?.computer.running
  return (
    <main className="flex min-h-dvh flex-1 flex-col bg-app" aria-label={`${bot?.name ?? 'Bot'}的電腦`}>
      <PhoneBar title={`${bot?.name ?? 'Bot'}的電腦`} sub={c ? `${c.host_name} · ${running ? '開機中' : '休眠中'}` : undefined} back={`/bots/${botId}`} center right={<span className="size-11" />} />
      <div className="mx-auto flex w-full max-w-[880px] flex-1 flex-col gap-3 px-4 pb-[max(16px,env(safe-area-inset-bottom))]">
        <div className="flex aspect-[16/10] w-full flex-col items-center justify-center gap-3 rounded-card bg-panel text-center">
          <Avatar shape={bot?.shape} color={bot?.color} state={running ? 'working' : 'sleeping'} dot="none" size={56} ring="#111111" />
          <p className="m-0 px-6 text-ui text-ink-2">{running ? '電腦開著。即時畫面與接手操作還在做，之後會出現在這裡。' : '電腦休眠中。有工作時會自動開機，也可以先叫醒。'}</p>
          {!running && c && (
            <Button variant="primary" className="min-h-11" loading={busy} onClick={() => void wake()}>
              叫醒
            </Button>
          )}
        </div>
        {error && <p className="m-0 text-sub text-danger">{error}</p>}
        {c?.outdated && <p className="m-0 rounded-field bg-warning-soft px-3 py-2 text-sub text-warning-ink">這台電腦要更新（資料保留），到「設定 › 電腦與裝置」更新。</p>}
      </div>
    </main>
  )
}
