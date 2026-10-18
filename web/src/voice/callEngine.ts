import { ApiError, approvalsDecide, chatListThreads, chatRespond, chatSend, chatStop, voiceCallEnd } from '../api/generated'
import type { Ask } from '../chat/ask'
import { activeTask, type ChatMsg, type ChatState } from '../chat/assemble'
import { chatStore, loadThread } from '../chat/chatStore'
import { inboxStore, type InboxItem } from '../data/inbox'
import { createStore } from '../data/store'
import { browserRecognition, decision, engineNeedsTap, loadVoiceSettings, Mic, opNeedsTap, speak, stopPlayback, TAP_MESSAGE, transcribe, type Recognition, type VoiceSettings } from './voice'

// 通話（K01 桌面、L18 手機，VC-3 的最小版）：半雙工的一來一回——
// 聽（音量判斷你在說話、停一下就算說完）→ 辨識 → 交給 Bot（xbot.chat.send）→ 字幕跟著回覆 → 念出來 → 再聽。
// 打斷：停止念、回到聽。靜音：不聽。核定卡：說「允許」「拒絕」（聽不清楚再問一次，不猜）或直接按，經 xbot.chat.respond。
// 掛斷：在對話裡留一列通話紀錄（xbot.voice.call_end）。

export type CallPhase = 'connecting' | 'listening' | 'hearing' | 'transcribing' | 'thinking' | 'working' | 'speaking' | 'waiting' | 'muted' | 'ended' | 'error'

/** 通話中的核定：Bot 在對話裡問的（chat.respond），或等核准的操作（approvals.decide，有 approvalId）。
 * tapOnly：高風險，要在畫面上按（用說的只能拒絕）。 */
export type CallAsk = Ask & { approvalId?: string; tapOnly: boolean }

export type CallLine = { id: string; who: 'me' | 'bot' | 'system'; text: string; at: number }

export type CallState = {
  phase: CallPhase
  muted: boolean
  captions: boolean
  threadId?: string
  startedAt: number
  /** 你說了幾句。 */
  turns: number
  /** 這通電話的逐字紀錄（K01 右欄）。 */
  lines: CallLine[]
  /** 正在說的這句（字幕）。 */
  caption: { who: 'me' | 'bot'; text: string } | null
  /** 上一句（L18：字幕顯示上一句與目前這句）。 */
  previous: { who: 'me' | 'bot'; text: string } | null
  /** Bot 在等你核定或回答（C12 的卡）。 */
  ask?: CallAsk
  /** 核定的結果（「已允許這次」）。 */
  decided?: { text: string; tone: 'ok' | 'muted' }
  error?: string
  /** 用哪種辨識：xbot（經 XBot 的語音引擎）、browser（瀏覽器內建）。 */
  stt?: 'xbot' | 'browser'
}

const THRESHOLD = 0.02 // 音量（RMS）超過這個算在說話
const END_SILENCE = 1200 // 說話後安靜這麼久算說完
const MIN_SPEECH = 300 // 太短的聲音不算（咳嗽、碰到麥克風）

const errText = (e: unknown) => (e instanceof ApiError || e instanceof Error ? e.message : String(e))

let lineSeq = 0
const lineId = () => `l${++lineSeq}`

export class CallEngine {
  readonly store = createStore<CallState>({ phase: 'connecting', muted: false, captions: true, startedAt: Date.now(), turns: 0, lines: [], caption: null, previous: null })
  private mic: Mic | null = null
  private rec: Recognition | null = null
  private settings: VoiceSettings | null = null
  private timer: ReturnType<typeof setInterval> | null = null
  private unsub: (() => void) | null = null
  private lastVoice = 0
  private lastActivity = Date.now()
  private taskId: string | null = null
  private spoken = new Set<string>()
  private announced = new Set<string>()
  private disposed = false
  private busy = false // 正在辨識、送出：不聽
  private started = false
  private pendingEnd: ReturnType<typeof setTimeout> | null = null

  constructor(
    readonly botId: string,
    threadId?: string,
  ) {
    if (threadId) this.patch({ threadId })
  }

  get state() {
    return this.store.get()
  }

  private patch(p: Partial<CallState>) {
    if (this.disposed && p.phase !== 'ended') return
    this.store.set((s) => ({ ...s, ...p }))
  }

  private setCaption(who: 'me' | 'bot', text: string) {
    const s = this.state
    const prev = s.caption && (s.caption.who !== who || !text.startsWith(s.caption.text.slice(0, 8))) ? s.caption : s.previous
    this.patch({ caption: { who, text }, previous: prev })
  }

  private addLine(who: CallLine['who'], text: string) {
    this.patch({ lines: [...this.state.lines, { id: lineId(), who, text, at: Date.now() }] })
  }

