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

// xbot.approvals.decide

export interface ApprovalsDecideInput {
  approval_id: string
  /** allow：允許這次並執行；deny：退回 */
  decision: 'allow' | 'deny'
}

export interface ApprovalsDecideOutput {
  approval: {
    id: string
    bot_id?: string
    /** 要執行的操作，例：xbot.bots.delete */
    op: string
    summary: string
    /** 操作的輸入（密鑰已遮蔽） */
    params: unknown
    /** 誰要求的：agent:<id>、bot:<id> */
    requested_by: string
    request_id?: string
    status: 'pending' | 'approved' | 'denied' | 'expired' | 'canceled'
    decided_by?: string
    decided_at?: number
    created_at: number
  }
  settled: boolean
  /** 允許後動作已經執行完成 */
  executed: boolean
  /** 允許了但執行失敗（error 是原因；你可以再按一次允許重試） */
  failed: boolean
  /** 動作的輸出（executed 時） */
  output?: unknown
  /** 允許了但執行失敗時的錯誤 */
  error?: {
    code: string
    message: string
    retryable: boolean
    needs_approval_id?: string
    fields?: {
      field: string
      message: string
    }[]
  }
}

/**
 * 允許或退回一個等核准的動作
 *
 * 允許後立刻執行原本的動作並回傳結果。目前只有你本人能決定；代為核准（MCP-10）之後才開放給有授權的代理。
 *
 * `xbot.approvals.decide`：權限 operate、風險 safe
 */
export function approvalsDecide(input: ApprovalsDecideInput, opts?: CallOptions): Promise<ApprovalsDecideOutput> {
  return invoke('xbot.approvals.decide', input, opts).then((r) => r.output)
}

// xbot.approvals.list

export interface ApprovalsListInput {
  /** 只列這個狀態；不填列全部 */
  status?: 'pending' | 'approved' | 'denied' | 'expired' | 'canceled'
  /** 只列和這個 Bot 有關的 */
  bot_id?: string
  /** 最多幾筆；預設 50 */
  limit?: number
}

export interface ApprovalsListOutput {
  /** 新到舊；代理只看得到自己要求的 */
  approvals: {
    id: string
    bot_id?: string
    /** 要執行的操作，例：xbot.bots.delete */
    op: string
    summary: string
    /** 操作的輸入（密鑰已遮蔽） */
    params: unknown
    /** 誰要求的：agent:<id>、bot:<id> */
    requested_by: string
    request_id?: string
    status: 'pending' | 'approved' | 'denied' | 'expired' | 'canceled'
    decided_by?: string
    decided_at?: number
    created_at: number
  }[]
}

/**
 * 列出等你核准的動作
 *
 * `xbot.approvals.list`：權限 read、風險 safe
 */
export function approvalsList(input: ApprovalsListInput = {}, opts?: CallOptions): Promise<ApprovalsListOutput> {
  return invoke('xbot.approvals.list', input, opts).then((r) => r.output)
}

// xbot.approvals.wait

export interface ApprovalsWaitInput {
  /** pending_approval 錯誤裡的 needs_approval_id */
  approval_id: string
  /** 最多等幾秒；預設 60 */
  timeout_s?: number
}

export interface ApprovalsWaitOutput {
  approval: {
    id: string
    bot_id?: string
    /** 要執行的操作，例：xbot.bots.delete */
    op: string
    summary: string
    /** 操作的輸入（密鑰已遮蔽） */
    params: unknown
    /** 誰要求的：agent:<id>、bot:<id> */
    requested_by: string
    request_id?: string
    status: 'pending' | 'approved' | 'denied' | 'expired' | 'canceled'
    decided_by?: string
    decided_at?: number
    created_at: number
  }
  settled: boolean
  /** 允許後動作已經執行完成 */
  executed: boolean
  /** 允許了但執行失敗（error 是原因；你可以再按一次允許重試） */
  failed: boolean
  /** 動作的輸出（executed 時） */
  output?: unknown
  /** 允許了但執行失敗時的錯誤 */
  error?: {
    code: string
    message: string
    retryable: boolean
    needs_approval_id?: string
    fields?: {
      field: string
      message: string
    }[]
  }
}

/**
 * 等一個核准的結果
 *
 * 收到 pending_approval 後用它等：使用者允許並執行完（executed）、允許了但執行失敗（failed）、退回、或逾時（settled=false，可以再呼叫）。代理只能等自己要求的核准。
 *
 * `xbot.approvals.wait`：權限 read、風險 safe
 */
export function approvalsWait(input: ApprovalsWaitInput, opts?: CallOptions): Promise<ApprovalsWaitOutput> {
  return invoke('xbot.approvals.wait', input, opts).then((r) => r.output)
}

// xbot.audit.list

export interface AuditListInput {
  /** 只列這個呼叫者：owner、agent:<id>、bot:<id>（代理只看得到自己的） */
  actor?: string
  /** 只列和這個 id 有關的 */
  target?: string
  /** 往前翻頁：只列 id 比它小的（上一頁的 next_before） */
  before?: number
  /** 最多幾筆；預設 50 */
  limit?: number
}

export interface AuditListOutput {
  /** 新到舊；參數已遮蔽密鑰 */
  entries: {
    id: number
    actor: string
    via: string
    op: string
    request_id: string
    target: string
    params: unknown
    status: string
    error_code: string
    created_at: number
  }[]
  next_before?: number
}

/**
 * 列出稽核紀錄（誰、從哪裡、做了什麼）
 *
 * `xbot.audit.list`：權限 read、風險 safe
 */
export function auditList(input: AuditListInput = {}, opts?: CallOptions): Promise<AuditListOutput> {
  return invoke('xbot.audit.list', input, opts).then((r) => r.output)
}

// xbot.bots.archive

export interface BotsArchiveInput {
  /** Bot id */
  id: string
  /** false 表示取消封存；不填是封存 */
  archived?: boolean
}

export interface BotsArchiveOutput {
  id: string
  name: string
  title: string
  description: string
  soul: string
  color: 'green' | 'blue' | 'red' | 'orange' | 'purple' | 'cyan' | 'pink' | 'yellow' | 'teal' | 'coral'
  shape: 'cursor' | 'blob' | 'circle' | 'squircle' | 'capsule' | 'drop' | 'shield' | 'hexagon' | 'diamond' | 'star'
  engine: string
  /** 空白表示用預設模型；「<供應商>/<模型>」或只寫模型 */
  model: string
  /** 備援模型「<供應商>/<模型>」；空白表示用全域備援（xbot.models.set_fallback） */
  fallback: string
  effort: string
  approval_mode: 'ask' | 'edits' | 'auto' | 'full'
  archived: boolean
  created_at: number
  updated_at: number
}

/**
 * 封存（或取消封存）一個 Bot
 *
 * 封存的 Bot 不會出現在清單，也不能對話；資料都保留，可以取消封存。封存時會先停下它正在做的事。
 *
 * `xbot.bots.archive`：權限 manage、風險 safe
 */
export function botsArchive(input: BotsArchiveInput, opts?: CallOptions): Promise<BotsArchiveOutput> {
  return invoke('xbot.bots.archive', input, opts).then((r) => r.output)
}

// xbot.bots.create

export interface BotsCreateInput {
  /** 名稱，例：小秘 */
  name: string
  /** 職稱，例：幕僚長 */
  title?: string
  /** 一兩句話說明它負責什麼 */
  description?: string
  /** 常駐指示（SOUL.md） */
  soul?: string
  /** 頭像顏色；不填自動挑 */
  color?: 'green' | 'blue' | 'red' | 'orange' | 'purple' | 'cyan' | 'pink' | 'yellow' | 'teal' | 'coral'
  /** 頭像形狀；不填自動挑 */
  shape?: 'cursor' | 'blob' | 'circle' | 'squircle' | 'capsule' | 'drop' | 'shield' | 'hexagon' | 'diamond' | 'star'
  /** 引擎；預設 hermes */
  engine?: 'hermes'
  /** 模型；不填用預設模型（xbot.models.get 看設定） */
  model?: string
  /** 思考強度；不填是 medium */
  effort?: 'low' | 'medium' | 'high'
  /** 權限等級；預設 ask（先問我）。設成其他等級要你核准 */
  approval_mode?: 'ask' | 'edits' | 'auto' | 'full'
}

