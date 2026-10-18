import { describe, expect, it } from 'vitest'
import { mentionQuery } from '../chat/mentionQuery'
import { rowTime } from './format'
import { backFallback, isTabPath, ownsPhoneHeader } from './nav'
import { connectionKind, urlBase64ToBytes } from './pwa'

describe('手機導覽（W31）', () => {
  it('分頁首頁：待我處理、對話、辦公室、行事曆', () => {
    expect(['/', '/chats', '/office', '/calendar'].every(isTabPath)).toBe(true)
    expect(isTabPath('/bots/b1')).toBe(false)
  })
  it('對話、群組、設定自己畫頁首；其他頁用共用返回列', () => {
    expect(ownsPhoneHeader('/bots/b1/threads/t1')).toBe(true)
    expect(ownsPhoneHeader('/rooms/r1')).toBe(true)
    expect(ownsPhoneHeader('/settings/usage')).toBe(true)
    expect(ownsPhoneHeader('/teams')).toBe(false)
  })
  it('直接打開時返回去哪裡', () => {
    expect(backFallback('/bots/b1/files')).toBe('/bots/b1')
    expect(backFallback('/settings/usage')).toBe('/settings')
    expect(backFallback('/teams')).toBe('/chats')
  })
})

describe('L01 列上的時間', () => {
  const now = new Date(2026, 9, 17, 15, 10).getTime()
  it('今天顯示上午／下午時間，昨天、這週、更早', () => {
    expect(rowTime(new Date(2026, 9, 17, 15, 2).getTime(), now)).toBe('下午 3:02')
    expect(rowTime(new Date(2026, 9, 17, 10, 42).getTime(), now)).toBe('上午 10:42')
    expect(rowTime(new Date(2026, 9, 16, 9, 0).getTime(), now)).toBe('昨天')
    expect(rowTime(new Date(2026, 9, 12, 9, 0).getTime(), now)).toBe('週一')
    expect(rowTime(new Date(2026, 8, 1, 9, 0).getTime(), now)).toBe('9/1')
    expect(rowTime(0, now)).toBe('')
  })
})

describe('L20 @ 提到', () => {
  it('打到「@…」才叫出選單', () => {
    expect(mentionQuery('也請 @')).toBe('')
    expect(mentionQuery('也請 @阿')).toBe('阿')
    expect(mentionQuery('@小編')).toBe('小編')
    expect(mentionQuery('寄信到 a@b')).toBeNull()
    expect(mentionQuery('@阿哲 好')).toBeNull()
  })
})

describe('PWA', () => {
  it('VAPID 公鑰 base64url 轉成位元組', () => {
    expect([...urlBase64ToBytes('AQID_-8')]).toEqual([1, 2, 3, 255, 239])
  })
  it('連線方式：Tailscale、同一個 Wi-Fi、這台電腦', () => {
    expect(connectionKind('xbot.tail3a9c.ts.net')).toBe('Tailscale')
    expect(connectionKind('100.101.2.3')).toBe('Tailscale')
    expect(connectionKind('192.168.1.20')).toBe('同一個 Wi-Fi')
    expect(connectionKind('localhost')).toBe('這台電腦')
    expect(connectionKind('xbot.example.com')).toBe('HTTPS')
  })
})
