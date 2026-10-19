import { expect, test } from './fixtures'
import { MockServer, settle } from './mock'

// X05 快判：J10 設定頁（層級、逐項開關、每日上限、今天的用量、影子一致率）與 C15 先判斷列（「不對？」改正）。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

const USES = [
  [1, 'route', '對話分流', 'tier1', 300, 'shadow'],
  [2, 'task_done', '任務完成了沒', 'tier1', 1000, 'shadow'],
  [3, 'approval', '要不要找你核定', 'tier1', 500, 'shadow'],
  [7, 'responder', '交給哪個 Bot', 'tier1', 1500, 'live'],
  [8, 'summary', '一行摘要', 'tier2', 2000, 'off'],
  [10, 'title', '對話串命名', 'tier2', 1000, 'off'],
  [13, 'precheck_clarity', '要不要先問清楚', 'tier1', 500, 'shadow'],
] as const

function judgeState() {
  const st = {
    settings: {
      tier1: { provider: 'opencode-zen', model: 'jev-1.13' },
      tier2: { provider: 'opencode-go', model: 'deepseek-v4-flash' },
      local: { provider: '', model: '' },
      modes: Object.fromEntries(USES.map((u) => [u[1], u[5]])) as Record<string, string>,
      daily_cap: 300,
      tier2_cap: 100,
    },
  }
  const view = () => ({
    settings: st.settings,
    uses: USES.map(([n, id, name, tier, wait]) => ({ id, n, name, desc: '說明', tier, wait_ms: wait, wired: id !== 'approval', default_mode: 'shadow', mode: st.settings.modes[id] })),
    today: { calls: 128, tier2_calls: 23, daily_cap: st.settings.daily_cap, tier2_cap: 100, capped: false, cost_micros: 5400, requests: 128, input_tokens: 120000, output_tokens: 900 },
    min_p: 0.6,
    problems: st.settings.tier1.provider === 'opencode-zen' ? [{ kind: 'no_funds', provider: 'opencode-zen', message: 'OpenCode Zen 餘額不足（Jev 需要 Zen 儲值）' }] : [],
  })
  return { st, view }
}

