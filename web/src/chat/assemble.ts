import type { ChatReadOutput, ChatStepsOutput, XBotEvent } from '../api/generated'
import { askFromEvent, type Answered, type Ask } from './ask'

// 把 chat.read 的快照和 SSE 事件組成畫面要的對話（純函式，好測）。
//
// 事件（internal/runtime）：
//   message.created   payload＝store.Message（scope_id 是對話串、meta 帶 task_id／streaming／status）
//   chat.text         即時、沒有 id；payload {message_id, text}，一段一段接上去
//   chat.thinking     即時；payload {message_id, text}
//   chat.tool_call    payload engine.Event {tool:{id,name,context,summary}}
//   chat.tool_result  payload engine.Event {tool:{id,ok,summary,duration_s}}
//   chat.needs_approval payload engine.Event {request:{id,kind,method,prompt,choices,params}}＋inbox_id、expires_at
//   chat.answered     payload {inbox_id, kind, resolution, text}（C12：回覆、過期、Bot 不等了）
//   chat.error        payload engine.Event {text, fatal}
//   chat.done         payload {message_id, status, verdict, text, judge}（text 是完整的最後回覆；judge 是做完了沒的系統判斷，X04）
//   task.state        payload {state, verdict}
// 有 id 的事件斷線重連時會補送：id 不大於已處理過的就略過，所以不會重複。
// 補送的事件沒有 thread_id，靠 task_id／message_id 對回對話串。

export type ChatMsg = {
  id: string
  seq: number
  sender: string
  kind: string
  content: string
  taskId?: string
  streaming: boolean
  /** complete、interrupted、error */
  status?: string
  createdAt: number
  updatedAt: number
  /** 這則回覆是從頭看著它串流的：內容完整，比資料庫的快照（每隔一段時間才存）新。 */
  live: boolean
  /** 代理（MCP）替你送的訊息：哪個代理（MCP-13，C16「由 Claude Code（MCP）傳送」）。 */
  actorName?: string
  /** 從哪個出口送的：mcp、http、cli。 */
  via?: string
  /** 隊友交辦或群組裡 Bot 回 [silent]：不顯示。 */
  silent?: boolean
  /** kind=peer：和隊友往來的那一行（X02、C13）。 */
  peer?: PeerLine
  /** kind=card：Bot 發的卡片（資訊、核准、檔案，C11；檔案卡點了預覽或下載，C33）。 */
  card?: ChatCard
  /** kind=activity（sender=system）：哪一種系統紀錄，例：auto_continue（自動繼續，X04）。 */
  activity?: string
  /** Bot 回覆的完成判定（X04）：系統判斷做完了沒，與你的改正。 */
  judge?: Judge
}

/** 系統判斷一件工作做完了沒（X04，PRD §20 用途 2）。 */
export type Judge = {
  /** done、needs_person、handed_back、only_promised、stuck */
  verdict: string
  /** 已完成、需要你、交回給你、只說要做、卡住了 */
  label: string
  reason: string
  /** rules（規則）或 model（快判模型） */
  by: string
  confidence?: number
  /** 系統叫 Bot 自己接著做的那件工作 */
  continued_by?: string
  /** 你按「不對？」改成哪一種 */
  corrected?: string
}

/** Bot 用 xbot.cards.post 發的卡片（chat.read 的 card）。 */
export type ChatCard = {
  type: string
  title: string
  body?: string
  approval_id?: string
  category?: string
  files?: { path: string; name?: string }[]
}

/** 對話裡和隊友往來的一行（chat.read 的 peer）。 */
export type PeerLine = {
  event: string
  peer_id?: string
  peer_name?: string
  intent?: string
  message_id?: string
  hop?: number
  chain?: string
  room_id?: string
  files?: number
  /** X03：提案與討論的 id（event=proposal／discussion）。 */
  agenda_id?: string
}

export type Step = {
  id: string
  name: string
  label?: string
  status: 'running' | 'ok' | 'fail'
  at: number
  endAt?: number
}

