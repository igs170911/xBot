import { expect, test } from '@playwright/test'
import { MockServer, settle } from './mock'

// E31（EN-7）：引擎額度用完卡（design/C09「額度用完 · 換一個模型繼續」）在待我處理與對話裡；
// Bot 設定的引擎選擇器列出 Gemini CLI、OpenCode、Grok 與這台電腦的登入狀態。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

const reset = () => {
  const d = new Date()
  d.setHours(d.getHours() + 3, 20, 0, 0)
  return d.getTime()
}
// 和畫面一樣：今天的只寫時間，跨日加上月/日。
const hhmm = (ms: number) => {
  const d = new Date(ms)
  const t = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  return new Date().toDateString() === d.toDateString() ? t : `${d.getMonth() + 1}/${d.getDate()} ${t}`
}

function quota(threadId: string, inboxId: string, at: number) {
  return {
    inbox_id: inboxId,
    engine: 'claude-code',
    kind: 'quota',
    reset_at: at,
    message: "You've hit your limit · resets 2pm",
    label: 'Claude Code',
    bot_name: '小秘',
    thread_id: threadId,
    task_id: 'task-q',
    retry_text: '整理 10 月報表',
    options: [
      { engine: 'codex', label: 'Codex CLI（ChatGPT 方案）', note: '要你同意使用訂閱', needs_link: true },
      { engine: 'grok', label: 'Grok（Grok 方案）', note: '已登入' },
      { engine: 'hermes', model: 'opencode-go/deepseek-v4.1-flash', label: 'Hermes · deepseek-v4.1-flash（OpenCode Go）', note: '用 AI 模型設定' },
    ],
  }
}

function seed(api: MockServer) {
  const xm = api.addBot({ name: '小秘', shape: 'circle', color: 'pink', engine: 'claude-code' })
  const t = api.addThread(xm.id, '10 月報表')
  const at = reset()
  const q = quota(t.id, 'iq', at)
  api.addMessage(t.id, { sender: 'owner', content: '整理 10 月報表', task_id: 'task-q' })
  api.addMessage(t.id, { sender: `bot:${xm.id}`, content: `Claude Code 的額度用完了（約 ${hhmm(at)} 恢復）。可以換成你有的其他引擎或模型繼續，或等額度恢復再請它做。`, task_id: 'task-q', status: 'error', quota: q })
  api.inbox = [
    { id: 'iq', kind: 'engine_quota', group: 'decide', bot_id: xm.id, task_id: 'task-q', thread_id: t.id, title: `Claude Code 的額度用完了（約 ${hhmm(at)} 恢復）`, body: '', payload: q, status: 'open', read: false, created_at: Date.now() - 60_000 },
  ]
  api.override['engines.switch'] = (i) => {
    api.inbox = api.inbox.filter((x) => x.id !== i.inbox_id)
    const b = api.bots.find((x) => x.id === i.bot_id)!
    b.engine = i.engine as string
    return { bot: { bot_id: b.id, name: b.name, engine: b.engine, engine_label: b.engine, needs_link: true, linked: true }, model: i.model ?? '', task_id: i.resend ? 'task-r' : undefined, thread_id: i.resend ? t.id : undefined }
  }
  api.override['inbox.dismiss'] = (i) => {
    api.inbox = api.inbox.filter((x) => x.id !== i.inbox_id)
    return { item: { id: i.inbox_id } }
  }
  return { xm, t, at }
}

test('待我處理：額度用完卡 → 同意後換到 Codex 並重送', async ({ page }, info) => {
  const api = new MockServer(page)
  const { xm, at } = seed(api)
  await api.install()
  await page.goto('/')
  const card = page.getByTestId('inbox-item').filter({ has: page.getByTestId('quota-card') })
  await expect(card.getByRole('heading')).toHaveText(`Claude Code 的額度用完了（約 ${hhmm(at)} 恢復），要換哪個繼續？`)
  const opts = card.getByRole('group', { name: '換哪個繼續' }).getByRole('button')
  await expect(opts).toHaveCount(4)
  await expect(opts.last()).toContainText('等額度恢復再繼續')
  await expect(opts.last()).toContainText(hhmm(at))
  await settle(page)
  await card.screenshot({ path: shot(`${info.project.name}-e31-quota-inbox`) })

  await opts.filter({ hasText: 'Codex CLI' }).click()
  const dlg = page.getByRole('dialog', { name: /讓小秘改用 Codex CLI/ })
  await expect(dlg.getByRole('button', { name: '同意並換過去' })).toBeDisabled()
  await dlg.getByText('我同意讓小秘使用我的訂閱').click()
  await dlg.getByRole('button', { name: '同意並換過去' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'engines.switch')?.input).toEqual({
    bot_id: xm.id,
    engine: 'codex',
    model: '',
    link: true,
    inbox_id: 'iq',
    resend: true,
  })
  await expect(page.getByTestId('inbox-empty')).toBeVisible()
})

