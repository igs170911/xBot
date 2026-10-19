import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi, opError, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { activeNav } from './nav'
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
  expect(activeNav('/settings/bots/b1')).toBe('/settings/models')
  expect(activeNav('/settings/advanced/agents')).toBe('/settings/advanced/agents')
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
  render(<GeneralSection />)
  const group = screen.getByRole('radiogroup', { name: '主題' })
  expect(within(group).getByRole('radio', { name: /Midnight/ })).toHaveAttribute('aria-checked', 'true')
  fireEvent.click(within(group).getByRole('radio', { name: /Daylight/ }))
  expect(within(group).getByRole('radio', { name: /Daylight/ })).toHaveAttribute('aria-checked', 'true')
  expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  expect(screen.getByText('粉圓', { selector: 'span.whitespace-nowrap' })).toBeInTheDocument()
  setTheme('dark')
})

const BOT = { id: 'b1', name: '小秘', title: '幕僚長', description: '', soul: '', color: 'blue', shape: 'cursor', engine: 'claude-code', model: '', fallback: '', effort: 'medium', approval_mode: 'ask', archived: false, created_at: 1, updated_at: 1 }

it('Bot 的訂閱：同意前要勾選，同意後標示用你的訂閱', async () => {
  let linked = false
  const row = () => ({ bot_id: 'b1', name: '小秘', engine: 'claude-code', engine_label: 'Claude Code', needs_link: true, linked, marker: linked ? '這個 Bot 用你的 Claude 訂閱' : undefined })
  api.ops['bots.get'] = () => BOT
  api.ops['models.presets'] = () => ({ presets: [] })
  api.ops['engines.status'] = () => ({ bots: [row()], host: [{ engine: 'claude-code', label: 'Claude Code', plan: 'Max', logged_in: true, login: 'claude login' }] })
  api.ops['engines.link_credentials'] = () => {
    linked = true
    return row()
  }
  render(
    <ToastProvider>
      <BotSettingsPage botId="b1" tab="advanced" />
    </ToastProvider>,
  )
  fireEvent.click(await screen.findByRole('button', { name: '同意使用我的訂閱' }))
  const dlg = screen.getByRole('dialog', { name: '讓小秘用你的 Claude 訂閱？' })
  const ok = within(dlg).getByRole('button', { name: '同意' })
  expect(ok).toBeDisabled()
  fireEvent.click(within(dlg).getByRole('checkbox'))
  fireEvent.click(ok)
  await waitFor(() => expect(screen.getAllByText('這個 Bot 用你的 Claude 訂閱').length).toBeGreaterThan(0))
  expect(api.calls.find((c) => c.path === 'engines.link_credentials')?.input).toEqual({ bot_id: 'b1', engine: 'claude-code' })
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

it('用訂閱的 Bot：模型列出 CLI 回報的清單，選項多時可以打字篩選', async () => {
  const models = Array.from({ length: 14 }, (_, i) => ({ value: `opencode/m${i}`, name: `Model ${i}` }))
  api.ops['bots.get'] = () => ({ ...BOT, engine: 'opencode', model: '' })
  api.ops['models.presets'] = () => ({ presets: [] })
  api.ops['engines.status'] = () => ({
    bots: [{ bot_id: 'b1', name: '小秘', engine: 'opencode', engine_label: 'OpenCode', needs_link: true, linked: true, agent_default: 'opencode/big-pickle', models }],
    host: [{ engine: 'opencode', label: 'OpenCode', plan: 'OpenCode Go', logged_in: true, login: 'opencode auth login' }],
  })
  api.ops['bots.update'] = (input) => ({ ...BOT, engine: 'opencode', ...input })
  render(
    <ToastProvider>
      <BotSettingsPage botId="b1" tab="advanced" />
    </ToastProvider>,
  )
  fireEvent.click(await screen.findByRole('button', { name: /這個 Bot 的模型/ }))
  const find = await screen.findByRole('textbox', { name: '找這個 Bot 的模型' })
  fireEvent.change(find, { target: { value: 'model 13' } })
  const list = screen.getByRole('listbox')
  expect(within(list).getAllByRole('option')).toHaveLength(1)
  fireEvent.click(within(list).getByRole('option', { name: /Model 13/ }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'bots.update')?.input).toEqual({ id: 'b1', model: 'opencode/m13' }))
})

it('用訂閱、CLI 還沒回報模型：可以直接輸入名稱', async () => {
  api.ops['bots.get'] = () => ({ ...BOT, engine: 'grok', model: '' })
  api.ops['models.presets'] = () => ({ presets: [] })
  api.ops['engines.status'] = () => ({ bots: [{ bot_id: 'b1', name: '小秘', engine: 'grok', engine_label: 'Grok', needs_link: true, linked: true }], host: [] })
  api.ops['bots.update'] = (input) => ({ ...BOT, engine: 'grok', ...input })
  render(
    <ToastProvider>
      <BotSettingsPage botId="b1" tab="advanced" />
    </ToastProvider>,
  )
  const box = await screen.findByRole('textbox', { name: '這個 Bot 的模型' })
  fireEvent.change(box, { target: { value: 'grok-5' } })
  fireEvent.blur(box)
  await waitFor(() => expect(api.calls.find((c) => c.path === 'bots.update')?.input).toEqual({ id: 'b1', model: 'grok-5' }))
})
