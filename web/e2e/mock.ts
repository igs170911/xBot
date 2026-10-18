import type { Page, Route } from '@playwright/test'

// 瀏覽器層的假服務（make e2e 用）：攔截 /api/v1/* 的操作，並把 EventSource 換成可以從測試推事件的假連線。
// 這樣不用 Docker、不用模型也能確定地測畫面流程。真的 Bot 電腦＋Hermes 見 e2e-docker/（make e2e-docker）。

export type Bot = {
  id: string
  name: string
  title: string
  description: string
  soul: string
  color: string
  shape: string
  engine: string
  model: string
  effort: string
  approval_mode: string
  memory_enabled: boolean
  archived: boolean
  created_at: number
  updated_at: number
}

export type Msg = {
  id: string
  seq: number
  sender: string
  kind: string
  content: string
  task_id?: string
  streaming?: boolean
  status?: string
  actor?: string
  actor_name?: string
  via?: string
  silent?: boolean
  peer?: Record<string, unknown>
  created_at: number
  updated_at: number
}

export type Thread = { id: string; bot_id: string; title: string; pinned: boolean; archived: boolean; unread: number; last_message_at: number; created_at: number }

type Handler = (input: Record<string, unknown>) => unknown

export class MockServer {
  bots: Bot[] = []
  threads: Thread[] = []
  messages: Record<string, Msg[]> = {}
  modelsReady = true
  /** 待我處理的卡（xbot.inbox.list 的項目）。 */
  inbox: Array<Record<string, unknown> & { id: string; status: string }> = []
  calls: Array<{ op: string; input: Record<string, unknown> }> = []
  /** 群組（xbot.rooms.list 的項目）與群組訊息（collab.room_read）。 */
  rooms: Array<Record<string, unknown> & { id: string; name: string; members: Array<Record<string, unknown>> }> = []
  roomMessages: Record<string, Array<Record<string, unknown>>> = {}
  /** collab.peer_history 的回應。 */
  peerHistory: Record<string, unknown> | null = null
  /** 測試可以換掉某個操作的回應。 */
  override: Record<string, Handler> = {}
  private seq = 0
  private n = 0
  eventId = 0

  constructor(private page: Page) {}

  id(prefix: string) {
    this.n += 1
    return `${prefix}${this.n}`
  }

  addBot(b: Partial<Bot> & { name: string }): Bot {
    const now = Date.now()
    const bot: Bot = { id: this.id('bot'), title: '', description: '', soul: '', color: 'blue', shape: 'cursor', engine: 'hermes', model: '', effort: 'medium', approval_mode: 'ask', memory_enabled: true, archived: false, created_at: now, updated_at: now, ...b }
    this.bots.push(bot)
    return bot
  }

  addThread(botId: string, title: string): Thread {
    const now = Date.now()
    const t: Thread = { id: this.id('th'), bot_id: botId, title, pinned: false, archived: false, unread: 0, last_message_at: now, created_at: now }
    this.threads.unshift(t)
    this.messages[t.id] = []
    return t
  }

  addMessage(threadId: string, m: Partial<Msg> & { sender: string; content: string }, at = Date.now()): Msg {
    this.seq += 1
    const msg: Msg = { id: this.id('m'), seq: this.seq, kind: 'text', created_at: at, updated_at: at, ...m }
    this.messages[threadId].push(msg)
    return msg
  }

