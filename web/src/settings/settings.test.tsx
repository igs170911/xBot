import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi, opError, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { activeNav, botBack, botOrigin } from './nav'
import { guessPreset, maskKey, splitRef, type Preset } from './modelData'
import { usd, pct, tokens, errText } from './format'
import { ApiError } from '../api/generated'
import { initTheme, resolveTheme, setTheme, themeStore } from './theme'
import { GeneralSection } from './GeneralSection'
import { BotSettingsPage } from './BotSettings'
import { UsageSection } from './UsageSection'
import { subscriptionMarker } from './botModes'

const preset = (id: string, label: string): Preset => ({ id, label, base_url: 'x', transport: 'chat_completions', local: false, models: [], configured: false })
const PRESETS = [preset('openrouter', 'OpenRouter'), preset('anthropic', 'Anthropic'), preset('openai-api', 'OpenAI API'), preset('xai', 'xAI')]

let api: FakeApi
beforeEach(() => {
  api = fakeApi()
  window.localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
})
afterEach(() => vi.unstubAllGlobals())

it('導覽：子頁也算在它的分類', () => {
  expect(activeNav('/settings/general')).toBe('/settings/general')
  expect(activeNav('/settings/models/routing')).toBe('/settings/models')
  expect(activeNav('/settings/advanced/agents')).toBe('/settings/advanced/agents')
  // Bot 設定不屬於「AI 模型」：要看是從哪裡打開的
  expect(activeNav('/settings/bots/b1')).toBe('')
})

it('Bot 設定的返回：從聊天回聊天、從設定的某一頁回那一頁、直接打開回 AI 模型的 Bot 清單', () => {
  expect(botBack('/bots/b1/threads/t1')).toEqual({ to: '/bots/b1/threads/t1', label: '回到對話', nav: '' })
  expect(botBack('/rooms/r1')).toEqual({ to: '/rooms/r1', label: '回到群組', nav: '' })
  expect(botBack('/settings/models')).toEqual({ to: '/settings/models', label: 'AI 模型', nav: '/settings/models' })
  expect(botBack('/settings/usage')).toEqual({ to: '/settings/usage', label: '用量與花費', nav: '/settings/usage' })
  expect(botBack('')).toEqual({ to: '/settings/models', label: 'AI 模型', nav: '' })
  expect(botBack('/teams')).toEqual({ to: '/settings/models', label: 'AI 模型', nav: '' })
  // 從聊天打開，切分頁、上一頁回到前一個分頁，都還是回聊天
  expect(botOrigin('/settings/bots/b1', '/bots/b1/threads/t1')).toBe('/bots/b1/threads/t1')
  expect(botOrigin('/settings/bots/b1/advanced', '/settings/bots/b1')).toBe('/bots/b1/threads/t1')
  expect(botOrigin('/settings/bots/b1', '/settings/bots/b1/advanced')).toBe('/bots/b1/threads/t1')
  // 之後從 AI 模型點進來就回 AI 模型
  expect(botOrigin('/settings/bots/b1/advanced', '/settings/models')).toBe('/settings/models')
  // 換一個 Bot：重新算
  expect(botOrigin('/settings/bots/b2', '')).toBe('')
  expect(botOrigin('/settings/bots/b2/advanced', '/settings/bots/b1')).toBe('/settings/bots/b1')
})

it('貼上金鑰：從開頭認出供應商，認不出就不猜', () => {
  expect(guessPreset('sk-or-v1-abc', PRESETS)?.id).toBe('openrouter')
  expect(guessPreset('sk-ant-api03-x', PRESETS)?.id).toBe('anthropic')
  expect(guessPreset('sk-proj-123', PRESETS)?.id).toBe('openai-api')
  expect(guessPreset('xai-123', PRESETS)?.id).toBe('xai')
  expect(guessPreset('abc123', PRESETS)).toBeUndefined()
  expect(guessPreset('  ', PRESETS)).toBeUndefined()
  expect(maskKey('sk-or-v1-abcdef0123456789')).toBe('sk-or-' + '•'.repeat(12) + '6789')
  expect(maskKey('short')).toBe('•••••')
})

