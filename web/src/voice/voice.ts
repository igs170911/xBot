import { voiceSettingsGet, voiceSpeak, voiceTranscribeForm, type VoiceSettingsGetOutput, type VoiceSpeakInput, type VoiceSpeakOutput } from '../api/generated'
import { createStore } from '../data/store'

// 語音（E32，PRD §6.10 VC-1～VC-3）：設定、播放（合成好的音訊或瀏覽器內建的聲音）、錄音與辨識。
// 合成與辨識都經 XBot（xbot.voice.speak／transcribe → Model Proxy），金鑰不會到瀏覽器；
// 沒有語音引擎時用瀏覽器內建的聲音（speechSynthesis）與辨識（SpeechRecognition，有的瀏覽器沒有）。

export type VoiceSettings = VoiceSettingsGetOutput

// ── 設定 ─────────────────────────────────────────────

export const voiceSettingsStore = createStore<VoiceSettings | null>(null)
let loading: Promise<VoiceSettings> | null = null

/** 讀語音設定（同一個頁面共用一份）；force 時重讀。 */
export function loadVoiceSettings(force = false): Promise<VoiceSettings> {
  const cur = voiceSettingsStore.get()
  if (cur && !force) return Promise.resolve(cur)
  if (loading && !force) return loading
  loading = voiceSettingsGet({})
    .then((s) => {
      voiceSettingsStore.set(s)
      return s
    })
    .finally(() => {
      loading = null
    })
  return loading
}

// ── 播放 ─────────────────────────────────────────────

/** 現在在念哪一段（key 由呼叫的人決定，例：訊息 id、試聽的聲音）。 */
export type PlayerState = { key: string | null; phase: 'idle' | 'loading' | 'playing'; error?: string }
export const playerStore = createStore<PlayerState>({ key: null, phase: 'idle' })

let stopCurrent: (() => void) | null = null
let token = 0

/** 停止正在念的（打斷）。 */
export function stopPlayback() {
  token++
  const s = stopCurrent
  stopCurrent = null
  s?.()
  playerStore.set({ key: null, phase: 'idle' })
}

export function canBrowserSpeak(): boolean {
  return typeof window !== 'undefined' && 'speechSynthesis' in window && typeof SpeechSynthesisUtterance !== 'undefined'
}

function pickVoice(lang: string): SpeechSynthesisVoice | undefined {
  const voices = window.speechSynthesis.getVoices()
  return voices.find((v) => v.lang === lang) ?? voices.find((v) => v.lang.startsWith('zh')) ?? undefined
}

/** 用瀏覽器內建的聲音念；念完（或停止、出錯）時 resolve。有的瀏覽器不會回報念完，所以照字數估一個最長時間。 */
function speakBrowser(text: string, rate: number, lang: string, register: (stop: () => void) => void): Promise<void> {
  return new Promise((resolve) => {
    if (!canBrowserSpeak() || !text) return resolve()
    const s = window.speechSynthesis
    let done = false
    const finish = () => {
      if (done) return
      done = true
      clearTimeout(timer)
      resolve()
    }
    const timer = setTimeout(finish, Math.min(90_000, 1500 + (text.length * 280) / Math.max(rate, 0.5)))
    register(() => {
      s.cancel()
      finish()
    })
    s.cancel()
    const u = new SpeechSynthesisUtterance(text)
    u.lang = lang
    u.rate = rate
    const v = pickVoice(lang)
    if (v) u.voice = v
    u.onend = finish
    u.onerror = finish
    s.speak(u)
  })
}

function playUrl(url: string, register: (stop: () => void) => void, onStart: () => void): Promise<void> {
  return new Promise((resolve, reject) => {
    const a = new Audio(url)
    let done = false
    const finish = (err?: string) => {
      if (done) return
      done = true
      a.onended = a.onerror = null
      if (err) reject(new Error(err))
      else resolve()
    }
    register(() => {
      a.pause()
      a.removeAttribute('src')
      finish()
    })
    a.onended = () => finish()
    a.onerror = () => finish('播放不了這段聲音')
    a.onplaying = onStart
    a.play().catch((e: unknown) => finish(e instanceof Error && e.name === 'NotAllowedError' ? '瀏覽器擋住了自動播放，請按一下播放' : '播放不了這段聲音'))
  })
}