export type Activity = {
  taskId: string
  botId?: string
  threadId?: string
  /** queued、waiting_box、running、waiting_input、waiting_approval、stopping、done */
  state: string
  /** answer、cancelled、failed */
  verdict?: string
  steps: Step[]
  thinking: boolean
  startedAt?: number
  endedAt?: number
  error?: string
  /** Bot 正在等你回覆的問題（C12）。 */
  request?: Ask
  /** 這件工作裡回覆過的問題（一行結果）。 */
  answered?: Answered[]
  /** 還在等電腦的原因（B12：主機離線、主機滿了排隊）。 */
  wait?: BoxWait
}

/** 開機前排隊的原因（task.state 的 wait，B12 CL-4、CL-6）。 */
export type BoxWait = { reason: 'host_offline' | 'host_full' | string; host_name?: string; position?: number; running?: number; max_running?: number }

/** waiting_box 時給人看的一句話；沒有排隊原因時是 undefined（就是在開機）。 */
export function waitText(w?: BoxWait): string | undefined {
  if (!w) return undefined
  const host = w.host_name ? `「${w.host_name}」` : ''
  if (w.reason === 'host_offline') return `主機${host}離線，工作先排隊`
  if (w.reason === 'host_full') return w.position ? `等電腦空出來（排第 ${w.position} 位）` : '等電腦空出來'
  return undefined
}

export type ThreadState = {
  id: string
  /** 依 seq 由舊到新。 */
  messages: ChatMsg[]
  loaded: boolean
  hasMore: boolean
}

export type ChatState = {
  threads: Record<string, ThreadState>
  tasks: Record<string, Activity>
  /** task_id → 對話串 */
  taskThread: Record<string, string>
  /** message_id → 對話串 */
  msgThread: Record<string, string>
  /** 已處理過的最大事件 id */
  lastId: number
}

export const emptyChat: ChatState = { threads: {}, tasks: {}, taskThread: {}, msgThread: {}, lastId: 0 }

/** 還不知道 seq 的訊息（只收到串流片段）排在最後。 */
const UNKNOWN_SEQ = Number.MAX_SAFE_INTEGER

type StoreMessage = {
  id: string
  seq: number
  scope: string
  scope_id: string
  sender: string
  kind: string
  content: string
  meta?: unknown
  created_at: number
  updated_at: number
}

type MessageMeta = {
  task_id?: string
  streaming?: boolean
  status?: string
  actor?: string
  actor_name?: string
  via?: string
  silent?: boolean
  peer?: PeerLine
  activity?: string
  judge?: Judge
  card?: ChatCard
}

type EngineEvent = {
  text?: string
  status?: string
  fatal?: boolean
  tool?: { id?: string; name?: string; context?: string; summary?: string; ok?: boolean; duration_s?: number }
}

function parseMeta(raw: unknown): MessageMeta {
  if (raw && typeof raw === 'object') return raw as MessageMeta
  if (typeof raw === 'string' && raw) {
    try {
      return JSON.parse(raw) as MessageMeta
    } catch {
      return {}
    }
  }
  return {}
}

function thread(s: ChatState, id: string): ThreadState {
  return s.threads[id] ?? { id, messages: [], loaded: false, hasMore: false }
}

function putMessages(s: ChatState, threadId: string, messages: ChatMsg[], patch: Partial<ThreadState> = {}): ChatState {
  const sorted = [...messages].sort((a, b) => a.seq - b.seq || a.createdAt - b.createdAt)
  const msgThread = { ...s.msgThread }
  const taskThread = { ...s.taskThread }
  for (const m of sorted) {
    msgThread[m.id] = threadId
    if (m.taskId) taskThread[m.taskId] = threadId
  }
  return { ...s, msgThread, taskThread, threads: { ...s.threads, [threadId]: { ...thread(s, threadId), ...patch, messages: sorted } } }
}

function updateMessage(s: ChatState, threadId: string, id: string, fn: (m: ChatMsg) => ChatMsg): ChatState {
  const th = thread(s, threadId)
  const i = th.messages.findIndex((m) => m.id === id)
  if (i < 0) return s
  const messages = th.messages.slice()
  messages[i] = fn(messages[i])
  return putMessages(s, threadId, messages)
}

