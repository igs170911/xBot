package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"xbot/internal/ops"
)

// GeneratedTSPath 是前端生成檔的位置（相對專案根目錄）。make gen 寫入，make check 檢查是否過期。
const GeneratedTSPath = "web/src/api/generated.ts"

// GenerateTS 由操作清單產生前端用的 TypeScript：每個操作的輸入輸出型別、一個呼叫函式，
// 加上共用的錯誤型別、invoke 與 SSE 訂閱 helper。輸出是固定的（同樣的清單產生同樣的內容），
// make check 用它判斷 generated.ts 有沒有過期。
func GenerateTS(list []ops.Info) []byte {
	var b strings.Builder
	b.WriteString(tsHeader)

	codes := []string{}
	for _, c := range []ops.Code{ops.CodeForbidden, ops.CodeNotFound, ops.CodeInvalidInput, ops.CodeLimit, ops.CodeBudget,
		ops.CodePendingApproval, ops.CodeConflict, ops.CodeInternal} {
		codes = append(codes, tsString(string(c)))
	}
	fmt.Fprintf(&b, "export const API_VERSION = %s\n\n", tsString(ops.Version))
	fmt.Fprintf(&b, "/** 固定的錯誤代碼（MCP-5）。依代碼判斷，不要解析 message。 */\nexport type ErrorCode = %s\n\n", strings.Join(codes, " | "))
	b.WriteString(tsRuntime)

	b.WriteString("\n// ── 操作 ─────────────────────────────────────────────\n")
	for _, info := range list {
		base := pascal(info.Domain) + pascal(info.Action)
		fmt.Fprintf(&b, "\n// %s\n\n", info.Name)
		writeTSType(&b, base+"Input", info.Input, true)
		writeTSType(&b, base+"Output", info.Output, false)
		doc := info.Summary
		if info.Description != "" {
			doc += "\n\n" + info.Description
		}
		doc += fmt.Sprintf("\n\n`%s`：權限 %s、風險 %s", info.Name, info.Scope, info.Risk)
		if info.DynamicRisk {
			doc += "（依輸入可能需要核准）"
		}
		writeDoc(&b, "", doc)
		param := "input: " + base + "Input"
		if len(info.Input.Required) == 0 {
			param += " = {}"
		}
		fmt.Fprintf(&b, "export function %s(%s, opts?: CallOptions): Promise<%sOutput> {\n", camel(info.Domain)+pascal(info.Action), param, base)
		fmt.Fprintf(&b, "  return invoke(%s, input, opts).then((r) => r.output)\n}\n", tsString(info.Name))
	}

	b.WriteString("\n// ── 操作清單 ─────────────────────────────────────────\n\n")
	b.WriteString("/** 每個操作的輸入與輸出型別，invoke 用它檢查型別。 */\nexport interface Ops {\n")
	for _, info := range list {
		base := pascal(info.Domain) + pascal(info.Action)
		fmt.Fprintf(&b, "  %s: { input: %sInput; output: %sOutput }\n", tsString(info.Name), base, base)
	}
	if len(list) == 0 {
		b.WriteString("  [name: string]: { input: Record<string, unknown>; output: unknown }\n")
	}
	b.WriteString("}\n\nexport type OpName = keyof Ops\n\n")
	b.WriteString("/** 每個操作的 HTTP 路徑、權限範圍、風險、一句話說明（稽核紀錄顯示用）。 */\nexport const OPS: Record<OpName, OpMeta> = {\n")
	for _, info := range list {
		fmt.Fprintf(&b, "  %s: { path: %s, scope: %s, risk: %s, mutates: %t, summary: %s },\n",
			tsString(info.Name), tsString(Route(info.Name)), tsString(string(info.Scope)), tsString(string(info.Risk)), info.Mutates, tsString(info.Summary))
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

const tsHeader = `// 由 make gen（xbot gen ts）從操作登錄表產生，不要手改。make check 會檢查它有沒有過期。
// 介面只能呼叫這裡的操作（AGENTS.md：MCP 優先，不准另開私有路由）。

`

const tsRuntime = `/** 操作失敗時的內容（MCP-5）。 */
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
  /** 一句白話中文（操作的 Summary） */
  summary: string
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
  /** 經內建 Tailscale 的 tailnet 連進來（C35）；tailscale_user 是查到的 Tailscale 帳號（只是提示，一樣要登入）。 */
  tailnet?: boolean
  tailscale_user?: string
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

/** 手機用 QR 裡的一次性配對碼登入（W31）；成功後瀏覽器會記住（cookie）。 */
export function pairLogin(code: string, opts: CallOptions = {}): Promise<{ ok: true }> {
  return authCall('pair', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code }) }, opts)
}

/** 登出這個瀏覽器。 */
export function logout(opts: CallOptions = {}): Promise<{ ok: true }> {
  return authCall('logout', { method: 'POST' }, opts)
}

// ── 檔案上傳下載（內容不走 JSON，C33）──────────────────

/** Bot 電腦裡一個檔案的下載網址（GET，同一個登入）。inline：在瀏覽器裡直接顯示（圖片、PDF、純文字）；thumb：縮圖。 */
export function filesDownloadUrl(botId: string, path: string, opts: { inline?: boolean; thumb?: boolean } = {}): string {
  const q = new URLSearchParams({ bot_id: botId, path })
  if (opts.inline) q.set('inline', '1')
  if (opts.thumb) q.set('thumb', '1')
  return '/api/v1/files.download?' + q.toString()
}

/** 上傳檔案到 Bot 電腦的資料夾（multipart，每個檔案經 xbot.files.upload）。dir 空白是 /workspace/uploads。 */
export async function filesUploadForm(botId: string, files: File[], dir = '', opts: CallOptions = {}): Promise<FilesUploadOutput[]> {
  const form = new FormData()
  for (const f of files) form.append('file', f, f.name)
  const q = new URLSearchParams({ bot_id: botId })
  if (dir) q.set('dir', dir)
  const res = await (opts.fetch ?? fetch)((opts.baseUrl ?? '') + '/api/v1/files.upload?' + q.toString(), {
    method: 'POST',
    body: form,
    credentials: 'same-origin',
    signal: opts.signal,
  })
  const body = (await res.json().catch(() => null)) as { files?: FilesUploadOutput[]; error?: ApiErrorBody } | null
  if (body?.error) throw new ApiError(res.status, body.error)
  if (!res.ok || !body?.files) throw new ApiError(res.status, { code: 'internal', message: '服務沒有正常回應，請稍後再試', retryable: true })
  return body.files
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
`

func writeTSType(b *strings.Builder, name string, s *ops.Schema, input bool) {
	if s != nil && s.Type == "object" && s.Properties != nil {
		if len(s.Properties) == 0 {
			fmt.Fprintf(b, "export type %s = Record<string, never>\n\n", name)
			return
		}
		fmt.Fprintf(b, "export interface %s %s\n\n", name, tsObject(s, input, ""))
		return
	}
	fmt.Fprintf(b, "export type %s = %s\n\n", name, tsType(s, input, ""))
}

func tsType(s *ops.Schema, input bool, indent string) string {
	if s == nil {
		return "unknown"
	}
	if len(s.Enum) > 0 {
		parts := make([]string, 0, len(s.Enum))
		for _, v := range s.Enum {
			if str, ok := v.(string); ok {
				parts = append(parts, tsString(str))
				continue
			}
			j, _ := json.Marshal(v)
			parts = append(parts, string(j))
		}
		return strings.Join(parts, " | ")
	}
	switch s.Type {
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		item := tsType(s.Items, input, indent)
		if strings.ContainsAny(item, "|&") && !strings.HasPrefix(item, "{") {
			item = "(" + item + ")"
		}
		return item + "[]"
	case "object":
		if s.Properties != nil {
			if len(s.Properties) == 0 {
				return "Record<string, never>"
			}
			return tsObject(s, input, indent)
		}
		if extra, ok := s.AdditionalProperties.(*ops.Schema); ok {
			return "Record<string, " + tsType(extra, input, indent) + ">"
		}
		return "Record<string, unknown>"
	}
	return "unknown"
}

func tsObject(s *ops.Schema, input bool, indent string) string {
	order := s.Order
	if len(order) != len(s.Properties) {
		order = slices.Sorted(maps.Keys(s.Properties))
	}
	inner := indent + "  "
	var b strings.Builder
	b.WriteString("{\n")
	for _, name := range order {
		p := s.Properties[name]
		doc := p.Description
		if p.Secret {
			doc = strings.TrimSpace(doc + "（密鑰：只能寫、不能讀）")
		}
		writeDoc(&b, inner, doc)
		optional := !s.IsRequired(name)
		if !input {
			optional = s.IsOmitEmpty(name)
		}
		q := ""
		if optional {
			q = "?"
		}
		fmt.Fprintf(&b, "%s%s%s: %s\n", inner, tsKey(name), q, tsType(p, input, inner))
	}
	b.WriteString(indent + "}")
	return b.String()
}

func writeDoc(b *strings.Builder, indent, doc string) {
	doc = strings.TrimSpace(strings.ReplaceAll(doc, "*/", "* /"))
	if doc == "" {
		return
	}
	lines := strings.Split(doc, "\n")
	if len(lines) == 1 {
		fmt.Fprintf(b, "%s/** %s */\n", indent, lines[0])
		return
	}
	fmt.Fprintf(b, "%s/**\n", indent)
	for _, l := range lines {
		if l == "" {
			fmt.Fprintf(b, "%s *\n", indent)
		} else {
			fmt.Fprintf(b, "%s * %s\n", indent, l)
		}
	}
	fmt.Fprintf(b, "%s */\n", indent)
}

var identRE = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func tsKey(name string) string {
	if identRE.MatchString(name) {
		return name
	}
	return tsString(name)
}

func tsString(s string) string {
	j, _ := json.Marshal(s)
	return "'" + strings.ReplaceAll(strings.Trim(string(j), `"`), `'`, `\'`) + "'"
}

// pascal：list_threads → ListThreads。
func pascal(s string) string {
	var b strings.Builder
	for part := range strings.SplitSeq(s, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

// camel：list_threads → listThreads。
func camel(s string) string {
	p := pascal(s)
	if p == "" {
		return p
	}
	return strings.ToLower(p[:1]) + p[1:]
}
