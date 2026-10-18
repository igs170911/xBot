import { useState } from 'react'
import { Check, ShieldCheck, Sparkles } from 'lucide-react'
import { ApiError, botsCreate, botsSuggest, roomsCreate, type BotsSuggestOutput } from '../api/generated'
import { botsStore, upsertBot } from '../data/bots'
import { upsertRoom } from '../data/rooms'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { COLOR_IDS, SHAPE_IDS, type AvatarColor, type AvatarShape } from '../ui/avatar-data'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { ROOM_MAX, RULES } from '../rooms/rules'
import { PhoneBar } from './Phone'

// L12 新增 Bot（一句話，/new/bot）與 L13 新增群組（勾成員 → 建立，/new/room）。手機是整頁；桌面也能用。

function errText(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}

const pick = <T,>(xs: readonly T[], seed: string): T => xs[[...seed].reduce((n, c) => n + c.charCodeAt(0), 0) % xs.length]

export function NewBotPage() {
  const [text, setText] = useState('')
  const [hint, setHint] = useState<BotsSuggestOutput | null>(null)
  const [busy, setBusy] = useState<'suggest' | 'create' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const shape: AvatarShape = pick(SHAPE_IDS, hint?.name ?? text)
  const color: AvatarColor = pick(COLOR_IDS, (hint?.title ?? '') + text)

  const suggest = async () => {
    if (!text.trim()) return
    setBusy('suggest')
    setError(null)
    try {
      setHint(await botsSuggest({ text: text.trim() }))
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(null)
    }
  }
  const create = async () => {
    if (!hint) return
    setBusy('create')
    setError(null)
    try {
      const bot = await botsCreate({ name: hint.name, title: hint.title || undefined, description: hint.description || text.trim(), soul: hint.soul || undefined, shape, color })
      upsertBot(bot)
      navigate(`/bots/${bot.id}`, { replace: true })
    } catch (e) {
      setError(errText(e))
      setBusy(null)
    }
  }

  return (
    <main className="flex min-h-dvh flex-1 flex-col bg-app" aria-label="新增 Bot">
      <PhoneBar title="新增 Bot" back="/chats" />
      <div className="mx-auto flex w-full max-w-[560px] flex-1 flex-col gap-3 px-4 pb-[max(16px,env(safe-area-inset-bottom))]">
        <label className="flex flex-col gap-2">
          <span className="text-title">一句話說你要它做什麼</span>
          <textarea
            value={text}
            onChange={(e) => {
              setText(e.target.value)
              setHint(null)
            }}
            rows={3}
            maxLength={200}
            placeholder="例：每天幫我回覆蝦皮買家的問題，不確定的先問我"
            className="w-full resize-none rounded-card border border-line bg-panel px-4 py-3 text-ui text-ink outline-none focus-visible:outline-2 focus-visible:outline-focus"
          />
        </label>
        {!hint && (
          <Button icon={Sparkles} className="min-h-12" disabled={!text.trim()} loading={busy === 'suggest'} onClick={() => void suggest()}>
            幫我想好其他的
          </Button>
        )}
        {hint && (
          <section aria-label="系統建議" className="flex flex-col gap-3 rounded-card border border-line bg-panel px-4 py-4">
            <div className="flex items-center gap-3">
              <Avatar shape={shape} color={color} state="happy" dot="none" size={48} ring="#111111" />
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="text-title">{hint.name}</span>
                <span className="truncate text-sub text-ink-2">{[hint.title, hint.description].filter(Boolean).join(' · ')}</span>
              </span>
              <span className="flex flex-none items-center gap-1 self-start rounded-full bg-control px-2.5 py-1 text-meta text-ink-2">
                <Icon icon={Sparkles} size={13} />
                系統建議
              </span>
            </div>
            {hint.why && <p className="m-0 text-sub text-ink-2">{hint.why}</p>}
            <div className="border-t border-line pt-3 text-sub text-ink-2">
              <span className="flex items-center gap-2">
                <Icon icon={ShieldCheck} size={16} />
                權限：先問我 · 花錢、對外發送、刪除前都問你
              </span>
            </div>
            <p className="m-0 text-meta text-ink-3">名字、人設、技能都可以之後在 Bot 資料裡改。</p>
          </section>
        )}
        {error && (
          <p role="alert" className="m-0 text-sub text-danger">
            {error}
          </p>
        )}
        <span className="flex-1" />
        {hint && (
          <>
            <Button variant="primary" className="min-h-12 w-full" loading={busy === 'create'} onClick={() => void create()}>
              建立
            </Button>
            <button type="button" onClick={() => setHint(null)} className="flex min-h-11 items-center gap-2 self-start text-ui text-ink-2">
              <Icon icon={Sparkles} size={16} />
              重新建議
            </button>
          </>
        )}
      </div>
    </main>
  )
}