function findThreadOfMessage(s: ChatState, id: string): string | undefined {
  return s.msgThread[id]
}

function activity(s: ChatState, taskId: string): Activity {
  return s.tasks[taskId] ?? { taskId, state: 'queued', steps: [], thinking: false }
}

function putActivity(s: ChatState, a: Activity): ChatState {
  return { ...s, tasks: { ...s.tasks, [a.taskId]: a } }
}

/** 套用 chat.read 的結果。資料庫是準的，只有「從頭看著串流、比快照新」的內容保留本地版本。 */
export function applyRead(s: ChatState, out: ChatReadOutput): ChatState {
  const threadId = out.thread.id
  const th = thread(s, threadId)
  const byId = new Map(th.messages.map((m) => [m.id, m]))
  for (const m of out.messages) {
    const local = byId.get(m.id)
    const server: ChatMsg = {
      id: m.id,
      seq: m.seq,
      sender: m.sender,
      kind: m.kind,
      content: m.content,
      taskId: m.task_id,
      streaming: !!m.streaming,
      status: m.status,
      createdAt: m.created_at,
      updatedAt: m.updated_at,
      live: false,
      ...(m.actor ? { actorName: m.actor_name || m.actor, via: m.via } : {}),
      ...(m.silent ? { silent: true } : {}),
      ...(m.peer ? { peer: m.peer } : {}),
      ...(m.activity ? { activity: m.activity } : {}),
      ...(m.judge ? { judge: m.judge as Judge } : {}),
      ...(m.card ? { card: m.card } : {}),
    }
    if (local && server.streaming && local.content.length > server.content.length && local.content.startsWith(server.content)) {
      // 還在串流：本地已經接到比快照更多的字
      byId.set(m.id, { ...server, content: local.content, live: local.live })
    } else if (local && !local.streaming && local.status && server.streaming) {
      // 本地已經收到 chat.done，快照是舊的
      byId.set(m.id, { ...local, seq: server.seq })
    } else {
      byId.set(m.id, server)
    }
  }
  let next = putMessages(s, threadId, [...byId.values()], { loaded: true, hasMore: out.has_more })
  // 從歷史訊息補上每件工作的起訖時間（沒有步驟紀錄時也能算出花了多久）
  for (const m of out.messages) {
    if (!m.task_id) continue
    // 只從歷史看不出還在不在做（例：剛好在開機）：標 unknown，等事件或 Bot 的回覆訊息再決定
    const a = next.tasks[m.task_id] ?? { taskId: m.task_id, state: 'unknown', steps: [], thinking: false }
    const patch: Partial<Activity> = { threadId }
    if (m.sender === 'owner' && !a.startedAt) patch.startedAt = m.created_at
    if (m.sender !== 'owner') {
      patch.botId = m.sender.startsWith('bot:') ? m.sender.slice(4) : a.botId
      if (m.streaming) {
        if (a.state === 'unknown' || a.state === 'queued') patch.state = 'running'
      } else if (m.status) {
        patch.state = 'done'
        patch.endedAt = a.endedAt ?? m.updated_at
        patch.verdict = a.verdict ?? (m.status === 'complete' ? 'answer' : m.status === 'interrupted' ? 'cancelled' : 'failed')
      }
    }
    next = putActivity(next, { ...a, ...patch })
  }
  return next
}

function threadOf(s: ChatState, e: XBotEvent, messageId?: string): string | undefined {
  return e.thread_id || (e.task_id ? s.taskThread[e.task_id] : undefined) || (messageId ? findThreadOfMessage(s, messageId) : undefined)
}