test('對話裡：額度用完卡 → 換到 Hermes 的模型；等額度恢復收掉卡', async ({ page }, info) => {
  const api = new MockServer(page)
  const { xm, t } = seed(api)
  await api.install()
  await page.goto(`/bots/${xm.id}/threads/${t.id}`)
  const card = page.getByRole('main').getByTestId('quota-card')
  await expect(card).toBeVisible()
  await expect(page.getByTestId('outcome')).toHaveCount(0)
  await settle(page)
  await card.screenshot({ path: shot(`${info.project.name}-e31-quota-chat`) })
  await card.getByRole('button', { name: /Hermes · deepseek/ }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'engines.switch')?.input).toEqual({
    bot_id: xm.id,
    engine: 'hermes',
    model: 'opencode-go/deepseek-v4.1-flash',
    inbox_id: 'iq',
    resend: true,
  })
  await expect(page.getByTestId('quota-done')).toContainText('已換成 Hermes · deepseek-v4.1-flash（OpenCode Go），重新送出')
})

test('對話裡：等額度恢復', async ({ page }) => {
  const api = new MockServer(page)
  const { xm, t, at } = seed(api)
  await api.install()
  await page.goto(`/bots/${xm.id}/threads/${t.id}`)
  await page.getByTestId('quota-card').getByRole('button', { name: /等額度恢復再繼續/ }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'inbox.dismiss')?.input).toEqual({ inbox_id: 'iq' })
  await expect(page.getByTestId('quota-done')).toContainText(`等額度恢復（約 ${hhmm(at)}）`)
  expect(api.calls.some((c) => c.op === 'engines.switch')).toBe(false)
})

test('Bot 設定：引擎選擇器列出 Gemini CLI、OpenCode、Grok 與登入狀態，換 Gemini 要同意 Google 訂閱', async ({ page }, info) => {
  const api = new MockServer(page)
  const xm = api.addBot({ name: '小秘', shape: 'circle', color: 'pink' })
  let linked = ''
  api.override['engines.status'] = () => ({
    bots: [{ bot_id: xm.id, name: xm.name, engine: xm.engine, engine_label: xm.engine, needs_link: xm.engine !== 'hermes', linked: !!linked, linked_engine: linked || undefined }],
    host: [
      { engine: 'claude-code', label: 'Claude Code', plan: 'Claude', logged_in: true, login: 'claude' },
      { engine: 'codex', label: 'Codex CLI', plan: 'ChatGPT', logged_in: false, login: 'codex login' },
      { engine: 'gemini', label: 'Gemini CLI', plan: 'Google', logged_in: true, login: 'gemini' },
      { engine: 'opencode', label: 'OpenCode', plan: 'OpenCode Go', logged_in: true, login: 'opencode auth login' },
      { engine: 'grok', label: 'Grok', plan: 'Grok', logged_in: false, login: 'grok login' },
    ],
  })
  api.override['engines.link_credentials'] = (i) => {
    linked = i.engine as string
    return { bot_id: xm.id, name: xm.name, engine: xm.engine, engine_label: xm.engine, needs_link: true, linked: true, linked_engine: linked, marker: '這個 Bot 用你的 Google 訂閱' }
  }
  await api.install()
  await page.goto(`/settings/bots/${xm.id}/advanced`)
  const engines = page.getByRole('radiogroup', { name: '引擎' })
  await expect(engines.getByRole('radio')).toHaveCount(6)
  await expect(engines.locator('[data-engine="gemini"]')).toContainText('已登入')
  await expect(engines.locator('[data-engine="grok"]')).toContainText('這台電腦沒登入')
  await expect(engines.locator('[data-engine="opencode"]')).toContainText('OpenCode Go')
  await settle(page)
  await engines.screenshot({ path: shot(`${info.project.name}-e31-engines`) })
  await engines.locator('[data-engine="gemini"]').click()
  await expect.poll(() => api.calls.find((c) => c.op === 'bots.update')?.input).toEqual({ id: xm.id, engine: 'gemini' })
  await page.getByRole('button', { name: '同意使用我的訂閱' }).click()
  const dlg = page.getByRole('dialog', { name: '讓小秘用你的 Google 訂閱？' })
  await dlg.getByText('我同意讓小秘使用我的 Google 訂閱').click()
  await dlg.getByRole('button', { name: '同意' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'engines.link_credentials')?.input).toEqual({ bot_id: xm.id, engine: 'gemini' })
})