  private ops: Record<string, Handler> = {
    'system.ping': () => ({ pong: true, api: 'xbot.v1', build: 'e2e-mock', caller: 'owner' }),
    'bots.list': () => ({ bots: this.bots }),
    'bots.get': (i) => this.bots.find((b) => b.id === i.id),
    'bots.create': (i) => this.addBot(i as Partial<Bot> & { name: string }),
    'inbox.list': (i) => ({ items: this.inbox.filter((x) => ((i.status ?? 'open') === 'all' || x.status === (i.status ?? 'open')) && (!i.kind || x.kind === i.kind)) }),
    'inbox.mark_read': () => ({ marked: 0 }),
    'chat.steps': () => ({ events: [], has_more: false }),
    'models.get': () =>
      this.modelsReady
        ? { providers: [{ id: 'openai', base_url: 'https://api.openai.com/v1', api_key_set: true }], default: { provider: 'openai', model: 'gpt-4.1-mini' }, ready: true }
        : { providers: [], ready: false },
    'models.set_provider': (i) => {
      this.modelsReady = true
      return { provider: { id: i.id, base_url: i.base_url, api_key_set: true }, default: { provider: i.id, model: i.default_model } }
    },
    'chat.list_threads': (i) => ({ threads: this.threads.filter((t) => t.bot_id === i.bot_id) }),
    'chat.read': (i) => {
      const t = this.threads.find((x) => x.id === i.thread_id)
      const all = (this.messages[i.thread_id as string] ?? []).filter((m) => i.before === undefined || m.seq < (i.before as number))
      const limit = (i.limit as number | undefined) ?? 50
      const page = all.slice(-limit)
      return { thread: t, messages: page, has_more: all.length > page.length, next_before: page[0]?.seq }
    },
    'bots.update': (i) => {
      const b = this.bots.find((x) => x.id === i.id)!
      Object.assign(b, i, { updated_at: Date.now() })
      return b
    },
    'chat.send': (i) => {
      const t = i.thread_id ? this.threads.find((x) => x.id === i.thread_id)! : this.addThread(i.bot_id as string, String(i.text).slice(0, 40))
      const task = this.id('task')
      const m = this.addMessage(t.id, { sender: 'owner', content: String(i.text), task_id: task })
      this.lastSend = { botId: i.bot_id as string, threadId: t.id, taskId: task, messageId: m.id, text: String(i.text) }
      return { bot_id: i.bot_id, thread_id: t.id, task_id: task, message_id: m.id }
    },
    'chat.stop': () => ({ stopped: true, task_id: this.lastSend?.taskId }),
    'rooms.list': () => ({ rooms: this.rooms }),
    'rooms.create': (i) => this.addRoom(i.member_ids as string[], (i.name as string) || '', (i.responder_rule as string) || 'auto'),
    'rooms.update': (i) => {
      const r = this.rooms.find((x) => x.id === i.room_id)!
      Object.assign(r, Object.fromEntries(Object.entries(i).filter(([k]) => k !== 'room_id')))
      return r
    },
    'rooms.pause_member': (i) => {
      const r = this.rooms.find((x) => x.id === i.room_id)!
      const m = r.members.find((x) => x.bot_id === i.bot_id)!
      m.paused = i.paused
      return r
    },
    'rooms.post': (i) => {
      const r = this.rooms.find((x) => x.id === i.room_id)!
      const now = Date.now()
      this.seq += 1
      const m = { id: this.id('rm'), seq: this.seq, sender: 'owner', content: String(i.text), mentions: [], created_at: now }
      ;(this.roomMessages[r.id] ??= []).push(m)
      return { message: m, woke: [] }
    },
    'collab.room_read': (i) => {
      const r = this.rooms.find((x) => x.id === i.room)!
      return { room: r, messages: this.roomMessages[r.id] ?? [], has_more: false }
    },
    'collab.stop_room': () => ({ stopped: [] }),
    'collab.peer_history': () => this.peerHistory,
    'collab.stop_chain': () => ({ stopped: 1 }),
    'teams.list': () => ({ teams: [] }),
    'agenda.list': () => ({ from: 0, to: 0, pending: [], decided: [], calendar: [], approvals: [], open_max: 3 }),
  }

  addRoom(memberIds: string[], name: string, rule = 'auto', seats: Record<string, string> = {}) {
    const members = memberIds.map((id, i) => {
      const b = this.bots.find((x) => x.id === id)!
      return { bot_id: id, name: b.name, title: b.title, color: b.color, shape: b.shape, lead: i === 0, paused: false, seat: seats[id] ?? 'idle', seat_thread_id: 'seat-' + id }
    })
    const now = Date.now()
    const room = { id: this.id('room'), name: name || members.map((m) => m.name).join('、'), auto_name: !name, responder_rule: rule, lead_id: memberIds[0], bulletin: '', created_by: 'owner', members, preview: '', last_message_at: now, created_at: now }
    this.rooms.unshift(room)
    this.roomMessages[room.id] = []
    return room
  }