/** 套用一筆事件。 */
export function applyEvent(s: ChatState, e: XBotEvent): ChatState {
  if (e.id) {
    if (e.id <= s.lastId) return s // 補送的、已經處理過
    s = { ...s, lastId: e.id }
  }
  const p = (e.payload ?? {}) as Record<string, unknown>
  switch (e.kind) {
    case 'message.created': {
      const m = p as unknown as StoreMessage
      if (!m.id || m.scope !== 'thread' || !m.scope_id) return s
      const meta = parseMeta(m.meta)
      const th = thread(s, m.scope_id)
      const existing = th.messages.find((x) => x.id === m.id)
      const msg: ChatMsg = {
        id: m.id,
        seq: m.seq,
        sender: m.sender,
        kind: m.kind,
        content: existing && existing.content.length > m.content.length ? existing.content : m.content,
        taskId: meta.task_id,
        streaming: !!meta.streaming,
        status: meta.status,
        createdAt: m.created_at,
        updatedAt: m.updated_at,
        live: existing ? existing.live : !!meta.streaming && m.content === '',
        ...(meta.actor ? { actorName: meta.actor_name || meta.actor, via: meta.via } : {}),
        ...(meta.silent ? { silent: true } : {}),
        ...(meta.peer ? { peer: meta.peer } : {}),
        ...(meta.activity ? { activity: meta.activity } : {}),
        ...(meta.judge ? { judge: meta.judge } : existing?.judge ? { judge: existing.judge } : {}),
        ...(meta.card ? { card: meta.card } : {}),
      }
      const messages = existing ? th.messages.map((x) => (x.id === m.id ? msg : x)) : [...th.messages, msg]
      let next = putMessages(s, m.scope_id, messages)
      if (meta.task_id) {
        const a = activity(next, meta.task_id)
        const patch: Partial<Activity> = { threadId: m.scope_id, botId: a.botId ?? e.bot_id }
        if (m.sender === 'owner') patch.startedAt = a.startedAt ?? m.created_at
        next = putActivity(next, { ...a, ...patch })
      }
      return next
    }
    case 'chat.text': {
      const id = p.message_id as string | undefined
      const text = (p.text as string | undefined) ?? ''
      if (!id || !text) return s
      const tid = threadOf(s, e, id)
      if (!tid) return s
      let next = s
      if (!thread(s, tid).messages.some((m) => m.id === id)) {
        // 還沒看過這則（例：開網頁時它已經在串流）：先放一則，chat.read 或 chat.done 會補齊
        const now = e.at
        const placeholder: ChatMsg = { id, seq: UNKNOWN_SEQ, sender: e.bot_id ? 'bot:' + e.bot_id : 'bot', kind: 'text', content: '', taskId: e.task_id, streaming: true, createdAt: now, updatedAt: now, live: false }
        next = putMessages(s, tid, [...thread(s, tid).messages, placeholder])
      }
      next = updateMessage(next, tid, id, (m) => (m.streaming ? { ...m, content: m.content + text, updatedAt: e.at } : m))
      if (e.task_id) {
        const a = activity(next, e.task_id)
        if (a.thinking) next = putActivity(next, { ...a, thinking: false })
      }
      return next
    }
    case 'chat.thinking': {
      if (!e.task_id) return s
      const a = activity(s, e.task_id)
      return a.thinking ? s : putActivity(s, { ...a, thinking: true, threadId: a.threadId ?? e.thread_id })
    }
    case 'chat.tool_call': {
      if (!e.task_id) return s
      const ev = p as EngineEvent
      const a = activity(s, e.task_id)
      const id = ev.tool?.id || `${e.task_id}:${a.steps.length}`
      if (a.steps.some((x) => x.id === id)) return s
      const step: Step = { id, name: ev.tool?.name || '工具', label: ev.tool?.context || ev.tool?.summary || undefined, status: 'running', at: e.at }
      return putActivity(s, { ...a, thinking: false, steps: [...a.steps, step], threadId: a.threadId ?? e.thread_id, startedAt: a.startedAt ?? e.at })
    }
    case 'chat.tool_result': {
      if (!e.task_id) return s
      const ev = p as EngineEvent
      const a = activity(s, e.task_id)
      let i = ev.tool?.id ? a.steps.findIndex((x) => x.id === ev.tool?.id) : -1
      if (i < 0) i = a.steps.findLastIndex((x) => x.status === 'running')
      if (i < 0) return s
      const steps = a.steps.slice()
      steps[i] = { ...steps[i], status: ev.tool?.ok === false ? 'fail' : 'ok', endAt: e.at, label: steps[i].label ?? (ev.tool?.summary || undefined) }
      return putActivity(s, { ...a, steps })
    }
    case 'chat.needs_approval': {
      if (!e.task_id) return s
      const a = activity(s, e.task_id)
      const ask = askFromEvent(p)
      // 同一張卡又問一次（回覆沒送出去）：拿掉那一行結果
      const answered = ask ? a.answered?.filter((x) => x.inboxId !== ask.inboxId) : a.answered
      return putActivity(s, { ...a, request: ask, answered, threadId: a.threadId ?? e.thread_id })
    }
    case 'chat.answered': {
      if (!e.task_id) return s
      const a = activity(s, e.task_id)
      const ans: Answered = {
        inboxId: (p.inbox_id as string | undefined) ?? '',
        kind: (p.kind as string | undefined) ?? '',
        resolution: (p.resolution as string | undefined) ?? '',
        text: (p.text as string | undefined) ?? '',
        at: e.at,
      }
      const answered = [...(a.answered ?? []).filter((x) => x.inboxId !== ans.inboxId), ans]
      const request = a.request?.inboxId === ans.inboxId ? undefined : a.request
      return putActivity(s, { ...a, answered, request })
    }
    case 'chat.error': {
      if (!e.task_id) return s
      const ev = p as EngineEvent
      const a = activity(s, e.task_id)
      return putActivity(s, { ...a, error: ev.text || a.error || '發生錯誤' })
    }
    case 'task.state': {
      if (!e.task_id) return s
      const st = (p.state as string | undefined) ?? ''
      if (!st) return s
      const a = activity(s, e.task_id)
      const patch: Partial<Activity> = { state: st, botId: a.botId ?? e.bot_id, threadId: a.threadId ?? e.thread_id, startedAt: a.startedAt ?? e.at }
      if (st !== 'waiting_input' && st !== 'waiting_approval') patch.request = undefined
      patch.wait = st === 'waiting_box' ? (p.wait as BoxWait | undefined) : undefined
      if (st === 'done') {
        patch.verdict = (p.verdict as string | undefined) ?? a.verdict
        patch.endedAt = e.at
        patch.thinking = false
        patch.steps = a.steps.map((x) => (x.status === 'running' ? { ...x, status: 'ok' as const, endAt: e.at } : x))
      }
      let next = putActivity(s, { ...a, ...patch })
      if (e.thread_id && !next.taskThread[e.task_id]) next = { ...next, taskThread: { ...next.taskThread, [e.task_id]: e.thread_id } }
      return next
    }
    case 'chat.done': {
      const id = p.message_id as string | undefined
      const status = p.status as string | undefined
      const verdict = p.verdict as string | undefined
      const text = p.text as string | undefined
      const judge = p.judge as Judge | undefined
      let next = s
      if (e.task_id) {
        const a = activity(next, e.task_id)
        next = putActivity(next, { ...a, state: 'done', verdict: verdict ?? a.verdict, endedAt: a.endedAt ?? e.at, thinking: false, request: undefined })
      }
      if (!id) return next
      const tid = threadOf(next, e, id)
      if (!tid) return next
      return updateMessage(next, tid, id, (m) => ({ ...m, content: text ?? m.content, streaming: false, status: status ?? m.status, updatedAt: e.at, ...(judge ? { judge } : {}) }))
    }
  }
  return s
}

