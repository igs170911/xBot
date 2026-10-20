import { describe, expect, it, vi, beforeEach } from 'vitest'

type Report = { kind: string; version?: string; data: string }
const report = vi.fn<(r: Report) => Promise<{ logged: boolean }>>(() => Promise.resolve({ logged: true }))
vi.mock('../api/generated', () => ({ systemClientReport: (i: Report) => report(i) }))

import { errorReportData, startErrorReport } from './errorReport'

describe('網頁錯誤回報', () => {
  beforeEach(() => report.mockClear())

  it('只送錯誤、位置與路徑，截短', () => {
    const d = errorReportData('x'.repeat(500), 'a.js:1:2', 'Error\n    at f', '/bots/b1')
    expect(d).toContain('at=a.js:1:2')
    expect(d).toContain('path=/bots/b1')
    expect(d).toContain('stack=Error at f')
    expect(d.length).toBeLessThan(2000)
  })

  it('例外和沒接住的 Promise 都回報，同一則只送一次，最多 5 則', () => {
    startErrorReport(() => 'v1')
    const fire = (msg: string) => window.dispatchEvent(new ErrorEvent('error', { message: msg, filename: 'app.js', lineno: 3, colno: 4, error: new Error(msg) }))
    fire('壞了')
    fire('壞了')
    const rej = new Event('unhandledrejection') as Event & { reason?: unknown }
    rej.reason = new TypeError('cannot read x')
    window.dispatchEvent(rej)
    expect(report).toHaveBeenCalledTimes(2)
    expect(report.mock.calls[0][0]).toMatchObject({ kind: 'js_error', version: 'v1' })
    for (let i = 0; i < 10; i++) fire('錯 ' + i)
    expect(report).toHaveBeenCalledTimes(5)
  })
})
