import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App'
import type { XBotEvent } from '../api/generated'
import { resetBots } from '../data/bots'
import { dispatchEvent } from '../data/events'
import { resetInbox } from '../data/inbox'
import { fakeApi, opError, type FakeApi } from '../test-api'
import { askFromEvent, askFromItem } from './ask'
import { AskCard } from './AskCard'
import { applyEvent, applySteps, emptyChat } from './assemble'
import { resetChat } from './chatStore'

const ev = (e: Partial<XBotEvent> & { kind: string }): XBotEvent => ({ bot_id: 'b1', thread_id: 't1', task_id: 'k1', at: 1000, ...e })

const needs = (id: number, inbox: string, request: object, extra: object = {}) =>
  ev({ id, kind: 'chat.needs_approval', payload: { type: 'needs_approval', request, inbox_id: inbox, ...extra } })

describe('ask', () => {
  it('needs_approval 轉成卡片：核准只在 Hermes 給 always 時才有一律允許', () => {
    expect(askFromEvent({ inbox_id: 'i', request: { kind: 'approval', prompt: 'rm', choices: ['once', 'session', 'deny'], params: { command: 'rm -rf x' } } })).toMatchObject({
      kind: 'approval',
      command: 'rm -rf x',
      choices: ['once', 'deny'],
    })
    const sec = askFromEvent({ inbox_id: 'i', expires_at: 5, request: { kind: 'secret', method: 'vault.save_login', prompt: '登入' } })
    expect(sec?.fields?.map((f) => f.name)).toEqual(['identifier', 'password'])
    expect(sec?.expiresAt).toBe(5)
    const multi = askFromEvent({ inbox_id: 'i', request: { kind: 'clarify', prompt: 'a\n\nb', params: { questions: [{ question: 'a', choices: ['1'] }, { question: 'b' }] } } })
    expect(multi?.questions).toHaveLength(2)
    expect(askFromItem({ id: 'x', kind: 'secret', body: '金鑰', payload: { method: 'secret' } }).fields?.[0].kind).toBe('password')
  })

  it('chat.answered 收掉提問並留下一行結果；讀回的歷史不會讓已回覆的問題再跳出來', () => {
    let s = applyEvent(emptyChat, ev({ id: 1, kind: 'task.state', payload: { state: 'waiting_approval' } }))
    s = applyEvent(s, needs(2, 'i1', { kind: 'approval', prompt: 'pay' }))
    expect(s.tasks.k1.request?.inboxId).toBe('i1')
    s = applyEvent(s, ev({ id: 3, kind: 'chat.answered', payload: { inbox_id: 'i1', kind: 'approval', resolution: 'once', text: '允許這次' } }))
    expect(s.tasks.k1.request).toBeUndefined()
    expect(s.tasks.k1.answered).toEqual([{ inboxId: 'i1', kind: 'approval', resolution: 'once', text: '允許這次', at: 1000 }])

    // 重新整理後：chat.steps 讀回步驟、提問與回覆（id 都比已處理的小也要補上）
    let r = applyEvent(emptyChat, ev({ id: 50, kind: 'op', payload: {} }))
    const steps = [
      { id: 10, kind: 'chat.tool_call', task_id: 'k1', thread_id: 't1', payload: { tool: { id: 'x', name: 'terminal' } }, at: 1 },
      { id: 11, kind: 'task.state', task_id: 'k1', thread_id: 't1', payload: { state: 'waiting_approval' }, at: 2 },
      { ...needs(12, 'i1', { kind: 'approval', prompt: 'pay' }), id: 12, payload: { inbox_id: 'i1', request: { kind: 'approval', prompt: 'pay' } } },
      { id: 13, kind: 'chat.answered', task_id: 'k1', thread_id: 't1', payload: { inbox_id: 'i1', kind: 'approval', resolution: 'deny', text: '拒絕' }, at: 3 },
      { id: 14, kind: 'task.state', task_id: 'k1', thread_id: 't1', payload: { state: 'done', verdict: 'answer' }, at: 4 },
    ] as Parameters<typeof applySteps>[1]
    r = applySteps(r, steps)
    expect(r.lastId).toBe(50)
    expect(r.tasks.k1.steps).toHaveLength(1)
    expect(r.tasks.k1.request).toBeUndefined()
    expect(r.tasks.k1.answered?.[0].resolution).toBe('deny')
    expect(r.taskThread.k1).toBe('t1')
    // 再讀一次不會重複
    expect(applySteps(r, steps).tasks.k1.steps).toHaveLength(1)
  })
})

