// 由 make gen（xbot gen ts）從操作登錄表產生，不要手改。make check 會檢查它有沒有過期。
// 介面只能呼叫這裡的操作（AGENTS.md：MCP 優先，不准另開私有路由）。

export const API_VERSION = 'xbot.v1'

/** 固定的錯誤代碼（MCP-5）。依代碼判斷，不要解析 message。 */
export type ErrorCode = 'forbidden' | 'not_found' | 'invalid_input' | 'limit' | 'budget' | 'pending_approval' | 'conflict' | 'internal'

/** 操作失敗時的內容（MCP-5）。 */
export interface ApiErrorBody {
  code: ErrorCode
  message: string
  retryable: boolean
  needs_approval_id?: string
  fields?: { field: string; message: string }[]
}

/** 呼叫操作失敗。code 是 pending_approval 時，動作已送到「待我處理」，needsApprovalId 是核准 id。 */
export class ApiError extends Error {
  readonly code: ErrorCode
  readonly status: number
  readonly retryable: boolean
  readonly needsApprovalId?: string
  readonly fields?: { field: string; message: string }[]

  constructor(status: number, body: ApiErrorBody) {
    super(body.message)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code
    this.retryable = body.retryable
    this.needsApprovalId = body.needs_approval_id
    this.fields = body.fields
  }
}

export interface CallOptions {
  /** 會改東西的操作：同一個 requestId 重送只會做一次（MCP-4）。沒給時服務會產生一個。 */
  requestId?: string
  /** 只回傳會發生什麼，不寫任何資料。 */
  dryRun?: boolean
  signal?: AbortSignal
  /** 測試或其他環境用；預設是全域 fetch。 */
  fetch?: typeof fetch
  /** API 的網址開頭；預設是同一個網站。 */
  baseUrl?: string
}

/** 成功呼叫的結果。 */
export interface CallResult<T> {
  op: string
  output: T
  /** 這是預演。 */
  dry_run?: boolean
  /** 真的執行時會先送到「待我處理」。 */
  needs_approval?: boolean
  /** 同一個 requestId 重送，回的是第一次的結果。 */
  replayed?: boolean
  /** 這次用的 requestId（服務回傳的 X-Request-Id）。 */
  requestId?: string
}

export interface OpMeta {
  path: string
  scope: 'read' | 'operate' | 'manage' | 'admin'
  risk: 'safe' | 'confirm'
  mutates: boolean
}

/** 呼叫一個操作，回傳完整結果。失敗時丟 ApiError。 */
export async function invoke<K extends OpName>(
  name: K,
  input: Ops[K]['input'],
  opts: CallOptions = {},
): Promise<CallResult<Ops[K]['output']>> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (opts.requestId) headers['X-Request-Id'] = opts.requestId
  if (opts.dryRun) headers['X-Dry-Run'] = '1'
  const doFetch = opts.fetch ?? fetch
  const res = await doFetch((opts.baseUrl ?? '') + OPS[name].path, {
    method: 'POST',
    headers,
    body: JSON.stringify(input ?? {}),
    credentials: 'same-origin',
    signal: opts.signal,
  })
  const body: unknown = await res.json().catch(() => null)
  if (body && typeof body === 'object' && 'error' in body && body.error) {
    throw new ApiError(res.status, body.error as ApiErrorBody)
  }
  if (!res.ok || !body || typeof body !== 'object') {
    throw new ApiError(res.status, { code: 'internal', message: '服務沒有正常回應，請稍後再試', retryable: true })
  }
  const result = body as CallResult<Ops[K]['output']>
  result.requestId = res.headers.get('X-Request-Id') ?? opts.requestId
  return result
}

// ── 登入 ─────────────────────────────────────────────

export interface AuthStatus {
  authenticated: boolean
  local: boolean
  kind?: 'owner' | 'agent' | 'bot'
  name?: string
}

