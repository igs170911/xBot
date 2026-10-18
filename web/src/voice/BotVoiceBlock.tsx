import { useContext, useEffect, useRef, useState } from 'react'
import { ChevronRight, Square, Volume2 } from 'lucide-react'
import { ApiError, voiceBotGet, voiceBotSet, type VoiceBotGetOutput, type VoiceBotSetInput } from '../api/generated'
import { linkClick } from '../router'
import { Button } from '../ui/Button'
import { Slider, Switch } from '../ui/Controls'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { ToastContext } from '../ui/toast-context'
import { loadVoiceSettings, playerStore, speak, stopPlayback, voiceSettingsStore } from './voice'
import { useStore } from '../data/store'

// Bot 設定 · 進階 › 聲音（design/G10「聲音」區塊）：選聲音＋試聽、語速、自動朗讀每則回覆。改了就存。
// 引擎與更多選項在 K02（/bots/:id/voice）。

type VoiceId = NonNullable<VoiceBotSetInput['voice']>
const errText = (e: unknown) => (e instanceof ApiError || e instanceof Error ? e.message : String(e))

export function BotVoiceBlock({ botId, botName }: { botId: string; botName: string }) {
  const settings = useStore(voiceSettingsStore)
  const player = useStore(playerStore)
  const toast = useContext(ToastContext)
  const [view, setView] = useState<VoiceBotGetOutput | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => {
    void loadVoiceSettings().catch(() => {})
    voiceBotGet({ bot_id: botId })
      .then(setView)
      .catch(() => {})
    return () => stopPlayback()
  }, [botId])
  const save = (patch: Omit<VoiceBotSetInput, 'bot_id'>) =>
    voiceBotSet({ bot_id: botId, ...patch })
      .then(setView)
      .catch((e: unknown) => toast?.show({ kind: 'error', text: errText(e) }))
  if (!view || !settings) return <div className="h-[180px] rounded-group bg-card" aria-busy="true" />
  const key = 'block:' + view.voice
  const playing = player.key === key
  const preview = () => {
    if (playing) return stopPlayback()
    const name = settings.voices.find((v) => v.id === view.voice)?.name ?? ''
    speak(key, { bot_id: botId, text: `你好，我是${botName}。我用「${name}」這個聲音念回覆給你聽。` }).catch((e: unknown) => toast?.show({ kind: 'error', text: errText(e) }))
  }
  return (
    <div className="flex flex-col overflow-hidden rounded-group bg-card" data-testid="bot-voice">
      <div className="flex items-end gap-2 px-4 py-3">
        <Select
          label="聲音"
          hideLabel
          className="min-w-0 flex-1"
          value={view.voice}
          options={settings.voices.map((v) => ({ value: v.id, label: `${v.name} · ${v.desc}` }))}
          onChange={(v) => {
            setView({ ...view, voice: v })
            void save({ voice: v as VoiceId })
          }}
        />
        <Button icon={playing ? Square : Volume2} loading={playing && player.phase === 'loading'} onClick={preview} className="h-10">
          {playing ? '停止' : '試聽'}
        </Button>
      </div>
      <div className="border-t border-line px-4 py-3">
        <Slider
          label="語速"
          value={view.speed}
          min={settings.limits.speed_min}
          max={settings.limits.speed_max}
          step={0.05}
          format={(v) => `${Math.round(v * 100) / 100} 倍`}
          onChange={(v) => {
            setView({ ...view, speed: v })
            clearTimeout(timer.current)
            timer.current = setTimeout(() => void save({ speed: v }), 400)
          }}
        />
        <div className="mt-1 flex justify-between text-meta text-ink-3" aria-hidden="true">
          <span>0.5 倍 慢</span>
          <span>1 倍</span>
          <span>2 倍 快</span>
        </div>
      </div>
      <div className="flex min-h-[52px] items-center gap-3 border-t border-line px-4">
        <span className="flex-1 text-ui">自動朗讀每則回覆</span>
        <Switch
          checked={view.auto_read}
          label="自動朗讀每則回覆"
          onChange={(on) => {
            setView({ ...view, auto_read: on })
            void save({ auto_read: on })
          }}
        />
      </div>
      <a href={`/bots/${botId}/voice`} onClick={linkClick(`/bots/${botId}/voice`)} className="flex min-h-[48px] items-center gap-2 border-t border-line px-4 text-sub text-ink-2 hover:text-ink">
        <span className="flex-1">{settings.tts_ready ? '語音引擎、通話設定' : '用更自然的聲音：設定語音引擎'}</span>
        <Icon icon={ChevronRight} size={16} />
      </a>
    </div>
  )
}