export interface BotsCreateOutput {
  id: string
  name: string
  title: string
  description: string
  soul: string
  color: 'green' | 'blue' | 'red' | 'orange' | 'purple' | 'cyan' | 'pink' | 'yellow' | 'teal' | 'coral'
  shape: 'cursor' | 'blob' | 'circle' | 'squircle' | 'capsule' | 'drop' | 'shield' | 'hexagon' | 'diamond' | 'star'
  engine: string
  /** 空白表示用預設模型；「<供應商>/<模型>」或只寫模型 */
  model: string
  /** 備援模型「<供應商>/<模型>」；空白表示用全域備援（xbot.models.set_fallback） */
  fallback: string
  effort: string
  approval_mode: 'ask' | 'edits' | 'auto' | 'full'
  archived: boolean
  created_at: number
  updated_at: number
}

/**
 * 建立一個 Bot
 *
 * 新 Bot 的權限等級預設是「先問我」（SEC-1）。它的電腦在第一次對話時才開機。受資源範圍限制的代理建立的 Bot，會自動加進它能碰的 Bot。
 *
 * `xbot.bots.create`：權限 manage、風險 safe（依輸入可能需要核准）
 */
export function botsCreate(input: BotsCreateInput, opts?: CallOptions): Promise<BotsCreateOutput> {
  return invoke('xbot.bots.create', input, opts).then((r) => r.output)
}

// xbot.bots.delete

export interface BotsDeleteInput {
  /** Bot id */
  id: string
}

export interface BotsDeleteOutput {
  id: string
  name: string
  /** 預演時是 false */
  deleted: boolean
  /** 會一起刪掉的對話串數 */
  threads: number
}

/**
 * 刪除一個 Bot
 *
 * 連同它的電腦（含檔案）、對話、任務一起刪除，無法復原。一定要你本人核准，不能代為核准。
 *
 * `xbot.bots.delete`：權限 manage、風險 confirm
 */
export function botsDelete(input: BotsDeleteInput, opts?: CallOptions): Promise<BotsDeleteOutput> {
  return invoke('xbot.bots.delete', input, opts).then((r) => r.output)
}

// xbot.bots.get

export interface BotsGetInput {
  /** Bot id */
  id: string
}

export interface BotsGetOutput {
  id: string
  name: string
  title: string
  description: string
  soul: string
  color: 'green' | 'blue' | 'red' | 'orange' | 'purple' | 'cyan' | 'pink' | 'yellow' | 'teal' | 'coral'
  shape: 'cursor' | 'blob' | 'circle' | 'squircle' | 'capsule' | 'drop' | 'shield' | 'hexagon' | 'diamond' | 'star'
  engine: string
  /** 空白表示用預設模型；「<供應商>/<模型>」或只寫模型 */
  model: string
  /** 備援模型「<供應商>/<模型>」；空白表示用全域備援（xbot.models.set_fallback） */
  fallback: string
  effort: string
  approval_mode: 'ask' | 'edits' | 'auto' | 'full'
  archived: boolean
  created_at: number
  updated_at: number
}

/**
 * 讀一個 Bot 的設定
 *
 * `xbot.bots.get`：權限 read、風險 safe
 */
export function botsGet(input: BotsGetInput, opts?: CallOptions): Promise<BotsGetOutput> {
  return invoke('xbot.bots.get', input, opts).then((r) => r.output)
}

// xbot.bots.list

export interface BotsListInput {
  /** 也列出已封存的 Bot */
  include_archived?: boolean
}

export interface BotsListOutput {
  bots: {
    id: string
    name: string
    title: string
    description: string
    soul: string
    color: 'green' | 'blue' | 'red' | 'orange' | 'purple' | 'cyan' | 'pink' | 'yellow' | 'teal' | 'coral'
    shape: 'cursor' | 'blob' | 'circle' | 'squircle' | 'capsule' | 'drop' | 'shield' | 'hexagon' | 'diamond' | 'star'
    engine: string
    /** 空白表示用預設模型；「<供應商>/<模型>」或只寫模型 */
    model: string
    /** 備援模型「<供應商>/<模型>」；空白表示用全域備援（xbot.models.set_fallback） */
    fallback: string
    effort: string
    approval_mode: 'ask' | 'edits' | 'auto' | 'full'
    archived: boolean
    created_at: number
    updated_at: number
  }[]
}

/**
 * 列出 Bot
 *
 * `xbot.bots.list`：權限 read、風險 safe
 */
export function botsList(input: BotsListInput = {}, opts?: CallOptions): Promise<BotsListOutput> {
  return invoke('xbot.bots.list', input, opts).then((r) => r.output)
}

// xbot.bots.update

export interface BotsUpdateInput {
  /** Bot id */
  id: string
  name?: string
  title?: string
  description?: string
  soul?: string
  color?: 'green' | 'blue' | 'red' | 'orange' | 'purple' | 'cyan' | 'pink' | 'yellow' | 'teal' | 'coral'
  shape?: 'cursor' | 'blob' | 'circle' | 'squircle' | 'capsule' | 'drop' | 'shield' | 'hexagon' | 'diamond' | 'star'
  engine?: 'hermes'
  /** 空白字串表示改回預設模型 */
  model?: string
  /** 備援模型「<供應商>/<模型>」：主要模型忙碌或出錯時改用；空白字串表示用全域備援 */
  fallback?: string
  effort?: 'low' | 'medium' | 'high'
  /** 改成 ask 以外的等級要你核准 */
  approval_mode?: 'ask' | 'edits' | 'auto' | 'full'
}

export interface BotsUpdateOutput {
  id: string
  name: string
  title: string
  description: string
  soul: string
  color: 'green' | 'blue' | 'red' | 'orange' | 'purple' | 'cyan' | 'pink' | 'yellow' | 'teal' | 'coral'
  shape: 'cursor' | 'blob' | 'circle' | 'squircle' | 'capsule' | 'drop' | 'shield' | 'hexagon' | 'diamond' | 'star'
  engine: string
  /** 空白表示用預設模型；「<供應商>/<模型>」或只寫模型 */
  model: string
  /** 備援模型「<供應商>/<模型>」；空白表示用全域備援（xbot.models.set_fallback） */
  fallback: string
  effort: string
  approval_mode: 'ask' | 'edits' | 'auto' | 'full'
  archived: boolean
  created_at: number
  updated_at: number
}

/**
 * 修改一個 Bot 的設定
 *
 * 只改有給的欄位。把權限等級改成 ask 以外要你核准。
 *
 * `xbot.bots.update`：權限 manage、風險 safe（依輸入可能需要核准）
 */
export function botsUpdate(input: BotsUpdateInput, opts?: CallOptions): Promise<BotsUpdateOutput> {
  return invoke('xbot.bots.update', input, opts).then((r) => r.output)
}

// xbot.chat.ask_again

export interface ChatAskAgainInput {
  /** 過期的那張提問卡 */
  inbox_id: string
}

export interface ChatAskAgainOutput {
  bot_id: string
  /** 預演且沒給 thread_id 時是空白（會開新的對話串） */
  thread_id: string
  /** 這件工作的 id，交給 wait_until_settled */
  task_id: string
  /** 你這則訊息的 id */
  message_id: string
}

/**
 * 請 Bot 把過期的問題再問一次
 *
 * 無人在場的提問 10 分鐘沒回會過期；之後用它在原本的對話串請 Bot 再問一次。Bot 正在忙時回 conflict（retryable）。
 *
 * `xbot.chat.ask_again`：權限 operate、風險 safe
 */
export function chatAskAgain(input: ChatAskAgainInput, opts?: CallOptions): Promise<ChatAskAgainOutput> {
  return invoke('xbot.chat.ask_again', input, opts).then((r) => r.output)
}

// xbot.chat.list_threads

export interface ChatListThreadsInput {
  bot_id: string
  include_archived?: boolean
  /** 最多幾條；預設 50 */
  limit?: number
}

export interface ChatListThreadsOutput {
  /** 釘選的在前，其餘新到舊 */
  threads: {
    id: string
    bot_id: string
    title: string
    pinned: boolean
    archived: boolean
    unread: number
    last_message_at: number
    created_at: number
  }[]
}