type StepEvent = ChatStepsOutput['events'][number]

/**
 * 套用 chat.steps 讀回來的歷史事件（重新整理後補回工作步驟）。
 * 比已處理過的事件新的照一般事件套用；舊的只補「加上去」的內容（步驟、回覆結果、錯誤），
 * 狀態只補還不知道的（unknown），不會把即時事件更新過的狀態倒回去。
 */
export function applySteps(s: ChatState, evs: StepEvent[]): ChatState {
  for (const raw of evs) {
    const e: XBotEvent = { ...raw }
    if (!e.id || e.id > s.lastId) {
      s = applyEvent(s, e)
      continue
    }
    const tid = e.task_id
    if (!tid) continue
    const a = s.tasks[tid]
    const replay = (): ChatState => ({ ...applyEvent({ ...s, lastId: 0 }, e), lastId: s.lastId })
    switch (e.kind) {
      case 'chat.tool_call':
      case 'chat.tool_result':
      case 'chat.error':
      case 'chat.answered':
        s = replay()
        break
      case 'task.state':
        if (!a || a.state === 'unknown') s = replay()
        break
      case 'chat.needs_approval':
        if (a && (a.state === 'waiting_input' || a.state === 'waiting_approval') && !a.request) s = replay()
        break
    }
    if (e.thread_id && !s.taskThread[tid]) s = { ...s, taskThread: { ...s.taskThread, [tid]: e.thread_id } }
  }
  return s
}

