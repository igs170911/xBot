import { act, fireEvent, render, screen, screen as screen_, waitFor, within } from '@testing-library/react'
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
  // waiting_box state - no bot message yet, so no WorkSteps visible
  expect(within(main).getByTestId('bot-status')).toHaveTextContent('開機中')
  // 工作中：停止鈕
  expect(within(main).getByRole('button', { name: '停止小秘' })).toBeInTheDocument()

  emit(
    ev({ id: 3, kind: 'message.created', payload: storeMsg('m2', 2, 'bot:b1', { task_id: 'k1', streaming: true }) }),
    ev({ id: 4, kind: 'task.state', payload: { state: 'running' } }),
    ev({ kind: 'chat.thinking', payload: { message_id: 'm2', text: '…' } }),
  )
  // Bot message exists now, WorkSteps shows the active state
  await waitFor(() => expect(within(main).queryByText('工作中…')).toBeInTheDocument())
  emit(ev({ id: 5, kind: 'chat.tool_call', payload: { type: 'tool_call', tool: { id: 'x1', name: 'browser_open', context: '打開蝦皮' } } }))
  expect(within(main).getByText('browser_open')).toBeInTheDocument()
  expect(within(main).getByText('打開蝦皮')).toBeInTheDocument()
  emit(ev({ id: 6, kind: 'chat.tool_result', payload: { type: 'tool_result', tool: { id: 'x1', ok: true } } }), ev({ kind: 'chat.text', payload: { message_id: 'm2', text: '三家' } }))
  expect(within(main).getByTestId('msg-bot')).toHaveTextContent('三家')
  emit(ev({ kind: 'chat.text', payload: { message_id: 'm2', text: '都比完了。' } }))
  expect(within(main).getByTestId('msg-bot')).toHaveTextContent('三家都比完了。')

  emit(ev({ id: 7, kind: 'task.state', payload: { state: 'done', verdict: 'answer' } }), ev({ id: 8, kind: 'chat.done', payload: { message_id: 'm2', status: 'complete', verdict: 'answer', text: '三家都比完了。' } }))
  expect(within(main).queryByRole('button', { name: '停止小秘' })).toBeNull()
  // Find work summary (uses details/summary, not button)
  await waitFor(() => expect(within(main).getByText(/工作了/)).toBeInTheDocument())
  const summary = within(main).getByText(/工作了/).closest('summary')
  expect(summary).toHaveAttribute('aria-expanded')
  expect(within(main).queryByText('browser_open')).toBeNull()
  if (summary) fireEvent.click(summary)
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

