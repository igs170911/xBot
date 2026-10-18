import { expect, test, type Page } from '@playwright/test'
import { MockServer, settle } from './mock'

// W12 設定：用假服務測一般／AI 模型／用量與花費／Bot 的引擎與權限，截圖和 J01～J04、G09、L06 畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`
const DAY = 86_400_000

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

type Provider = { id: string; label?: string; base_url: string; preset?: string; api_key_set: boolean; auth?: string; models?: string[]; probed_at?: number }

function day(offset: number) {
  const d = new Date(Date.now() - offset * DAY)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function row(key: string, cost: number, calls: number, label?: string) {
  return { key, label, calls, input_tokens: calls * 9000, output_tokens: calls * 700, cache_read_tokens: calls * 5200, cache_write_tokens: 0, cache_hit_rate: 0.58, cost_micros: Math.round(cost * 1e6), cost_usd: cost, unpriced_calls: 0, fallback_calls: key.includes('grok') ? 3 : 0 }
}

function cap(key: string, label: string, daily: number, monthly: number, today: number, month: number) {
  return { key, label, daily_usd: daily, monthly_usd: monthly, spent_today_usd: today, spent_month_usd: month, exceeded: monthly > 0 && month >= monthly, daily_micros: 0, monthly_micros: 0, spent_day_micros: 0, spent_month_micros: 0 }
}

/** 設定頁要的操作。 */
function seed(api: MockServer) {
  const xm = api.addBot({ name: '小秘', title: '幕僚長', shape: 'cursor', color: 'blue' })
  const az = api.addBot({ name: '阿哲', shape: 'blob', color: 'orange' })
  api.addBot({ name: '小編', shape: 'circle', color: 'pink' })
  const state = {
    providers: [
      { id: 'openai-api', label: 'OpenAI API', base_url: 'https://api.openai.com/v1', preset: 'openai-api', api_key_set: true, models: ['gpt-5.5', 'gpt-5.5-mini', 'gpt-4.1-mini'], probed_at: Date.now() - DAY },
      { id: 'opencode-go', label: 'OpenCode Go', base_url: 'https://opencode.ai/zen/go/v1', preset: 'opencode-go', api_key_set: true, models: ['deepseek-v4-flash', 'qwen3.8-flash'], probed_at: 0 },
    ] as Provider[],
    def: { provider: 'openai-api', model: 'gpt-5.5-mini' } as { provider: string; model: string } | undefined,
    fallback: { provider: 'opencode-go', model: 'deepseek-v4-flash' } as { provider: string; model: string } | undefined,
    linked: {} as Record<string, string>,
    oauthPolls: 0,
    caps: {
      global: cap('all', '全部 Bot', 0, 100, 2.86, 42.1),
      bots: [cap(az.id, '阿哲', 2, 20, 1.1, 17.4), cap(xm.id, '小秘', 0, 30, 0.9, 10.6)],
      providers: [cap('opencode-go', 'OpenCode Go', 1, 0, 0.42, 3.2)],
    },
  }
  const presets = [
    { id: 'openrouter', label: 'OpenRouter', base_url: 'https://openrouter.ai/api/v1', transport: 'chat_completions', local: false, models: ['anthropic/claude-sonnet-5.5', 'x-ai/grok-5'], default_model: 'anthropic/claude-sonnet-5.5', configured: false, signup_url: 'https://openrouter.ai/keys' },
    { id: 'openai-api', label: 'OpenAI API', base_url: 'https://api.openai.com/v1', transport: 'chat_completions', local: false, models: ['gpt-5.5', 'gpt-5.5-mini'], default_model: 'gpt-5.5-mini', configured: true },
    { id: 'opencode-go', label: 'OpenCode Go', base_url: 'https://opencode.ai/zen/go/v1', transport: 'chat_completions', local: false, models: ['deepseek-v4-flash'], default_model: 'deepseek-v4-flash', configured: true },
    { id: 'lmstudio', label: 'LM Studio', base_url: 'http://127.0.0.1:1234/v1', transport: 'chat_completions', local: true, models: [], configured: false },
  ]
  const models = () => ({ providers: state.providers, default: state.def, fallback: state.fallback, ready: !!state.def })
  const engineRow = (b: { id: string; name: string; engine: string }) => ({
    bot_id: b.id,
    name: b.name,
    engine: b.engine,
    engine_label: b.engine,
    needs_link: b.engine !== 'hermes',
    linked: !!state.linked[b.id],
    linked_engine: state.linked[b.id],
    marker: state.linked[b.id] ? '這個 Bot 用你的 Claude 訂閱' : undefined,
  })
  const days = Array.from({ length: 30 }, (_, i) => row(day(29 - i), i % 6 === 0 ? 0 : 0.4 + ((i * 37) % 23) / 10, 10 + ((i * 13) % 40)))

  Object.assign(api.override, {
    'models.get': () => models(),
    'models.presets': () => ({ presets }),
    'models.set_default': (i: Record<string, unknown>) => {
      state.def = { provider: i.provider as string, model: i.model as string }
      return state.def
    },
    'models.set_fallback': (i: Record<string, unknown>) => {
      state.fallback = i.provider ? { provider: i.provider as string, model: i.model as string } : undefined
      return { fallback: state.fallback }
    },
    'models.add_provider': (i: Record<string, unknown>) => {
      const pr = presets.find((p) => p.id === i.preset)
      const p: Provider = { id: (i.id as string) || (i.preset as string), label: pr?.label, base_url: pr?.base_url ?? (i.base_url as string), preset: i.preset as string, api_key_set: !!i.api_key, models: pr?.models, probed_at: 0 }
      state.providers.push(p)
      return { provider: p, default: state.def }
    },
    'models.probe': (i: Record<string, unknown>) => {
      const p = state.providers.find((x) => x.id === i.provider)!
      p.probed_at = Date.now()
      const ok = p.id !== 'opencode-go'
      return {
        provider: p.id,
        models: p.models ?? [],
        check: ok
          ? { model: (i.model as string) || p.models?.[0] || 'x', status: 'ok', transport: 'chat_completions', message: '可以用', at: Date.now() }
          : { model: 'deepseek-v4-flash', status: 'auth', message: '金鑰不對：供應商回 401', at: Date.now() },
        default_updated: false,
        at: Date.now(),
      }
    },
    'models.remove_provider': (i: Record<string, unknown>) => {
      state.providers = state.providers.filter((p) => p.id !== i.id)
      if (state.fallback?.provider === i.id) state.fallback = undefined
      return { id: i.id, removed: true, affected_bots: [], cleared_fallback: true }
    },
    'models.oauth_start': () => ({ login_id: 'lg1', user_code: 'WXYZ-1234', verification_url: 'https://auth.openai.com/codex/device', interval_seconds: 1, expires_at: Date.now() + 600_000 }),
    'models.oauth_complete': () => {
      state.oauthPolls += 1
      if (state.oauthPolls < 2) return { status: 'pending' }
      const p: Provider = { id: 'openai-codex', label: 'ChatGPT 方案', base_url: 'https://chatgpt.com/backend-api/codex', api_key_set: false, auth: 'oauth_codex', models: ['gpt-5.5'] }
      state.providers.push(p)
      return { status: 'done', provider: p }
    },
    'models.pricing': () => ({ prices: [], unpriced: [{ provider: 'opencode-go', model: 'qwen3.8-flash', calls: 12, last_at: Date.now() }] }),
    'models.set_price': (i: Record<string, unknown>) => ({ model: i.model, input: i.input, output: i.output, cache_read: 0, cache_write: 0, source: 'custom' }),
    'usage.summary': () => ({
      since: Date.now() - 30 * DAY,
      until: Date.now(),
      total: row('total', 42.1, 1180),
      by_bot: [row(az.id, 17.4, 420, '阿哲'), row(xm.id, 10.6, 380, '小秘'), row('b3', 6.2, 210, '小編')],
      by_provider: [],
      by_model: [row('openai-api/gpt-5.5', 24.5, 400), row('openai-api/gpt-5.5-mini', 12.4, 600), row('opencode-go/deepseek-v4-flash', 5.2, 180)],
      by_day: days,
      unpriced: [],
    }),
    'usage.caps': () => ({ day: day(0), month: day(0).slice(0, 7), ...state.caps }),
    'usage.set_caps': (i: Record<string, unknown>) => {
      const list = i.bot_id ? state.caps.bots : i.provider ? state.caps.providers : null
      const key = (i.bot_id ?? i.provider ?? 'all') as string
      let c = list ? list.find((x) => x.key === key) : state.caps.global
      if (!c && list) {
        c = cap(key, key, 0, 0, 0, 0)
        list.push(c)
      }
      if (i.daily_usd !== undefined) c!.daily_usd = i.daily_usd as number
      if (i.monthly_usd !== undefined) c!.monthly_usd = i.monthly_usd as number
      return c
    },
    'usage.export': () => ({ filename: 'xbot-usage.csv', csv: 'time,bot,model,cost_usd\n2026-10-18T09:00:00Z,小秘,gpt-5.5,0.01\n', rows: 1, truncated: false }),
    'engines.status': (i: Record<string, unknown>) => ({
      bots: api.bots.filter((b) => !i.bot_id || b.id === i.bot_id).map(engineRow),
      host: [
        { engine: 'claude-code', label: 'Claude Code', plan: 'Max 方案', logged_in: true, login: 'claude login' },
        { engine: 'codex', label: 'Codex', plan: '', logged_in: false, login: 'codex login' },
      ],
    }),
    'engines.link_credentials': (i: Record<string, unknown>) => {
      state.linked[i.bot_id as string] = i.engine as string
      return engineRow(api.bots.find((b) => b.id === i.bot_id)!)
    },
    'engines.unlink_credentials': (i: Record<string, unknown>) => {
      delete state.linked[i.bot_id as string]
      return engineRow(api.bots.find((b) => b.id === i.bot_id)!)
    },
  })
  return { xm, az, state }
}

async function open(page: Page, path: string) {
  const api = new MockServer(page)
  const s = seed(api)
  await api.install()
  await page.goto(path)
  return { api, ...s }
}

const main = (page: Page) => page.getByRole('main')

test('一般：主題切換立刻生效、重新整理後還在', async ({ page }, info) => {
  await open(page, '/settings/general')
  await expect(main(page).getByRole('heading', { name: '一般', level: 2 })).toBeVisible()
  if (info.project.name === 'desktop') {
    const nav = page.getByRole('navigation', { name: '設定分類' })
    await expect(nav.getByRole('link')).toHaveText(['一般', 'AI 模型', '電腦與裝置', '用量與花費', '進階', '備份與更新', '成員與紀錄', '技能庫與實驗', '網路與 Tailscale', '外部代理與 MCP'])
    await expect(nav.getByRole('link', { name: '一般' })).toHaveAttribute('aria-current', 'page')
  }
  const themes = page.getByRole('radiogroup', { name: '主題' })
  await expect(themes.getByRole('radio', { name: /Midnight/ })).toHaveAttribute('aria-checked', 'true')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w12-general`), fullPage: true })

  await themes.getByRole('radio', { name: /Daylight/ }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await expect(page.getByRole('radiogroup', { name: '主題' }).getByRole('radio', { name: /Daylight/ })).toHaveAttribute('aria-checked', 'true')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w12-general-light`), fullPage: true })
  await page.getByRole('radiogroup', { name: '主題' }).getByRole('radio', { name: /Midnight/ }).click()
  await expect(page.locator('html')).not.toHaveAttribute('data-theme', 'light')
})