it('模型參照：只切第一個斜線', () => {
  expect(splitRef('openrouter/anthropic/claude')).toEqual({ provider: 'openrouter', model: 'anthropic/claude' })
  expect(splitRef('gpt-5')).toEqual({ provider: '', model: 'gpt-5' })
})

it('金額、token、比例的寫法', () => {
  expect(usd(0)).toBe('US$0')
  expect(usd(0.0042)).toBe('US$0.0042')
  expect(usd(12.5)).toBe('US$12.50')
  expect(usd(1280)).toBe('US$1,280')
  expect(tokens(1500)).toBe('1.5k')
  expect(tokens(12_000_000)).toBe('12M')
  expect(pct(0.583)).toBe('58%')
  expect(errText(new ApiError(403, { code: 'pending_approval', message: 'x', retryable: false }))).toContain('待我處理')
  expect(subscriptionMarker('claude-code')).toBe('這個 Bot 用你的 Claude 訂閱')
})

it('主題：預設深色；選了就存起來、立刻套用；跟隨系統看系統設定', () => {
  initTheme()
  expect(themeStore.get()).toBe('dark')
  expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  setTheme('light')
  expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  expect(window.localStorage.getItem('xbot.theme')).toBe('light')
  setTheme('dark')
  expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  window.localStorage.setItem('xbot.theme', 'light')
  initTheme()
  expect(themeStore.get()).toBe('light')
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('light'), addEventListener() {}, removeEventListener() {} }))
  expect(resolveTheme('system')).toBe('light')
  setTheme('dark')
})

it('一般：點主題卡就切換', () => {
  render(
    <ToastProvider>
      <GeneralSection />
    </ToastProvider>,
  )
  const group = screen.getByRole('radiogroup', { name: '主題' })
  expect(within(group).getByRole('radio', { name: /Midnight/ })).toHaveAttribute('aria-checked', 'true')
  fireEvent.click(within(group).getByRole('radio', { name: /Daylight/ }))
  expect(within(group).getByRole('radio', { name: /Daylight/ })).toHaveAttribute('aria-checked', 'true')
  expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  expect(screen.getByText('粉圓', { selector: 'span.whitespace-nowrap' })).toBeInTheDocument()
  setTheme('dark')
})

it('一般：回話風格所有 Bot 共用，選了就存', async () => {
  api.ops['bots.reply_style_get'] = () => ({ style: 'brief' })
  api.ops['bots.reply_style_set'] = (input) => ({ style: input.style })
  render(
    <ToastProvider>
      <GeneralSection />
    </ToastProvider>,
  )
  const group = await screen.findByRole('radiogroup', { name: '回話風格' })
  expect(within(group).getByRole('radio', { name: '簡短' })).toHaveAttribute('aria-checked', 'true')
  expect(screen.getByText(/像傳訊息：一段一兩句/)).toBeInTheDocument()
  fireEvent.click(within(group).getByRole('radio', { name: '詳細' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'bots.reply_style_set')?.input).toEqual({ style: 'detailed' }))
  expect(await screen.findByText('已更新回話風格，下一段新對話開始生效')).toBeInTheDocument()
})

const BOT = { id: 'b1', name: '小秘', title: '幕僚長', description: '', soul: '', color: 'blue', shape: 'cursor', engine: 'claude-code', model: '', fallback: '', effort: 'medium', approval_mode: 'ask', archived: false, created_at: 1, updated_at: 1 }

const account = (engine: string, over: Record<string, unknown> = {}) => ({
  engine,
  label: engine,
  plan: engine,
  login: engine === 'opencode' ? 'key' : engine === 'claude-code' ? 'code' : engine === 'gemini' ? 'host' : 'web',
  ready: false,
  host_logged_in: false,
  ...over,
})

