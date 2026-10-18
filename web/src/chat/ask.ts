// Bot 在等你回覆的問題（核准指令、補充問題、密碼）：對話裡的 chat.needs_approval 事件和待我處理的卡片
// 都轉成同一個 Ask，用同一張卡（AskCard）回覆，回覆一律走 xbot.chat.respond（internal/runtime/questions.go）。

export type AskKind = 'approval' | 'question' | 'secret'

export type AskField = { name: string; label: string; kind: 'text' | 'password' | 'code' }

export type AskQuestion = { question: string; choices?: string[] }

export type Ask = {
  inboxId: string
  kind: AskKind
  /** 引擎原本的請求名稱（Hermes：approval、clarify、secret、sudo、vault.code…）。 */
  method?: string
  prompt: string
  command?: string
  description?: string
  /** 核准：once／always／deny；單題提問：選項文字。 */
  choices?: string[]
  questions?: AskQuestion[]
  fields?: AskField[]
  /** 無人在場的工作：這個時間沒回就過期（毫秒）。 */
  expiresAt?: number
  botName?: string
}

/** 回覆後在對話裡留下的一行結果（chat.answered）。 */
export type Answered = {
  inboxId: string
  kind: string
  /** once、always、deny、answered、provided、declined、expired、gone、canceled */
  resolution: string
  text: string
  at: number
}

type Params = {
  command?: string
  description?: string
  prompt?: string
  env_var?: string
  hint?: string
  site?: string
  display_name?: string
  questions?: { question?: string; choices?: string[] }[]
}

function first(...v: (string | undefined)[]): string {
  return v.find((s) => s && s.trim()) ?? ''
}

/** 和後端 secretFields 一樣的輸入欄。 */
export function secretFields(method: string | undefined, label: string): AskField[] {
  switch (method) {
    case 'vault.save_login':
      return [
        { name: 'identifier', label: '帳號', kind: 'text' },
        { name: 'password', label: '密碼', kind: 'password' },
      ]
    case 'vault.code':
      return [{ name: 'value', label: label || '驗證碼', kind: 'code' }]
    case 'vault.unlock_prompt':
      return [{ name: 'value', label: label || '解鎖密碼', kind: 'password' }]
  }
  return [{ name: 'value', label: label || '秘密輸入', kind: 'password' }]
}

function kindOf(k: string | undefined): AskKind {
  return k === 'approval' ? 'approval' : k === 'secret' ? 'secret' : 'question'
}

/** chat.needs_approval 的 payload（engine.Event＋inbox_id、expires_at）。沒有卡片 id 時回 undefined（舊版事件）。 */
export function askFromEvent(payload: unknown): Ask | undefined {
  const p = (payload ?? {}) as {
    inbox_id?: string
    expires_at?: number
    request?: { kind?: string; method?: string; prompt?: string; choices?: string[]; params?: Params }
  }
  const r = p.request
  if (!r) return undefined
  const kind = kindOf(r.kind)
  const params = r.params ?? {}
  const ask: Ask = { inboxId: p.inbox_id ?? '', kind, method: r.method, prompt: r.prompt ?? '', expiresAt: p.expires_at || undefined }
  if (kind === 'approval') {
    ask.command = params.command
    ask.description = params.description
    ask.choices = ['once', ...((r.choices ?? []).includes('always') ? ['always'] : []), 'deny']
  } else if (kind === 'secret') {
    ask.fields = secretFields(r.method, first(params.hint, params.site, params.display_name, params.prompt, params.env_var, r.prompt))
  } else {
    const qs = (params.questions ?? []).map((q) => ({ question: q.question ?? '', choices: q.choices }))
    if (qs.length > 1) ask.questions = qs
    else ask.choices = r.choices
  }
  return ask
}

/** 待我處理卡片（xbot.inbox.list 的一項）的 payload。 */
export function askFromItem(item: { id: string; kind: string; body: string; payload: unknown; expires_at?: number }): Ask {
  const p = (item.payload ?? {}) as {
    method?: string
    prompt?: string
    command?: string
    description?: string
    choices?: string[]
    questions?: AskQuestion[]
    fields?: AskField[]
    bot_name?: string
  }
  const ask: Ask = {
    inboxId: item.id,
    kind: kindOf(item.kind),
    method: p.method,
    prompt: p.prompt || item.body,
    command: p.command,
    description: p.description,
    choices: p.choices,
    questions: p.questions && p.questions.length > 1 ? p.questions : undefined,
    fields: p.fields,
    expiresAt: item.expires_at || undefined,
    botName: p.bot_name,
  }
  if (ask.kind === 'secret' && !ask.fields?.length) ask.fields = secretFields(p.method, '')
  return ask
}

/** 回覆後那一行要寫什麼（C08～C10 的「處理後」）。 */
export function answeredLine(a: Pick<Answered, 'kind' | 'resolution' | 'text'>): { text: string; tone: 'ok' | 'muted' | 'expired' } {
  switch (a.resolution) {
    case 'once':
      return { text: '已允許這次', tone: 'ok' }
    case 'always':
      return { text: '一律允許', tone: 'ok' }
    case 'allow':
      return { text: '已允許', tone: 'ok' }
    case 'deny':
      return { text: '已拒絕', tone: 'muted' }
    case 'answered':
      return { text: `你回答了：${a.text}`, tone: 'ok' }
    case 'provided':
      return { text: '已提供 · 沒寫進對話', tone: 'ok' }
    case 'declined':
      return { text: a.kind === 'secret' ? '不提供' : '已略過', tone: 'muted' }
    case 'expired':
      return { text: a.text || '已過期 · 10 分鐘沒回覆', tone: 'expired' }
    case 'canceled':
    case 'gone':
      return { text: 'Bot 不再等這個回覆', tone: 'muted' }
  }
  return { text: a.text || '已回覆', tone: 'ok' }
}