/**
 * 列出和一個 Bot 的對話串
 *
 * `xbot.chat.list_threads`：權限 read、風險 safe
 */
export function chatListThreads(input: ChatListThreadsInput, opts?: CallOptions): Promise<ChatListThreadsOutput> {
  return invoke('xbot.chat.list_threads', input, opts).then((r) => r.output)
}

// xbot.chat.read

export interface ChatReadInput {
  thread_id: string
  /** 往前翻頁：只讀 seq 比它小的訊息（上一頁的 next_before） */
  before?: number
  /** 最多幾則；預設 50 */
  limit?: number
}

export interface ChatReadOutput {
  thread: {
    id: string
    bot_id: string
    title: string
    pinned: boolean
    archived: boolean
    unread: number
    last_message_at: number
    created_at: number
  }
  /** 由舊到新 */
  messages: {
    id: string
    seq: number
    /** owner（你）、bot:<id>、system */
    sender: string
    kind: string
    content: string
    task_id?: string
    /** Bot 還在寫這則回覆 */
    streaming?: boolean
    /** Bot 回覆的結果：complete、interrupted、error */
    status?: string
    created_at: number
    updated_at: number
  }[]
  /** 還有更早的訊息 */
  has_more: boolean
  /** 讀更早一頁時帶的 before */
  next_before?: number
}

/**
 * 讀一條對話串的訊息
 *
 * 預設回最新的 50 則（由舊到新）；has_more 時用 next_before 往前翻。
 *
 * `xbot.chat.read`：權限 read、風險 safe
 */
export function chatRead(input: ChatReadInput, opts?: CallOptions): Promise<ChatReadOutput> {
  return invoke('xbot.chat.read', input, opts).then((r) => r.output)
}

// xbot.chat.respond

export interface ChatRespondInput {
  /** 待我處理卡的 id（chat.needs_approval 事件或 wait_until_settled 的 request.inbox_id） */
  inbox_id?: string
  /** 或：這件工作目前在等的那個問題 */
  task_id?: string
  /** 核准卡：once 允許這次、always 一律允許（卡片有給才行）、deny 拒絕 */
  choice?: 'once' | 'always' | 'deny'
  /** 提問的回答（點選項就送選項的文字） */
  text?: string
  /** 多個問題時逐題的回答（依題目順序） */
  answers?: string[]
  /** 密碼、金鑰、驗證碼；只交給 Bot 的電腦，不寫進紀錄（密鑰：只能寫、不能讀） */
  value?: string
  /** 多欄位的密碼類輸入（例：identifier、password）（密鑰：只能寫、不能讀） */
  values?: Record<string, string>
  /** 不回答：核准＝拒絕、提問＝略過、密碼＝不提供 */
  decline?: boolean
}

export interface ChatRespondOutput {
  inbox_id: string
  task_id: string
  thread_id: string
  /** 卡片狀態：resolved（已交給 Bot）；預演時是 open */
  status: string
  /** once、always、deny、answered、provided、declined */
  resolution?: string
}

/**
 * 回覆 Bot 的提問（允許指令、回答問題、提供密碼）
 *
 * Bot 在等人時（needs_person）用它回覆；對話裡和待我處理用的是同一個操作。inbox_id 或 task_id 擇一。核准卡用 choice（once／always／deny）；提問用 text（多題用 answers）；密碼類用 value（帳號＋密碼用 values），值只交給 Bot 的電腦，不寫進紀錄。decline=true 表示不回答（拒絕、略過、不提供）。目前只有你本人能回覆。
 *
 * `xbot.chat.respond`：權限 operate、風險 safe
 */
export function chatRespond(input: ChatRespondInput = {}, opts?: CallOptions): Promise<ChatRespondOutput> {
  return invoke('xbot.chat.respond', input, opts).then((r) => r.output)
}

// xbot.chat.send

export interface ChatSendInput {
  /** 要對哪個 Bot 說 */
  bot_id: string
  /** 訊息內容 */
  text: string
  /** 接續的對話串；不填開一條新的 */
  thread_id?: string
}

export interface ChatSendOutput {
  bot_id: string
  /** 預演且沒給 thread_id 時是空白（會開新的對話串） */
  thread_id: string
  /** 這件工作的 id，交給 wait_until_settled */
  task_id: string
  /** 你這則訊息的 id */
  message_id: string
}

/**
 * 對一個 Bot 說話（交辦一件事）
 *
 * 訊息收下就回傳 thread_id、task_id；Bot 在背景做事（第一次會先開機），用 xbot.chat.wait_until_settled 等回覆。Bot 正在忙另一件事時回 conflict（retryable），等它做完或先 xbot.chat.stop。
 *
 * `xbot.chat.send`：權限 operate、風險 safe
 */
export function chatSend(input: ChatSendInput, opts?: CallOptions): Promise<ChatSendOutput> {
  return invoke('xbot.chat.send', input, opts).then((r) => r.output)
}

// xbot.chat.steps

export interface ChatStepsInput {
  thread_id: string
  /** 只讀這件工作的 */
  task_id?: string
  /** 只讀 id 比它大的（上一頁的 next_after） */
  after?: number
  /** 最多幾筆；預設 500 */
  limit?: number
}

export interface ChatStepsOutput {
  /** 由舊到新 */
  events: {
    id: number
    /** chat.tool_call、chat.tool_result、chat.needs_approval、chat.answered、chat.error、task.state */
    kind: string
    bot_id?: string
    thread_id?: string
    task_id?: string
    payload: unknown
    at: number
  }[]
  has_more: boolean
  next_after?: number
}

/**
 * 讀一條對話串的工作步驟（工具、提問、回覆、狀態）
 *
 * 重新整理後用它把工作步驟補回來（chat.read 只有訊息）。事件和 SSE 的格式一樣；has_more 時用 next_after 往後讀。
 *
 * `xbot.chat.steps`：權限 read、風險 safe
 */
export function chatSteps(input: ChatStepsInput, opts?: CallOptions): Promise<ChatStepsOutput> {
  return invoke('xbot.chat.steps', input, opts).then((r) => r.output)
}

// xbot.chat.stop

export interface ChatStopInput {
  bot_id: string
  /** 只停這條對話串的工作；不填停 Bot 正在做的 */
  thread_id?: string
}

export interface ChatStopOutput {
  /** false 表示本來就沒有在做事（預演時表示會不會停到東西） */
  stopped: boolean
  task_id?: string
}

/**
 * 停止 Bot 正在做的事
 *
 * `xbot.chat.stop`：權限 operate、風險 safe
 */
export function chatStop(input: ChatStopInput, opts?: CallOptions): Promise<ChatStopOutput> {
  return invoke('xbot.chat.stop', input, opts).then((r) => r.output)
}

// xbot.chat.wait_until_settled

export interface ChatWaitUntilSettledInput {
  /** xbot.chat.send 回的 task_id */
  task_id: string
  /** 最多等幾秒；預設 60 */
  timeout_s?: number
}

export interface ChatWaitUntilSettledOutput {
  task_id: string
  bot_id: string
  thread_id: string
  /** 任務狀態：queued、running、waiting_input、waiting_approval、waiting_box、stopping、done */
  state: string
  /** done 時的結果：answer、cancelled、failed */
  verdict?: string
  /** true：完成了，或在等人；false：等到逾時，還在做 */
  settled: boolean
  /** Bot 在等人回覆（核准、補充問題） */
  needs_person: boolean
  /** needs_person 時 Bot 問的內容 */
  request?: {
    /** approval（核准）、clarify（補充問題）、secret（密碼、驗證碼）… */
    kind: string
    prompt: string
    choices?: string[]
    /** 這個問題的待我處理卡；用 xbot.chat.respond 回覆 */
    inbox_id?: string
  }
  /** Bot 這次的回覆（目前為止） */
  text: string
  /** Bot 回覆那則訊息的 id */
  message_id?: string
  waited_s: number
}

/**
 * 等 Bot 做完（或需要人）再回傳它的回覆
 *
 * 最多等 timeout_s 秒（上限 300）。settled=true 表示做完了，或在等人回覆（needs_person）；settled=false 表示逾時、還在做，可以再呼叫一次。
 *
 * `xbot.chat.wait_until_settled`：權限 read、風險 safe
 */