  async start() {
    if (this.started) return
    this.started = true
    try {
      this.settings = await loadVoiceSettings(true)
      let threadId = this.state.threadId
      if (!threadId) {
        const ths = await chatListThreads({ bot_id: this.botId }).catch(() => null)
        threadId = ths?.threads[0]?.id
      }
      if (threadId) {
        this.patch({ threadId })
        await loadThread(threadId).catch(() => {})
      }
      const useXbot = this.settings.stt_ready
      if (!useXbot && !browserRecognition()) throw new Error('還沒設定語音輸入的引擎，這個瀏覽器也沒有內建的辨識。請到「聲音」的進階設定選一個。')
      this.mic = await Mic.open()
      if (this.disposed) return this.mic.close()
      this.patch({ stt: useXbot ? 'xbot' : 'browser', startedAt: Date.now() })
      const unChat = chatStore.subscribe(() => this.watch(chatStore.get()))
      const unInbox = inboxStore.subscribe(() => this.watchInbox(inboxStore.get().items))
      this.unsub = () => {
        unChat()
        unInbox()
      }
      this.watchInbox(inboxStore.get().items)
      this.timer = setInterval(() => this.tick(), 80)
      this.listen()
    } catch (e) {
      this.patch({ phase: 'error', error: errText(e) })
    }
  }

  /** 回到聽。 */
  private listen() {
    if (this.disposed) return
    if (this.state.muted) return this.patch({ phase: 'muted' })
    this.busy = false
    this.lastActivity = Date.now()
    this.patch({ phase: this.state.ask ? 'waiting' : 'listening' })
    if (this.state.stt === 'browser') this.startRecognition()
  }

  private listening(): boolean {
    const p = this.state.phase
    return !this.busy && !this.state.muted && (p === 'listening' || p === 'hearing' || p === 'waiting')
  }

  // ── 聽 ─────────────────────────────────────────────

  private tick() {
    const mic = this.mic
    if (!mic || this.disposed) return
    const now = Date.now()
    const hangup = (this.settings?.settings.hangup_minutes ?? 5) * 60_000
    if (this.listening() && this.state.stt === 'xbot') {
      const loud = mic.level() > THRESHOLD
      if (loud) {
        this.lastVoice = now
        this.lastActivity = now
        if (!mic.recording) {
          mic.start()
          this.patch({ phase: 'hearing' })
        }
      } else if (mic.recording && now - this.lastVoice > END_SILENCE) {
        void this.endUtterance()
      }
      if (mic.recording && mic.elapsed() > (this.settings?.limits.max_seconds ?? 120) * 1000) void this.endUtterance()
    }
    const quiet = this.state.phase === 'listening' || this.state.phase === 'waiting' || this.state.phase === 'muted'
    if (quiet && now - this.lastActivity > hangup) void this.hangup('idle')
  }

  private async endUtterance() {
    const mic = this.mic
    if (!mic || this.busy) return
    this.busy = true
    const rec = await mic.stop()
    if (rec.ms - END_SILENCE < MIN_SPEECH || rec.blob.size === 0) return this.listen()
    this.patch({ phase: 'transcribing' })
    let text: string
    try {
      text = await transcribe(rec, this.botId)
    } catch (e) {
      this.addLine('system', '沒聽清楚：' + errText(e))
      return this.listen()
    }
    if (this.disposed) return
    if (!text) return this.listen()
    await this.heard(text)
  }

  private startRecognition() {
    const Ctor = browserRecognition()
    if (!Ctor || this.rec) return
    const r = new Ctor()
    r.lang = this.settings?.lang || 'zh-TW'
    r.continuous = false
    r.interimResults = true
    r.onresult = (e) => {
      let fin = ''
      let mid = ''
      for (let i = 0; i < e.results.length; i++) {
        const res = e.results[i]
        if (res.isFinal) fin += res[0].transcript
        else mid += res[0].transcript
      }
      this.lastActivity = Date.now()
      if (mid && !fin) {
        this.patch({ phase: 'hearing' })
        this.setCaption('me', mid)
      }
      if (fin) {
        this.rec = null
        r.abort()
        this.busy = true
        void this.heard(fin.trim())
      }
    }
    r.onend = () => {
      if (this.rec === r) this.rec = null
      if (this.listening()) setTimeout(() => this.listening() && this.startRecognition(), 150)
    }
    r.onerror = () => {}
    this.rec = r
    try {
      r.start()
    } catch {
      this.rec = null
    }
  }

  private stopRecognition() {
    const r = this.rec
    this.rec = null
    r?.abort()
  }

  // ── 聽到了 ─────────────────────────────────────────

