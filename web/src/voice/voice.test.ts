import { describe, expect, it } from 'vitest'
import { clock, decision, estimateSeconds, waveform } from './voice'

describe('通話中用說的核定（L18：聽不清楚再問一次，不猜）', () => {
  it.each([
    ['允許', 'allow'],
    ['好，允許。', 'allow'],
    ['可以', 'allow'],
    ['OK', 'allow'],
    ['拒絕', 'deny'],
    ['不要', 'deny'],
    ['不行喔', 'deny'],
    ['不允許', 'deny'],
    ['不可以', 'deny'],
  ])('「%s」→ %s', (text, want) => expect(decision(text)).toBe(want))
  it.each(['', '嗯……讓我想想', '今天天氣怎麼樣', '允許，不對，拒絕'])('「%s」聽不清楚', (text) => expect(decision(text)).toBeNull())
})

describe('小工具', () => {
  it('錄音時間', () => {
    expect(clock(0)).toBe('0:00')
    expect(clock(7_400)).toBe('0:07')
    expect(clock(192_000)).toBe('3:12')
  })
  it('語音訊息的聲波：同一則固定、高度在 0～1', () => {
    const a = waveform('m1', 32)
    expect(a).toEqual(waveform('m1', 32))
    expect(a).not.toEqual(waveform('m2', 32))
    expect(a.every((v) => v > 0 && v <= 1)).toBe(true)
  })
  it('語音訊息的長度用字數估', () => {
    expect(estimateSeconds(45)).toBe(10)
    expect(estimateSeconds(45, 2)).toBe(5)
    expect(estimateSeconds(1)).toBe(1)
  })
})