/** 播放 xbot.voice.speak 的結果：有音訊網址就播網址，否則用瀏覽器內建的聲音念 text。念完 resolve；被別的打斷也 resolve。 */
export async function play(key: string, out: VoiceSpeakOutput): Promise<void> {
  stopPlayback()
  const my = ++token
  const register = (stop: () => void) => {
    if (my === token) stopCurrent = stop
    else stop()
  }
  playerStore.set({ key, phase: 'playing' })
  try {
    if (out.engine === 'proxy' && out.url) await playUrl(out.url, register, () => {})
    else await speakBrowser(out.text, out.speed || 1, out.lang || 'zh-TW', register)
  } catch (e) {
    if (my === token) playerStore.set({ key, phase: 'idle', error: e instanceof Error ? e.message : String(e) })
    throw e
  }
  if (my === token) {
    stopCurrent = null
    playerStore.set({ key: null, phase: 'idle' })
  }
}

/** 請 XBot 準備好聲音（合成或給瀏覽器念的文字）再播放。 */
export async function speak(key: string, input: VoiceSpeakInput): Promise<VoiceSpeakOutput> {
  stopPlayback()
  const my = token
  playerStore.set({ key, phase: 'loading' })
  let out: VoiceSpeakOutput
  try {
    out = await voiceSpeak(input)
  } catch (e) {
    if (my === token) playerStore.set({ key: null, phase: 'idle', error: e instanceof Error ? e.message : String(e) })
    throw e
  }
  if (my !== token) return out // 準備的時候被打斷了
  await play(key, out)
  return out
}

// ── 錄音 ─────────────────────────────────────────────

export type Recording = { blob: Blob; ms: number }

function recorderType(): string {
  if (typeof MediaRecorder === 'undefined') return ''
  for (const t of ['audio/webm;codecs=opus', 'audio/webm', 'audio/ogg;codecs=opus', 'audio/mp4']) if (MediaRecorder.isTypeSupported?.(t)) return t
  return ''
}

export function canRecord(): boolean {
  return typeof navigator !== 'undefined' && !!navigator.mediaDevices?.getUserMedia && typeof MediaRecorder !== 'undefined'
}

/** 麥克風：音量（畫聲波、判斷有沒有在說話）與錄音。用完要 close。 */
export class Mic {
  private ctx: AudioContext | null = null
  private analyser: AnalyserNode | null = null
  private buf: Float32Array<ArrayBuffer> | null = null
  private rec: MediaRecorder | null = null
  private chunks: Blob[] = []
  private started = 0
  private constructor(readonly stream: MediaStream) {}

  static async open(): Promise<Mic> {
    if (!canRecord()) throw new Error('這個瀏覽器不能錄音')
    let stream: MediaStream
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true } })
    } catch (e) {
      const name = e instanceof Error ? e.name : ''
      throw new Error(name === 'NotAllowedError' ? '沒有麥克風權限：請在瀏覽器允許使用麥克風' : name === 'NotFoundError' ? '找不到麥克風' : '打不開麥克風', { cause: e })
    }
    const m = new Mic(stream)
    try {
      const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
      if (Ctx) {
        m.ctx = new Ctx()
        void m.ctx.resume().catch(() => {})
        const src = m.ctx.createMediaStreamSource(stream)
        m.analyser = m.ctx.createAnalyser()
        m.analyser.fftSize = 1024
        src.connect(m.analyser)
        m.buf = new Float32Array(m.analyser.fftSize)
      }
    } catch {
      m.analyser = null
    }
    return m
  }

  /** 目前的音量（0～1，RMS）。 */
  level(): number {
    if (!this.analyser || !this.buf) return 0
    this.analyser.getFloatTimeDomainData(this.buf)
    let sum = 0
    for (const v of this.buf) sum += v * v
    return Math.sqrt(sum / this.buf.length)
  }

  get recording(): boolean {
    return this.rec?.state === 'recording'
  }

  /** 錄了多久（毫秒）。 */
  elapsed(): number {
    return this.recording ? performance.now() - this.started : 0
  }

  start() {
    if (this.recording) return
    const type = recorderType()
    this.rec = new MediaRecorder(this.stream, type ? { mimeType: type } : undefined)
    this.chunks = []
    this.rec.ondataavailable = (e) => {
      if (e.data.size) this.chunks.push(e.data)
    }
    this.rec.start(250)
    this.started = performance.now()
  }

  /** 停止錄音，回傳這一段。 */
  stop(): Promise<Recording> {
    const rec = this.rec
    if (!rec || rec.state === 'inactive') return Promise.resolve({ blob: new Blob([]), ms: 0 })
    const ms = performance.now() - this.started
    return new Promise((resolve) => {
      rec.onstop = () => resolve({ blob: new Blob(this.chunks, { type: rec.mimeType || 'audio/webm' }), ms })
      rec.stop()
    })
  }

  /** 丟掉這一段（取消）。 */
  cancel() {
    const rec = this.rec
    if (rec && rec.state !== 'inactive') {
      rec.onstop = null
      rec.stop()
    }
    this.chunks = []
  }

  close() {
    this.cancel()
    this.stream.getTracks().forEach((t) => t.stop())
    void this.ctx?.close().catch(() => {})
  }
}