export function NewRoomPage() {
  const { bots, activity } = useStore(botsStore)
  const live = (bots ?? []).filter((b) => !b.archived)
  const [picked, setPicked] = useState<string[]>([])
  const [step, setStep] = useState<1 | 2>(1)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const max = ROOM_MAX
  const chosen = live.filter((b) => picked.includes(b.id))
  const autoName = chosen.map((b) => b.name).join('、')
  const toggle = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : p.length >= max ? p : [...p, id]))
  const create = async () => {
    setBusy(true)
    setError(null)
    try {
      const r = await roomsCreate({ member_ids: picked, responder_rule: 'auto' })
      upsertRoom(r)
      navigate(`/rooms/${r.id}`, { replace: true })
    } catch (e) {
      setError(errText(e))
      setBusy(false)
    }
  }
  return (
    <main className="flex min-h-dvh flex-1 flex-col bg-app" aria-label="新增群組">
      <PhoneBar title={step === 1 ? '新增群組：勾成員' : '新增群組：確認'} back="/chats" onBack={step === 2 ? () => setStep(1) : undefined} right={<span className="pr-1 text-sub text-ink-2">{step}/2</span>} />
      {step === 1 ? (
        <>
          <p className="m-0 px-5 pb-2 text-sub text-ink-2">勾 2–10 個 Bot。名字和回應規則會自動設好。</p>
          <div role="group" aria-label="成員" className="flex flex-1 flex-col">
            {live.map((b) => {
              const on = picked.includes(b.id)
              const full = !on && picked.length >= max
              const st = activity[b.id]?.state
              const waiting = st === 'waiting_input' || st === 'waiting_approval'
              return (
                <button
                  key={b.id}
                  type="button"
                  role="checkbox"
                  aria-checked={on}
                  disabled={full}
                  onClick={() => toggle(b.id)}
                  className={cx('flex min-h-14 items-center gap-3 px-5 py-1.5 text-left disabled:opacity-40', on && 'bg-panel')}
                >
                  <Avatar shape={b.shape} color={b.color} state={waiting ? 'notifying' : 'idle'} dot={waiting ? 'waiting' : 'none'} size={36} ring={on ? '#111111' : '#070707'} />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="text-name text-ink">{b.name}</span>
                    <span className={cx('truncate text-sub', waiting ? 'text-warning-ink' : 'text-ink-2')}>{[b.title, waiting ? '等你回覆' : st && st !== 'done' ? '工作中' : '待命'].filter(Boolean).join(' · ')}</span>
                  </span>
                  <span className={cx('flex size-6 flex-none items-center justify-center rounded-[7px] border-2', on ? 'border-accent bg-accent text-white' : 'border-check-off')}>{on && <Icon icon={Check} size={16} strokeWidth={2.5} />}</span>
                </button>
              )
            })}
          </div>
          <div className="sticky bottom-0 flex flex-col gap-2 border-t border-divider bg-app px-4 pb-[max(12px,env(safe-area-inset-bottom))] pt-3">
            <div className="flex items-center gap-2">
              <span className="flex -space-x-1.5">
                {chosen.slice(0, 5).map((b) => (
                  <Avatar key={b.id} shape={b.shape} color={b.color} dot="none" size={24} blink={false} ring="#070707" />
                ))}
              </span>
              <span className="flex-1 text-sub text-ink-2">{picked.length < 2 ? '至少 2 位' : `已選 ${picked.length} 位`}</span>
              <span className="text-meta text-ink-3">最多 {max} 位</span>
            </div>
            <Button variant="primary" className="min-h-12 w-full" disabled={picked.length < 2} onClick={() => setStep(2)}>
              下一步
            </Button>
          </div>
        </>
      ) : (
        <div className="flex flex-1 flex-col gap-3 px-4 pb-[max(16px,env(safe-area-inset-bottom))]">
          <div className="flex flex-col overflow-hidden rounded-card bg-card">
            <div className="flex items-center gap-2 border-b border-line px-4 py-3">
              <span className="text-sub text-ink-2">名稱</span>
              <span className="min-w-0 flex-1 truncate text-right text-ui">{autoName}</span>
              <span className="rounded-full bg-control px-2 py-0.5 text-meta text-ink-2">自動</span>
            </div>
            <div className="flex items-center gap-2 px-4 py-3">
              <span className="text-sub text-ink-2">沒點名時</span>
              <span className="flex-1 text-right text-ui">{RULES.find((r) => r.value === 'auto')?.label(chosen[0]?.name ?? '') ?? '自動挑一位'}</span>
            </div>
          </div>
          <p className="m-0 px-1 text-meta text-ink-3">之後可以在群組裡改名字和回應規則。Bot 之間每次最多交棒 4 輪。</p>
          {error && (
            <p role="alert" className="m-0 text-sub text-danger">
              {error}
            </p>
          )}
          <span className="flex-1" />
          <Button variant="primary" className="min-h-12 w-full" loading={busy} onClick={() => void create()}>
            建立
          </Button>
        </div>
      )}
    </main>
  )
}