async function authCall<T>(path: string, init: RequestInit, opts: CallOptions): Promise<T> {
  const res = await (opts.fetch ?? fetch)((opts.baseUrl ?? '') + '/api/v1/auth/' + path, { credentials: 'same-origin', ...init })
  const body = (await res.json().catch(() => null)) as { error?: ApiErrorBody } | null
  if (body?.error) throw new ApiError(res.status, body.error)
  if (!res.ok || !body) throw new ApiError(res.status, { code: 'internal', message: '服務沒有正常回應，請稍後再試', retryable: true })
  return body as T
}

/** 看目前有沒有登入（本機開啟時不用登入）。 */
export function authStatus(opts: CallOptions = {}): Promise<AuthStatus> {
  return authCall('status', { method: 'GET' }, opts)
}

/** 用登入碼登入；成功後瀏覽器會記住（cookie）。 */
export function login(token: string, opts: CallOptions = {}): Promise<{ ok: true }> {
  return authCall('login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token }) }, opts)
}

/** 登出這個瀏覽器。 */
export function logout(opts: CallOptions = {}): Promise<{ ok: true }> {
  return authCall('logout', { method: 'POST' }, opts)
}

// ── 事件（SSE）────────────────────────────────────────

/** 一筆事件。沒有 id 的是即時事件（例：串流文字），斷線後不會補送。 */
export interface XBotEvent {
  id?: number
  /** 例：op、message.created、chat.text、chat.done */
  kind: string
  bot_id?: string
  thread_id?: string
  task_id?: string
  payload?: unknown
  /** 發生時間（unix 毫秒） */
  at: number
}

export interface EventsOptions {
  /** 從這個事件 id 之後開始（含補送）；沒給時從現在開始。 */
  lastEventId?: number
  /** 連線狀態改變：open 已連上、reconnecting 斷線重連中。 */
  onStatus?: (status: 'open' | 'reconnecting') => void
  baseUrl?: string
  /** 測試用；預設是全域 EventSource。 */
  EventSource?: typeof EventSource
}

/**
 * 訂閱事件（單一 SSE 連線）。斷線會自動重連，並從最後收到的事件之後補送，不會漏。
 * 回傳的函式用來停止訂閱。
 */
export function subscribeEvents(onEvent: (e: XBotEvent) => void, opts: EventsOptions = {}): () => void {
  const ES = opts.EventSource ?? EventSource
  let last = opts.lastEventId
  let source: EventSource | null = null
  let timer: ReturnType<typeof setTimeout> | undefined
  let delay = 1000
  let stopped = false

  const open = () => {
    if (stopped) return
    const query = last === undefined ? '' : '?last_event_id=' + last
    const es = new ES((opts.baseUrl ?? '') + '/api/v1/events' + query, { withCredentials: true })
    source = es
    es.onopen = () => {
      delay = 1000
      opts.onStatus?.('open')
    }
    es.onmessage = (msg: MessageEvent<string>) => {
      let e: XBotEvent
      try {
        e = JSON.parse(msg.data) as XBotEvent
      } catch {
        return
      }
      if (e.id) last = e.id
      onEvent(e)
    }
    es.onerror = () => {
      opts.onStatus?.('reconnecting')
      // 瀏覽器自己會重連（會帶 Last-Event-ID）；連線被關掉（例：服務重啟、回 401）時自己重開。
      if (es.readyState === ES.CLOSED) {
        es.close()
        timer = setTimeout(open, delay)
        delay = Math.min(delay * 2, 30000)
      }
    }
  }
  open()
  return () => {
    stopped = true
    if (timer) clearTimeout(timer)
    source?.close()
  }
}

// ── 操作 ─────────────────────────────────────────────

// xbot.agents.list

export interface AgentsListInput {
  /** 也列出已撤銷、已過期的代理 */
  include_revoked?: boolean
}

export interface AgentsListOutput {
  agents: {
    id: string
    name: string
    status: 'active' | 'revoked' | 'expired'
    scopes: ('read' | 'operate' | 'manage' | 'admin')[]
    resources: {
      bots: string[]
      teams: string[]
      hosts: string[]
    }
    /** 最後使用時間（Unix 毫秒；0 表示還沒用過） */
    last_used_at: number
    /** 0 表示不過期 */
    expires_at: number
    /** 0 表示有效 */
    revoked_at: number
    created_at: number
  }[]
}

/**
 * 列出已連線的代理
 *
 * `xbot.agents.list`：權限 admin、風險 safe
 */
export function agentsList(input: AgentsListInput = {}, opts?: CallOptions): Promise<AgentsListOutput> {
  return invoke('xbot.agents.list', input, opts).then((r) => r.output)
}

// xbot.agents.pair_start

export interface AgentsPairStartInput {
  /** 代理的名字，例：Claude Code；空白時用代理換權杖時報的名字 */
  name?: string
  /** 給這個代理的權限範圍（可疊加）：read、operate、manage、admin */
  scopes: ('read' | 'operate' | 'manage' | 'admin')[]
  /** 限制只能碰哪些 Bot／團隊／主機；不填表示不限制 */
  resources?: {
    bots?: string[]
    teams?: string[]
    hosts?: string[]
  }
  /** 權杖有效天數；0 或不填表示不過期（可隨時撤銷） */
  token_days?: number
}

export interface AgentsPairStartOutput {
  /** 一次性配對碼（XXXX-XXXX）；預演時是空白 */
  code?: string
  /** 配對碼到期時間（Unix 毫秒） */
  expires_at: number
  /** 代理換權杖的端點：POST {code, name} */
  pair_path: string
  name?: string
  scopes: ('read' | 'operate' | 'manage' | 'admin')[]
  resources?: {
    bots: string[]
    teams: string[]
    hosts: string[]
  }
  token_days?: number
}

/**
 * 產生一個配對碼，讓外部代理換到權杖
 *
 * 配對碼一次性、5 分鐘內有效。把配對碼交給代理（Claude Code、Codex…），代理呼叫 POST /api/v1/agents.pair 換到權杖，之後用 Authorization: Bearer <權杖> 連 /mcp。只有你本人能產生配對碼。
 *
 * `xbot.agents.pair_start`：權限 admin、風險 safe
 */
export function agentsPairStart(input: AgentsPairStartInput, opts?: CallOptions): Promise<AgentsPairStartOutput> {
  return invoke('xbot.agents.pair_start', input, opts).then((r) => r.output)
}

// xbot.agents.revoke

export interface AgentsRevokeInput {
  /** 代理 id（xbot.agents.list 的 id） */
  id: string
}

export interface AgentsRevokeOutput {
  id: string
  name: string
  status: 'active' | 'revoked' | 'expired'
  scopes: ('read' | 'operate' | 'manage' | 'admin')[]
  resources: {
    bots: string[]
    teams: string[]
    hosts: string[]
  }
  /** 最後使用時間（Unix 毫秒；0 表示還沒用過） */
  last_used_at: number
  /** 0 表示不過期 */
  expires_at: number
  /** 0 表示有效 */
  revoked_at: number
  created_at: number
}

/**
 * 撤銷一個代理的權杖
 *
 * 撤銷後這個代理的下一個請求就會被拒絕，要重新配對才能再連線。
 *
 * `xbot.agents.revoke`：權限 admin、風險 confirm
 */
export function agentsRevoke(input: AgentsRevokeInput, opts?: CallOptions): Promise<AgentsRevokeOutput> {
  return invoke('xbot.agents.revoke', input, opts).then((r) => r.output)
}

// xbot.agents.update_scopes

export interface AgentsUpdateScopesInput {
  /** 代理 id */
  id: string
  /** 新的權限範圍（整組取代）；空陣列表示什麼都不能做 */
  scopes: ('read' | 'operate' | 'manage' | 'admin')[]
  /** 新的資源範圍；不填表示不變 */
  resources?: {
    bots?: string[]
    teams?: string[]
    hosts?: string[]
  }
}

export interface AgentsUpdateScopesOutput {
  id: string
  name: string
  status: 'active' | 'revoked' | 'expired'
  scopes: ('read' | 'operate' | 'manage' | 'admin')[]
  resources: {
    bots: string[]
    teams: string[]
    hosts: string[]
  }
  /** 最後使用時間（Unix 毫秒；0 表示還沒用過） */
  last_used_at: number
  /** 0 表示不過期 */
  expires_at: number
  /** 0 表示有效 */
  revoked_at: number
  created_at: number
}

/**
 * 改一個代理的權限範圍
 *
 * scopes 整組取代原本的權限；resources 有給才取代資源範圍。只有你本人能改。
 *
 * `xbot.agents.update_scopes`：權限 admin、風險 safe
 */
export function agentsUpdateScopes(input: AgentsUpdateScopesInput, opts?: CallOptions): Promise<AgentsUpdateScopesOutput> {
  return invoke('xbot.agents.update_scopes', input, opts).then((r) => r.output)
}

// xbot.settings.get

export interface SettingsGetInput {
  /** 設定名稱 */
  name: string
}

export interface SettingsGetOutput {
  name: string
  /** 有沒有設定過 */
  set: boolean
  /** 設定值（JSON）；密鑰類設定不會回傳 */
  value?: unknown
  /** 這是密鑰，只能寫、不能讀 */
  secret?: boolean
}

/**
 * 讀一項設定
 *
 * `xbot.settings.get`：權限 admin、風險 safe
 */
export function settingsGet(input: SettingsGetInput, opts?: CallOptions): Promise<SettingsGetOutput> {
  return invoke('xbot.settings.get', input, opts).then((r) => r.output)
}

// xbot.system.ping

export interface SystemPingInput {
  /** 會原樣回傳的文字 */
  echo?: string
}

export interface SystemPingOutput {
  pong: boolean
  echo?: string
  /** 操作的版本，例：xbot.v1 */
  api: string
  /** XBot 的建置版本 */
  build: string
  /** 呼叫者代號：owner、agent:<id>、bot:<id> */
  caller: string
}

/**
 * 確認 XBot 有在運作
 *
 * `xbot.system.ping`：權限 read、風險 safe
 */
export function systemPing(input: SystemPingInput = {}, opts?: CallOptions): Promise<SystemPingOutput> {
  return invoke('xbot.system.ping', input, opts).then((r) => r.output)
}

// ── 操作清單 ─────────────────────────────────────────

/** 每個操作的輸入與輸出型別，invoke 用它檢查型別。 */
export interface Ops {
  'xbot.agents.list': { input: AgentsListInput; output: AgentsListOutput }
  'xbot.agents.pair_start': { input: AgentsPairStartInput; output: AgentsPairStartOutput }
  'xbot.agents.revoke': { input: AgentsRevokeInput; output: AgentsRevokeOutput }
  'xbot.agents.update_scopes': { input: AgentsUpdateScopesInput; output: AgentsUpdateScopesOutput }
  'xbot.settings.get': { input: SettingsGetInput; output: SettingsGetOutput }
  'xbot.system.ping': { input: SystemPingInput; output: SystemPingOutput }
}

export type OpName = keyof Ops

/** 每個操作的 HTTP 路徑、權限範圍、風險。 */
export const OPS: Record<OpName, OpMeta> = {
  'xbot.agents.list': { path: '/api/v1/agents.list', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.agents.pair_start': { path: '/api/v1/agents.pair_start', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.agents.revoke': { path: '/api/v1/agents.revoke', scope: 'admin', risk: 'confirm', mutates: true },
  'xbot.agents.update_scopes': { path: '/api/v1/agents.update_scopes', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.settings.get': { path: '/api/v1/settings.get', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.system.ping': { path: '/api/v1/system.ping', scope: 'read', risk: 'safe', mutates: false },
}
