import { expect, test } from '@playwright/test'
import { MockServer, settle } from './mock'

// C21 代為核准與外部 MCP 權限：用假服務測畫面流程，截圖和 J12、C16、F08 畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 1000 })
})

const NEVER = ['刪除 Bot 或資料', '移除主機', '提高花費上限', '改代為核准授權']

function agent(id: string, name: string, extra: Record<string, unknown> = {}) {
  const now = Date.now()
  return {
    id,
    name,
    status: 'active',
    scopes: ['read', 'operate'],
    resources: { bots: [], teams: [], hosts: [] },
    resource_scope: 'own',
    last_used_at: now - 3 * 60_000,
    expires_at: 0,
    revoked_at: 0,
    created_at: now - 86_400_000,
    rate_per_min: 60,
    daily_calls: 0,
    daily_spend_micros: 0,
    calls_today: 0,
    spend_today_micros: 0,
    delegation: { agent_id: id, enabled: false, categories: [], daily_count: 0, daily_amount_micros: 0, expires_at: 0, used_today: 0, amount_today_micros: 0, never: NEVER },
    ...extra,
  }
}

test('已連線的代理：清單、權限與代為核准、配對碼', async ({ page }, info) => {
  const api = new MockServer(page)
  const xm = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue' })
  api.addBot({ name: '阿哲', shape: 'blob', color: 'orange' })
  api.addBot({ name: '小編', shape: 'circle', color: 'pink' })
  const now = Date.now()
  const agents = [
    agent('cc', 'Claude Code（工作筆電）', {
      scopes: ['operate', 'manage'],
      resource_scope: 'custom',
      resources: { bots: [xm.id], teams: [], hosts: [] },
      daily_calls: 500,
      calls_today: 42,
      daily_spend_micros: 2_000_000,
      delegation: { agent_id: 'cc', enabled: true, categories: ['external_send'], daily_count: 5, daily_amount_micros: 0, expires_at: now + 7 * 86_400_000, used_today: 1, amount_today_micros: 0, never: NEVER },
    }),
    agent('cx', 'Codex（CI）', { resource_scope: 'all', resources: { bots: null, teams: null, hosts: null }, last_used_at: now - 3_600_000, daily_calls: 300, calls_today: 118 }),
    agent('xb', '另一台 XBot（公司）', { scopes: ['read'], resource_scope: 'all', last_used_at: now - 86_400_000 }),
  ]
  api.override['agents.list'] = () => ({ agents })
  api.override['audit.list'] = () => ({
    entries: [
      { id: 3, actor: 'agent:cc', via: 'mcp', op: 'xbot.bots.update', request_id: '', target: xm.id, params: {}, status: 'ok', error_code: '', created_at: now - 60_000 },
      { id: 2, actor: 'agent:cc', via: 'mcp', op: 'xbot.bots.delete', request_id: 'cc-7f3a', target: 'old', params: {}, status: 'pending_approval', error_code: 'pending_approval', created_at: now - 120_000 },
      { id: 1, actor: 'agent:cx', via: 'mcp', op: 'xbot.settings.get', request_id: '', target: '', params: {}, status: 'error', error_code: 'forbidden', created_at: now - 1_800_000 },
    ],
  })
  api.override['agents.update_scopes'] = (i) => ({ ...agents[1], ...i })
  api.override['agents.set_delegation'] = (i) => ({ agent_id: i.id, enabled: !!i.enabled, categories: i.categories ?? [], daily_count: i.daily_count ?? 0, daily_amount_micros: i.daily_amount_micros ?? 0, expires_at: now + 86_400_000, used_today: 0, amount_today_micros: 0, never: NEVER })
  api.override['agents.pair_start'] = (i) => ({ code: 'XB7K-4P9Q', expires_at: Date.now() + 5 * 60_000, pair_path: '/api/v1/agents.pair', scopes: i.scopes, resource_scope: i.resource_scope })
  await api.install()

  // 從設定進來
  await page.goto('/settings/advanced/agents')
  await expect(page.getByRole('heading', { level: 1, name: '已連線的代理（MCP）' })).toBeVisible()
  const rows = page.getByTestId('agent-row')
  await expect(rows).toHaveCount(3)
  await expect(rows.first()).toContainText('代為核准')
  await expect(rows.first()).toContainText('只有這些 Bot：小秘')
  await expect(rows.first()).toContainText('今天 42 / 500 次')
  await expect(page.getByTestId('audit-row')).toHaveCount(3)
  await expect(page.getByTestId('audit-row').nth(2)).toContainText('被拒：權限不足')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-j12-agents`), fullPage: true })

  // 改 Codex 的權限：打開代為核准，勾對外發送與小額花費
  await rows.nth(1).getByRole('button', { name: '權限' }).click()
  const dlg = page.getByRole('dialog', { name: 'Codex（CI） 的權限' })
  await expect(dlg.getByTestId('never-delegate')).toContainText('提高花費上限')
  await dlg.getByRole('switch', { name: '允許代為核准' }).click()
  await dlg.getByText('小額花費').click()
  await dlg.getByRole('textbox', { name: '每日代為核准金額' }).fill('20')
  await expect(dlg.getByRole('status')).toContainText('已儲存，下一次呼叫就生效')
  await expect
    .poll(() => api.calls.filter((c) => c.op === 'agents.set_delegation').at(-1)?.input)
    .toEqual({ id: 'cx', enabled: true, categories: ['external_send', 'spend_small'], daily_count: 5, daily_amount_micros: 20_000_000, days: 7 })
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-j12-edit-delegation`) })
  await dlg.getByRole('button', { name: '完成' }).click()
  await expect(dlg).toHaveCount(0)

  // 連接新的代理：預設只能碰它建立的
  await page.getByRole('button', { name: '連接新的代理' }).click()
  const nd = page.getByRole('dialog', { name: '連接新的代理' })
  await expect(nd.getByRole('radio', { name: '它建立的' })).toHaveAttribute('aria-checked', 'true')
  await nd.getByRole('button', { name: '產生配對碼' }).click()
  await expect(nd.getByTestId('pair-code')).toContainText('XB7K-4P9Q')
  expect(api.calls.find((c) => c.op === 'agents.pair_start')?.input).toMatchObject({ scopes: ['read', 'operate'], resource_scope: 'own' })
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-j12-pairing`) })
})

test('待我處理：代理要求的核准標出 MCP，代為核准紀錄卡可以復原', async ({ page }, info) => {
  const api = new MockServer(page)
  const old = api.addBot({ name: '舊客服', shape: 'squircle', color: 'green' })
  const xb = api.addBot({ name: '小編', shape: 'circle', color: 'pink' })
  const now = Date.now()
  api.inbox = [
    {
      id: 'ap',
      kind: 'approval',
      group: 'decide',
      bot_id: old.id,
      ref: 'ap1',
      title: 'Claude Code 想要：刪除一個 Bot',
      body: '要允許嗎？按允許才會執行。',
      payload: { op: 'xbot.bots.delete', actor: 'agent:cc', actor_name: 'Claude Code', via: 'mcp', request_id: 'cc-7f3a', delegable: false },
      status: 'open',
      read: false,
      created_at: now - 60_000,
    },
    {
      id: 'dg',
      kind: 'delegated',
      group: 'done',
      bot_id: xb.id,
      ref: 'ap2',
      title: '已由 Claude Code（MCP）代為核准 · 對外發送',
      body: '小編 發佈「秋季新品」貼文到 Threads · 今天第 1／5 次',
      payload: { actor_name: 'Claude Code', via: 'mcp', category: 'external_send', reversible: true },
      status: 'resolved',
      resolution: 'delegated',
      read: false,
      undo_until: now + 9 * 60_000,
      created_at: now - 60_000,
    },
  ]
  api.override['inbox.undo'] = (i) => {
    const it = api.inbox.find((x) => x.id === i.inbox_id)!
    Object.assign(it, { resolution: 'undone', undo_until: 0 })
    return { item: it }
  }
  await api.install()
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1, name: '早安，有 1 件事等你' })).toBeVisible()
  await expect(page.getByTestId('approval-op')).toContainText('xbot.bots.delete · request_id cc-7f3a')
  await expect(page.locator('[data-kind="approval"]')).toContainText('透過 MCP · 只有你能決定')
  const card = page.getByTestId('inbox-delegated')
  await expect(card).toContainText('還能復原 9 分鐘')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-c16-delegated`), fullPage: true })
  await card.getByRole('button', { name: '復原' }).click()
  await expect(card).toContainText('已復原')
  expect(api.calls.find((c) => c.op === 'inbox.undo')?.input).toEqual({ inbox_id: 'dg' })
  await settle(page)
  await card.screenshot({ path: shot(`${info.project.name}-c16-delegated-undone`) })
})

test('對話：代理（MCP）送的訊息標「由 Claude Code（MCP）傳送」', async ({ page }, info) => {
  const api = new MockServer(page)
  const bot = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue' })
  const t = api.addThread(bot.id, '競品價格')
  api.addMessage(t.id, { sender: 'owner', content: '整理三家競品今天的價格', task_id: 'k1', actor: 'agent:cc', actor_name: 'Claude Code', via: 'mcp' }, Date.now() - 120_000)
  api.addMessage(t.id, { sender: `bot:${bot.id}`, content: '競品價格整理好了：三家今天都沒有變動。', task_id: 'k1', status: 'complete' }, Date.now() - 60_000)
  api.addMessage(t.id, { sender: 'owner', content: '謝謝', task_id: 'k2' }, Date.now() - 30_000)
  await api.install()
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  await expect(main.getByTestId('agent-mark')).toHaveCount(1)
  await expect(main.getByTestId('agent-mark')).toHaveText('由 Claude Code（MCP）傳送')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-c16-chat-mark`) })
})
