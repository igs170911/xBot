import { vi } from 'vitest'

// 測試用的假服務：依網址回操作結果（只給單元測試用）。
export type OpHandler = (input: Record<string, unknown>) => unknown

export type FakeApi = {
  calls: Array<{ path: string; input: Record<string, unknown> }>
  ops: Record<string, OpHandler>
  auth: { authenticated: boolean; local: boolean }
}

const defaults: Record<string, OpHandler> = {
  'system.ping': () => ({ pong: true, api: 'xbot.v1', build: 'v-test', caller: 'owner' }),
  'bots.list': () => ({ bots: [] }),
  'inbox.list': () => ({ items: [] }),
  'models.get': () => ({ providers: [], ready: true, default: { provider: 'openai', model: 'gpt-test' } }),
  'chat.list_threads': () => ({ threads: [] }),
  'teams.list': () => ({ teams: [] }),
  'agenda.list': () => ({ from: 0, to: 0, pending: [], decided: [], calendar: [], approvals: [], open_max: 3 }),
}

/** 換掉全域 fetch；ops 的 key 是去掉 /api/v1/ 的路徑（例：bots.list）。丟 {error} 物件就回錯誤。 */
export function fakeApi(ops: Record<string, OpHandler> = {}): FakeApi {
  const api: FakeApi = { calls: [], ops: { ...defaults, ...ops }, auth: { authenticated: true, local: true } }
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    const path = String(url).replace(/^.*\/api\/v1\//, '')
    if (path === 'auth/status') return new Response(JSON.stringify(api.auth))
    if (path === 'auth/login') {
      const body = JSON.parse(String(init?.body ?? '{}')) as { token?: string }
      if (body.token === 'good') {
        api.auth.authenticated = true
        return new Response(JSON.stringify({ ok: true }))
      }
      return new Response(JSON.stringify({ error: { code: 'forbidden', message: '登入碼或權杖不正確', retryable: false } }), { status: 401 })
    }
    const input = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
    api.calls.push({ path, input })
    const h = api.ops[path]
    if (!h) return new Response(JSON.stringify({ error: { code: 'not_found', message: '沒有這個操作 ' + path, retryable: false } }), { status: 404 })
    try {
      return new Response(JSON.stringify({ op: 'xbot.' + path, output: h(input) }))
    } catch (e) {
      const err = e as { status?: number; error?: unknown }
      return new Response(JSON.stringify({ error: err.error ?? { code: 'internal', message: String(e), retryable: false } }), { status: err.status ?? 500 })
    }
  })
  return api
}

/** 讓 handler 丟出操作錯誤。 */
export function opError(status: number, code: string, message: string, retryable = false): never {
  throw { status, error: { code, message, retryable } }
}