export function chatWaitUntilSettled(input: ChatWaitUntilSettledInput, opts?: CallOptions): Promise<ChatWaitUntilSettledOutput> {
  return invoke('xbot.chat.wait_until_settled', input, opts).then((r) => r.output)
}

// xbot.inbox.counts

export interface InboxCountsInput {
  /** 只算這個 Bot 的 */
  bot_id?: string
}

export interface InboxCountsOutput {
  /** 待我處理：還沒處理的卡（側欄徽章） */
  open: number
  /** 通知中心的未讀數 */
  unread: number
  /** 各分類：decide、done、mention、error */
  groups: Record<string, {
    /** 還沒處理的 */
    open: number
    /** 未讀的 */
    unread: number
  }>
}

/**
 * 待我處理與通知的數量（徽章用）
 *
 * `xbot.inbox.counts`：權限 read、風險 safe
 */
export function inboxCounts(input: InboxCountsInput = {}, opts?: CallOptions): Promise<InboxCountsOutput> {
  return invoke('xbot.inbox.counts', input, opts).then((r) => r.output)
}

// xbot.inbox.dismiss

export interface InboxDismissInput {
  inbox_id: string
}

export interface InboxDismissOutput {
  item: {
    id: string
    /** approval、question、secret、signin、takeover、proposal、discussion、routine_failed、budget、done、error、mention */
    kind: string
    /** decide（要你決定）、done（完成）、mention（提到你）、error（出錯） */
    group: string
    bot_id?: string
    /** Bot 在對話裡問的：是哪件工作 */
    task_id?: string
    /** Bot 在對話裡問的：是哪條對話串 */
    thread_id?: string
    /** 例：核准的 id */
    ref?: string
    title: string
    body: string
    /** 卡片內容（選項、指令、輸入欄…）；不含任何密碼類的值 */
    payload: unknown
    /** open、resolved、dismissed、expired */
    status: string
    /** 怎麼處理的：once、always、deny、allow、answered、provided、declined、expired、gone、canceled、asked_again、dismissed、info */
    resolution?: string
    resolved_by?: string
    resolved_at?: number
    read: boolean
    /** 無人在場的提問：這個時間沒回就過期（0 表示不會過期） */
    expires_at?: number
    /** 這個時間之前可以用 xbot.inbox.undo 復原（0 表示不能復原） */
    undo_until?: number
    created_at: number
  }
}

/**
 * 略過一張不用處理的卡
 *
 * 操作的核准請用 xbot.approvals.decide；Bot 在對話裡的提問請用 xbot.chat.respond（decline）。10 分鐘內可以復原。
 *
 * `xbot.inbox.dismiss`：權限 operate、風險 safe
 */
export function inboxDismiss(input: InboxDismissInput, opts?: CallOptions): Promise<InboxDismissOutput> {
  return invoke('xbot.inbox.dismiss', input, opts).then((r) => r.output)
}

// xbot.inbox.list

export interface InboxListInput {
  /** 預設 open（還沒處理的）；通知中心用 all */
  status?: 'open' | 'resolved' | 'dismissed' | 'expired' | 'all'
  /** 只列這一類：decide（要你決定）、done（完成）、mention（提到你）、error（出錯） */
  group?: 'decide' | 'done' | 'mention' | 'error'
  /** 只列這一種 */
  kind?: string
  bot_id?: string
  /** 只列這條對話串的 */
  thread_id?: string
  /** 只列未讀的 */
  unread?: boolean
  /** 最多幾筆；預設 50 */
  limit?: number
}

export interface InboxListOutput {
  /** 新到舊 */
  items: {
    id: string
    /** approval、question、secret、signin、takeover、proposal、discussion、routine_failed、budget、done、error、mention */
    kind: string
    /** decide（要你決定）、done（完成）、mention（提到你）、error（出錯） */
    group: string
    bot_id?: string
    /** Bot 在對話裡問的：是哪件工作 */
    task_id?: string
    /** Bot 在對話裡問的：是哪條對話串 */
    thread_id?: string
    /** 例：核准的 id */
    ref?: string
    title: string
    body: string
    /** 卡片內容（選項、指令、輸入欄…）；不含任何密碼類的值 */
    payload: unknown
    /** open、resolved、dismissed、expired */
    status: string
    /** 怎麼處理的：once、always、deny、allow、answered、provided、declined、expired、gone、canceled、asked_again、dismissed、info */
    resolution?: string
    resolved_by?: string
    resolved_at?: number
    read: boolean
    /** 無人在場的提問：這個時間沒回就過期（0 表示不會過期） */
    expires_at?: number
    /** 這個時間之前可以用 xbot.inbox.undo 復原（0 表示不能復原） */
    undo_until?: number
    created_at: number
  }[]
}

/**
 * 列出「待我處理」與通知
 *
 * 預設只列還沒處理的（待我處理）；通知中心用 status=all 加 group 篩選。
 *
 * `xbot.inbox.list`：權限 read、風險 safe
 */
export function inboxList(input: InboxListInput = {}, opts?: CallOptions): Promise<InboxListOutput> {
  return invoke('xbot.inbox.list', input, opts).then((r) => r.output)
}

// xbot.inbox.mark_read

export interface InboxMarkReadInput {
  /** 這幾張 */
  ids?: string[]
  /** 這條對話串的全部（打開對話時） */
  thread_id?: string
  /** 全部標為已讀 */
  all?: boolean
}

export interface InboxMarkReadOutput {
  /** 這次從未讀變成已讀的張數 */
  marked: number
}

/**
 * 把通知標為已讀
 *
 * ids、thread_id、all 擇一。只有你本人能用（已讀是你的狀態）。
 *
 * `xbot.inbox.mark_read`：權限 operate、風險 safe
 */
export function inboxMarkRead(input: InboxMarkReadInput = {}, opts?: CallOptions): Promise<InboxMarkReadOutput> {
  return invoke('xbot.inbox.mark_read', input, opts).then((r) => r.output)
}

// xbot.inbox.undo

export interface InboxUndoInput {
  inbox_id: string
}

export interface InboxUndoOutput {
  item: {
    id: string
    /** approval、question、secret、signin、takeover、proposal、discussion、routine_failed、budget、done、error、mention */
    kind: string
    /** decide（要你決定）、done（完成）、mention（提到你）、error（出錯） */
    group: string
    bot_id?: string
    /** Bot 在對話裡問的：是哪件工作 */
    task_id?: string
    /** Bot 在對話裡問的：是哪條對話串 */
    thread_id?: string
    /** 例：核准的 id */
    ref?: string
    title: string
    body: string
    /** 卡片內容（選項、指令、輸入欄…）；不含任何密碼類的值 */
    payload: unknown
    /** open、resolved、dismissed、expired */
    status: string
    /** 怎麼處理的：once、always、deny、allow、answered、provided、declined、expired、gone、canceled、asked_again、dismissed、info */
    resolution?: string
    resolved_by?: string
    resolved_at?: number
    read: boolean
    /** 無人在場的提問：這個時間沒回就過期（0 表示不會過期） */
    expires_at?: number
    /** 這個時間之前可以用 xbot.inbox.undo 復原（0 表示不能復原） */
    undo_until?: number
    created_at: number
  }
}

/**
 * 復原剛剛的決定
 *
 * 10 分鐘內可以復原：退回的核准會回到等你決定，略過的卡會回到待我處理。已經執行的動作、已經交給 Bot 的回答不能復原（undo_until 是 0）。
 *
 * `xbot.inbox.undo`：權限 operate、風險 safe
 */
export function inboxUndo(input: InboxUndoInput, opts?: CallOptions): Promise<InboxUndoOutput> {
  return invoke('xbot.inbox.undo', input, opts).then((r) => r.output)
}

// xbot.models.add_provider

export interface ModelsAddProviderInput {
  /** 內建範本代號（xbot.models.presets），例：openai-api；選了就只要再給金鑰 */
  preset?: string
  /** 供應商代號（小寫英文、數字、-）；用範本時預設和範本同名 */
  id?: string
  /** 顯示名稱 */
  label?: string
  /** 服務網址；用範本時可不填 */
  base_url?: string
  /** 預設的呼叫方式；用範本時可不填 */
  transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
  /** 金鑰（只能寫、不能讀）（密鑰：只能寫、不能讀） */
  api_key?: string
  /** 順便把這個模型設成預設模型 */
  default_model?: string
}