it('Bot 自己登入：網頁登入給網址和代碼，登入好了列出這個帳號的模型，按「改用」才換', async () => {
  let ready = false
  const status = () => ({
    bots: [{ bot_id: 'b1', name: '小秘', engine: 'hermes', engine_label: 'Hermes', needs_link: false, linked: false, accounts: [account('codex', ready ? { ready: true, source: 'bot', models: [{ value: 'gpt-6-sol' }, { value: 'gpt-5.5' }], default: 'gpt-6-sol' } : {})] }],
    host: [],
  })
  api.ops['bots.get'] = () => ({ ...BOT, engine: 'hermes' })
  api.ops['models.get'] = () => ({ providers: [], default: null, fallback: null })
  api.ops['models.presets'] = () => ({ presets: [] })
  api.ops['engines.status'] = status
  api.ops['engines.login_start'] = () => ({ bot_id: 'b1', engine: 'codex', label: 'Codex CLI', state: 'waiting', url: 'https://auth.openai.com/codex/device', code: '78EM-T8MY9' })
  api.ops['engines.login_status'] = () => {
    ready = true
    return { bot_id: 'b1', engine: 'codex', label: 'Codex CLI', state: 'done', message: '已登入' }
  }
  api.ops['engines.switch'] = () => ({ bot: status().bots[0], model: 'gpt-5.5' })
  render(
    <ToastProvider>
      <BotSettingsPage botId="b1" tab="advanced" />
    </ToastProvider>,
  )
  fireEvent.click(await screen.findByRole('radio', { name: /^Codex/ }))
  fireEvent.click(screen.getByRole('button', { name: '登入 ChatGPT 帳號' }))
  const dlg = await screen.findByRole('dialog', { name: '小秘登入 ChatGPT 帳號' })
  expect(await within(dlg).findByTestId('login-code')).toHaveTextContent('78EM-T8MY9')
  expect(within(dlg).getByTestId('login-url')).toHaveAttribute('href', 'https://auth.openai.com/codex/device')
  expect(await within(dlg).findByText(/小秘已登入 ChatGPT 帳號/, undefined, { timeout: 4000 })).toBeInTheDocument()
  fireEvent.click(within(dlg).getByRole('button', { name: '關閉' }))
  fireEvent.click(await screen.findByRole('button', { name: /^模型/ }))
  fireEvent.click(within(screen.getByRole('listbox')).getByRole('option', { name: 'gpt-5.5' }))
  expect(api.calls.some((c) => c.path === 'bots.update')).toBe(false)
  fireEvent.click(screen.getByRole('button', { name: '改用 Codex' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'engines.switch')?.input).toEqual({ bot_id: 'b1', engine: 'codex', model: 'gpt-5.5', link: false }))
  expect(api.calls.some((c) => c.path === 'engines.login_cancel')).toBe(false)
})

it('Bot 的權限：要核准時說明已送到待我處理', async () => {
  api.ops['bots.get'] = () => ({ ...BOT, engine: 'hermes' })
  api.ops['models.presets'] = () => ({ presets: [] })
  api.ops['engines.status'] = () => ({ bots: [], host: [] })
  api.ops['bots.update'] = () => {
    throw { status: 403, error: { code: 'pending_approval', message: '要核准', retryable: false, needs_approval_id: 'ap1' } }
  }
  render(
    <ToastProvider>
      <BotSettingsPage botId="b1" tab="permissions" />
    </ToastProvider>,
  )
  const modes = await screen.findByRole('radiogroup', { name: '權限等級' })
  expect(screen.getByText(/就算選「完全放手」/)).toBeInTheDocument()
  expect(within(modes).getAllByRole('radio').map((r) => r.textContent?.split(/[花讀大幾]/)[0].replace(/預設|（風險較高）/, ''))).toEqual(['先問我', '一般的事自己做', '放手做', '完全放手'])
  fireEvent.click(within(modes).getByRole('radio', { name: /^完全放手/ }))
  expect(await screen.findByText(/已送到「待我處理」/)).toBeInTheDocument()
})