test('J10 快判設定：逐項切換影子／生效、改每日上限、看一致率', async ({ page }, info) => {
  const api = new MockServer(page)
  const { st, view } = judgeState()
  Object.assign(api.override, {
    'models.get': () => ({
      providers: [
        { id: 'opencode-zen', label: 'OpenCode Zen', base_url: 'https://opencode.ai/zen/v1', preset: 'opencode-zen', api_key_set: true, models: ['gpt-5.4-nano'] },
        { id: 'opencode-go', label: 'OpenCode Go', base_url: 'https://opencode.ai/zen/go/v1', preset: 'opencode-go', api_key_set: true, models: ['deepseek-v4-flash', 'glm-5.3-flash'] },
      ],
      default: { provider: 'opencode-go', model: 'deepseek-v4-flash' },
      ready: true,
    }),
    'models.presets': () => ({ presets: [] }),
    'judge.settings_get': () => view(),
    'judge.settings_set': (i: Record<string, unknown>) => {
      if (i.modes) Object.assign(st.settings.modes, i.modes)
      if (typeof i.daily_cap === 'number') st.settings.daily_cap = i.daily_cap
      if (i.tier1) st.settings.tier1 = i.tier1 as typeof st.settings.tier1
      if (i.tier2) st.settings.tier2 = i.tier2 as typeof st.settings.tier2
      return view()
    },
    'judge.stats': () => ({
      days: 7,
      today: view().today,
      recent: [],
      uses: USES.map(([n, id, name]) => ({
        use: id, n, name, mode: st.settings.modes[id], runs: id === 'task_done' ? 64 : 0, calls: id === 'task_done' ? 64 : 0, answered: id === 'task_done' ? 60 : 0,
        agreed: id === 'task_done' ? 58 : 0, used_by_model: 0, failed: 0, corrected: id === 'task_done' ? 20 : 0, corrected_model: id === 'task_done' ? 20 : 0,
        corrected_rule: 12, avg_latency_ms: 420, agreement: id === 'task_done' ? 58 / 60 : -1, corrected_agreement: id === 'task_done' ? 1 : -1, ready_for_live: id === 'task_done',
      })),
    }),
  })
  await api.install()
  await page.goto('/settings/models/judge')
  const m = page.getByRole('main')
  await expect(m.getByRole('switch', { name: '快判' })).toHaveAttribute('aria-checked', 'true')
  await expect(m.getByText('今天 128／300 次 · $0.0054')).toBeVisible()

  // Zen 沒儲值：顯示餘額不足，一鍵改用預設模型。
  await expect(m.getByText('OpenCode Zen 餘額不足（Jev 需要 Zen 儲值）；先照規則。')).toBeVisible()
  await m.getByRole('button', { name: '改用 deepseek-v4-flash' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'judge.settings_set')?.input).toEqual({ tier1: { provider: 'opencode-go', model: 'deepseek-v4-flash' } })
  await expect(m.getByText(/餘額不足/)).toHaveCount(0)

  // 細節收在「進階」。
  await expect(m.getByTestId('judge-use-task_done')).toHaveCount(0)
  await m.getByRole('button', { name: '進階' }).click()
  const done = m.getByTestId('judge-use-task_done')
  await expect(done).toContainText('和你一致 100%')
  await expect(done).toContainText('可以生效')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-j10-fastjudge`), fullPage: true })

  // 逐項切換：立刻送出。
  await done.getByRole('radio', { name: '生效' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'judge.settings_set').at(-1)?.input).toEqual({ modes: { task_done: 'live' } })
  await expect(done.getByRole('radio', { name: '生效' })).toHaveAttribute('aria-checked', 'true')

  // 每日上限。
  const cap = m.getByLabel('每日呼叫上限')
  await cap.fill('150')
  await m.getByRole('button', { name: '儲存' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'judge.settings_set').at(-1)?.input).toEqual({ daily_cap: 150 })

  // 關掉：全部照規則。
  await m.getByRole('switch', { name: '快判' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'judge.settings_set').at(-1)?.input).toEqual({ tier1: { provider: '', model: '' }, tier2: { provider: '', model: '' } })
  await expect(m.getByRole('switch', { name: '快判' })).toHaveAttribute('aria-checked', 'false')
})

// 設定的供應商不存在（例：選了 Zen 的 Jev，但只有 Go 的金鑰）：提醒並一鍵改用預設模型。
test('J10 快判：供應商不存在時提醒', async ({ page }) => {
  const api = new MockServer(page)
  const { st, view } = judgeState()
  Object.assign(api.override, {
    'models.get': () => ({ providers: [{ id: 'openai', base_url: 'https://opencode.ai/zen/go/v1', api_key_set: true }], default: { provider: 'openai', model: 'deepseek-v4-flash' }, ready: true }),
    'models.presets': () => ({ presets: [] }),
    'judge.settings_get': () => ({ ...view(), problems: [] }),
    'judge.settings_set': (i: Record<string, unknown>) => {
      Object.assign(st.settings, i)
      return { ...view(), problems: [] }
    },
    'judge.stats': () => ({ days: 7, today: view().today, recent: [], uses: [] }),
  })
  await api.install()
  await page.goto('/settings/models/judge')
  const m = page.getByRole('main')
  await expect(m.getByText('opencode-zen 還沒設定，快判現在只照規則。')).toBeVisible()
  await m.getByRole('button', { name: '改用 deepseek-v4-flash' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'judge.settings_set')?.input).toEqual({ tier1: { provider: 'openai', model: 'deepseek-v4-flash' }, tier2: { provider: 'openai', model: 'deepseek-v4-flash' } })
  await expect(m.getByText(/還沒設定/)).toHaveCount(0)
})

test('C15 先判斷：訊息下方出現膠囊，「不對？」可改正', async ({ page }, info) => {
  const api = new MockServer(page)
  const bot = api.addBot({ name: '小秘' })
  Object.assign(api.override, {
    'judge.precheck': () => ({
      chips: [
        { use: 'precheck_clarity', n: 13, label: 'clear', shown: '夠清楚', p: 0.96, by: 'model', options: [{ key: 'missing', shown: '缺資訊 · 先問清楚' }] },
        { use: 'precheck_computer', n: 12, label: 'web', shown: '不用開電腦 · 先查網路', p: 0.93, by: 'model', options: [{ key: 'browser', shown: '要開電腦 · 用瀏覽器' }] },
        { use: 'precheck_engine', n: 11, label: 'hermes_medium', shown: '交給 Hermes · 中', p: 0.93, by: 'model', options: [{ key: 'codex_high', shown: '交給 Codex · 高' }] },
        { use: 'precheck_cost', n: 14, label: 'small', shown: '預估 小（< 2 萬 token）', p: 0.93, by: 'model', options: [{ key: 'large', shown: '預估 大' }] },
      ],
    }),
    'judge.feedback': (i: Record<string, unknown>) => ({ correction: { id: 'c1', ...i } }),
  })
  await api.install()
  await page.goto(`/bots/${bot.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  const box = main.getByRole('textbox')
  await box.fill('幫我查這週台北飛東京最便宜的是哪一天')
  await box.press('Enter')
  const s = api.lastSend!
  await api.emit({ bot_id: s.botId, thread_id: s.threadId, task_id: s.taskId, kind: 'message.created', payload: api.storeMessage(s.threadId, api.messages[s.threadId][0]) })
  const row = main.getByTestId('precheck-row')
  await expect(row).toContainText('夠清楚')
  await expect(row).toContainText('不用開電腦 · 先查網路')
  await expect(row).toContainText('96%')
  expect(api.calls.find((c) => c.op === 'judge.precheck')?.input).toEqual({ bot_id: bot.id, text: '幫我查這週台北飛東京最便宜的是哪一天', task_id: s.taskId })
  await settle(page)
  await row.screenshot({ path: shot(`${info.project.name}-c15-precheck`) })

  await row.getByTestId('precheck-precheck_computer').getByRole('button', { name: '不對？' }).click()
  await page.getByRole('menuitem', { name: '要開電腦 · 用瀏覽器' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'judge.feedback')?.input).toEqual({
    use: 'precheck_computer', task_id: s.taskId, judged: 'web', judged_by: 'model', corrected: 'browser',
  })
  await expect(row).toContainText('已記下：要開電腦 · 用瀏覽器')
})
