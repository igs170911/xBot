import { useEffect, useState } from 'react'
import { Captions, CaptionsOff, Check, ChevronDown, Crown, Hand, Mic, MicOff, Phone, PhoneOff, ShieldAlert } from 'lucide-react'
import { botsStore, type Bot } from '../data/bots'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { usePhone } from '../mobile/nav'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { cx } from '../ui/cx'
import { CallEngine, type CallPhase, type CallState } from './callEngine'
import { clock } from './voice'
import { LevelMeter, Wave } from './Wave'

// K01（桌面）／L18（手機）和 Bot 通話：大頭像隨狀態換眼睛、字幕、靜音／打斷／掛斷、通話中的核定卡、右欄逐字紀錄。

const STATUS: Record<CallPhase, string> = {
  connecting: '接通中…',
  listening: '正在聽…',
  hearing: '正在聽…',
  transcribing: '想一下…',
  thinking: '想一下…',
  working: '去做了',
  speaking: '正在說…',
  waiting: '等你核定',
  muted: '已靜音',
  ended: '已掛斷',
  error: '沒辦法通話',
}

const AVATAR: Record<CallPhase, string> = {
  connecting: 'waking',
  listening: 'listening',
  hearing: 'listening',
  transcribing: 'thinking',
  thinking: 'thinking',
  working: 'working',
  speaking: 'happy',
  waiting: 'notifying',
  muted: 'idle',
  ended: 'sleeping',
  error: 'sad',
}

function useCall(botId: string): [CallEngine, CallState] {
  const [engine] = useState(() => new CallEngine(botId))
  const state = useStore(engine.store)
  useEffect(() => {
    engine.keep()
    void engine.start()
    // 離開頁面（收起、上一頁）也算掛斷：留下通話紀錄。
    return () => engine.hangupSoon()
  }, [engine])
  return [engine, state]
}

function useElapsed(since: number, on: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!on) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [on])
  return Math.max(0, now - since)
}

/** 通話中的核定卡（L18：說「允許」或「拒絕」，也可以直接按）。 */
function AskCard({ state, engine, bot, phone }: { state: CallState; engine: CallEngine; bot: Bot; phone: boolean }) {
  const ask = state.ask
  if (!ask) {
    if (!state.decided) return null
    return (
      <div className={cx('flex items-center gap-2 rounded-card bg-panel px-4 py-3 text-ui', state.decided.tone === 'ok' ? 'text-success' : 'text-ink-2')} role="status" data-testid="call-decided">
        <Icon icon={Check} size={16} />
        {state.decided.text}
      </div>
    )
  }
  const approval = ask.kind === 'approval'
  const title = ask.description || ask.prompt || ask.command || '需要你決定'
  return (
    <section aria-label="通話中的核定" className={cx('flex flex-col gap-3 rounded-card border border-line bg-card p-4', phone && 'w-full')} data-testid="call-ask">
      <div className="flex items-start gap-3">
        {phone ? <Icon icon={ShieldAlert} size={20} className="mt-0.5 text-warning" /> : <Avatar shape={bot.shape} color={bot.color} state="notifying" dot="waiting" size={28} ring="#262626" />}
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex flex-wrap items-center gap-2 text-ui">
            {title}
            {phone && (
              <StatusChip tone="warning" size="sm">
                待你核定
              </StatusChip>
            )}
          </span>
          {ask.command && ask.command !== title && <code className="truncate font-mono text-meta text-ink-3">{ask.command}</code>}
          {!phone && <span className="text-meta text-warning-ink">{approval ? '要你核定 · 用說的也算' : '要你回答'}</span>}
        </div>
      </div>
      {approval ? (
        <>
          {phone && (
            <span className="flex items-center gap-2 rounded-field bg-inset px-3 py-2 text-sub text-ink">
              <Icon icon={Mic} size={16} className="text-accent-ink" />
              說「允許」或「拒絕」，也可以直接按
            </span>
          )}
          <div className="flex gap-2">
            <Button variant="primary" className={phone ? 'h-11 flex-1' : undefined} onClick={() => void engine.decide('once')}>
              允許這次
            </Button>
            <Button className={phone ? 'h-11 flex-1' : undefined} onClick={() => void engine.decide('deny')}>
              拒絕
            </Button>
          </div>
        </>
      ) : (
        <Button onClick={() => navigate(`/bots/${bot.id}${state.threadId ? '/threads/' + state.threadId : ''}`)}>到對話裡回答</Button>
      )}
    </section>
  )
}

