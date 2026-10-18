import { describe, expect, it } from 'vitest'
import type { ModelsGetOutput } from '../api/generated'
import { emptyChat, type ChatMsg, type ChatState } from './assemble'
import { clockLabel, isLong, modelLabel, parseModel, relTime, taskTitle } from './format'

const models: ModelsGetOutput = {
  providers: [
    { id: 'openai', base_url: 'https://api.openai.com/v1', api_key_set: true },
    { id: 'openrouter', base_url: 'https://openrouter.ai/api/v1', api_key_set: false },
  ],
  default: { provider: 'openai', model: 'gpt-4.1-mini' },
  ready: true,
}

const msg = (p: Partial<ChatMsg>): ChatMsg => ({ id: 'm', seq: 1, sender: 'owner', kind: 'text', content: '', streaming: false, createdAt: 0, updatedAt: 0, live: false, ...p })

describe('format', () => {
  it('parseModel：空白是預設模型；有已知供應商前綴就拆開；其他當成預設供應商的模型', () => {
    expect(parseModel('', models)).toEqual({ provider: 'openai', model: 'gpt-4.1-mini' })
    expect(parseModel('openrouter/anthropic/claude', models)).toEqual({ provider: 'openrouter', model: 'anthropic/claude' })
    expect(parseModel('gpt-4.1', models)).toEqual({ provider: 'openai', model: 'gpt-4.1' })
    expect(parseModel('meta/llama', models)).toEqual({ provider: 'openai', model: 'meta/llama' })
  })

  it('modelLabel', () => {
    const bot = { model: '' } as Parameters<typeof modelLabel>[0]
    expect(modelLabel(bot, models)).toBe('gpt-4.1-mini')
    expect(modelLabel({ ...bot, model: 'openrouter/x' }, models)).toBe('x')
    expect(modelLabel(bot, { providers: [], ready: false })).toBe('還沒設定模型')
  })

  it('clockLabel／relTime', () => {
    const at = new Date(2026, 9, 18, 14, 5).getTime()
    expect(clockLabel(at)).toBe('下午 2:05')
    expect(clockLabel(new Date(2026, 9, 18, 0, 7).getTime())).toBe('上午 12:07')
    expect(relTime(at, at + 30_000)).toBe('剛剛')
    expect(relTime(at, at + 12 * 60_000)).toBe('12 分鐘前')
    expect(relTime(at, at + 3 * 3600_000)).toBe('今天 下午 2:05')
  })

  it('isLong：你的長訊息（字數或行數）、Bot 的超長回覆；串流中不收', () => {
    expect(isLong(msg({ content: 'a'.repeat(801) }))).toBe(true)
    expect(isLong(msg({ content: 'a\n'.repeat(15) }))).toBe(true)
    expect(isLong(msg({ content: '短' }))).toBe(false)
    expect(isLong(msg({ sender: 'bot:b', content: 'a'.repeat(5000) }))).toBe(false)
    expect(isLong(msg({ sender: 'bot:b', content: 'a'.repeat(7000) }))).toBe(true)
    expect(isLong(msg({ sender: 'bot:b', content: 'a'.repeat(7000), streaming: true }))).toBe(false)
  })

  it('taskTitle：交代的那句話第一行', () => {
    const s: ChatState = { ...emptyChat, threads: { t: { id: 't', loaded: true, hasMore: false, messages: [msg({ taskId: 'k', content: '幫我比價\n細節…' })] } } }
    expect(taskTitle(s, 't', 'k')).toBe('幫我比價')
    expect(taskTitle(s, 't', 'x')).toBe('這件工作')
  })
})