  private async heard(text: string) {
    this.stopRecognition()
    this.busy = true
    this.lastActivity = Date.now()
    this.setCaption('me', text)
    this.addLine('me', text)
    this.patch({ turns: this.state.turns + 1 })
    const ask = this.state.ask
    if (ask) {
      if (ask.kind === 'approval') {
        const d = decision(text)
        if (!d) return this.say(ask.tapOnly ? '我沒聽清楚。這件事要在畫面上按確認；要拒絕可以直接說「拒絕」。' : '我沒聽清楚。請說「允許」或「拒絕」，也可以直接按。')
        if (d === 'allow' && ask.tapOnly) return this.say(TAP_MESSAGE + '。')
        return this.decide(d === 'allow' ? 'once' : 'deny', true)
      }
      if (ask.kind === 'question') {
        try {
          await chatRespond({ inbox_id: ask.inboxId, text })
          this.patch({ ask: undefined, decided: { text: `你回答了：${text}`, tone: 'ok' }, phase: 'thinking' })
        } catch (e) {
          this.addLine('system', errText(e))
          this.listen()
        }
        return
      }
      return this.say('這個要在畫面上輸入，不能用說的。')
    }
    try {
      const out = await chatSend({ bot_id: this.botId, text, thread_id: this.state.threadId })
      this.taskId = out.task_id
      if (out.thread_id !== this.state.threadId) {
        this.patch({ threadId: out.thread_id })
        void loadThread(out.thread_id).catch(() => {})
      }
      this.patch({ phase: 'thinking', decided: undefined })
      this.watch(chatStore.get())
    } catch (e) {
      if (e instanceof ApiError && e.code === 'conflict' && e.retryable) return this.say('我正在忙另一件事，等我做完再說，或按打斷讓我停下來。')
      this.addLine('system', errText(e))
      this.listen()
    }
  }

  /** 核定（按的或說的）：xbot.chat.respond。 */
  async decide(choice: 'once' | 'deny', spoken = false) {
    const ask = this.state.ask
    if (!ask) return
    // 用說的決定帶 via=voice：服務端會再擋一次高風險的允許。
    const via = spoken ? ('voice' as const) : undefined
    try {
      if (ask.approvalId) await approvalsDecide({ approval_id: ask.approvalId, decision: choice === 'deny' ? 'deny' : 'allow', via })
      else await chatRespond({ inbox_id: ask.inboxId, choice, via })
      this.announced.add(ask.inboxId)
      const text = choice === 'deny' ? '已拒絕' : '已允許這次'
      this.addLine('system', `${spoken ? '你說' : '你按了'}「${choice === 'deny' ? '拒絕' : '允許'}」· ${text}`)
      this.patch({ ask: undefined, decided: { text, tone: choice === 'deny' ? 'muted' : 'ok' }, phase: 'thinking' })
      this.busy = false
    } catch (e) {
      if (spoken && e instanceof ApiError && e.code === 'forbidden') {
        // 服務端判斷要在畫面上按：卡片改成只能按
        this.patch({ ask: { ...ask, tapOnly: true } })
        return this.say(e.message + '。')
      }
      this.addLine('system', errText(e))
      this.listen()
    }
  }

  /** 等核准的操作（approvals.decide）：這條對話串裡、這個 Bot 的。 */
  private watchInbox(items: InboxItem[] | null) {
    const tid = this.state.threadId
    if (!items || !tid || this.disposed || this.state.ask) return
    const it = items.find((x) => x.kind === 'approval' && x.ref && !x.task_id && x.bot_id === this.botId && x.thread_id === tid && !this.announced.has(x.id))
    if (!it) return
    const p = (it.payload ?? {}) as { op?: string; params?: { subject?: string; details?: string; category?: string } }
    const ask: CallAsk = { inboxId: it.id, approvalId: it.ref, kind: 'approval', prompt: it.title, description: p.params?.subject || it.title, tapOnly: opNeedsTap(p.op, p.params) }
    this.announced.add(it.id)
    this.announce(ask)
  }

  private announce(ask: CallAsk) {
    this.patch({ ask, decided: undefined })
    const what = ask.description || ask.prompt || ask.command || '一件事'
    if (ask.kind === 'approval') void this.say(ask.tapOnly ? `需要你核定：${what}。這件事要在畫面上按確認；要拒絕可以直接說。` : `需要你核定：${what}。說「允許」或「拒絕」。`)
    else void this.say(ask.kind === 'secret' ? `需要你在畫面上輸入：${what}。` : `我想問你：${ask.prompt}`)
  }

  // ── Bot 那邊 ───────────────────────────────────────