export interface ModelsAddProviderOutput {
  provider: {
    id: string
    label?: string
    base_url: string
    /** 預設的呼叫方式；空白是 chat_completions */
    transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
    /** 從哪個內建範本建立 */
    preset?: string
    /** 有沒有設定金鑰；金鑰本身永遠不會回傳 */
    api_key_set: boolean
    /** 探測時供應商列出的模型（沒探測過時是範本的常用模型） */
    models?: string[]
    /** 上次探測的時間；0 表示還沒探測 */
    probed_at?: number
  }
  default?: {
    provider: string
    model: string
    transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
  }
}

/**
 * 新增一個模型供應商
 *
 * 可以從內建範本（preset）建立，只要再貼金鑰。金鑰加密存在 Model Proxy，不會進 Bot 的電腦，也不會再回傳。只有你本人能新增。
 *
 * `xbot.models.add_provider`：權限 admin、風險 safe
 */
export function modelsAddProvider(input: ModelsAddProviderInput = {}, opts?: CallOptions): Promise<ModelsAddProviderOutput> {
  return invoke('xbot.models.add_provider', input, opts).then((r) => r.output)
}

// xbot.models.get

export type ModelsGetInput = Record<string, never>

export interface ModelsGetOutput {
  providers: {
    id: string
    label?: string
    base_url: string
    /** 預設的呼叫方式；空白是 chat_completions */
    transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
    /** 從哪個內建範本建立 */
    preset?: string
    /** 有沒有設定金鑰；金鑰本身永遠不會回傳 */
    api_key_set: boolean
    /** 探測時供應商列出的模型（沒探測過時是範本的常用模型） */
    models?: string[]
    /** 上次探測的時間；0 表示還沒探測 */
    probed_at?: number
  }[]
  /** 沒設定預設模型時不出現 */
  default?: {
    provider: string
    model: string
    transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
  }
  /** 全域備援模型；沒設定時不出現 */
  fallback?: {
    provider: string
    model: string
    transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
  }
  /** 預設模型和它的供應商都設好了，新 Bot 可以直接對話 */
  ready: boolean
}

/**
 * 看模型設定（供應商與預設模型，不含金鑰）
 *
 * `xbot.models.get`：權限 read、風險 safe
 */
export function modelsGet(input: ModelsGetInput = {}, opts?: CallOptions): Promise<ModelsGetOutput> {
  return invoke('xbot.models.get', input, opts).then((r) => r.output)
}

// xbot.models.presets

export type ModelsPresetsInput = Record<string, never>

export interface ModelsPresetsOutput {
  presets: {
    id: string
    label: string
    base_url: string
    transport: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
    /** 申請金鑰的網址 */
    signup_url?: string
    /** 本機模型（LM Studio 等），不需要金鑰 */
    local: boolean
    /** 常用模型 */
    models: string[]
    /** 建議先用的模型 */
    default_model?: string
    /** 供應商要求的對話識別標頭；Model Proxy 會自動帶上對話串 id */
    session_header?: string
    /** 已經用這個代號設定過 */
    configured: boolean
  }[]
}

/**
 * 列出內建的模型供應商範本
 *
 * 選一個範本（名稱、網址、呼叫方式都填好了），用 xbot.models.add_provider 加上金鑰就能用。
 *
 * `xbot.models.presets`：權限 read、風險 safe
 */
export function modelsPresets(input: ModelsPresetsInput = {}, opts?: CallOptions): Promise<ModelsPresetsOutput> {
  return invoke('xbot.models.presets', input, opts).then((r) => r.output)
}

// xbot.models.pricing

export type ModelsPricingInput = Record<string, never>

export interface ModelsPricingOutput {
  /** 你設定的在前，內建的在後 */
  prices: {
    /** 模型名稱，或「<供應商>/<模型>」只套用在那個供應商 */
    model: string
    input: number
    output: number
    /** 快取命中；0 表示照輸入價 */
    cache_read: number
    /** 寫入快取；0 表示照輸入價 */
    cache_write: number
    /** custom：你設定的；default：內建 */
    source: 'custom' | 'default'
  }[]
  /** 用過、但沒有價格的模型（花費記成 0），請補上價格 */
  unpriced: {
    provider: string
    model: string
    calls: number
    last_at: number
  }[]
}

/**
 * 看模型價目表
 *
 * 用量的花費照這張表算（供應商自己回報花費時用回報的）。不在表上的模型花費記成 0，列在 unpriced。
 *
 * `xbot.models.pricing`：權限 read、風險 safe
 */
export function modelsPricing(input: ModelsPricingInput = {}, opts?: CallOptions): Promise<ModelsPricingOutput> {
  return invoke('xbot.models.pricing', input, opts).then((r) => r.output)
}

// xbot.models.probe

export interface ModelsProbeInput {
  /** 已設定的供應商代號 */
  provider: string
  /** 要測的模型；不填時測預設模型（預設模型不是這個供應商的，就測範本建議的模型） */
  model?: string
}

export interface ModelsProbeOutput {
  provider: string
  /** 供應商 /models 列出的模型 */
  models: string[]
  /** 列模型失敗的原因 */
  list_error?: string
  /** 這次測的模型能不能用、用哪種呼叫方式 */
  check?: {
    model: string
    /** ok、protocol、unavailable、auth、busy、unknown、free_tier */
    status: string
    /** 能用的呼叫方式（status=ok 時） */
    transport?: string
    message: string
    /** 供應商回的錯誤（前 400 字） */
    detail?: string
    at: number
  }
  default_updated: boolean
  at: number
}

/**
 * 探測模型供應商：列出模型、找出能用的呼叫方式
 *
 * 讀供應商的模型清單，並對一個模型依序試 Chat Completions、Responses、Anthropic Messages（各送一個很小的請求），記下能用的那一種（EN-4）。之後 Bot 用這個模型時就照它設定。會花一點點 token。預演不會連到供應商。
 *
 * `xbot.models.probe`：權限 admin、風險 safe
 */
export function modelsProbe(input: ModelsProbeInput, opts?: CallOptions): Promise<ModelsProbeOutput> {
  return invoke('xbot.models.probe', input, opts).then((r) => r.output)
}

// xbot.models.remove_provider

export interface ModelsRemoveProviderInput {
  id: string
}

export interface ModelsRemoveProviderOutput {
  id: string
  /** 預演時是 false */
  removed: boolean
  /** 模型或備援模型指定了這個供應商的 Bot（之後會改用預設模型） */
  affected_bots: string[]
  /** 全域備援模型用的是這個供應商，一起清掉 */
  cleared_fallback: boolean
}

/**
 * 移除一個模型供應商（連同金鑰）
 *
 * 預設模型用的供應商不能移除，先換預設模型。用到它的 Bot 之後會改用預設模型。
 *
 * `xbot.models.remove_provider`：權限 admin、風險 confirm
 */
export function modelsRemoveProvider(input: ModelsRemoveProviderInput, opts?: CallOptions): Promise<ModelsRemoveProviderOutput> {
  return invoke('xbot.models.remove_provider', input, opts).then((r) => r.output)
}

// xbot.models.set_default

export interface ModelsSetDefaultInput {
  /** 已設定的供應商代號 */
  provider: string
  /** 模型名稱，例：gpt-4.1-mini */
  model: string
  /** 預設 chat_completions */
  transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
}

export interface ModelsSetDefaultOutput {
  provider: string
  model: string
  transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
}

/**
 * 設定預設模型
 *
 * 沒有指定模型的 Bot 用這個模型。改了之後，新的對話串就會用新的模型。
 *
 * `xbot.models.set_default`：權限 admin、風險 safe
 */
export function modelsSetDefault(input: ModelsSetDefaultInput, opts?: CallOptions): Promise<ModelsSetDefaultOutput> {
  return invoke('xbot.models.set_default', input, opts).then((r) => r.output)
}

// xbot.models.set_fallback