it('用量：讀不到時顯示原因', async () => {
  api.ops['usage.summary'] = () => opError(500, 'internal', '資料庫忙碌')
  api.ops['usage.caps'] = () => ({ day: '2026-10-18', month: '2026-10', global: { key: 'all', daily_usd: 0, monthly_usd: 10, spent_today_usd: 1, spent_month_usd: 5, exceeded: false, daily_micros: 0, monthly_micros: 0, spent_day_micros: 0, spent_month_micros: 0 }, bots: [], providers: [] })
  api.ops['models.pricing'] = () => ({ prices: [], unpriced: [] })
  render(
    <ToastProvider>
      <UsageSection />
    </ToastProvider>,
  )
  expect(await screen.findByText(/讀不到用量：資料庫忙碌/)).toBeInTheDocument()
})

it('用訂閱的 Bot：模型列出這個帳號能用的，選項多時可以打字篩選', async () => {
  const models = Array.from({ length: 14 }, (_, i) => ({ value: `opencode/m${i}`, name: `Model ${i}` }))
  api.ops['bots.get'] = () => ({ ...BOT, engine: 'opencode', model: '' })
  api.ops['models.presets'] = () => ({ presets: [] })
  api.ops['engines.status'] = () => ({
    bots: [{ bot_id: 'b1', name: '小秘', engine: 'opencode', engine_label: 'OpenCode', needs_link: true, linked: true, accounts: [account('opencode', { ready: true, source: 'bot', default: 'opencode/big-pickle', models })] }],
    host: [],
  })
  api.ops['bots.update'] = (input) => ({ ...BOT, engine: 'opencode', ...input })
  render(
    <ToastProvider>
      <BotSettingsPage botId="b1" tab="advanced" />
    </ToastProvider>,
  )
  fireEvent.click(await screen.findByRole('button', { name: /^模型/ }))
  const find = await screen.findByRole('textbox', { name: '找模型' })
  fireEvent.change(find, { target: { value: 'model 13' } })
  const list = screen.getByRole('listbox')
  expect(within(list).getAllByRole('option')).toHaveLength(1)
  fireEvent.click(within(list).getByRole('option', { name: /Model 13/ }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'bots.update')?.input).toEqual({ id: 'b1', model: 'opencode/m13' }))
})

it('用訂閱、還沒讀到模型：可以直接輸入名稱', async () => {
  api.ops['bots.get'] = () => ({ ...BOT, engine: 'grok', model: '' })
  api.ops['models.presets'] = () => ({ presets: [] })
  api.ops['engines.status'] = () => ({ bots: [{ bot_id: 'b1', name: '小秘', engine: 'grok', engine_label: 'Grok', needs_link: true, linked: true, accounts: [account('grok', { ready: true, source: 'bot' })] }], host: [] })
  api.ops['bots.update'] = (input) => ({ ...BOT, engine: 'grok', ...input })
  render(
    <ToastProvider>
      <BotSettingsPage botId="b1" tab="advanced" />
    </ToastProvider>,
  )
  const box = await screen.findByRole('textbox', { name: '模型' })
  fireEvent.change(box, { target: { value: 'grok-5' } })
  fireEvent.blur(box)
  await waitFor(() => expect(api.calls.find((c) => c.path === 'bots.update')?.input).toEqual({ id: 'b1', model: 'grok-5' }))
})

