import { describe, expect, it } from 'vitest'
import { fetchHealth } from './health'

describe('fetchHealth', () => {
  it('呼叫 system.ping 並回傳服務狀態', async () => {
    let called = ''
    const fake = (async (url: string, init: RequestInit) => {
      called = `${init.method} ${url}`
      return new Response(
        JSON.stringify({ op: 'xbot.system.ping', output: { pong: true, api: 'xbot.v1', build: 'dev', caller: 'owner' } }),
      )
    }) as typeof fetch
    expect(await fetchHealth(fake)).toEqual({ ok: true, version: 'dev' })
    expect(called).toBe('POST /api/v1/system.ping')
  })

  it('沒登入時回 null', async () => {
    const fake = (async () =>
      new Response(JSON.stringify({ error: { code: 'forbidden', message: '請先登入', retryable: false } }), {
        status: 401,
      })) as typeof fetch
    expect(await fetchHealth(fake)).toBeNull()
  })

  it('服務沒開時回 null', async () => {
    const fake = (async () => {
      throw new Error('ECONNREFUSED')
    }) as typeof fetch
    expect(await fetchHealth(fake)).toBeNull()
  })
})
