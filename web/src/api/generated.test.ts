import { describe, expect, it } from 'vitest'
import { ApiError, invoke, subscribeEvents, type XBotEvent } from './generated'

describe('invoke', () => {
  it('帶 request id 與 dry run 標頭，回傳服務給的 request id', async () => {
    let headers: Record<string, string> = {}
    const fake = (async (_url: string, init: RequestInit) => {
      headers = init.headers as Record<string, string>
      return new Response(JSON.stringify({ op: 'xbot.system.ping', output: { pong: true }, dry_run: true }), {
        headers: { 'X-Request-Id': 'r1' },
      })
    }) as typeof fetch
    const res = await invoke('xbot.system.ping', {}, { fetch: fake, requestId: 'r1', dryRun: true })
    expect(headers['X-Request-Id']).toBe('r1')
    expect(headers['X-Dry-Run']).toBe('1')
    expect(res.dry_run).toBe(true)
    expect(res.requestId).toBe('r1')
  })

  it('等核准時丟出 ApiError，帶核准 id', async () => {
    const fake = (async () =>
      new Response(
        JSON.stringify({ error: { code: 'pending_approval', message: '等你允許', retryable: false, needs_approval_id: 'a1' } }),
        { status: 202 },
      )) as typeof fetch
    const err = await invoke('xbot.system.ping', {}, { fetch: fake }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe('pending_approval')
    expect((err as ApiError).needsApprovalId).toBe('a1')
    expect((err as ApiError).status).toBe(202)
  })
})

class FakeEventSource {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  static all: FakeEventSource[] = []
  readyState = FakeEventSource.CONNECTING
  onopen: (() => void) | null = null
  onmessage: ((m: MessageEvent<string>) => void) | null = null
  onerror: (() => void) | null = null
  url: string
  constructor(url: string) {
    this.url = url
    FakeEventSource.all.push(this)
  }
  close() {
    this.readyState = FakeEventSource.CLOSED
  }
  emit(e: Partial<XBotEvent>) {
    this.onmessage?.({ data: JSON.stringify(e) } as MessageEvent<string>)
  }
}

describe('subscribeEvents', () => {
  it('連線被關掉時，從最後收到的事件之後重連', async () => {
    FakeEventSource.all = []
    const got: number[] = []
    const stop = subscribeEvents((e) => got.push(e.id ?? 0), {
      EventSource: FakeEventSource as unknown as typeof EventSource,
    })
    const first = FakeEventSource.all[0]
    expect(first.url).toBe('/api/v1/events')
    first.emit({ id: 7, kind: 'op' })
    first.emit({ kind: 'delta' })
    first.readyState = FakeEventSource.CLOSED
    first.onerror?.()
    await new Promise((r) => setTimeout(r, 1100))
    expect(FakeEventSource.all).toHaveLength(2)
    expect(FakeEventSource.all[1].url).toBe('/api/v1/events?last_event_id=7')
    expect(got).toEqual([7, 0])
    stop()
    expect(FakeEventSource.all[1].readyState).toBe(FakeEventSource.CLOSED)
  })
})
