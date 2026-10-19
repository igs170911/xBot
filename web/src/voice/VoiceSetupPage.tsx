import { useEffect, useRef, useState } from 'react'
import { Check, ChevronRight, Phone, Play, Square, X } from 'lucide-react'
import { ApiError, voiceBotGet, voiceBotSet, voiceSettingsSet, type VoiceBotGetOutput, type VoiceBotSetInput } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { goBack, navigate } from '../router'
import { PhoneBar } from '../mobile/Phone'
import { usePhone } from '../mobile/nav'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Slider, Switch } from '../ui/Controls'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'
import { loadVoiceSettings, playerStore, speak, stopPlayback, voiceSettingsStore, type VoiceSettings } from './voice'
import { Wave } from './Wave'


type VoiceId = NonNullable<VoiceBotSetInput['voice']>

const errText = (e: unknown) => (e instanceof ApiError || e instanceof Error ? e.message : String(e))

const SAMPLE = (name: string, bot: string) => `你好，我是${bot}。之後我就用「${name}」這個聲音念回覆給你聽。`

function speedLabel(v: number) {
  return `${Number.isInteger(v) ? v : v.toFixed(2).replace(/0$/, '')} 倍`
}

function engineSummary(s: VoiceSettings | null): string {
  if (!s) return '讀取中…'
  const label = (p: string) => s.engines.find((e) => e.provider === p)?.label ?? p
  const tts = s.tts_ready ? `朗讀用 ${label(s.settings.tts.provider)}` : '朗讀用瀏覽器內建'
  const stt = s.stt_ready ? `語音輸入用 ${label(s.settings.stt.provider)}` : '語音輸入用瀏覽器內建'
  return `${tts} · ${stt} · ${s.settings.hangup_minutes} 分鐘沒說話就掛斷`
}