  addRoomMessage(roomId: string, m: Record<string, unknown> & { sender: string; content: string }) {
    this.seq += 1
    const msg = { id: this.id('rm'), seq: this.seq, mentions: [], created_at: Date.now(), ...m }
    this.roomMessages[roomId].push(msg)
    return msg
  }

  lastSend: { botId: string; threadId: string; taskId: string; messageId: string; text: string } | null = null

  async install() {
    await this.page.addInitScript(() => {
      type Fake = { readyState: number; onopen?: () => void; onmessage?: (m: { data: string }) => void; onerror?: () => void; close: () => void }
      const sources: Fake[] = []
      class FakeEventSource {
        static CONNECTING = 0
        static OPEN = 1
        static CLOSED = 2
        readyState = 0
        onopen?: () => void
        onmessage?: (m: { data: string }) => void
        onerror?: () => void
        constructor(public url: string) {
          sources.push(this)
          setTimeout(() => {
            this.readyState = 1
            this.onopen?.()
          }, 0)
        }
        close() {
          this.readyState = 2
        }
      }
      const w = window as unknown as Record<string, unknown>
      w.EventSource = FakeEventSource
      w.__xbotEmit = (events: unknown[]) => {
        for (const e of events) for (const s of sources) if (s.readyState === 1) s.onmessage?.({ data: JSON.stringify(e) })
      }
    })
    await this.page.route('**/api/v1/**', (route) => this.handle(route))
  }

  private async handle(route: Route) {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1/', '')
    if (path === 'auth/status') return route.fulfill({ json: { authenticated: true, local: true, kind: 'owner', name: '你' } })
    let input: Record<string, unknown> = {}
    try {
      input = (route.request().postDataJSON() ?? {}) as Record<string, unknown>
    } catch {
      // multipart 上傳（例：語音輸入的錄音）不是 JSON
    }
    this.calls.push({ op: path, input })
    const h = this.override[path] ?? this.ops[path]
    if (!h) return route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '假服務沒有 ' + path, retryable: false } } })
    try {
      return route.fulfill({ json: { op: 'xbot.' + path, output: h(input) } })
    } catch (e) {
      const err = e as { status?: number; error?: unknown }
      return route.fulfill({ status: err.status ?? 500, json: { error: err.error ?? { code: 'internal', message: String(e), retryable: false } } })
    }
  }

  /** 推事件給網頁（有 persist 的帶流水號）。 */
  async emit(...events: Array<Record<string, unknown> & { kind: string }>) {
    const now = Date.now()
    const out = events.map((e) => {
      const { persist, ...rest } = e as { persist?: boolean } & Record<string, unknown>
      return { at: now, ...(persist === false ? {} : { id: ++this.eventId }), ...rest }
    })
    await this.page.evaluate((evs) => (window as unknown as { __xbotEmit: (e: unknown[]) => void }).__xbotEmit(evs), out)
  }

  /** message.created 的 payload（store.Message 的樣子）。 */
  storeMessage(threadId: string, m: Msg) {
    return {
      id: m.id,
      seq: m.seq,
      scope: 'thread',
      scope_id: threadId,
      parent_id: '',
      sender: m.sender,
      kind: 'text',
      content: m.content,
      attachments: [],
      meta: { task_id: m.task_id, streaming: m.streaming || undefined, status: m.status },
      created_at: m.created_at,
      updated_at: m.updated_at,
    }
  }
}

export function opError(status: number, code: string, message: string, retryable = false): never {
  throw { status, error: { code, message, retryable } }
}

export async function settle(page: Page) {
  await page.evaluate(() => document.fonts.ready)
  await page.addStyleTag({ content: '*{animation:none!important;transition:none!important;caret-color:transparent!important}' })
  await page.evaluate(() => document.querySelectorAll('svg').forEach((s) => (s as SVGSVGElement).pauseAnimations?.()))
}