export interface ModelsSetFallbackInput {
  /** 已設定的供應商代號 */
  provider?: string
  model?: string
  /** 不填時依探測結果或供應商設定 */
  transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
}

export interface ModelsSetFallbackOutput {
  /** 清掉時不出現 */
  fallback?: {
    provider: string
    model: string
    transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
  }
}

/**
 * 設定備援模型
 *
 * 主要模型回 429／5xx 或連不上、而且還沒開始回應時，改用備援模型重試一次。Bot 自己的備援（bots.update fallback）優先。兩個欄位都空白表示不用備援。
 *
 * `xbot.models.set_fallback`：權限 admin、風險 safe
 */
export function modelsSetFallback(input: ModelsSetFallbackInput = {}, opts?: CallOptions): Promise<ModelsSetFallbackOutput> {
  return invoke('xbot.models.set_fallback', input, opts).then((r) => r.output)
}

// xbot.models.set_price

export interface ModelsSetPriceInput {
  /** 模型名稱，或「<供應商>/<模型>」 */
  model: string
  /** 每百萬輸入 token 的美元 */
  input?: number
  /** 每百萬輸出 token 的美元 */
  output?: number
  cache_read?: number
  cache_write?: number
  /** 刪掉你設定的價格，改回內建（或沒有價格） */
  remove?: boolean
}

export interface ModelsSetPriceOutput {
  /** 模型名稱，或「<供應商>/<模型>」只套用在那個供應商 */
  model: string
  input: number
  output: number
  /** 快取命中；0 表示照輸入價 */
  cache_read: number
  /** 寫入快取；0 表示照輸入價 */
  cache_write: number
  /** custom：你設定的；default：內建 */
  source: 'custom' | 'default'
}

/**
 * 設定一個模型的價格
 *
 * 價格會影響花費上限的計算（把價格調低等於放寬上限），所以要你核准，也不能代為核准。之後的用量才會照新價格算。
 *
 * `xbot.models.set_price`：權限 admin、風險 confirm
 */
export function modelsSetPrice(input: ModelsSetPriceInput, opts?: CallOptions): Promise<ModelsSetPriceOutput> {
  return invoke('xbot.models.set_price', input, opts).then((r) => r.output)
}

// xbot.models.set_provider

export interface ModelsSetProviderInput {
  /** 供應商代號（小寫英文、數字、-），例：openai；Bot 的電腦經 /model/<代號>/v1 使用 */
  id: string
  /** OpenAI 相容的服務網址，例：https://api.openai.com/v1 */
  base_url: string
  /** 金鑰（只能寫、不能讀）；不填時保留原本的金鑰（密鑰：只能寫、不能讀） */
  api_key?: string
  /** 順便把這個模型設成預設模型 */
  default_model?: string
  /** 設預設模型時的呼叫方式；預設 chat_completions */
  transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
}

export interface ModelsSetProviderOutput {
  provider: {
    id: string
    label?: string
    base_url: string
    /** 預設的呼叫方式；空白是 chat_completions */
    transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
    /** 從哪個內建範本建立 */
    preset?: string
    /** 有沒有設定金鑰；金鑰本身永遠不會回傳 */
    api_key_set: boolean
    /** 探測時供應商列出的模型（沒探測過時是範本的常用模型） */
    models?: string[]
    /** 上次探測的時間；0 表示還沒探測 */
    probed_at?: number
  }
  default?: {
    provider: string
    model: string
    transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
  }
}

/**
 * 設定一個模型供應商（網址與金鑰）
 *
 * 金鑰加密存在服務端的 Model Proxy，不會進 Bot 的電腦，也不會再回傳（MCP-11）。可以同時用 default_model 設定預設模型。只有你本人能設定。在指令列建議用 --json - 從標準輸入給金鑰，避免留在指令紀錄裡。
 *
 * `xbot.models.set_provider`：權限 admin、風險 safe
 */
export function modelsSetProvider(input: ModelsSetProviderInput, opts?: CallOptions): Promise<ModelsSetProviderOutput> {
  return invoke('xbot.models.set_provider', input, opts).then((r) => r.output)
}

// xbot.models.update_provider

export interface ModelsUpdateProviderInput {
  id: string
  label?: string
  base_url?: string
  transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
  /** 新金鑰；空字串表示清掉金鑰（本機模型用）（密鑰：只能寫、不能讀） */
  api_key?: string
}

export interface ModelsUpdateProviderOutput {
  id: string
  label?: string
  base_url: string
  /** 預設的呼叫方式；空白是 chat_completions */
  transport?: 'chat_completions' | 'codex_responses' | 'anthropic_messages'
  /** 從哪個內建範本建立 */
  preset?: string
  /** 有沒有設定金鑰；金鑰本身永遠不會回傳 */
  api_key_set: boolean
  /** 探測時供應商列出的模型（沒探測過時是範本的常用模型） */
  models?: string[]
  /** 上次探測的時間；0 表示還沒探測 */
  probed_at?: number
}

/**
 * 修改一個模型供應商
 *
 * 只改有給的欄位；不給金鑰就保留原本的。只有你本人能修改。
 *
 * `xbot.models.update_provider`：權限 admin、風險 safe
 */