function Captioned({ state, bot, big }: { state: CallState; bot: Bot; big?: boolean }) {
  if (!state.captions) return <p className="m-0 text-sub text-ink-3">字幕已關閉</p>
  const c = state.caption
  const prev = state.previous
  return (
    <div className="flex max-w-[560px] flex-col items-center gap-1.5 text-center" aria-live="polite" data-testid="call-caption">
      {big && prev && (
        <span className="text-sub text-ink-3">
          {prev.who === 'me' ? '你' : bot.name}：「{prev.text.slice(0, 60)}」
        </span>
      )}
      {c ? (
        <>
          {big && <span className="text-meta text-ink-3">{c.who === 'me' ? '你' : bot.name} · 字幕</span>}
          <span className={cx('line-clamp-3', big ? 'text-[17px] leading-[1.5]' : 'text-[16px] leading-[1.6]')}>「{c.text}」</span>
          {!big && <span className="text-meta text-ink-3">{c.who === 'me' ? '你' : bot.name} · 字幕</span>}
        </>
      ) : (
        <span className="text-sub text-ink-3">說完停一下就會送出</span>
      )}
    </div>
  )
}

function Meter({ state, engine }: { state: CallState; engine: CallEngine }) {
  if (state.phase === 'speaking') return <Wave bars={[6, 10, 16, 22, 14, 20, 26, 18, 12, 22, 16, 10, 18, 24, 14, 8, 12, 6]} />
  if (state.phase === 'listening' || state.phase === 'hearing' || state.phase === 'waiting') return <LevelMeter level={engine.level} bars={28} height={28} />
  return <span className="h-7" />
}

/** 通話紀錄的一列預覽（K01 右下：掛斷後對話裡會留下這一列）。 */
function callSummary(state: CallState, ms: number) {
  const s = Math.round(ms / 1000)
  return `${s >= 60 ? `${Math.floor(s / 60)} 分 ${s % 60} 秒` : `${s} 秒`}${state.turns ? ` · 你說了 ${state.turns} 句` : ''}`
}

export function CallPage({ botId }: { botId: string }) {
  const bot = useStore(botsStore).bots?.find((b) => b.id === botId)
  if (!bot) return <main className="flex-1" aria-busy="true" />
  return <Call key={bot.id} bot={bot} />
}

