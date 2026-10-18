import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import App from '../App'
import { fakeApi, type FakeApi } from '../test-api'
import { resetBots } from '../data/bots'
import { resetInbox } from '../data/inbox'
import { resetChat } from '../chat/chatStore'
import { describe as describeEntry } from '../settings/bot/HistoryTab'
import { Avatar } from '../ui/Avatar'
import { resetOnboard, startTour } from './state'

const STATE = { completed: false, tour_done: false, dismissed: [] as string[] }
const status = (over: Record<string, unknown> = {}) => ({
  first_run: true,
  bots: 0,
  state: STATE,
  engines: [
    { engine: 'claude-code', label: 'Claude Code', plan: 'Claude', logged_in: true, login: 'claude login' },
    { engine: 'codex', label: 'Codex', plan: 'ChatGPT', logged_in: false, login: 'codex login' },
  ],
  local: [{ id: 'lmstudio', label: 'LM Studio', base_url: 'http://127.0.0.1:1234/v1', preset: 'lmstudio', models: ['qwen3-8b', 'gemma-3'] }],
  computer: { docker: true, image: 'xbot-box:dev', ready: false, hint: '還沒有 Bot 電腦映像：在這台電腦執行 make box' },
  ...over,
})

const BOT = { id: 'x1', name: '小秘', title: '幕僚長', description: '', soul: '', color: 'blue', shape: 'cursor', eyes: 'happy', engine: 'claude-code', model: '', fallback: '', effort: '', approval_mode: 'ask', memory_enabled: true, archived: false, created_at: 1, updated_at: 1 }

let api: FakeApi
beforeEach(() => {
  api = fakeApi()
  window.history.replaceState(null, '', '/')
})
afterEach(() => {
  resetBots()
  resetInbox()
  resetChat()
  resetOnboard()
  vi.unstubAllGlobals()
})

it('首次設定：偵測到已登入的 Claude Code 與本機模型，用這個 → 認識小秘 → 開始（建 Bot、同意訂閱、記下做完）', async () => {
  let bots: unknown[] = []
  api.ops['bots.list'] = () => ({ bots })
  api.ops['onboard.status'] = () => status()
  api.ops['models.get'] = () => ({ providers: [], ready: false })
  api.ops['bots.create'] = (input) => {
    bots = [{ ...BOT, ...input }]
    return bots[0]
  }
  api.ops['engines.link_credentials'] = () => ({ bot_id: 'x1', linked: true })
  api.ops['onboard.update'] = (input) => ({ ...STATE, completed: !!input.completed, dismissed: [] })
  render(<App />)

  const card = await screen.findByRole('region', { name: '接上 AI' })
  expect(screen.getByText('簡化：6 步 → 2 步')).toBeInTheDocument()
  const list = await within(card).findByRole('list', { name: '找到的 AI' })
  const rows = within(list).getAllByRole('listitem')
  expect(rows).toHaveLength(2) // 沒登入的 Codex 不列
  expect(rows[0]).toHaveTextContent('Claude Code建議')
  expect(rows[1]).toHaveTextContent('LM Studio · 找到 2 個模型')
  // 找到時金鑰欄位預設收起
  expect(within(card).queryByLabelText('API 金鑰')).toBeNull()
  fireEvent.click(within(rows[0]).getByRole('button', { name: '用這個：Claude Code' }))

  const meet = await screen.findByRole('region', { name: '認識小秘' })
  expect(within(meet).getByTestId('computer-line')).toHaveTextContent('make box')
  fireEvent.change(within(meet).getByLabelText('名字'), { target: { value: '阿秘' } })
  expect(await screen.findByRole('region', { name: '認識阿秘' })).toBeInTheDocument()
  fireEvent.click(within(meet).getByRole('button', { name: '換個樣子' }))
  fireEvent.click(within(meet).getByRole('radio', { name: '紫' }))
  fireEvent.click(within(meet).getByRole('radio', { name: '睜大' }))
  expect(meet).toHaveTextContent('同意讓阿秘用你在這台電腦登入的 Claude 訂閱')
  fireEvent.click(within(meet).getByRole('button', { name: '開始' }))

  await waitFor(() => expect(window.location.pathname).toBe('/bots/x1'))
  const created = api.calls.find((c) => c.path === 'bots.create')?.input
  expect(created).toMatchObject({ name: '阿秘', shape: 'cursor', color: 'purple', eyes: 'wide', engine: 'claude-code' })
  expect(api.calls.find((c) => c.path === 'engines.link_credentials')?.input).toEqual({ bot_id: 'x1', engine: 'claude-code' })
  expect(api.calls.find((c) => c.path === 'onboard.update')?.input).toEqual({ completed: true })
})

