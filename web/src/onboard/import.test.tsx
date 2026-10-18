import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import App from '../App'
import { resetBots } from '../data/bots'
import { dispatchEvent } from '../data/events'
import { resetInbox } from '../data/inbox'
import { fakeApi, type FakeApi } from '../test-api'
import { defaultSelection, effective, importRunStore, toggle, type Plan } from './importData'
import { resetOnboard } from './state'

const SOURCE = { id: 'lazyboy-1', kind: 'lazyboy', label: 'LazyBoy', path: '/home/me/LazyBoyHermes/.data', bots: 2, skills: 1, memory: 1, routines: 1, teams: 0, rooms: 0, keys: 1, logins: 1, imported: 0, summary: '在這台電腦找到 LazyBoy 的 2 個 Bot、1 把金鑰和 1 個定時任務。' }

const PLAN: Plan = {
  source: SOURCE,
  items: [
    { key: 'bot:a', kind: 'bot', title: '小秘', original: '小秘', name: '小秘 2', renamed: true, status: 'ready', checked: true, detail: '助理', color: 'blue', shape: 'circle', attention: '權限從「先問我」開始' },
    { key: 'skills:a', kind: 'skills', bot: 'bot:a', title: '技能 1 個', detail: 'taiwan-opendata', status: 'ready', checked: true, count: 1, attention: '先關閉；看過內容再打開' },
    { key: 'login:a', kind: 'login', bot: 'bot:a', title: '瀏覽器登入', status: 'ready', checked: false, explicit: true, bytes: 5 << 20 },
    { key: 'routine:r1', kind: 'routine', bot: 'bot:a', title: '每日整理', detail: '平日 09:00', status: 'ready', checked: true },
    { key: 'bot:b', kind: 'bot', title: '研究員', original: '研究員', status: 'done', checked: false, target: 'x9' },
    { key: 'memory:b', kind: 'memory', bot: 'bot:b', title: '記憶', status: 'ready', checked: true, count: 2 },
    { key: 'key:openai', kind: 'key', title: 'OpenAI', status: 'ready', checked: false, explicit: true, detail: 'openai' },
  ],
  new_bots: 1,
  remaining: 49,
  can_create: true,
  not_carried: ['對話紀錄（舊的對話留在原本的系統）'],
  notes: [],
}