export function modelsUpdateProvider(input: ModelsUpdateProviderInput, opts?: CallOptions): Promise<ModelsUpdateProviderOutput> {
  return invoke('xbot.models.update_provider', input, opts).then((r) => r.output)
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

// xbot.usage.caps

export interface UsageCapsInput {
  /** 只看這個 Bot */
  bot_id?: string
}

export interface UsageCapsOutput {
  /** 今天（本機時區） */
  day: string
  month: string
  /** 全部 Bot 合計（只有月上限）；指定 bot_id 時不出現 */
  global?: {
    /** Bot id、供應商代號；全域是 all */
    key: string
    label?: string
    /** 每日上限；0 表示不限（全域沒有每日上限） */
    daily_usd: number
    /** 每月上限；0 表示不限 */
    monthly_usd: number
    spent_today_usd: number
    spent_month_usd: number
    /** 已經到上限，模型請求會被擋下 */
    exceeded: boolean
    daily_micros: number
    monthly_micros: number
    spent_day_micros: number
    spent_month_micros: number
  }
  bots: {
    /** Bot id、供應商代號；全域是 all */
    key: string
    label?: string
    /** 每日上限；0 表示不限（全域沒有每日上限） */
    daily_usd: number
    /** 每月上限；0 表示不限 */
    monthly_usd: number
    spent_today_usd: number
    spent_month_usd: number
    /** 已經到上限，模型請求會被擋下 */
    exceeded: boolean
    daily_micros: number
    monthly_micros: number
    spent_day_micros: number
    spent_month_micros: number
  }[]
  /** 有設上限的模型供應商 */
  providers: {
    /** Bot id、供應商代號；全域是 all */
    key: string
    label?: string
    /** 每日上限；0 表示不限（全域沒有每日上限） */
    daily_usd: number
    /** 每月上限；0 表示不限 */
    monthly_usd: number
    spent_today_usd: number
    spent_month_usd: number
    /** 已經到上限，模型請求會被擋下 */
    exceeded: boolean
    daily_micros: number
    monthly_micros: number
    spent_day_micros: number
    spent_month_micros: number
  }[]
}

/**
 * 看花費上限與目前花費
 *
 * `xbot.usage.caps`：權限 read、風險 safe
 */
export function usageCaps(input: UsageCapsInput = {}, opts?: CallOptions): Promise<UsageCapsOutput> {
  return invoke('xbot.usage.caps', input, opts).then((r) => r.output)
}

// xbot.usage.export

export interface UsageExportInput {
  /** 看最近幾天（含今天）；預設 30 */
  days?: number
  /** 從這個時間開始（毫秒）；給了就不看 days */
  since?: number
  /** 到這個時間之前（毫秒）；預設現在 */
  until?: number
  /** 只看這個 Bot */
  bot_id?: string
  /** 只看這個模型供應商 */
  provider?: string
}

export interface UsageExportOutput {
  /** 建議的檔名 */
  filename: string
  /** CSV（UTF-8，第一列是欄位名稱） */
  csv: string
  rows: number
  /** 超過 100000 筆，只匯出前面的 */
  truncated: boolean
}

/**
 * 匯出用量明細（CSV）
 *
 * 每一次模型請求一列。最多 100000 筆。
 *
 * `xbot.usage.export`：權限 read、風險 safe
 */
export function usageExport(input: UsageExportInput = {}, opts?: CallOptions): Promise<UsageExportOutput> {
  return invoke('xbot.usage.export', input, opts).then((r) => r.output)
}

// xbot.usage.set_caps

export interface UsageSetCapsInput {
  /** 設這個 Bot 的上限 */
  bot_id?: string
  /** 設這個模型供應商的上限（所有 Bot 合計；用來省著用某把金鑰的額度） */
  provider?: string
  /** 每日上限（美元）；0 表示不限。全域不能設 */
  daily_usd?: number
  /** 每月上限（美元）；0 表示不限 */
  monthly_usd?: number
}

export interface UsageSetCapsOutput {
  /** Bot id、供應商代號；全域是 all */
  key: string
  label?: string
  /** 每日上限；0 表示不限（全域沒有每日上限） */
  daily_usd: number
  /** 每月上限；0 表示不限 */
  monthly_usd: number
  spent_today_usd: number
  spent_month_usd: number
  /** 已經到上限，模型請求會被擋下 */
  exceeded: boolean
  daily_micros: number
  monthly_micros: number
  spent_day_micros: number
  spent_month_micros: number
}

/**
 * 設定花費上限
 *
 * 到上限時，Bot 的模型請求會被擋下、正在做的工作會停下，並在「待我處理」出現一張卡（US-2）。降低上限直接生效；提高或取消上限要你核准，而且不能代為核准。
 *
 * `xbot.usage.set_caps`：權限 manage、風險 safe（依輸入可能需要核准）
 */
export function usageSetCaps(input: UsageSetCapsInput = {}, opts?: CallOptions): Promise<UsageSetCapsOutput> {
  return invoke('xbot.usage.set_caps', input, opts).then((r) => r.output)
}

// xbot.usage.summary

export interface UsageSummaryInput {
  /** 看最近幾天（含今天）；預設 30 */
  days?: number
  /** 從這個時間開始（毫秒）；給了就不看 days */
  since?: number
  /** 到這個時間之前（毫秒）；預設現在 */
  until?: number
  /** 只看這個 Bot */
  bot_id?: string
  /** 只看這個模型供應商 */
  provider?: string
}

export interface UsageSummaryOutput {
  since: number
  until: number
  total: {
    /** Bot id、供應商、「<供應商>/<模型>」或日期（2026-10-18） */
    key: string
    /** 顯示名稱（Bot 名稱） */
    label?: string
    calls: number
    /** 含快取命中與寫入快取 */
    input_tokens: number
    output_tokens: number
    cache_read_tokens: number
    cache_write_tokens: number
    /** 快取命中率：cache_read_tokens ÷ input_tokens（0～1） */
    cache_hit_rate: number
    /** 花費（百萬分之一美元） */
    cost_micros: number
    cost_usd: number
    /** 沒有價格、花費記成 0 的次數 */
    unpriced_calls: number
    /** 改用備援模型的次數 */
    fallback_calls: number
  }
  /** 花費多到少 */
  by_bot: {
    /** Bot id、供應商、「<供應商>/<模型>」或日期（2026-10-18） */
    key: string
    /** 顯示名稱（Bot 名稱） */
    label?: string
    calls: number
    /** 含快取命中與寫入快取 */
    input_tokens: number
    output_tokens: number
    cache_read_tokens: number
    cache_write_tokens: number
    /** 快取命中率：cache_read_tokens ÷ input_tokens（0～1） */
    cache_hit_rate: number
    /** 花費（百萬分之一美元） */
    cost_micros: number
    cost_usd: number
    /** 沒有價格、花費記成 0 的次數 */
    unpriced_calls: number
    /** 改用備援模型的次數 */
    fallback_calls: number
  }[]
  /** 花費多到少 */
  by_provider: {
    /** Bot id、供應商、「<供應商>/<模型>」或日期（2026-10-18） */
    key: string
    /** 顯示名稱（Bot 名稱） */
    label?: string
    calls: number
    /** 含快取命中與寫入快取 */
    input_tokens: number
    output_tokens: number
    cache_read_tokens: number
    cache_write_tokens: number
    /** 快取命中率：cache_read_tokens ÷ input_tokens（0～1） */
    cache_hit_rate: number
    /** 花費（百萬分之一美元） */
    cost_micros: number
    cost_usd: number
    /** 沒有價格、花費記成 0 的次數 */
    unpriced_calls: number
    /** 改用備援模型的次數 */
    fallback_calls: number
  }[]
  /** 花費多到少 */
  by_model: {
    /** Bot id、供應商、「<供應商>/<模型>」或日期（2026-10-18） */
    key: string
    /** 顯示名稱（Bot 名稱） */
    label?: string
    calls: number
    /** 含快取命中與寫入快取 */
    input_tokens: number
    output_tokens: number
    cache_read_tokens: number
    cache_write_tokens: number
    /** 快取命中率：cache_read_tokens ÷ input_tokens（0～1） */
    cache_hit_rate: number
    /** 花費（百萬分之一美元） */
    cost_micros: number
    cost_usd: number
    /** 沒有價格、花費記成 0 的次數 */
    unpriced_calls: number
    /** 改用備援模型的次數 */
    fallback_calls: number
  }[]
  /** 日期舊到新 */
  by_day: {
    /** Bot id、供應商、「<供應商>/<模型>」或日期（2026-10-18） */
    key: string
    /** 顯示名稱（Bot 名稱） */
    label?: string
    calls: number
    /** 含快取命中與寫入快取 */
    input_tokens: number
    output_tokens: number
    cache_read_tokens: number
    cache_write_tokens: number
    /** 快取命中率：cache_read_tokens ÷ input_tokens（0～1） */
    cache_hit_rate: number
    /** 花費（百萬分之一美元） */
    cost_micros: number
    cost_usd: number
    /** 沒有價格、花費記成 0 的次數 */
    unpriced_calls: number
    /** 改用備援模型的次數 */
    fallback_calls: number
  }[]
  /** 這段期間用過、但沒有價格的模型 */
  unpriced: {
    provider: string
    model: string
    calls: number
    last_at: number
  }[]
}

/**
 * 看用量與花費
 *
 * 依 Bot、供應商、模型、日期合計 token、花費與快取命中率（US-1）。日期用本機時區切。
 *
 * `xbot.usage.summary`：權限 read、風險 safe
 */
export function usageSummary(input: UsageSummaryInput = {}, opts?: CallOptions): Promise<UsageSummaryOutput> {
  return invoke('xbot.usage.summary', input, opts).then((r) => r.output)
}

// ── 操作清單 ─────────────────────────────────────────

/** 每個操作的輸入與輸出型別，invoke 用它檢查型別。 */
export interface Ops {
  'xbot.agents.list': { input: AgentsListInput; output: AgentsListOutput }
  'xbot.agents.pair_start': { input: AgentsPairStartInput; output: AgentsPairStartOutput }
  'xbot.agents.revoke': { input: AgentsRevokeInput; output: AgentsRevokeOutput }
  'xbot.agents.update_scopes': { input: AgentsUpdateScopesInput; output: AgentsUpdateScopesOutput }
  'xbot.approvals.decide': { input: ApprovalsDecideInput; output: ApprovalsDecideOutput }
  'xbot.approvals.list': { input: ApprovalsListInput; output: ApprovalsListOutput }
  'xbot.approvals.wait': { input: ApprovalsWaitInput; output: ApprovalsWaitOutput }
  'xbot.audit.list': { input: AuditListInput; output: AuditListOutput }
  'xbot.bots.archive': { input: BotsArchiveInput; output: BotsArchiveOutput }
  'xbot.bots.create': { input: BotsCreateInput; output: BotsCreateOutput }
  'xbot.bots.delete': { input: BotsDeleteInput; output: BotsDeleteOutput }
  'xbot.bots.get': { input: BotsGetInput; output: BotsGetOutput }
  'xbot.bots.list': { input: BotsListInput; output: BotsListOutput }
  'xbot.bots.update': { input: BotsUpdateInput; output: BotsUpdateOutput }
  'xbot.chat.ask_again': { input: ChatAskAgainInput; output: ChatAskAgainOutput }
  'xbot.chat.list_threads': { input: ChatListThreadsInput; output: ChatListThreadsOutput }
  'xbot.chat.read': { input: ChatReadInput; output: ChatReadOutput }
  'xbot.chat.respond': { input: ChatRespondInput; output: ChatRespondOutput }
  'xbot.chat.send': { input: ChatSendInput; output: ChatSendOutput }
  'xbot.chat.steps': { input: ChatStepsInput; output: ChatStepsOutput }
  'xbot.chat.stop': { input: ChatStopInput; output: ChatStopOutput }
  'xbot.chat.wait_until_settled': { input: ChatWaitUntilSettledInput; output: ChatWaitUntilSettledOutput }
  'xbot.inbox.counts': { input: InboxCountsInput; output: InboxCountsOutput }
  'xbot.inbox.dismiss': { input: InboxDismissInput; output: InboxDismissOutput }
  'xbot.inbox.list': { input: InboxListInput; output: InboxListOutput }
  'xbot.inbox.mark_read': { input: InboxMarkReadInput; output: InboxMarkReadOutput }
  'xbot.inbox.undo': { input: InboxUndoInput; output: InboxUndoOutput }
  'xbot.models.add_provider': { input: ModelsAddProviderInput; output: ModelsAddProviderOutput }
  'xbot.models.get': { input: ModelsGetInput; output: ModelsGetOutput }
  'xbot.models.presets': { input: ModelsPresetsInput; output: ModelsPresetsOutput }
  'xbot.models.pricing': { input: ModelsPricingInput; output: ModelsPricingOutput }
  'xbot.models.probe': { input: ModelsProbeInput; output: ModelsProbeOutput }
  'xbot.models.remove_provider': { input: ModelsRemoveProviderInput; output: ModelsRemoveProviderOutput }
  'xbot.models.set_default': { input: ModelsSetDefaultInput; output: ModelsSetDefaultOutput }
  'xbot.models.set_fallback': { input: ModelsSetFallbackInput; output: ModelsSetFallbackOutput }
  'xbot.models.set_price': { input: ModelsSetPriceInput; output: ModelsSetPriceOutput }
  'xbot.models.set_provider': { input: ModelsSetProviderInput; output: ModelsSetProviderOutput }
  'xbot.models.update_provider': { input: ModelsUpdateProviderInput; output: ModelsUpdateProviderOutput }
  'xbot.settings.get': { input: SettingsGetInput; output: SettingsGetOutput }
  'xbot.system.ping': { input: SystemPingInput; output: SystemPingOutput }
  'xbot.usage.caps': { input: UsageCapsInput; output: UsageCapsOutput }
  'xbot.usage.export': { input: UsageExportInput; output: UsageExportOutput }
  'xbot.usage.set_caps': { input: UsageSetCapsInput; output: UsageSetCapsOutput }
  'xbot.usage.summary': { input: UsageSummaryInput; output: UsageSummaryOutput }
}

export type OpName = keyof Ops

/** 每個操作的 HTTP 路徑、權限範圍、風險。 */
export const OPS: Record<OpName, OpMeta> = {
  'xbot.agents.list': { path: '/api/v1/agents.list', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.agents.pair_start': { path: '/api/v1/agents.pair_start', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.agents.revoke': { path: '/api/v1/agents.revoke', scope: 'admin', risk: 'confirm', mutates: true },
  'xbot.agents.update_scopes': { path: '/api/v1/agents.update_scopes', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.approvals.decide': { path: '/api/v1/approvals.decide', scope: 'operate', risk: 'safe', mutates: true },
  'xbot.approvals.list': { path: '/api/v1/approvals.list', scope: 'read', risk: 'safe', mutates: false },
  'xbot.approvals.wait': { path: '/api/v1/approvals.wait', scope: 'read', risk: 'safe', mutates: false },
  'xbot.audit.list': { path: '/api/v1/audit.list', scope: 'read', risk: 'safe', mutates: false },
  'xbot.bots.archive': { path: '/api/v1/bots.archive', scope: 'manage', risk: 'safe', mutates: true },
  'xbot.bots.create': { path: '/api/v1/bots.create', scope: 'manage', risk: 'safe', mutates: true },
  'xbot.bots.delete': { path: '/api/v1/bots.delete', scope: 'manage', risk: 'confirm', mutates: true },
  'xbot.bots.get': { path: '/api/v1/bots.get', scope: 'read', risk: 'safe', mutates: false },
  'xbot.bots.list': { path: '/api/v1/bots.list', scope: 'read', risk: 'safe', mutates: false },
  'xbot.bots.update': { path: '/api/v1/bots.update', scope: 'manage', risk: 'safe', mutates: true },
  'xbot.chat.ask_again': { path: '/api/v1/chat.ask_again', scope: 'operate', risk: 'safe', mutates: true },
  'xbot.chat.list_threads': { path: '/api/v1/chat.list_threads', scope: 'read', risk: 'safe', mutates: false },
  'xbot.chat.read': { path: '/api/v1/chat.read', scope: 'read', risk: 'safe', mutates: false },
  'xbot.chat.respond': { path: '/api/v1/chat.respond', scope: 'operate', risk: 'safe', mutates: true },
  'xbot.chat.send': { path: '/api/v1/chat.send', scope: 'operate', risk: 'safe', mutates: true },
  'xbot.chat.steps': { path: '/api/v1/chat.steps', scope: 'read', risk: 'safe', mutates: false },
  'xbot.chat.stop': { path: '/api/v1/chat.stop', scope: 'operate', risk: 'safe', mutates: true },
  'xbot.chat.wait_until_settled': { path: '/api/v1/chat.wait_until_settled', scope: 'read', risk: 'safe', mutates: false },
  'xbot.inbox.counts': { path: '/api/v1/inbox.counts', scope: 'read', risk: 'safe', mutates: false },
  'xbot.inbox.dismiss': { path: '/api/v1/inbox.dismiss', scope: 'operate', risk: 'safe', mutates: true },
  'xbot.inbox.list': { path: '/api/v1/inbox.list', scope: 'read', risk: 'safe', mutates: false },
  'xbot.inbox.mark_read': { path: '/api/v1/inbox.mark_read', scope: 'operate', risk: 'safe', mutates: true },
  'xbot.inbox.undo': { path: '/api/v1/inbox.undo', scope: 'operate', risk: 'safe', mutates: true },
  'xbot.models.add_provider': { path: '/api/v1/models.add_provider', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.models.get': { path: '/api/v1/models.get', scope: 'read', risk: 'safe', mutates: false },
  'xbot.models.presets': { path: '/api/v1/models.presets', scope: 'read', risk: 'safe', mutates: false },
  'xbot.models.pricing': { path: '/api/v1/models.pricing', scope: 'read', risk: 'safe', mutates: false },
  'xbot.models.probe': { path: '/api/v1/models.probe', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.models.remove_provider': { path: '/api/v1/models.remove_provider', scope: 'admin', risk: 'confirm', mutates: true },
  'xbot.models.set_default': { path: '/api/v1/models.set_default', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.models.set_fallback': { path: '/api/v1/models.set_fallback', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.models.set_price': { path: '/api/v1/models.set_price', scope: 'admin', risk: 'confirm', mutates: true },
  'xbot.models.set_provider': { path: '/api/v1/models.set_provider', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.models.update_provider': { path: '/api/v1/models.update_provider', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.settings.get': { path: '/api/v1/settings.get', scope: 'admin', risk: 'safe', mutates: true },
  'xbot.system.ping': { path: '/api/v1/system.ping', scope: 'read', risk: 'safe', mutates: false },
  'xbot.usage.caps': { path: '/api/v1/usage.caps', scope: 'read', risk: 'safe', mutates: false },
  'xbot.usage.export': { path: '/api/v1/usage.export', scope: 'read', risk: 'safe', mutates: false },
  'xbot.usage.set_caps': { path: '/api/v1/usage.set_caps', scope: 'manage', risk: 'safe', mutates: true },
  'xbot.usage.summary': { path: '/api/v1/usage.summary', scope: 'read', risk: 'safe', mutates: false },
}