test('AI 模型：探測、貼金鑰認出供應商、換預設模型、移除要確認', async ({ page }, info) => {
  const { api } = await open(page, '/settings/models')
  const m = main(page)
  const list = m.getByRole('group', { name: '已設定的供應商' })
  await expect(list.getByText('OpenAI API')).toBeVisible()
  await expect(m.getByText('Claude Code', { exact: true })).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w12-models`), fullPage: true })

  // 探測：成功與失敗都顯示結果
  await list.getByRole('button', { name: '探測 OpenAI API' }).click()
  await expect(list.getByRole('status').filter({ hasText: '能用' })).toContainText('Chat Completions')
  await list.getByRole('button', { name: '探測 OpenCode Go' }).click()
  await expect(list.getByText('金鑰不對：供應商回 401')).toBeVisible()

  // 貼金鑰 → 認出 OpenRouter → 加入並探測
  await m.getByRole('textbox', { name: 'API 金鑰' }).fill('sk-or-v1-abcdef0123456789abcdef3f9a')
  await expect(m.getByText(/認出是 OpenRouter/)).toBeVisible()
  await m.getByRole('button', { name: '加入並探測' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'models.add_provider')?.input).toMatchObject({ preset: 'openrouter', api_key: 'sk-or-v1-abcdef0123456789abcdef3f9a' })
  await expect(list.getByText('OpenRouter')).toBeVisible()
  await expect.poll(() => api.calls.filter((c) => c.op === 'models.probe').at(-1)?.input).toMatchObject({ provider: 'openrouter' })

  // 預設模型：換模型就存
  await m.getByRole('button', { name: /預設模型：模型/ }).click()
  await page.getByRole('option', { name: 'gpt-5.5', exact: true }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'models.set_default')?.input).toEqual({ provider: 'openai-api', model: 'gpt-5.5' })

  // 移除：確認對話框
  await list.getByRole('button', { name: 'OpenCode Go 的更多動作' }).click()
  await page.getByRole('menuitem', { name: '移除' }).click()
  const dlg = page.getByRole('alertdialog', { name: '移除 OpenCode Go？' })
  await expect(dlg).toContainText('無法復原')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w12-remove-confirm`) })
  await dlg.getByRole('button', { name: '移除' }).click()
  await expect(dlg).toBeHidden()
  await expect(list.getByText('OpenCode Go')).toBeHidden()
  expect(api.calls.filter((c) => c.op === 'models.remove_provider')).toHaveLength(1)
})