it('首次設定：沒偵測到時直接顯示金鑰欄位，貼上自動認出供應商', async () => {
  api.ops['bots.list'] = () => ({ bots: [] })
  api.ops['onboard.status'] = () => status({ engines: [], local: [] })
  api.ops['models.get'] = () => ({ providers: [], ready: false })
  api.ops['models.presets'] = () => ({
    presets: [{ id: 'openrouter', label: 'OpenRouter', base_url: 'https://openrouter.ai/api/v1', transport: 'chat_completions', signup_url: '', local: false, models: ['openai/gpt-5.5'], configured: false }],
  })
  api.ops['models.add_provider'] = () => ({ provider: { id: 'openrouter' } })
  render(<App />)
  const card = await screen.findByRole('region', { name: '接上 AI' })
  const key = await within(card).findByLabelText('API 金鑰')
  fireEvent.change(key, { target: { value: 'sk-or-v1-abcdefghijklmnop' } })
  expect(await within(card).findByText(/認出是 OpenRouter/)).toBeInTheDocument()
  fireEvent.click(within(card).getByRole('button', { name: '用這把金鑰' }))
  expect(await screen.findByRole('region', { name: '認識小秘' })).toBeInTheDocument()
  expect(api.calls.find((c) => c.path === 'models.add_provider')?.input).toEqual({ preset: 'openrouter', api_key: 'sk-or-v1-abcdefghijklmnop', default_model: 'openai/gpt-5.5' })
})

it('首次設定可以先跳過，跳過後是「建立第一個 Bot」（A09），範本點一下就建立', async () => {
  let bots: unknown[] = []
  api.ops['bots.list'] = () => ({ bots })
  api.ops['onboard.status'] = () => status({ engines: [], local: [] })
  api.ops['onboard.update'] = () => ({ ...STATE, completed: true })
  api.ops['bots.create'] = (input) => {
    bots = [{ ...BOT, id: 'z1', ...input }]
    return bots[0]
  }
  render(<App />)
  fireEvent.click(await screen.findByRole('button', { name: '先跳過' }))
  expect(await screen.findByRole('heading', { level: 1, name: '建立第一個 Bot' })).toBeInTheDocument()
  const tpl = screen.getByRole('region', { name: '或從範本開始' })
  expect(within(tpl).getAllByRole('button', { name: /用範本建立/ })).toHaveLength(10)
  fireEvent.click(within(tpl).getByRole('button', { name: '用範本建立：Bug 重現' }))
  await waitFor(() => expect(window.location.pathname).toBe('/bots/z1'))
  expect(api.calls.find((c) => c.path === 'bots.create')?.input).toMatchObject({ name: '抓蟲', title: 'Bug 重現', shape: 'shield', color: 'red' })
})

it('A09：一句話產生建議卡，按「建立」用建議的名字', async () => {
  api.ops['bots.list'] = () => ({ bots: [] })
  api.ops['bots.suggest'] = () => ({ name: '阿哲', title: '選品研究員', description: '每天看熱賣榜', soul: '你是阿哲', by: 'rules' })
  api.ops['bots.create'] = (input) => ({ ...BOT, id: 'a1', ...input })
  render(<App />)
  fireEvent.change(await screen.findByLabelText('一句話說你要它做什麼'), { target: { value: '每天找蝦皮熱賣的居家小物' } })
  fireEvent.click(screen.getByRole('button', { name: '產生建議' }))
  const sug = await screen.findByRole('region', { name: '建議的 Bot' })
  expect(sug).toHaveTextContent('系統建議 · 都可以改')
  expect(sug).toHaveTextContent('權限預設「先問我」')
  fireEvent.click(within(sug).getByRole('button', { name: '建立阿哲' }))
  await waitFor(() => expect(window.location.pathname).toBe('/bots/a1'))
  expect(api.calls.find((c) => c.path === 'bots.create')?.input).toMatchObject({ name: '阿哲', title: '選品研究員', soul: '你是阿哲' })
})

