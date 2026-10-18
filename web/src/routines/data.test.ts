import { describe, expect, it } from 'vitest'
import { clockText, routineStatus, whenText, type Routine } from './data'

const base: Routine = {
  id: 'r',
  bot_id: 'b',
  title: 't',
  prompt: 'p',
  schedule: { kind: 'daily', time: '09:00' },
  when: '每天上午 9:00',
  jitter_minutes: 0,
  notify_on_failure: true,
  status: 'active',
  running: false,
  created_at: 0,
  updated_at: 0,
}

describe('定時任務的寫法', () => {
  it('時間不出現 AM/PM', () => {
    expect(clockText(new Date(2026, 9, 13, 9, 0))).toBe('上午 9:00')
    expect(clockText(new Date(2026, 9, 13, 12, 30))).toBe('中午 12:30')
    expect(clockText(new Date(2026, 9, 13, 15, 5))).toBe('下午 3:05')
    expect(clockText(new Date(2026, 9, 13, 21, 0))).toBe('晚上 9:00')
    expect(clockText(new Date(2026, 9, 13, 0, 10))).toBe('凌晨 12:10')
  })
  it('今天、明天、其他日期', () => {
    const now = new Date(2026, 9, 13, 10, 0)
    expect(whenText(new Date(2026, 9, 13, 18, 0).getTime(), now)).toBe('今天晚上 6:00')
    expect(whenText(new Date(2026, 9, 14, 9, 0).getTime(), now)).toBe('明天上午 9:00')
    expect(whenText(new Date(2026, 9, 20, 9, 0).getTime(), now)).toBe('10 月 20 日（二） 上午 9:00')
  })
  it('狀態膠囊（H03）', () => {
    expect(routineStatus(base).label).toBe('啟用中')
    expect(routineStatus({ ...base, running: true, goal: { done_when: 'x', max_rounds: 10, rounds: 5, progress_file: 'f' } }).label).toBe('第 5 輪執行中…')
    expect(routineStatus({ ...base, failures: 2, last_error: 'x' }).key).toBe('problem')
    expect(routineStatus({ ...base, status: 'paused' }).label).toBe('已暫停')
  })
})
