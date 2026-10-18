import { useEffect } from 'react'
import { LoaderCircle, Pause, Play } from 'lucide-react'
import type { ChatMsg, VoiceNote } from '../chat/assemble'
import { useStore } from '../data/store'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { useReadAloud } from './useChatVoice'
import { clock, estimateSeconds, loadVoiceSettings, voiceSettingsStore, waveform } from './voice'

/** 語音訊息（VC-1）：Bot 的回覆念成了一段聲音，泡泡上方放播放器，文字留在下面當字幕。 */
export function VoiceNoteBar({ botId, msg, note }: { botId?: string; msg: ChatMsg; note: VoiceNote }) {
  const { state, toggle } = useReadAloud(botId, msg)
  const voices = useStore(voiceSettingsStore)?.voices
  useEffect(() => {
    void loadVoiceSettings().catch(() => {})
  }, [])
  const bars = waveform(msg.id, 32)
  const secs = estimateSeconds(note.chars, note.speed)
  const name = voices?.find((v) => v.id === note.voice)?.name
  const playing = state === 'playing'
  return (
    <div className="flex items-center gap-2.5 pb-2" data-testid="voice-note" data-engine={note.engine}>
      <button
        type="button"
        onClick={toggle}
        aria-label={state === 'idle' ? '播放語音訊息' : '停止播放'}
        className="relative flex size-9 flex-none items-center justify-center rounded-full bg-ink text-app touch-target hover:brightness-[1.08]"
      >
        <Icon icon={state === 'loading' ? LoaderCircle : playing ? Pause : Play} size={16} strokeWidth={2.25} className={cx(state === 'loading' && 'animate-spin')} />
      </button>
      <span className="flex h-7 min-w-0 flex-1 items-center gap-[2px] overflow-hidden" aria-hidden="true">
        {bars.map((v, i) => (
          <span key={i} className={cx('w-[3px] flex-none rounded-full bg-ink', playing && 'animate-pulse')} style={{ height: 4 + v * 22, opacity: playing ? 0.95 : 0.55, animationDelay: `${(i % 5) * 90}ms` }} />
        ))}
      </span>
      <span className="flex-none text-meta text-ink-2 tabular-nums">{clock(secs * 1000)}</span>
      {name && <span className="flex-none rounded-full bg-control px-2 py-px text-[12px] text-ink-2">{name}</span>}
    </div>
  )
}

