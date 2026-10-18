import { useContext, useEffect, useRef, useState } from 'react'
import { ApiError } from '../api/generated'
import { navigate } from '../router'
import { ToastContext } from '../ui/toast-context'
import { browserRecognition, loadVoiceSettings, Mic as MicCapture, stopPlayback, transcribe, type Recognition } from './voice'

// 語音輸入（VC-2，design/C05 #9「聽寫中」）：按麥克風開始錄，按 ✓ 送去辨識，文字放進輸入框（可以再改）；✕ 取消。
// 有語音輸入引擎時錄音經 XBot 辨識（xbot.voice.transcribe）；沒有時用瀏覽器內建的辨識（邊說邊出字）。

const errText = (e: unknown) => (e instanceof ApiError || e instanceof Error ? e.message : String(e))

type Phase = { kind: 'idle' } | { kind: 'starting' } | { kind: 'recording'; mode: 'xbot' | 'browser' } | { kind: 'transcribing' }

/** 輸入框右邊的麥克風鈕；錄音時整列換成聽寫列（render 由 ChatComposer 決定放哪裡）。 */
export function useDictation({ botId, onText }: { botId?: string; onText: (text: string) => void }) {
  // 沒有 ToastProvider（單獨測試輸入框）時不顯示提示。
  const toast = useContext(ToastContext) ?? { show: () => 0, dismiss: () => {} }
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const [interim, setInterim] = useState('')
  const [elapsed, setElapsed] = useState(0)
  const mic = useRef<MicCapture | null>(null)
  const rec = useRef<Recognition | null>(null)
  const heard = useRef('')
  const maxMs = useRef(120_000)
  const onTextRef = useRef(onText)
  useEffect(() => {
    onTextRef.current = onText
  }, [onText])

  const cleanup = () => {
    mic.current?.close()
    mic.current = null
    rec.current?.abort()
    rec.current = null
  }
  useEffect(() => cleanup, [])

  const fail = (e: unknown) => {
    cleanup()
    setPhase({ kind: 'idle' })
    toast.show({ kind: 'error', text: errText(e) })
  }

  const start = async () => {
    if (phase.kind !== 'idle') return
    stopPlayback()
    setPhase({ kind: 'starting' })
    setElapsed(0)
    setInterim('')
    heard.current = ''
    let settings
    try {
      settings = await loadVoiceSettings()
    } catch (e) {
      return fail(e)
    }
    maxMs.current = settings.limits.max_seconds * 1000
    try {
      mic.current = await MicCapture.open()
    } catch (e) {
      return fail(e)
    }
    if (settings.stt_ready) {
      mic.current.start()
      setPhase({ kind: 'recording', mode: 'xbot' })
      return
    }
    const Ctor = browserRecognition()
    if (!Ctor) {
      cleanup()
      setPhase({ kind: 'idle' })
      toast.show({ kind: 'info', text: '還沒設定語音輸入，這個瀏覽器也沒有內建的辨識。', action: botId ? { label: '設定', onClick: () => navigate(`/bots/${botId}/voice`) } : undefined })
      return
    }
    const r = new Ctor()
    r.lang = settings.lang || 'zh-TW'
    r.continuous = true
    r.interimResults = true
    r.onresult = (e) => {
      let fin = ''
      let mid = ''
      for (let i = 0; i < e.results.length; i++) {
        const res = e.results[i]
        if (res.isFinal) fin += res[0].transcript
        else mid += res[0].transcript
      }
      heard.current = fin
      setInterim(fin + mid)
    }
    r.onerror = (e) => {
      if (e.error !== 'aborted' && e.error !== 'no-speech') fail(new Error(e.error === 'not-allowed' ? '沒有麥克風權限' : '瀏覽器的辨識出了問題'))
    }
    rec.current = r
    r.start()
    setPhase({ kind: 'recording', mode: 'browser' })
  }

  const cancel = () => {
    cleanup()
    setInterim('')
    setPhase({ kind: 'idle' })
  }

  const finish = async () => {
    if (phase.kind !== 'recording') return
    if (phase.mode === 'browser') {
      const r = rec.current
      rec.current = null
      const text = await new Promise<string>((resolve) => {
        if (!r) return resolve(heard.current)
        r.onend = () => resolve(heard.current || interim)
        r.stop()
        setTimeout(() => resolve(heard.current || interim), 1500)
      })
      cleanup()
      setPhase({ kind: 'idle' })
      setInterim('')
      if (text.trim()) onTextRef.current(text.trim())
      else toast.show({ kind: 'info', text: '沒有聽到說話' })
      return
    }
    const m = mic.current
    if (!m) return
    setPhase({ kind: 'transcribing' })
    try {
      const recording = await m.stop()
      cleanup()
      const text = await transcribe(recording, botId)
      setPhase({ kind: 'idle' })
      if (text) onTextRef.current(text)
      else toast.show({ kind: 'info', text: '沒有聽到說話' })
    } catch (e) {
      fail(e)
    }
  }

  // 計時；到上限自動停（服務端也擋）。
  const recording = phase.kind === 'recording'
  useEffect(() => {
    if (!recording) return
    const t0 = performance.now()
    const id = setInterval(() => setElapsed(performance.now() - t0), 250)
    return () => clearInterval(id)
  }, [recording])
  const finishRef = useRef(finish)
  useEffect(() => {
    finishRef.current = finish
  })
  useEffect(() => {
    if (recording && elapsed >= maxMs.current) void finishRef.current()
  }, [elapsed, recording])

  return { phase, interim, elapsed, start, cancel, finish, level: () => mic.current?.level() ?? 0, active: phase.kind !== 'idle' }
}

export type Dictation = ReturnType<typeof useDictation>