describe('核准卡的內容', () => {
  it('先寫白話的說明，原始指令收在「細節」裡（預設收起）', () => {
    render(<AskCard ask={{ inboxId: 'i1', kind: 'approval', prompt: 'pay', command: 'python3 /workspace/pay.py --amount 1280', description: '付款 NT$1,280 給晨光廣告' }} bot={{ name: '帳房' }} />)
    const card = screen.getByTestId('ask-card')
    expect(card.textContent?.indexOf('付款 NT$1,280')).toBeLessThan(card.textContent?.indexOf('細節') ?? -1)
    const details = within(card).getByTestId('ask-details')
    expect(details).not.toHaveAttribute('open')
    expect(within(details).getByText('細節')).toBeInTheDocument()
    expect(within(details).getByText('python3 /workspace/pay.py --amount 1280')).toBeInTheDocument()
  })

  it('沒有說明時直接給看指令（沒有別的可以看）；說明和指令一樣時不重複', () => {
    const { unmount } = render(<AskCard ask={{ inboxId: 'i1', kind: 'approval', prompt: 'rm -rf build', command: 'rm -rf build' }} />)
    expect(screen.getByTestId('ask-card')).toHaveTextContent('rm -rf build')
    expect(screen.queryByTestId('ask-details')).toBeNull()
    unmount()
    render(<AskCard ask={{ inboxId: 'i2', kind: 'approval', prompt: 'Run ls -la', description: '列出資料夾內容' }} />)
    expect(screen.getByTestId('ask-card')).toHaveTextContent('列出資料夾內容')
    expect(screen.queryByTestId('ask-details')).toBeNull()
  })
})

const BOT = { id: 'b1', name: '帳房', title: '', description: '', soul: '', color: 'yellow', shape: 'hexagon', engine: 'hermes', model: '', effort: 'medium', approval_mode: 'ask', archived: false, created_at: 1, updated_at: 1 }
const THREAD = { id: 't1', bot_id: 'b1', title: '付款', pinned: false, archived: false, unread: 0, last_message_at: 1, created_at: 1 }