let api: FakeApi
beforeEach(() => {
  api = fakeApi()
  importRunStore.set(null)
})
afterEach(() => {
  resetBots()
  resetInbox()
  resetOnboard()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

it('預設勾選：金鑰、登入不勾；取消 Bot 時它的東西一起取消，已經帶過的 Bot 可以補帶記憶', () => {
  const sel = defaultSelection(PLAN)
  expect([...sel].sort()).toEqual(['bot:a', 'memory:b', 'routine:r1', 'skills:a'])
  const off = toggle(PLAN, sel, 'bot:a', false)
  expect(effective(PLAN, off)).toEqual(['memory:b'])
  const on = toggle(PLAN, off, 'bot:a', true)
  expect(effective(PLAN, on).sort()).toEqual(['bot:a', 'memory:b', 'routine:r1', 'skills:a'])
  // Bot 沒勾時，就算它的登入被勾了也不送
  const odd = new Set(['login:a'])
  expect(effective(PLAN, odd)).toEqual([])
})

it('帶入舊設定（F10 版型）：預演、勾金鑰、改名、開始匯入、看進度', async () => {
  api.ops['import.detect'] = () => ({ sources: [SOURCE] })
  api.ops['import.plan'] = () => PLAN
  api.ops['import.run'] = () => ({ id: 'run1', source_id: SOURCE.id, label: 'LazyBoy', state: 'running', done: 0, total: 5, percent: 0, bytes: 0, results: [], bots: [], started_at: 1 })
  window.history.replaceState(null, '', '/import')
  render(<App />)

  expect(await screen.findByText(SOURCE.summary)).toBeInTheDocument()
  const bots = screen.getByRole('region', { name: 'Bot' })
  expect(within(bots).getByTestId('bot-bot:b')).toHaveTextContent('已經帶進來了')
  const name = within(bots).getByLabelText('小秘 帶進來後的名字') as HTMLInputElement
  expect(name.value).toBe('小秘 2')
  expect(within(bots).getByText(/你已經有一個「小秘」/)).toBeInTheDocument()
  const notes = screen.getByRole('complementary', { name: '加入前請看一下' })
  expect(within(notes).getByText('1 個技能先關閉')).toBeInTheDocument()
  expect(within(notes).getByText('1 個定時任務先暫停')).toBeInTheDocument()
  expect(within(notes).getByText('對話紀錄（舊的對話留在原本的系統）')).toBeInTheDocument()
  expect(screen.getByTestId('import-summary')).toHaveTextContent('會建立 1 個 Bot')

  fireEvent.click(within(notes).getByRole('checkbox', { name: /OpenAI/ }))
  fireEvent.change(name, { target: { value: '舊小秘' } })
  fireEvent.click(screen.getByRole('button', { name: '開始匯入' }))

  await screen.findByTestId('import-progress')
  const call = api.calls.find((c) => c.path === 'import.run')!.input
  expect((call.items as string[]).sort()).toEqual(['bot:a', 'key:openai', 'memory:b', 'routine:r1', 'skills:a'])
  expect(call.names).toEqual([{ key: 'bot:a', name: '舊小秘' }])
  expect(call.source).toBe(SOURCE.id)

  act(() =>
    dispatchEvent({
      kind: 'import.progress',
      payload: { id: 'run1', source_id: SOURCE.id, label: 'LazyBoy', state: 'done', done: 5, total: 5, percent: 100, bytes: 0, bots: ['n1'], started_at: 1,
        results: [{ key: 'bot:a', kind: 'bot', title: '小秘', status: 'ok', detail: '權限從「先問我」開始', target: 'n1' }, { key: 'key:openai', kind: 'key', title: 'OpenAI', status: 'failed', detail: '壞掉了' }] },
    } as never),
  )
  await waitFor(() => expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('帶完了，有幾項沒成功'))
  expect(screen.getByRole('list', { name: '結果' })).toHaveTextContent('壞掉了')
  expect(screen.getByRole('button', { name: '去看帶進來的 Bot' })).toBeInTheDocument()
})

it('首頁「帶入舊設定」卡片（A05）只在找到舊資料時出現，點了到預演頁', async () => {
  const bot = { id: 'x1', name: '小秘', title: '幕僚長', description: '', soul: '', color: 'blue', shape: 'cursor', engine: 'hermes', model: '', fallback: '', effort: '', approval_mode: 'ask', memory_enabled: true, archived: false, created_at: 1, updated_at: 1 }
  const st = { completed: true, tour_done: true, dismissed: [] as string[] }
  let legacy = { found: true, summary: SOURCE.summary, sources: 1 }
  api.ops['bots.list'] = () => ({ bots: [bot] })
  api.ops['onboard.status'] = () => ({ first_run: false, bots: 1, state: st, engines: [], local: [], computer: { docker: true, image: 'x', ready: true }, legacy })
  api.ops['import.detect'] = () => ({ sources: [SOURCE] })
  api.ops['import.plan'] = () => PLAN
  window.history.replaceState(null, '', '/bots/x1')
  const { unmount } = render(<App />)
  const card = await screen.findByTestId('later-import')
  expect(card).toHaveTextContent('帶入舊設定')
  expect(card).toHaveTextContent(SOURCE.summary)
  fireEvent.click(within(card).getByRole('button', { name: /看看要帶哪些/ }))
  await waitFor(() => expect(window.location.pathname).toBe('/import'))
  unmount()
  resetOnboard()

  legacy = { found: false, summary: '', sources: 0 }
  window.history.replaceState(null, '', '/bots/x1')
  render(<App />)
  expect(await screen.findByTestId('later-phone')).toBeInTheDocument()
  expect(screen.queryByTestId('later-import')).toBeNull()
})

it('沒找到舊資料：可以填別的資料夾', async () => {
  api.ops['import.detect'] = () => ({ sources: [] })
  api.ops['import.plan'] = () => PLAN
  window.history.replaceState(null, '', '/import')
  render(<App />)
  expect(await screen.findByTestId('import-none')).toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('資料夾'), { target: { value: '~/.openmausbot' } })
  fireEvent.click(screen.getByRole('button', { name: '讀取' }))
  expect(await screen.findByText(SOURCE.summary)).toBeInTheDocument()
  expect(api.calls.find((c) => c.path === 'import.plan')?.input).toEqual({ path: '~/.openmausbot' })
})
