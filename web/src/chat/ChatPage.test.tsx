import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import App from '../App'
import type { XBotEvent } from '../api/generated'
import { resetBots } from '../data/bots'
import { dispatchEvent } from '../data/events'
import { resetInbox } from '../data/inbox'
import { fakeApi, opError, type FakeApi } from '../test-api'
import { registerCard } from './cards'
import { resetChat } from './chatStore'

const BOT = {
  id: 'b1',
  name: '小秘',
  title: '幕僚長',
  description: '',
  soul: '',
  color: 'blue',
  shape: 'cursor',
  engine: 'hermes',
  model: '',
  effort: 'medium',
  approval_mode: 'ask',
  archived: false,
  created_at: 1,
  updated_at: 1,
}
const THREAD = { id: 't1', bot_id: 'b1', title: '比價', pinned: false, archived: false, unread: 0, last_message_at: 1, created_at: 1 }

let api: FakeApi
let messages: unknown[]
beforeEach(() => {
  messages = []
  api = fakeApi({
    'bots.list': () => ({ bots: [BOT] }),
    'chat.list_threads': () => ({ threads: [THREAD] }),
    'chat.read': () => ({ thread: THREAD, messages, has_more: false }),
    'chat.send': () => ({ bot_id: 'b1', thread_id: 't1', task_id: 'k1', message_id: 'm1' }),
    'chat.stop': () => ({ stopped: true, task_id: 'k1' }),
  })
  window.history.replaceState(null, '', '/bots/b1')
})
afterEach(() => {
  resetBots()
  resetInbox()
  resetChat()
  vi.unstubAllGlobals()
})

const ev = (e: Partial<XBotEvent> & { kind: string }): XBotEvent => ({ bot_id: 'b1', thread_id: 't1', task_id: 'k1', at: Date.now(), ...e })
const emit = (...es: XBotEvent[]) => act(() => es.forEach(dispatchEvent))
const storeMsg = (id: string, seq: number, sender: string, meta: object, content = '') => ({
  id,
  seq,
  scope: 'thread',
  scope_id: 't1',
  sender,
  kind: 'text',
  content,
  meta,
  created_at: Date.now(),
  updated_at: Date.now(),
})

it('送出訊息 → 串流回覆一段段出現 → 工具步驟 → 完成後摺疊成「工作了 N · M 步」', async () => {
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  await waitFor(() => expect(window.location.pathname).toBe('/bots/b1/threads/t1'))
  const input = within(main).getByRole('textbox')
  fireEvent.change(input, { target: { value: '幫我比價' } })
  fireEvent.keyDown(input, { key: 'Enter' })
  await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.send')?.input).toEqual({ bot_id: 'b1', text: '幫我比價', thread_id: 't1' }))

  emit(
    ev({ id: 1, kind: 'message.created', payload: storeMsg('m1', 1, 'owner', { task_id: 'k1' }, '幫我比價') }),
    ev({ id: 2, kind: 'task.state', payload: { state: 'waiting_box' } }),
  )
  expect(within(main).getByText('幫我比價')).toBeInTheDocument()
  expect(within(main).getByText('電腦開機中')).toBeInTheDocument()
  expect(within(main).getByTestId('bot-status')).toHaveTextContent('開機中')
  // 工作中：停止鈕
  expect(within(main).getByRole('button', { name: '停止小秘' })).toBeInTheDocument()

  emit(
    ev({ id: 3, kind: 'message.created', payload: storeMsg('m2', 2, 'bot:b1', { task_id: 'k1', streaming: true }) }),
    ev({ id: 4, kind: 'task.state', payload: { state: 'running' } }),
    ev({ kind: 'chat.thinking', payload: { message_id: 'm2', text: '…' } }),
  )
  expect(within(main).getByText('思考中')).toBeInTheDocument()
  emit(ev({ id: 5, kind: 'chat.tool_call', payload: { type: 'tool_call', tool: { id: 'x1', name: 'browser_open', context: '打開蝦皮' } } }))
  expect(within(main).getByText('browser_open')).toBeInTheDocument()
  expect(within(main).getByText('打開蝦皮')).toBeInTheDocument()
  emit(ev({ id: 6, kind: 'chat.tool_result', payload: { type: 'tool_result', tool: { id: 'x1', ok: true } } }), ev({ kind: 'chat.text', payload: { message_id: 'm2', text: '三家' } }))
  expect(within(main).getByTestId('msg-bot')).toHaveTextContent('三家')
  emit(ev({ kind: 'chat.text', payload: { message_id: 'm2', text: '都比完了。' } }))
  expect(within(main).getByTestId('msg-bot')).toHaveTextContent('三家都比完了。')

  emit(ev({ id: 7, kind: 'task.state', payload: { state: 'done', verdict: 'answer' } }), ev({ id: 8, kind: 'chat.done', payload: { message_id: 'm2', status: 'complete', verdict: 'answer', text: '三家都比完了。' } }))
  expect(within(main).queryByRole('button', { name: '停止小秘' })).toBeNull()
  const summary = within(main).getByRole('button', { name: /工作了 .* · 1 步/ })
  expect(summary).toHaveAttribute('aria-expanded', 'false')
  expect(within(main).queryByText('browser_open')).toBeNull()
  fireEvent.click(summary)
  expect(within(main).getByText('browser_open')).toBeInTheDocument()
  expect(within(main).getByTestId('bot-status')).toHaveTextContent('待命')
})