function Call({ bot }: { bot: Bot }) {
  const phone = usePhone()
  const [engine, state] = useCall(bot.id)
  const live = state.phase !== 'ended' && state.phase !== 'error' && state.phase !== 'connecting'
  const elapsed = useElapsed(state.startedAt, live)
  const hangup = async () => {
    const tid = await engine.hangup()
    navigate(tid ? `/bots/${bot.id}/threads/${tid}` : `/bots/${bot.id}`, { replace: true })
  }
  // ⌘⇧M／Ctrl+Shift+M 靜音（K01）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key.toLowerCase() === 'm') {
        e.preventDefault()
        engine.toggleMute()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [engine])

  const status = STATUS[state.phase]
  const timeLabel = state.phase === 'connecting' ? '接通中' : `通話中 · ${clock(elapsed)}`
  const canInterrupt = state.phase === 'speaking' || state.phase === 'thinking' || state.phase === 'working' || state.phase === 'transcribing'
  const avatarRing = state.phase === 'speaking' ? 'border-accent' : 'border-accent/40'

  const error = state.phase === 'error' && (
    <p role="alert" className="m-0 max-w-[420px] text-center text-sub text-danger">
      {state.error}
    </p>
  )

  if (phone) {
    const round = 'relative flex size-16 items-center justify-center rounded-full touch-target'
    return (
      <main className="flex min-h-dvh flex-1 flex-col items-center bg-app px-4 pb-[max(24px,env(safe-area-inset-bottom))] pt-[max(8px,env(safe-area-inset-top))]" aria-label="通話" data-phase={state.phase}>
        <div className="flex w-full items-center">
          <button type="button" aria-label="收起" onClick={() => void hangup()} className="flex size-11 items-center justify-center rounded-full border border-divider bg-panel text-ink">
            <Icon icon={ChevronDown} size={22} />
          </button>
          <div className="flex flex-1 flex-col items-center">
            <span className="flex items-center gap-1 text-title">
              {bot.name}
              {bot.primary && <Icon icon={Crown} size={15} className="text-ink-3" />}
            </span>
            <span className={cx('text-meta', live ? 'text-success' : 'text-ink-3')} data-testid="call-time">
              {timeLabel}
            </span>
          </div>
          <button
            type="button"
            aria-label={state.captions ? '關閉字幕' : '打開字幕'}
            aria-pressed={state.captions}
            onClick={() => engine.toggleCaptions()}
            className="flex size-11 items-center justify-center rounded-full border border-divider bg-panel text-ink"
          >
            <Icon icon={state.captions ? Captions : CaptionsOff} size={20} />
          </button>
        </div>
        <div className={cx('mt-6 flex size-[196px] items-center justify-center rounded-full border-2 bg-panel transition-colors', avatarRing)}>
          <Avatar shape={bot.shape} color={bot.color} state={AVATAR[state.phase]} dot="none" size={128} ring="#111111" />
        </div>
        <p className="m-0 mt-5 text-title" role="status" data-testid="call-status">
          {status}
        </p>
        <div className="mt-2">
          <Meter state={state} engine={engine} />
        </div>
        <div className="mt-3 flex w-full flex-col items-center">
          {error || <Captioned state={state} bot={bot} big />}
        </div>
        <div className="mt-4 w-full">
          <AskCard state={state} engine={engine} bot={bot} phone />
        </div>
        <span className="flex-1" />
        <div className="flex w-full max-w-[340px] justify-between pt-4">
          <span className="flex flex-col items-center gap-1.5 text-sub text-ink-2">
            <button type="button" aria-label={state.muted ? '取消靜音' : '靜音'} aria-pressed={state.muted} onClick={() => engine.toggleMute()} className={cx(round, state.muted ? 'bg-ink text-app' : 'bg-control text-ink')}>
              <Icon icon={state.muted ? MicOff : Mic} size={24} />
            </button>
            {state.muted ? '已靜音' : '靜音'}
          </span>
          <span className="flex flex-col items-center gap-1.5 text-sub text-ink-2">
            <button type="button" aria-label="打斷" disabled={!canInterrupt} onClick={() => engine.interrupt()} className={cx(round, 'bg-control text-ink disabled:opacity-50')}>
              <Icon icon={Hand} size={24} />
            </button>
            打斷
          </span>
          <span className="flex flex-col items-center gap-1.5 text-sub text-ink-2">
            <button type="button" aria-label="掛斷" onClick={() => void hangup()} className={cx(round, 'bg-danger text-white')}>
              <Icon icon={PhoneOff} size={24} />
            </button>
            掛斷
          </span>
        </div>
      </main>
    )
  }

  return (
    <div className="flex h-dvh min-w-0 flex-1" data-phase={state.phase}>
      <main className="flex min-w-0 flex-1 flex-col bg-app" aria-label="通話">
        <header className="flex h-14 flex-none items-center gap-2 pl-5 pr-3">
          <Avatar shape={bot.shape} color={bot.color} state="idle" dot="none" size={26} ring="#070707" />
          <span className="flex items-center gap-1 text-[16px]">
            {bot.name}
            {bot.primary && <Icon icon={Crown} size={15} className="text-ink-3" />}
          </span>
          <span className="ml-1 flex h-7 items-center gap-1.5 rounded-full border border-line px-3 text-meta text-ink-2" data-testid="call-time">
            <span className={cx('size-1.5 rounded-full', live ? 'bg-success' : 'bg-hover')} />
            {timeLabel}
          </span>
          <span className="flex-1" />
          <IconButton icon={state.captions ? Captions : CaptionsOff} label={state.captions ? '關閉字幕' : '打開字幕'} pressed={state.captions} onClick={() => engine.toggleCaptions()} />
        </header>
        <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-5 px-6 pb-10">
          <div className={cx('flex size-[300px] items-center justify-center rounded-full border transition-colors', avatarRing)}>
            <div className="flex size-[248px] items-center justify-center rounded-full bg-[#0a1a2e]">
              <Avatar shape={bot.shape} color={bot.color} state={AVATAR[state.phase]} dot="none" size={176} ring="#0a1a2e" />
            </div>
          </div>
          <p className="m-0 text-[20px]" role="status" data-testid="call-status">
            {status}
          </p>
          {error || <Captioned state={state} bot={bot} />}
          <Meter state={state} engine={engine} />
          <div className="flex items-center gap-3">
            <button
              type="button"
              aria-label={state.muted ? '取消靜音' : '靜音'}
              aria-pressed={state.muted}
              onClick={() => engine.toggleMute()}
              className={cx('flex size-14 items-center justify-center rounded-full', state.muted ? 'bg-ink text-app' : 'bg-control text-ink hover:bg-hover')}
            >
              <Icon icon={state.muted ? MicOff : Mic} size={22} />
            </button>
            <button type="button" onClick={() => engine.interrupt()} disabled={!canInterrupt} className="flex h-14 items-center gap-2 rounded-full bg-control px-5 text-ui text-ink hover:bg-hover disabled:opacity-50">
              <Icon icon={Hand} size={20} />
              打斷
            </button>
            <button type="button" onClick={() => void hangup()} className="flex h-14 items-center gap-2 rounded-full bg-danger-soft px-5 text-ui text-danger hover:brightness-110">
              <Icon icon={PhoneOff} size={20} />
              結束通話
            </button>
          </div>
          <p className="m-0 text-meta text-ink-3">{state.muted ? '已靜音 · 再按一次或 ⌘⇧M 恢復' : '說完停一下就會送出 · 它在說話時按「打斷」 · ⌘⇧M 靜音'}</p>
        </div>
      </main>
      <aside aria-label="這通電話" className="flex w-[380px] flex-none flex-col border-l border-divider bg-app max-xl:w-[320px]">
        <header className="flex h-14 flex-none items-center px-5">
          <h2 className="m-0 flex-1 text-[16px] font-normal">這通電話</h2>
          <span className="text-meta text-ink-3">自動存進對話</span>
        </header>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4 pb-4" data-testid="call-transcript">
          {state.lines.length === 0 && <p className="m-0 self-center pt-4 text-sub text-ink-3">說句話就開始，例如「今天有什麼要我處理的？」</p>}
          {state.lines.map((l) =>
            l.who === 'system' ? (
              <span key={l.id} className="self-center text-meta text-ink-3">
                {l.text}
              </span>
            ) : (
              <div key={l.id} className={cx('max-w-[88%] whitespace-pre-wrap break-words rounded-bubble px-4 py-2.5 text-ui', l.who === 'me' ? 'self-end bg-bubble-user text-white' : 'self-start bg-bubble-bot')}>
                {l.text}
              </div>
            ),
          )}
          <AskCard state={state} engine={engine} bot={bot} phone={false} />
        </div>
        <footer className="flex flex-none flex-col gap-2 border-t border-divider px-4 py-4">
          <span className="text-meta text-ink-3">掛斷後，對話裡會留下這一列</span>
          <div className="flex items-center gap-3 rounded-group bg-panel px-3 py-2.5">
            <span className="flex size-8 items-center justify-center rounded-full bg-control text-ink-2">
              <Icon icon={Phone} size={16} />
            </span>
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="text-ui">和{bot.name}通話</span>
              <span className="text-meta text-ink-3">{callSummary(state, elapsed)}</span>
            </span>
          </div>
        </footer>
      </aside>
    </div>
  )
}