it('服務重開時做到一半被中斷：顯示「做到一半被中斷了」，按「接著做」呼叫 chat.resume；按過後按鈕收掉', async () => {
  messages = [
    { id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '整理出貨單', task_id: 'k1', created_at: Date.now(), updated_at: Date.now() },
    { id: 'm2', seq: 2, sender: 'bot:b1', kind: 'text', content: '整理到一半', task_id: 'k1', status: 'interrupted', restart: true, created_at: Date.now(), updated_at: Date.now() },
  ]
  api.ops['chat.resume'] = () => ({ bot_id: 'b1', thread_id: 't1', task_id: 'k2', message_id: 'm3' })
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  const row = await within(main).findByTestId('outcome')
  expect(row).toHaveTextContent('做到一半被中斷了')
  expect(row).toHaveTextContent('服務重開過，檔案都還在')
  expect(within(main).queryByText('已停止')).toBeNull()
  fireEvent.click(within(row).getByRole('button', { name: '接著做' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.resume')?.input).toEqual({ task_id: 'k1' }))

  // 服務記下已經接著做了：按鈕收掉，你的「接著做」和新的工作接在後面
  emit(
    ev({ id: 1, kind: 'message.created', payload: storeMsg('m2', 2, 'bot:b1', { task_id: 'k1', status: 'interrupted', restart: true, resumed: true }, '整理到一半') }),
    ev({ id: 2, kind: 'message.created', task_id: 'k2', payload: storeMsg('m3', 3, 'owner', { task_id: 'k2' }, '接著做') }),
  )
  expect(within(main).getByTestId('outcome')).toHaveTextContent('已經接著做')
  expect(within(main).queryByRole('button', { name: '接著做' })).toBeNull()
})

it('服務重開後系統自動接著做：寫到一半的回覆照常顯示，接著一行「服務剛重開，接著做」，沒有中斷的提示', async () => {
  messages = [
    { id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '整理出貨單', task_id: 'k1', created_at: Date.now(), updated_at: Date.now() },
    { id: 'm2', seq: 2, sender: 'bot:b1', kind: 'text', content: '先整理了三筆', task_id: 'k1', restart: true, created_at: Date.now(), updated_at: Date.now() },
    { id: 'm3', seq: 3, sender: 'system', kind: 'activity', content: '服務剛重開，接著做', task_id: 'k1', activity: 'restart_resume', created_at: Date.now(), updated_at: Date.now() },
    { id: 'm4', seq: 4, sender: 'bot:b1', kind: 'text', content: '', task_id: 'k1', streaming: true, created_at: Date.now(), updated_at: Date.now() },
  ]
  api.ops['chat.read'] = () => ({ thread: THREAD, messages, has_more: false, tasks: { k1: 'running' } })
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  expect(await within(main).findByText('先整理了三筆')).toBeInTheDocument()
  expect(within(main).getByTestId('activity-line')).toHaveTextContent('服務剛重開，接著做')
  expect(within(main).queryByTestId('outcome')).toBeNull()
  expect(within(main).queryByRole('button', { name: '接著做' })).toBeNull()
  // 還在做：停止鈕
  expect(within(main).getByRole('button', { name: '停止小秘' })).toBeInTheDocument()
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

it('Bot 的回覆照段落拆成幾個泡泡（像傳訊息）；複製、朗讀還是整則一份', async () => {
  messages = [
    { id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '比價', task_id: 'k1', created_at: Date.now(), updated_at: Date.now() },
    { id: 'm2', seq: 2, sender: 'bot:b1', kind: 'text', content: '比價表做好了。\n\nA 店最便宜。\n\n要調價嗎？', task_id: 'k1', created_at: Date.now(), updated_at: Date.now() },
  ]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const msg = await screen.findByTestId('msg-bot')
  expect(within(msg).getAllByTestId('bot-bubble').map((b) => b.textContent)).toEqual(['比價表做好了。', 'A 店最便宜。', '要調價嗎？'])
  expect(screen.getAllByRole('toolbar', { name: '訊息動作' })).toHaveLength(1)
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

it('打開聊完的對話串就標成看過了（聊天清單的藍點消失）；看著時 Bot 回的也算', async () => {
  api.ops['chat.list_threads'] = () => ({ threads: [{ ...THREAD, unread: 2 }] })
  api.ops['chat.mark_read'] = () => ({ marked: true })
  messages = [{ id: 'm1', seq: 1, sender: 'bot:b1', kind: 'text', content: '好了', created_at: Date.now(), updated_at: Date.now() }]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  await screen.findByRole('main', { name: '和小秘的對話' })
  await waitFor(() => expect(api.calls.filter((c) => c.path === 'chat.mark_read').map((c) => c.input)).toEqual([{ thread_id: 't1' }]))
  emit(ev({ id: 9, kind: 'message.created', payload: storeMsg('m2', 2, 'bot:b1', {}, '還有一件') }))
  await waitFor(() => expect(api.calls.filter((c) => c.path === 'chat.mark_read')).toHaveLength(2))
})

it('「想先請它做什麼？」只在第一次和這個 Bot 聊時出現；之後開新對話只打招呼', async () => {
  api.ops['chat.list_threads'] = () => ({ threads: [] })
  window.history.replaceState(null, '', '/bots/b1/threads/new')
  const first = render(<App />)
  expect(await screen.findByText(/想先請小秘做什麼/)).toBeInTheDocument()
  first.unmount()
  resetChat()
  api.ops['chat.list_threads'] = () => ({ threads: [THREAD] })
  window.history.replaceState(null, '', '/bots/b1/threads/new')
  render(<App />)
  expect(await screen.findByText(/嗨，我是小秘/)).toBeInTheDocument()
  expect(screen.queryByText(/想先請小秘做什麼/)).toBeNull()
})

it('刪除對話串：確認後呼叫 chat.delete_thread，換到最近的另一條（沒有了才開新的）', async () => {
  api.ops['chat.delete_thread'] = () => ({ deleted: true, bot_id: 'b1' })
  messages = [{ id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '查價格', created_at: Date.now(), updated_at: Date.now() }]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  fireEvent.click(within(main).getByRole('button', { name: '更多' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: '刪除這個對話串' }))
  expect(await screen.findByRole('alertdialog', { name: '刪除「比價」？' })).toBeInTheDocument()
  fireEvent.click(await screen.findByRole('button', { name: '刪除' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.delete_thread')?.input).toEqual({ thread_id: 't1' }))
  await waitFor(() => expect(window.location.pathname).toBe('/bots/b1/threads/new'))
})

it('對話串選單：每一條都能直接刪，不用先切過去；刪別條時留在目前這條', async () => {
  const other = { ...THREAD, id: 't2', title: '舊的報價' }
  let list = [THREAD, other]
  api.ops['chat.list_threads'] = () => ({ threads: list })
  api.ops['chat.delete_thread'] = () => {
    list = [THREAD]
    return { deleted: true, bot_id: 'b1' }
  }
  messages = [{ id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '查價格', created_at: Date.now(), updated_at: Date.now() }]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  fireEvent.click(await within(main).findByRole('button', { name: /^對話串：比價/ }))
  fireEvent.click(await screen.findByRole('menuitem', { name: '刪除「舊的報價」' }))
  fireEvent.click(await within(await screen.findByRole('alertdialog', { name: '刪除「舊的報價」？' })).findByRole('button', { name: '刪除' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.delete_thread')?.input).toEqual({ thread_id: 't2' }))
  expect(window.location.pathname).toBe('/bots/b1/threads/t1')
})

it('刪掉正在看的對話串：換到最近的另一條', async () => {
  const other = { ...THREAD, id: 't2', title: '舊的報價' }
  api.ops['chat.list_threads'] = () => ({ threads: [THREAD, other] })
  api.ops['chat.delete_thread'] = () => ({ deleted: true, bot_id: 'b1' })
  messages = [{ id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '查價格', created_at: Date.now(), updated_at: Date.now() }]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  fireEvent.click(await within(main).findByRole('button', { name: /^對話串：比價/ }))
  fireEvent.click(await screen.findByRole('menuitem', { name: '刪除「比價」' }))
  fireEvent.click(await screen.findByRole('button', { name: '刪除' }))
  await waitFor(() => expect(window.location.pathname).toBe('/bots/b1/threads/t2'))
})

it('用訂閱的 Bot：「⋯」裡的模型那一項寫出方案，列出 CLI 回報的模型，選了只改這個 Bot', async () => {
  api.ops['bots.list'] = () => ({ bots: [{ ...BOT, engine: 'opencode' }] })
  api.ops['engines.status'] = () => ({
    bots: [{ bot_id: 'b1', name: '小秘', engine: 'opencode', engine_label: 'OpenCode', needs_link: true, linked: true, plan: 'OpenCode Go', agent_default: 'opencode/big-pickle', models: [{ value: 'opencode/big-pickle' }, { value: 'opencode-go/glm-5', name: 'GLM 5' }] }],
    host: [],
  })
  api.ops['bots.update'] = (input) => ({ ...BOT, engine: 'opencode', ...input })
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  expect(screen.queryByText('還沒設定模型')).toBeNull()
  // 頁首只留常用的：模型收在「⋯」裡，那一項寫出目前用的模型
  fireEvent.click(within(main).getByRole('button', { name: '更多' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: /模型：OpenCode · opencode\/big-pickle/ }))
  const dialog = await screen.findByRole('dialog', { name: '選擇模型' })
  expect(within(dialog).getByText('OpenCode Go 訂閱')).toBeInTheDocument()
  fireEvent.click(within(dialog).getByRole('menuitemradio', { name: /GLM 5/ }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'bots.update')?.input).toEqual({ id: 'b1', model: 'opencode-go/glm-5' }))
})

it('接手中：輸入框上方說明 Bot 暫停、訊息會排隊，可以一鍵交還', async () => {
  let taken = true
  const screen = () => ({ bot_id: 'b1', box: 'running', working: false, viewers: 1, ...(taken ? { takeover: { by: 'owner', by_name: '你', since: 1 } } : {}) })
  api.ops['computers.screen'] = screen
  api.ops['computers.handback'] = () => {
    taken = false
    return { screen: screen(), note: '' }
  }
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const bar = await screen_.findByTestId('takeover-bar')
  expect(bar).toHaveTextContent('你正在操作小秘的電腦，小秘暫停中；交還後才會處理新訊息')
  fireEvent.click(within(bar).getByRole('button', { name: '交還電腦' }))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'computers.handback')).toBe(true))
  await waitFor(() => expect(screen_.queryByTestId('takeover-bar')).toBeNull())
})

it('回覆裡的檔案路徑可以點：打開 Bot 的檔案頁並預覽那個檔案', async () => {
  messages = [{ id: 'm1', seq: 1, sender: 'bot:b1', kind: 'text', content: '報告在 `/workspace/reports/漲粉方案.md`，資料夾是 `/workspace/reports/`。', status: 'complete', created_at: Date.now(), updated_at: Date.now() }]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  // 只顯示檔名（2026-10-21 使用者：不要給看不懂的路徑），點了打開
  const link = await screen.findByRole('link', { name: '漲粉方案.md' })
  expect(link).toHaveAttribute('data-href', `/bots/b1/files?path=${encodeURIComponent('/workspace/reports/漲粉方案.md')}`)
  expect(screen.queryByText(/\/workspace\/reports\/漲粉方案/)).toBeNull()
  // 下面有一張檔案卡
  expect(screen.getByRole('link', { name: '預覽 漲粉方案.md' })).toBeInTheDocument()
  // 資料夾不變成連結
  expect(screen.queryByRole('link', { name: '/workspace/reports/' })).toBeNull()
})

it('你上傳的附件：圖片直接顯示、檔案是卡片，不顯示「附件：路徑」', async () => {
  messages = [
    { id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '幫我看這張\n\n附件：/workspace/uploads/IMG_0401.png\n附件：/workspace/uploads/訂單.csv', created_at: Date.now(), updated_at: Date.now() },
  ]
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  expect(await screen.findByText('幫我看這張')).toBeInTheDocument()
  expect(screen.queryByText(/附件：/)).toBeNull()
  const img = screen.getByRole('img', { name: 'IMG_0401.png' })
  expect(img.getAttribute('src')).toContain('thumb=1')
  expect(screen.getByRole('link', { name: '預覽 訂單.csv' })).toBeInTheDocument()
})

it('這條對話串還沒回答的提問：對話裡也有同一張卡，可以就地回答', async () => {
  let open = true
  api.ops['inbox.list'] = (input) => ({
    items:
      open && input.status === 'open'
        ? [{ id: 'q1', kind: 'question', group: 'decide', bot_id: 'b1', task_id: 'k9', thread_id: 't1', title: '小秘有問題問你', body: '團隊要做什麼？', payload: { prompt: '團隊要做什麼？', choices: ['內容團隊', '電商小隊'] }, status: 'open', read: false, created_at: 1 }]
        : [],
  })
  api.ops['chat.respond'] = () => {
    open = false
    return { inbox_id: 'q1', task_id: 'k9', thread_id: 't1', status: 'resolved', resolution: 'answered' }
  }
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  const card = await within(main).findByTestId('pending-ask')
  expect(card).toHaveTextContent('團隊要做什麼？')
  fireEvent.click(within(card).getByRole('button', { name: /內容團隊/ }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'chat.respond')?.input).toEqual({ inbox_id: 'q1', text: '內容團隊' }))
})

it('Bot 要求、要你允許的動作（例：分享登入、刪 Bot）也出現在它的對話裡，可以就地允許', async () => {
  let open = true
  api.ops['inbox.list'] = (input) => ({
    items:
      open && input.status === 'open'
        ? [{ id: 'a1', kind: 'approval', group: 'decide', bot_id: 'b1', ref: 'ap1', title: '小秘 想要：請使用者把另一個 Bot 已登入的網站分享給你', body: '', payload: { op: 'xbot.logins.request_share', actor: 'bot:b1' }, status: 'open', read: false, created_at: 1 }]
        : [],
  })
  api.ops['approvals.decide'] = () => {
    open = false
    return { approval_id: 'ap1', status: 'approved' }
  }
  window.history.replaceState(null, '', '/bots/b1/threads/t1')
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  const card = await within(main).findByTestId('pending-approval')
  expect(card).toHaveTextContent('分享給你')
  fireEvent.click(within(card).getByRole('button', { name: /允許/ }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'approvals.decide')?.input).toEqual({ approval_id: 'ap1', decision: 'allow' }))
})

// 2026-10-19 實機：Bot 一件工作講了好幾則（例：調整五個隊友＝五行），「工作了…」重複畫五次
it('一件工作有好幾則 Bot 訊息：工作步驟只畫一次', async () => {
  render(<App />)
  const main = await screen.findByRole('main', { name: '和小秘的對話' })
  await waitFor(() => expect(window.location.pathname).toBe('/bots/b1/threads/t1'))
  emit(
    ev({ id: 1, kind: 'message.created', payload: storeMsg('m1', 1, 'owner', { task_id: 'k1' }, '調整隊友') }),
    ev({ id: 2, kind: 'task.state', payload: { state: 'running' } }),
    ev({ id: 3, kind: 'chat.tool_call', payload: { type: 'tool_call', tool: { id: 'x1', name: 'terminal' } } }),
    ev({ id: 4, kind: 'chat.tool_result', payload: { type: 'tool_result', tool: { id: 'x1', ok: true } } }),
  )
  for (let i = 0; i < 3; i++) emit(ev({ id: 10 + i, kind: 'message.created', payload: storeMsg('p' + i, 2 + i, 'bot:b1', { task_id: 'k1' }, `調整了第 ${i + 1} 位`) }))
  emit(ev({ id: 20, kind: 'task.state', payload: { state: 'done', verdict: 'answer' } }))
  await waitFor(() => expect(within(main).getAllByTestId('msg-bot')).toHaveLength(3))
  // Work summary appears once (uses details/summary)
  expect(within(main).getAllByText(/工作了/)).toHaveLength(1)
})