it('停止：呼叫 chat.stop，輸入框變成正在停止；收到 cancelled 後顯示已停止', async () => {
  messages = [
    { id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '整理出貨單', task_id: 'k1', created_at: Date.now(), updated_at: Date.now() },
    { id: 'm2', seq: 2, sender: 'bot:b1', kind: 'text', content: '正在整理', task_id: 'k1', streaming: true, created_at: Date.now(), updated_at: Date.now() },
  ]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  fireEvent.click(await within(main).findByRole('button', { name: '停止小秘' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.stop')?.input).toEqual({ bot_id: 'b1', thread_id: 't1' }))
  expect(await within(main).findByRole('button', { name: '正在停止' })).toBeDisabled()
  emit(ev({ id: 1, kind: 'task.state', payload: { state: 'stopping' } }), ev({ id: 2, kind: 'task.state', payload: { state: 'done', verdict: 'cancelled' } }), ev({ id: 3, kind: 'chat.done', payload: { message_id: 'm2', status: 'interrupted', verdict: 'cancelled', text: '正在整理' } }))
  expect(within(main).getByText('已停止')).toBeInTheDocument()
  expect(within(main).getByRole('button', { name: '送出' })).toBeInTheDocument()
})

it('新 Bot 沒有對話串：顯示空狀態（C03），送出後開新的對話串', async () => {
  api.ops['chat.list_threads'] = () => ({ threads: [] })
  api.ops['chat.send'] = () => ({ bot_id: 'b1', thread_id: 't9', task_id: 'k9', message_id: 'm9' })
  render(<App />)
  const empty = await screen.findByTestId('chat-empty')
  expect(empty).toHaveTextContent('小秘')
  expect(empty).toHaveTextContent('先問我')
  const input = screen.getByRole('textbox')
  expect(input).toHaveAttribute('placeholder', '交代小秘…')
  fireEvent.change(input, { target: { value: '嗨' } })
  fireEvent.keyDown(input, { key: 'Enter' })
  await waitFor(() => expect(window.location.pathname).toBe('/bots/b1/threads/t9'))
  expect(api.calls.find((c) => c.path === 'chat.send')?.input).toEqual({ bot_id: 'b1', text: '嗨' })
})

it('還沒設定模型：顯示提示卡，填好模型設定後消失', async () => {
  let ready = false
  api.ops['models.get'] = () => (ready ? { providers: [{ id: 'openai', base_url: 'https://x/v1', api_key_set: true }], default: { provider: 'openai', model: 'gpt-x' }, ready } : { providers: [], ready })
  api.ops['models.set_provider'] = (input) => {
    ready = true
    return { provider: { id: input.id, base_url: input.base_url, api_key_set: true }, default: { provider: input.id, model: input.default_model } }
  }
  render(<App />)
  const card = await screen.findByRole('region', { name: '還沒設定模型' })
  fireEvent.click(within(card).getByRole('button', { name: '設定模型' }))
  const dlg = await screen.findByRole('dialog', { name: '設定模型' })
  fireEvent.change(within(dlg).getByLabelText('金鑰'), { target: { value: 'sk-test' } })
  fireEvent.change(within(dlg).getByLabelText('預設模型'), { target: { value: 'gpt-x' } })
  fireEvent.click(within(dlg).getByRole('button', { name: '儲存' }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: '設定模型' })).toBeNull())
  expect(api.calls.find((c) => c.path === 'models.set_provider')?.input).toEqual({ id: 'openai', base_url: 'https://api.openai.com/v1', api_key: 'sk-test', default_model: 'gpt-x' })
  expect(screen.queryByRole('region', { name: '還沒設定模型' })).toBeNull()
})

it('Bot 在忙別的事：送出時提示，可以直接停止它', async () => {
  api.ops['chat.send'] = () => opError(409, 'conflict', '這個 Bot 正在忙另一件事', true)
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  const input = within(main).getByRole('textbox')
  fireEvent.change(input, { target: { value: '再做一件' } })
  fireEvent.keyDown(input, { key: 'Enter' })
  const alert = await within(main).findByRole('alert')
  expect(alert).toHaveTextContent('小秘正在忙另一件事')
  expect(input).toHaveValue('再做一件')
  fireEvent.click(within(alert).getByRole('button', { name: '停止它' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.stop')?.input).toEqual({ bot_id: 'b1' }))
})

it('Bot 回報錯誤：顯示工作未完成與原因', async () => {
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  await waitFor(() => expect(window.location.pathname).toBe('/bots/b1/threads/t1'))
  emit(
    ev({ id: 1, kind: 'message.created', payload: storeMsg('m1', 1, 'owner', { task_id: 'k1' }, '嗨') }),
    ev({ id: 2, kind: 'task.state', payload: { state: 'waiting_box' } }),
    ev({ id: 3, kind: 'chat.error', payload: { type: 'error', text: '開不了機' } }),
    ev({ id: 4, kind: 'message.created', payload: storeMsg('m2', 2, 'bot:b1', { task_id: 'k1', status: 'error' }, '工作未完成：開不了機') }),
    ev({ id: 5, kind: 'task.state', payload: { state: 'done', verdict: 'failed' } }),
    ev({ id: 6, kind: 'chat.done', payload: { message_id: 'm2', status: 'error', verdict: 'failed', text: '工作未完成：開不了機' } }),
  )
  expect(within(main).getByTestId('msg-bot')).toHaveTextContent('工作未完成：開不了機')
  expect(within(main).getByText('工作未完成')).toBeInTheDocument()
})

it('卡片登錄表：kind 不是 text 的訊息用登記的元件畫；沒登記的退回文字泡泡', async () => {
  const off = registerCard('demo_card', ({ msg }) => <section aria-label="示範卡片">{msg.content}</section>)
  messages = [
    { id: 'm1', seq: 1, sender: 'bot:b1', kind: 'demo_card', content: '卡片內容', created_at: Date.now(), updated_at: Date.now() },
    { id: 'm2', seq: 2, sender: 'bot:b1', kind: 'unknown_kind', content: '照樣看得到', status: 'complete', created_at: Date.now(), updated_at: Date.now() },
  ]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  expect(await screen.findByRole('region', { name: '示範卡片' })).toHaveTextContent('卡片內容')
  expect(screen.getByTestId('msg-bot')).toHaveTextContent('照樣看得到')
  off()
})

it('Markdown 回覆與重試：工作未完成的最後一則可以再送一次同樣的話', async () => {
  messages = [
    { id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '匯出訂單', task_id: 'k1', created_at: Date.now(), updated_at: Date.now() },
    { id: 'm2', seq: 2, sender: 'bot:b1', kind: 'text', content: '**登入過期**了', task_id: 'k1', status: 'error', created_at: Date.now(), updated_at: Date.now() },
  ]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const bubble = await screen.findByTestId('msg-bot')
  expect(bubble.querySelector('strong')).toHaveTextContent('登入過期')
  fireEvent.click(screen.getByRole('button', { name: '再試一次' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.send')?.input).toEqual({ bot_id: 'b1', text: '匯出訂單', thread_id: 't1' }))
})

it('X04：判斷不是「已完成」才在回覆下面標出來；不對？選正確的一種會記下來；自動繼續是一行系統紀錄', async () => {
  api.ops['judge.correct'] = (input) => ({ correction: { ...input, judged: 'done', corrected: input.verdict } })
  messages = [
    { id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '查價格', task_id: 'k1', created_at: Date.now(), updated_at: Date.now() },
    {
      id: 'm2',
      seq: 2,
      sender: 'bot:b1',
      kind: 'text',
      content: '我來查一下',
      task_id: 'k1',
      status: 'complete',
      judge: { verdict: 'only_promised', label: '只說要做', reason: '只回了一句要去做的話', by: 'rules', continued_by: 'k2' },
      created_at: Date.now(),
      updated_at: Date.now(),
    },
    { id: 'm3', seq: 3, sender: 'system', kind: 'activity', activity: 'auto_continue', content: '自動繼續：剛才只說要做，現在真的去做', task_id: 'k2', created_at: Date.now(), updated_at: Date.now() },
  ]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  const tag = await within(main).findByTestId('judge-tag')
  expect(tag).toHaveTextContent('只說要做')
  expect(tag).not.toHaveTextContent('規則')
  expect(within(main).getByTestId('activity-line')).toHaveTextContent('自動繼續：剛才只說要做，現在真的去做')
  // 新的一輪做完：chat.done 帶判斷。
  emit(
    ev({ id: 1, task_id: 'k2', kind: 'message.created', payload: storeMsg('m4', 4, 'bot:b1', { task_id: 'k2', streaming: true }) }),
    ev({ id: 2, task_id: 'k2', kind: 'chat.done', payload: { message_id: 'm4', status: 'complete', verdict: 'answer', text: 'A 店 120 元。', judge: { verdict: 'done', label: '已完成', reason: '', by: 'rules' } } }),
  )
  // 「已完成」是常態，不標（避免每則回覆下面都一行雜訊）。
  await within(main).findByText('A 店 120 元。')
  expect(within(main).getAllByTestId('judge-tag')).toHaveLength(1)
  fireEvent.click(within(tag).getByRole('button', { name: '不對？' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: '卡住了' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'judge.correct')?.input).toEqual({ task_id: 'k1', verdict: 'stuck' }))
  await waitFor(() => expect(within(main).getAllByTestId('judge-tag')[0]).toHaveTextContent('你改成卡住了'))
})

it('刪除對話串：確認後呼叫 chat.delete_thread，回到新對話串', async () => {
  api.ops['chat.delete_thread'] = () => ({ deleted: true, bot_id: 'b1' })
  messages = [{ id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '查價格', created_at: Date.now(), updated_at: Date.now() }]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  fireEvent.click(within(main).getByRole('button', { name: '更多' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: '刪除這個對話串' }))
  fireEvent.click(await screen.findByRole('button', { name: '刪除' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.delete_thread')?.input).toEqual({ thread_id: 't1' }))
  await waitFor(() => expect(window.location.pathname).toBe('/bots/b1/threads/new'))
})