test('ChatGPT 方案登入：說明條款風險，直接走裝置代碼（使用者決定正式使用）', async ({ page }, info) => {
  const { api } = await open(page, '/settings/models')
  const m = main(page)
  const block = m.getByRole('region', { name: '用 ChatGPT 方案登入' })
  await block.getByRole('button', { name: '登入' }).click()
  const dlg = page.getByRole('dialog', { name: /ChatGPT 方案登入/ })
  await expect(dlg).toContainText('使用條款')
  await dlg.getByRole('button', { name: '開始登入' }).click()
  await expect(dlg.getByTestId('oauth-code')).toHaveText('WXYZ-1234')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w12-oauth`) })
  await expect(dlg).toContainText('登入完成', { timeout: 10_000 })
  expect(api.calls.filter((c) => c.op === 'models.oauth_complete').length).toBeGreaterThanOrEqual(2)
  await dlg.getByRole('button', { name: '完成' }).click()
  await expect(m.getByRole('group', { name: '已設定的供應商' }).getByText('ChatGPT 方案')).toBeVisible()
})

test('進階：備援模型可以設定或關掉', async ({ page }) => {
  const { api } = await open(page, '/settings/models/routing')
  const m = main(page)
  await expect(m.getByText('一般人不用動')).toBeVisible()
  await m.getByRole('button', { name: /備援模型：供應商/ }).click()
  await page.getByRole('option', { name: '不用備援' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'models.set_fallback')?.input).toEqual({})
})

test('用量與花費：圖表、沒價格的模型、上限（提高要確認）、匯出 CSV', async ({ page }, info) => {
  const { api, az } = await open(page, '/settings/usage')
  const m = main(page)
  await expect(m.getByRole('img', { name: '最近 30 天每天的花費' })).toBeVisible()
  await expect(m.getByRole('list', { name: '各 Bot 的花費' })).toContainText('阿哲')
  await expect(m.getByText('US$42.10').first()).toBeVisible()
  await expect(m.getByRole('group', { name: '每個 Bot 的上限' }).getByText('接近上限')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w12-usage`), fullPage: true })

  // 沒有價格 → 設定價格
  await m.getByRole('button', { name: /opencode-go\/qwen3.8-flash/ }).click()
  const dlg = page.getByRole('dialog', { name: '設定 opencode-go/qwen3.8-flash 的價格' })
  await dlg.getByLabel('輸入（US$）').fill('0.2')
  await dlg.getByLabel('輸出（US$）').fill('0.8')
  await dlg.getByRole('button', { name: '儲存' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'models.set_price')?.input).toMatchObject({ model: 'opencode-go/qwen3.8-flash', input: 0.2, output: 0.8 })

  // 降低直接生效
  const day = m.getByLabel('阿哲每日上限')
  await day.fill('1.5')
  await day.press('Enter')
  await expect.poll(() => api.calls.find((c) => c.op === 'usage.set_caps')?.input).toEqual({ bot_id: az.id, daily_usd: 1.5 })
  // 提高要確認
  const cur = m.getByLabel('OpenCode Go每日上限')
  await cur.fill('5')
  await cur.press('Enter')
  const confirm = page.getByRole('alertdialog', { name: '提高上限？' })
  await expect(confirm).toContainText('不能交給別人代為核准')
  await confirm.getByRole('button', { name: '提高' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'usage.set_caps').at(-1)?.input).toEqual({ provider: 'opencode-go', daily_usd: 5 })

  // 匯出
  const download = page.waitForEvent('download')
  await m.getByRole('button', { name: '匯出 CSV' }).click()
  expect((await download).suggestedFilename()).toBe('xbot-usage.csv')
})