describe('對話裡回覆 Bot 的提問', () => {
  let api: FakeApi
  beforeEach(() => {
    api = fakeApi({
      'bots.list': () => ({ bots: [BOT] }),
      'chat.list_threads': () => ({ threads: [THREAD] }),
      'chat.read': () => ({ thread: THREAD, messages: [], has_more: false }),
      'chat.steps': () => ({ events: [], has_more: false }),
      'inbox.mark_read': () => ({ marked: 0 }),
    })
    window.history.replaceState(null, '', '/bots/b1/threads/t1')
  })
  afterEach(() => {
    resetBots()
    resetInbox()
    resetChat()
    vi.unstubAllGlobals()
  })

  it('核准卡：允許這次／一律允許／拒絕，送出後收成一行結果；打開對話會標已讀', async () => {
    api.ops['chat.respond'] = () => ({ inbox_id: 'i1', task_id: 'k1', thread_id: 't1', status: 'resolved', resolution: 'always' })
    render(<App />)
    const main = await screen.findByRole('main', { name: '和帳房的對話' })
    await waitFor(() => expect(api.calls.some((c) => c.path === 'inbox.mark_read' && c.input.thread_id === 't1')).toBe(true))
    act(() => {
      dispatchEvent(ev({ id: 1, kind: 'task.state', payload: { state: 'waiting_approval' } }))
      dispatchEvent(needs(2, 'i1', { kind: 'approval', prompt: 'pay', choices: ['once', 'always', 'deny'], params: { command: 'python3 pay.py', description: '付款 NT$1,280' } }))
    })
    const card = within(main).getByTestId('ask-card')
    expect(card).toHaveTextContent('python3 pay.py')
    expect(card).toHaveTextContent('付款 NT$1,280')
    fireEvent.click(within(card).getByRole('button', { name: '一律允許' }))
    await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.respond')?.input).toEqual({ inbox_id: 'i1', choice: 'always' }))
    act(() => dispatchEvent(ev({ id: 3, kind: 'chat.answered', payload: { inbox_id: 'i1', kind: 'approval', resolution: 'always', text: '一律允許' } })))
    expect(within(main).queryByTestId('ask-card')).toBeNull()
    expect(within(main).getByTestId('ask-answered')).toHaveTextContent('一律允許')
  })

  it('密碼卡：值只送給 chat.respond；錯誤顯示在卡片上', async () => {
    let fail = true
    api.ops['chat.respond'] = () => {
      if (fail) {
        fail = false
        opError(409, 'conflict', '這個問題已結束，Bot 沒有在等了')
      }
      return { inbox_id: 'i2', task_id: 'k1', thread_id: 't1', status: 'resolved', resolution: 'provided' }
    }
    render(<App />)
    const main = await screen.findByRole('main', { name: '和帳房的對話' })
    act(() => {
      dispatchEvent(ev({ id: 1, kind: 'task.state', payload: { state: 'waiting_input' } }))
      dispatchEvent(needs(2, 'i2', { kind: 'secret', method: 'secret', prompt: '請提供 API 金鑰', params: { env_var: 'SHOPEE_API_KEY' } }))
    })
    const card = within(main).getByTestId('ask-card')
    expect(card).toHaveTextContent('不會寫進對話紀錄')
    const input = within(card).getByLabelText('SHOPEE_API_KEY')
    expect(input).toHaveAttribute('type', 'password')
    fireEvent.change(input, { target: { value: 'sk-123' } })
    fireEvent.click(within(card).getByRole('button', { name: '送出' }))
    expect(await within(card).findByRole('alert')).toHaveTextContent('這個問題已結束')
    fireEvent.click(within(card).getByRole('button', { name: '送出' }))
    await waitFor(() => expect(api.calls.filter((c) => c.path === 'chat.respond')).toHaveLength(2))
    expect(api.calls.filter((c) => c.path === 'chat.respond')[1].input).toEqual({ inbox_id: 'i2', value: 'sk-123' })
  })

  it('重新整理後用 chat.steps 補回步驟與等你回覆的問題', async () => {
    api.ops['chat.read'] = () => ({
      thread: THREAD,
      messages: [{ id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '付廣告費', task_id: 'k1', created_at: 1, updated_at: 1 }],
      has_more: false,
    })
    api.ops['chat.steps'] = () => ({
      events: [
        { id: 5, kind: 'chat.tool_call', bot_id: 'b1', thread_id: 't1', task_id: 'k1', payload: { tool: { id: 'x', name: 'terminal', context: '比對帳單' } }, at: 2 },
        { id: 6, kind: 'task.state', bot_id: 'b1', thread_id: 't1', task_id: 'k1', payload: { state: 'waiting_approval' }, at: 3 },
        { id: 7, kind: 'chat.needs_approval', bot_id: 'b1', thread_id: 't1', task_id: 'k1', payload: { inbox_id: 'i9', request: { kind: 'approval', prompt: 'pay', choices: ['once', 'deny'] } }, at: 4 },
      ],
      has_more: false,
    })
    render(<App />)
    const main = await screen.findByRole('main', { name: '和帳房的對話' })
    const card = await within(main).findByTestId('ask-card')
    expect(within(card).queryByRole('button', { name: '一律允許' })).toBeNull()
    // The main point: chat.steps reconstructed the approval request
  })
})