/** K02 進階：語音引擎、金鑰狀態、自動掛斷。改了就存（全域設定，所有 Bot 共用）。 */
function Advanced({ settings, onSaved }: { settings: VoiceSettings; onSaved: (s: VoiceSettings) => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const options = settings.engines.filter((e) => e.usable).map((e) => ({ value: e.provider || 'browser', label: e.label }))
  const save = async (patch: Parameters<typeof voiceSettingsSet>[0]) => {
    setBusy(true)
    try {
      onSaved(await voiceSettingsSet(patch))
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }
  const tts = settings.settings.tts
  const stt = settings.settings.stt
  const hasKeyEngine = settings.engines.some((e) => e.provider && e.usable)
  return (
    <div className="flex flex-col gap-3 border-t border-divider px-4 pb-4 pt-3" data-testid="voice-advanced">
      <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
        <Select
          label="朗讀用的語音引擎"
          value={tts.provider || 'browser'}
          options={options}
          disabled={busy}
          onChange={(v) => void save({ tts: { provider: v === 'browser' ? '' : v, model: '' } })}
        />
        <Select
          label="語音輸入用的引擎"
          value={stt.provider || 'browser'}
          options={options}
          disabled={busy}
          onChange={(v) => void save({ stt: { provider: v === 'browser' ? '' : v, model: '' } })}
        />
      </div>
      {(tts.provider || stt.provider) && (
        <p className="m-0 text-meta text-ink-3">
          模型：{[tts.provider && `朗讀 ${tts.model}`, stt.provider && `語音輸入 ${stt.model}`].filter(Boolean).join('、')}（OpenAI 相容的 audio API）
        </p>
      )}
      <div className="flex items-center gap-2 rounded-field bg-inset px-3 py-2.5 text-sub text-ink-2">
        <span className={cx('size-2 flex-none rounded-full', hasKeyEngine ? 'bg-success' : 'bg-hover')} />
        <span className="flex-1">{hasKeyEngine ? '金鑰只存在這台主機，不會放進 Bot 的電腦。' : '還沒有可以用的模型金鑰：先到「模型與金鑰」加一個（例：OpenAI），沒有時用系統內建的聲音。'}</span>
        {!hasKeyEngine && (
          <Button size="sm" onClick={() => navigate('/settings/models')}>
            模型與金鑰
          </Button>
        )}
      </div>
      <div className="flex items-center gap-3">
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="text-ui">沒人說話多久就掛斷</span>
          <span className="text-meta text-ink-3">通話中太久沒有聲音，自動掛斷</span>
        </span>
        <Select
          label="自動掛斷"
          hideLabel
          look="compact"
          value={String(settings.settings.hangup_minutes)}
          options={[1, 3, 5, 10, 30].map((n) => ({ value: String(n), label: `${n} 分鐘` }))}
          disabled={busy}
          onChange={(v) => void save({ hangup_minutes: Number(v) })}
        />
      </div>
      <div className="flex items-center gap-3">
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="text-ui">隨時插話</span>
          <span className="text-meta text-ink-3">不用等它說完就能開口（全雙工即時語音）· 之後的版本開放</span>
        </span>
        <Switch checked={false} disabled label="隨時插話" onChange={() => {}} />
      </div>
    </div>
  )
}

/** K02 設定 Bot 的聲音（/bots/:id/voice）：選一個聲音（A～F，選了就存）、試聽、語速、自動朗讀；引擎收在「進階」。 */
export function VoiceSetupPage({ botId }: { botId: string }) {
  const bot = useStore(botsStore).bots?.find((b) => b.id === botId)
  const settings = useStore(voiceSettingsStore)
  const player = useStore(playerStore)
  const phone = usePhone()
  const toast = useToast()
  const [view, setView] = useState<VoiceBotGetOutput | null>(null)
  const [adv, setAdv] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const speedTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => {
    void loadVoiceSettings(true).catch((e: unknown) => setError(errText(e)))
    voiceBotGet({ bot_id: botId })
      .then(setView)
      .catch((e: unknown) => setError(errText(e)))
  }, [botId])
  useEffect(() => () => stopPlayback(), [])

  const save = async (patch: Omit<VoiceBotSetInput, 'bot_id'>) => {
    const prev = view
    if (view) setView({ ...view, ...patch } as VoiceBotGetOutput)
    try {
      setView(await voiceBotSet({ bot_id: botId, ...patch }))
    } catch (e) {
      setView(prev)
      toast.show({ kind: 'error', text: errText(e) })
    }
  }

  const name = bot?.name ?? 'Bot'
  const preview = (id: string, label: string) => {
    const key = 'preview:' + id
    if (player.key === key) return stopPlayback()
    speak(key, { bot_id: botId, text: SAMPLE(label, name), voice: id as VoiceId, speed: view?.speed }).catch((e: unknown) => toast.show({ kind: 'error', text: errText(e) }))
  }

  const voices = settings?.voices ?? []
  const selected = voices.find((v) => v.id === view?.voice)
  const close = () => goBack(`/bots/${botId}`)

  const body = (
    <section aria-label={`${name}的聲音`} className={cx('flex w-full flex-col', phone ? 'gap-3 px-4 pb-6' : 'max-w-[640px] gap-4 rounded-card border border-divider bg-panel px-6 pb-0 pt-6')}>
      {!phone && (
        <header className="flex items-start gap-3">
          <Avatar shape={bot?.shape} color={bot?.color} state="happy" dot="none" size={40} ring="#111111" />
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <h1 className="m-0 text-title">給{name}一個聲音</h1>
            <p className="m-0 text-sub text-ink-2">選好就能講電話，也能讓它把回覆念給你聽。</p>
          </div>
          <IconButton icon={X} label="關閉" onClick={close} />
        </header>
      )}
      {error && (
        <p role="alert" className="m-0 text-sub text-danger">
          {error}
        </p>
      )}
      <div role="radiogroup" aria-label="聲音" className="flex flex-col overflow-hidden rounded-card border border-divider bg-edge">
        {voices.map((v, i) => {
          const on = v.id === view?.voice
          const key = 'preview:' + v.id
          const playing = player.key === key
          return (
            <div key={v.id} className={cx('flex min-h-[59px] items-center gap-3 px-4 not-first:border-t not-first:border-divider', on ? 'bg-control' : 'bg-edge')}>
              <button
                type="button"
                role="radio"
                aria-checked={on}
                onClick={() => void save({ voice: v.id as VoiceId })}
                className="flex min-h-11 min-w-0 flex-1 items-center gap-3 text-left"
              >
                <span className={cx('flex size-5 flex-none items-center justify-center rounded-[6px] text-[11px]', on ? 'bg-accent text-white' : 'bg-control text-ink-2')}>
                  {on ? <Icon icon={Check} size={14} strokeWidth={2.5} /> : 'ABCDEF'[i]}
                </span>
                <span className="flex min-w-0 flex-col">
                  <span className="text-ui">{v.name}</span>
                  <span className="truncate text-meta text-ink-3">{v.desc}</span>
                </span>
              </button>
              {playing && <Wave live={player.phase === 'playing'} />}
              <Button size="sm" variant={playing ? 'ghost' : 'secondary'} icon={playing ? Square : Play} aria-label={(playing ? '停止試聽 ' : '試聽 ') + v.name} loading={playing && player.phase === 'loading'} onClick={() => preview(v.id, v.name)}>
                {playing ? '停止' : '試聽'}
              </Button>
            </div>
          )
        })}
        {!voices.length && <div className="px-4 py-6 text-sub text-ink-3">讀取中…</div>}
      </div>
      {view && (
        <div className="flex flex-col rounded-card bg-inset">
          <Slider
            label="語速"
            value={view.speed}
            min={settings?.limits.speed_min ?? 0.5}
            max={settings?.limits.speed_max ?? 2}
            step={0.05}
            format={speedLabel}
            onChange={(v) => {
              setView({ ...view, speed: v })
              clearTimeout(speedTimer.current)
              speedTimer.current = setTimeout(() => void save({ speed: v }), 400)
            }}
            className="px-4 pb-3 pt-3"
          />
          <div className="-mt-1 flex justify-between px-4 pb-3 text-meta text-ink-3" aria-hidden="true">
            <span>0.5 倍 慢</span>
            <span>1 倍</span>
            <span>2 倍 快</span>
          </div>
          <div className="flex min-h-[52px] items-center gap-3 border-t border-divider px-4">
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="text-ui">自動朗讀每則回覆</span>
              <span className="text-meta text-ink-3">回完自動念出來，對話裡變成語音訊息</span>
            </span>
            <Switch checked={view.auto_read} label="自動朗讀每則回覆" onChange={(on) => void save({ auto_read: on })} />
          </div>
        </div>
      )}
      <div className="flex flex-col overflow-hidden rounded-card bg-inset">
        <button type="button" aria-expanded={adv} onClick={() => setAdv((a) => !a)} className="flex min-h-[52px] items-center gap-3 px-4 py-2.5 text-left">
          <Icon icon={ChevronRight} size={16} className={cx('flex-none text-ink-2 transition-transform', adv && 'rotate-90')} />
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="text-ui">進階</span>
            <span className="text-meta text-ink-3">{engineSummary(settings)}</span>
          </span>
        </button>
        {adv && settings && <Advanced settings={settings} onSaved={(s) => voiceSettingsStore.set(s)} />}
      </div>
      <footer className={cx('flex items-center gap-3', phone ? 'pt-1' : '-mx-6 mt-2 border-t border-divider px-6 py-4')}>
        <span className="flex flex-1 items-center gap-1.5 text-sub text-success" data-testid="voice-saved">
          {selected && (
            <>
              <Icon icon={Check} size={16} />
              已選「{selected.name}」· 自動儲存
            </>
          )}
        </span>
        <Button variant="primary" icon={Phone} onClick={() => navigate(`/bots/${botId}/call`)}>
          開始通話
        </Button>
      </footer>
    </section>
  )

  if (phone) {
    return (
      <main className="flex min-h-dvh flex-1 flex-col bg-app" aria-label={`${name}的聲音`}>
        <PhoneBar title={`給${name}一個聲音`} sub="選好就能講電話，也能念回覆給你聽" back={`/bots/${botId}`} />
        {body}
      </main>
    )
  }
  return (
    <main className="flex min-h-0 flex-1 items-start justify-center overflow-y-auto bg-app px-6 py-10" aria-label={`${name}的聲音`}>
      {body}
    </main>
  )
}