  private watch(s: ChatState) {
    const tid = this.state.threadId
    if (!tid || this.disposed) return
    const act = this.taskId ? s.tasks[this.taskId] : activeTask(s, tid)
    // 等你核定或回答
    const req = act?.request
    if (req && !this.announced.has(req.inboxId) && this.state.ask?.inboxId !== req.inboxId) {
      this.announced.add(req.inboxId)
      this.announce({ ...req, tapOnly: req.kind === 'approval' && engineNeedsTap(req) })
      return
    }
    if (!req && this.state.ask && !this.state.ask.approvalId && act && act.state !== 'waiting_approval' && act.state !== 'waiting_input') this.patch({ ask: undefined })
    if (!this.taskId) return
    const reply = this.replyOf(s, tid, this.taskId)
    const p = this.state.phase
    if (reply && reply.streaming && reply.content && (p === 'thinking' || p === 'working')) this.setCaption('bot', reply.content)
    else if (act && act.steps.some((x) => x.status === 'running') && p === 'thinking') this.patch({ phase: 'working' })
    if (reply && !reply.streaming && reply.status && !this.spoken.has(reply.id)) {
      this.spoken.add(reply.id)
      void this.speakReply(reply)
    } else if (act?.state === 'done' && !reply && (p === 'thinking' || p === 'working')) {
      this.taskId = null
      this.listen()
    }
  }

  private replyOf(s: ChatState, threadId: string, taskId: string): ChatMsg | undefined {
    return s.threads[threadId]?.messages.findLast((m) => m.taskId === taskId && m.sender !== 'owner' && m.kind === 'text')
  }

  private async speakReply(m: ChatMsg) {
    this.stopRecognition()
    this.busy = true
    this.taskId = null
    this.setCaption('bot', m.content)
    this.addLine('bot', m.content)
    this.patch({ phase: 'speaking' })
    try {
      if (m.content.trim()) await speak('call:' + m.id, { bot_id: this.botId, message_id: m.id, voice_note: true })
    } catch (e) {
      this.addLine('system', '念不出來：' + errText(e))
    }
    if (this.state.phase === 'speaking') this.listen()
  }

  private async say(text: string) {
    this.stopRecognition()
    this.busy = true
    this.setCaption('bot', text)
    this.patch({ phase: 'speaking' })
    try {
      await speak('call:say', { bot_id: this.botId, text })
    } catch {
      // 念不出來也沒關係，字幕上有
    }
    if (this.state.phase === 'speaking') this.listen()
  }

  // ── 控制 ───────────────────────────────────────────

  /** 打斷：停止念、回到聽（K01、L18）。 */
  interrupt() {
    stopPlayback()
    this.mic?.cancel()
    this.stopRecognition()
    this.busy = false
    this.addLine('system', '你打斷了')
    this.listen()
  }

  /** 也請 Bot 停下正在做的事。 */
  async stopWork() {
    this.interrupt()
    await chatStop({ bot_id: this.botId, thread_id: this.state.threadId }).catch(() => {})
  }

  toggleMute() {
    const muted = !this.state.muted
    this.patch({ muted })
    if (muted) {
      this.mic?.cancel()
      this.stopRecognition()
      if (this.listening() || ['listening', 'hearing', 'waiting'].includes(this.state.phase)) this.patch({ phase: 'muted' })
    } else if (this.state.phase === 'muted') this.listen()
  }

  toggleCaptions() {
    this.patch({ captions: !this.state.captions })
  }

  /** 離開頁面時掛斷；React 開發模式會卸載再掛上一次，所以晚一點才真的掛，keep() 取消。 */
  hangupSoon() {
    this.pendingEnd = setTimeout(() => void this.hangup(), 0)
  }

  keep() {
    if (this.pendingEnd) clearTimeout(this.pendingEnd)
    this.pendingEnd = null
  }

  /** 掛斷：停下一切，對話裡留一列通話紀錄。回傳通話所在的對話串。 */
  async hangup(reason: 'hangup' | 'idle' | 'error' = 'hangup'): Promise<string | undefined> {
    if (this.state.phase === 'ended') return this.state.threadId
    const s = this.state
    this.dispose()
    this.patch({ phase: 'ended' })
    if (s.threadId && s.phase !== 'error') {
      await voiceCallEnd({ bot_id: this.botId, thread_id: s.threadId, duration_s: Math.round((Date.now() - s.startedAt) / 1000), turns: s.turns, reason }).catch(() => {})
    }
    return s.threadId
  }

  dispose() {
    if (this.disposed) return
    this.disposed = true
    if (this.timer) clearInterval(this.timer)
    this.unsub?.()
    this.stopRecognition()
    stopPlayback()
    this.mic?.close()
    this.mic = null
  }

  /** 麥克風音量（畫聲波）。 */
  level = () => this.mic?.level() ?? 0
}
