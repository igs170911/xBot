import { describe, expect, it } from 'vitest'
import { fetchHealth } from './health'

describe('fetchHealth', () => {
  it('回傳服務狀態', async () => {
    const fake = (async () => new Response(JSON.stringify({ ok: true, version: 'dev' }))) as typeof fetch
    expect(await fetchHealth(fake)).toEqual({ ok: true, version: 'dev' })
  })

  it('服務沒開時回 null', async () => {
    const fake = (async () => {
      throw new Error('ECONNREFUSED')
    }) as typeof fetch
    expect(await fetchHealth(fake)).toBeNull()
  })
})