// ── 給畫面用的整理 ──────────────────────────────────

export type ChatItem =
  | { type: 'message'; msg: ChatMsg; activity?: Activity }
  | { type: 'activity'; activity: Activity }

/** 工作還在進行（沒有 done）。 */
export function isActive(a: Activity | undefined): boolean {
  return !!a && a.state !== 'done' && a.state !== 'unknown'
}

/**
 * 一條對話串要畫的項目：訊息依序排列；Bot 的回覆前面接著那件工作的進度。
 * 還沒有回覆訊息的工作（開機中、剛送出）接在最後。
 */
export function threadItems(s: ChatState, threadId: string): ChatItem[] {
  const th = s.threads[threadId]
  const items: ChatItem[] = []
  const shown = new Set<string>()
  for (const m of th?.messages ?? []) {
    // 你的訊息、系統記的一行（自動繼續）不接工作進度：進度接在 Bot 的回覆上。
    const a = m.sender !== 'owner' && m.kind !== 'activity' && m.taskId ? s.tasks[m.taskId] : undefined
    if (a) shown.add(a.taskId)
    items.push({ type: 'message', msg: m, activity: a })
  }
  for (const [taskId, tid] of Object.entries(s.taskThread)) {
    if (tid !== threadId || shown.has(taskId)) continue
    const a = s.tasks[taskId]
    if (a && (isActive(a) || a.steps.length > 0 || a.error)) items.push({ type: 'activity', activity: a })
  }
  return items
}

/** 這條對話串正在進行的工作（最後一件）。 */
export function activeTask(s: ChatState, threadId: string): Activity | undefined {
  let found: Activity | undefined
  for (const [taskId, tid] of Object.entries(s.taskThread)) {
    if (tid !== threadId) continue
    const a = s.tasks[taskId]
    if (isActive(a) && (!found || (a.startedAt ?? 0) >= (found.startedAt ?? 0))) found = a
  }
  return found
}

/** 「1 分 12 秒」／「40 秒」 */
export function formatElapsed(ms: number): string {
  const sec = Math.max(0, Math.floor(ms / 1000))
  if (sec < 60) return `${sec} 秒`
  const m = Math.floor(sec / 60)
  const r = sec % 60
  return r ? `${m} 分 ${r} 秒` : `${m} 分`
}

/** CH-1 摺疊後的摘要：「工作了 N 分鐘 · M 步」（不到一分鐘寫秒數）。 */
export function workSummary(a: Activity): string {
  const ms = a.endedAt && a.startedAt ? a.endedAt - a.startedAt : 0
  const sec = Math.max(1, Math.round(ms / 1000))
  const dur = sec < 60 ? `${sec} 秒` : `${Math.round(sec / 60)} 分鐘`
  return `工作了 ${dur} · ${a.steps.length} 步`
}

/** 改正後更新本地的判斷（xbot.judge.correct 成功之後）。 */
export function setCorrection(s: ChatState, threadId: string, messageId: string, verdict: string): ChatState {
  return updateMessage(s, threadId, messageId, (m) => (m.judge ? { ...m, judge: { ...m.judge, corrected: verdict } } : m))
}