it('首頁之後再做（A05）＋導覽三站（A10）：關掉卡片會記住，導覽看完記住', async () => {
  window.history.replaceState(null, '', '/bots/x1')
  api.ops['bots.list'] = () => ({ bots: [BOT] })
  api.ops['onboard.status'] = () => status({ first_run: false, bots: 1, state: { ...STATE, completed: true } })
  api.ops['onboard.update'] = (input) => ({ ...STATE, completed: true, tour_done: !!input.tour_done, dismissed: input.dismiss ? [input.dismiss] : [] })
  render(<App />)
  const home = await screen.findByTestId('first-home')
  expect(home).toHaveTextContent('嗨，我是小秘')
  expect(screen.getByRole('group', { name: '可以這樣開始' })).toHaveTextContent('整理今天的信')
  fireEvent.click(within(home).getByRole('button', { name: '關掉「連手機」' }))
  await waitFor(() => expect(within(home).queryByTestId('later-phone')).toBeNull())
  expect(api.calls.find((c) => c.path === 'onboard.update')?.input).toEqual({ dismiss: 'phone' })

  fireEvent.click(within(home).getByRole('button', { name: /開始導覽/ }))
  expect(await screen.findByRole('dialog', { name: '導覽 1 / 3：在這裡交代事情' })).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '下一步' }))
  expect(await screen.findByRole('dialog', { name: /導覽 2 \/ 3：看它的電腦/ })).toHaveTextContent('接手')
  fireEvent.click(screen.getByRole('button', { name: '下一步' }))
  const last = await screen.findByRole('dialog', { name: /導覽 3 \/ 3：要你決定的事/ })
  expect(last).toHaveTextContent('核准卡')
  fireEvent.click(within(last).getByRole('button', { name: '完成' }))
  await waitFor(() => expect(screen.queryByTestId('tour')).toBeNull())
  expect(await screen.findByText('導覽看完了')).toBeInTheDocument()
  expect(api.calls.some((c) => c.path === 'onboard.update' && c.input.tour_done === true)).toBe(true)
  expect(window.localStorage.getItem('xbot.tour.done')).toBe('1')

  // Esc 跳過
  act(() => startTour())
  expect(await screen.findByTestId('tour')).toBeInTheDocument()
  fireEvent.keyDown(window, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByTestId('tour')).toBeNull())
})

it('Bot 設定歷史：把稽核紀錄翻成白話', () => {
  const e = (op: string, params: unknown = {}) => ({ id: 1, actor: 'owner', via: 'ui', op, request_id: '', target: 'b1', params, status: 'ok', error_code: '', created_at: 1 })
  expect(describeEntry(e('xbot.bots.update', { id: 'b1', name: 'x', soul: 'y' }))).toBe('改了名字、人設')
  expect(describeEntry(e('xbot.bots.update', { id: 'b1', approval_mode: 'full' }))).toBe('權限改成「完全放手」')
  expect(describeEntry(e('xbot.skills.disable'))).toBe('停用技能')
  expect(describeEntry(e('xbot.chat.send'))).toBeNull()
})

it('頭像：平常的眼睛只在待命時用，其他狀態照狀態換', () => {
  const { container, rerender } = render(<Avatar shape="cursor" color="blue" eyes="happy" />)
  expect(container.querySelector('[data-eyes]')).toHaveAttribute('data-eyes', 'happy')
  rerender(<Avatar shape="cursor" color="blue" eyes="happy" state="working" />)
  expect(container.querySelector('[data-eyes]')).toHaveAttribute('data-eyes', 'aside')
  rerender(<Avatar shape="cursor" color="blue" eyes="nope" />)
  expect(container.querySelector('[data-eyes]')).toHaveAttribute('data-eyes', 'dot')
})