it('模型下拉：還沒探測時先列範本的模型；「其他」可以自己輸入', async () => {
  const { ModelChooser } = await import('./ModelChooser')
  const onCommit = vi.fn()
  const presets: Preset[] = [{ ...preset('opencode-go', 'OpenCode Go'), base_url: 'https://opencode.ai/zen/go/v1', models: ['deepseek-v4-flash', 'glm-5.3'] }]
  render(
    <ModelChooser
      label="預設模型"
      value={{ provider: 'openai', model: 'deepseek-v4-flash' }}
      providers={[{ id: 'openai', base_url: 'https://opencode.ai/zen/go/v1', api_key_set: true }]}
      presets={presets}
      onCommit={onCommit}
    />,
  )
  fireEvent.click(screen.getByRole('button', { name: /預設模型：模型/ }))
  fireEvent.click(screen.getByRole('option', { name: 'glm-5.3' }))
  expect(onCommit).toHaveBeenCalledWith({ provider: 'openai', model: 'glm-5.3' })
  fireEvent.click(screen.getByRole('button', { name: /預設模型：模型/ }))
  fireEvent.click(screen.getByRole('option', { name: '其他（自己輸入）' }))
  const box = screen.getByRole('textbox', { name: '預設模型：模型' })
  fireEvent.change(box, { target: { value: 'kimi-k3' } })
  fireEvent.blur(box)
  expect(onCommit).toHaveBeenLastCalledWith({ provider: 'openai', model: 'kimi-k3' })
})

it('頭像顏色：最後一顆是調色盤，可以自己選顏色；選了之後它是選中的那顆', async () => {
  const { ColorPicker } = await import('./bot/pickers')
  const { colorHex, isCustomColor } = await import('../ui/avatar-data')
  const onChange = vi.fn()
  const { container, rerender } = render(<ColorPicker value="blue" onChange={onChange} />)
  const custom = screen.getByRole('radio', { name: '自己選顏色' })
  expect(custom).toHaveAttribute('aria-checked', 'false')
  fireEvent.change(container.querySelector('input[type="color"]')!, { target: { value: '#12ABEF' } })
  expect(onChange).toHaveBeenCalledWith('#12abef')
  rerender(<ColorPicker value="#12abef" onChange={onChange} />)
  expect(screen.getByRole('radio', { name: '自己選顏色' })).toHaveAttribute('aria-checked', 'true')
  expect(isCustomColor('#12abef') && colorHex('#12abef')).toBe('#12abef')
  expect(colorHex('blue')).toBe('#3f8cff')
})

it('Bot 的權限：團隊管理權限在「權限與花費」就能開', async () => {
  api.ops['bots.get'] = () => ({ ...BOT, can_manage: false })
  api.ops['models.presets'] = () => ({ presets: [] })
  api.ops['engines.status'] = () => ({ bots: [], host: [] })
  api.ops['bots.update'] = (input) => ({ ...BOT, ...input })
  render(
    <ToastProvider>
      <BotSettingsPage botId="b1" tab="permissions" />
    </ToastProvider>,
  )
  fireEvent.click(await screen.findByRole('switch', { name: '團隊管理權限' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'bots.update')?.input).toEqual({ id: 'b1', can_manage: true }))
})

it('限制：列出可以調的數字與預設值，改了就存；超出範圍不送出', async () => {
  const { LimitsPage } = await import('./LimitsPage')
  const values = { max_hops: 20, room_rounds: 10, bot_creates_per_day: 10, manager_bot_creates_per_day: 50, bots_max: 200, auto_continue_max: 5, agenda_open_max: 10 }
  const ranges = { max_hops: [1, 100], room_rounds: [1, 50], bot_creates_per_day: [0, 500], manager_bot_creates_per_day: [0, 500], bots_max: [1, 1000], auto_continue_max: [0, 20], agenda_open_max: [1, 50] }
  api.ops['limits.get'] = () => ({ values, defaults: values, ranges })
  api.ops['limits.set'] = (input) => ({ values: { ...values, ...input }, defaults: values, ranges })
  render(
    <ToastProvider>
      <LimitsPage />
    </ToastProvider>,
  )
  const hops = await screen.findByRole('textbox', { name: 'Bot 之間同一條往來' })
  expect(hops).toHaveValue('20')
  fireEvent.change(hops, { target: { value: '30' } })
  fireEvent.blur(hops)
  await waitFor(() => expect(api.calls.find((c) => c.path === 'limits.set')?.input).toEqual({ max_hops: 30 }))
  const n = api.calls.length
  fireEvent.change(hops, { target: { value: '999' } })
  fireEvent.blur(hops)
  expect(api.calls.length).toBe(n)
})