test('Bot 的引擎：換 Claude Code、同意使用訂閱並標示、權限等級白話', async ({ page }, info) => {
  const { api, xm } = await open(page, '/settings/models/bots')
  await main(page).getByRole('link', { name: /小秘/ }).click()
  await expect(page).toHaveURL(new RegExp(`/settings/bots/${xm.id}$`))
  const m = main(page)
  // 12 個分頁 → 4 個（G02）：引擎與模型在「進階」，權限在「權限與花費」
  await m.getByRole('navigation', { name: 'Bot 設定分頁' }).getByRole('link', { name: '進階' }).click()
  await expect(page).toHaveURL(new RegExp(`/settings/bots/${xm.id}/advanced$`))
  await m.getByRole('radiogroup', { name: '引擎' }).getByRole('radio', { name: 'Claude Code' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'bots.update')?.input).toEqual({ id: xm.id, engine: 'claude-code' })
  await m.getByRole('button', { name: '同意使用我的訂閱' }).click()
  const dlg = page.getByRole('dialog', { name: '讓小秘用你的 Claude 訂閱？' })
  await expect(dlg).toContainText('唯讀')
  await expect(dlg).toContainText('隨時可以收回')
  await expect(dlg.getByRole('button', { name: '同意' })).toBeDisabled()
  await dlg.getByText('我同意讓小秘使用我的 Claude 訂閱').click()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w12-consent`) })
  await dlg.getByRole('button', { name: '同意' }).click()
  await expect(m.getByText('這個 Bot 用你的 Claude 訂閱').first()).toBeVisible()
  expect(api.calls.find((c) => c.op === 'engines.link_credentials')?.input).toEqual({ bot_id: xm.id, engine: 'claude-code' })

  await m.getByRole('navigation', { name: 'Bot 設定分頁' }).getByRole('link', { name: '權限與花費' }).click()
  const modes = m.getByRole('radiogroup', { name: '權限等級' })
  await expect(modes.getByRole('radio')).toHaveCount(4)
  for (const name of ['先問我', '一般的事自己做', '放手做', '完全放手']) await expect(modes.getByRole('radio', { name: new RegExp(`^${name}`) })).toBeVisible()
  await expect(m.getByText(/就算選「完全放手」.*一定會先到「待我處理」/)).toBeVisible()
  await modes.getByRole('radio', { name: /^放手做/ }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'bots.update').at(-1)?.input).toEqual({ id: xm.id, approval_mode: 'auto' })
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w12-bot`), fullPage: true })

  // 收回
  await m.getByRole('navigation', { name: 'Bot 設定分頁' }).getByRole('link', { name: '進階' }).click()
  await m.getByRole('button', { name: '收回' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '收回' }).click()
  await expect.poll(() => api.calls.some((c) => c.op === 'engines.unlink_credentials')).toBe(true)
})

test('手機：設定總覽（L06）→ 點進一區 → 返回', async ({ page }, info) => {
  test.skip(info.project.name !== 'mobile', '只有手機')
  await open(page, '/settings')
  const m = main(page)
  for (const label of ['一般', 'AI 模型', '電腦與裝置', '用量與花費']) await expect(m.getByRole('heading', { name: label, exact: true })).toBeVisible()
  await expect(m.getByText('gpt-5.5-mini')).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await settle(page)
  await page.screenshot({ path: shot('mobile-w12-overview'), fullPage: true })
  await m.getByRole('link', { name: /用量與花費|本月花費/ }).click()
  await expect(page).toHaveURL(/\/settings\/usage$/)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await m.getByRole('button', { name: '返回' }).click()
  await expect(page).toHaveURL(/\/settings$/)
})

test('桌面：/settings 直接進「一般」', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop', '只有桌面')
  await open(page, '/settings')
  await expect(page).toHaveURL(/\/settings\/general$/)
})