/** 把錄音送去辨識（經 XBot 的語音引擎）。 */
export async function transcribe(rec: Recording, botId?: string): Promise<string> {
  const out = await voiceTranscribeForm(rec.blob, { bot_id: botId, duration_ms: rec.ms })
  return out.text.trim()
}

// ── 瀏覽器內建的辨識（沒有語音輸入引擎時）────────────────

type RecognitionResult = { isFinal: boolean; 0: { transcript: string } }
export type Recognition = {
  lang: string
  continuous: boolean
  interimResults: boolean
  onresult: ((e: { resultIndex: number; results: ArrayLike<RecognitionResult> }) => void) | null
  onerror: ((e: { error: string }) => void) | null
  onend: (() => void) | null
  start: () => void
  stop: () => void
  abort: () => void
}

export function browserRecognition(): (new () => Recognition) | null {
  if (typeof window === 'undefined') return null
  const w = window as unknown as { SpeechRecognition?: new () => Recognition; webkitSpeechRecognition?: new () => Recognition }
  return w.SpeechRecognition ?? w.webkitSpeechRecognition ?? null
}

/** 錄音長度的文字：0:07。 */
export function clock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

/** 依 id 產生固定的聲波高度（語音訊息卡用）。 */
export function waveform(seed: string, n: number): number[] {
  let h = 2166136261
  for (const c of seed) h = Math.imul(h ^ c.charCodeAt(0), 16777619)
  const out: number[] = []
  for (let i = 0; i < n; i++) {
    h = Math.imul(h ^ (h >>> 13), 1274126177)
    const r = ((h >>> 0) % 1000) / 1000
    const env = Math.sin(((i + 0.5) / n) * Math.PI)
    out.push(0.25 + 0.75 * r * (0.45 + 0.55 * env))
  }
  return out
}

/** 語音訊息大約多長（秒）：中文每秒約 4.5 字。 */
export function estimateSeconds(chars: number, speed = 1): number {
  return Math.max(1, Math.round(chars / 4.5 / Math.max(speed, 0.5)))
}

// ── 用說的核定的界線（和服務端 internal/voice/guard.go 一樣）────────────

/** 用說的允許被擋下時的訊息。 */
export const TAP_MESSAGE = '這件事要在畫面上按確認，不能只用說的'

const RISKY = /刪除|移除|清除|清空|銷毀|付款|支付|匯款|轉帳|花錢|花費|支出|購買|下單|扣款|刷卡|退款|金額|NT\$|\$\s?\d|\d\s*元|對外|寄出|寄信|發信|發送|發布|公開|上架|簽約|合約|不可逆|無法復原|危險|\bdelete\b|\bremove\b|\bdrop\b|\bdestroy\b|\bpay(ment)?\b|\bpurchase\b|\btransfer\b|\bsend\b|\bemail\b|\bpublish\b|\bcontract\b|irreversible|danger/i

export function risky(...texts: (string | undefined)[]): boolean {
  return texts.some((t) => !!t && RISKY.test(t))
}

/** Bot 在對話裡要求核准的引擎動作：終端機指令、看得出高風險的，要在畫面上按。 */
export function engineNeedsTap(ask: { command?: string; description?: string; prompt?: string }): boolean {
  return !!ask.command || risky(ask.description, ask.prompt)
}

/** 等核准的操作：confirm 級的一律要按；Bot 請你決定（xbot.approvals.request）而且看不出高風險的才可以用說的。 */
export function opNeedsTap(op: string | undefined, params: { subject?: string; details?: string; category?: string } | undefined): boolean {
  if (op !== 'xbot.approvals.request') return true
  return risky(params?.subject, params?.details, params?.category)
}

/** 聽到的話是不是「允許」或「拒絕」（通話中的核定，L18：聽不清楚時再問一次，不猜）。 */
export function decision(text: string): 'allow' | 'deny' | null {
  const t = text.replace(/[\s，。！？、,.!?「」"']/g, '')
  if (!t) return null
  const negated = /不(允許|可以|行|要|准|同意)/g
  const deny = negated.test(t) || /拒絕|否決|^deny|^no(?![a-z])/i.test(t)
  const allow = /允許|可以|同意|批准|核准|沒問題|^好|^ok|^yes|^准/i.test(t.replace(negated, ''))
  if (deny === allow) return null
  return allow ? 'allow' : 'deny'
}
