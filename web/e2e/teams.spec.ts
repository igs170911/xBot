import { expect, test, type Page } from '@playwright/test'
import { MockServer, settle } from './mock'

// X03：團隊（F02/F03 建立團隊、F04 團隊地圖、F01 組織架構）、提案與討論（H04、待我處理）。
// 截圖存在 e2e-results/screens，和 design/ 的畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

test('真的服務：用範本建團隊 → 側欄出現團隊分區 → 團隊地圖有節點', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop', '同一個服務只跑一次')
  const tag = String(Date.now() % 100000)
  await page.goto('/')
  const nav = page.getByRole('navigation', { name: '側欄' })
  await nav.getByRole('button', { name: '建立團隊' }).click()
  const dlg = page.getByRole('dialog', { name: /建立團隊/ })
  await dlg.getByRole('radio', { name: /工程團隊/ }).click()
  await expect(dlg.getByRole('button', { name: '建立 6 個 Bot' })).toBeEnabled()
  // 同一份資料可能跑過：名字加上編號，避免和上次的 Bot 撞名。
  await dlg.getByRole('button', { name: /進階設定/ }).click()
  await dlg.getByRole('textbox', { name: '團隊名稱' }).fill(`工程${tag}`)
  await dlg.getByRole('textbox', { name: '主管名字' }).fill(`技術主管${tag}`)
  for (const n of ['後端工程師', 'QA 工程師', 'DBA', 'DevOps 工程師']) await dlg.getByRole('button', { name: `移除 ${n}` }).click()
  await dlg.getByRole('textbox', { name: '成員 1 名字' }).fill(`前端${tag}`)
  await dlg.getByRole('button', { name: '建立 2 個 Bot' }).click()
  await expect(dlg).toBeHidden()
  await expect(page).toHaveURL(/\/teams/)
  const sec = nav.getByRole('group', { name: `工程${tag}` })
  await expect(sec.getByRole('link')).toHaveCount(2)
  await expect(sec.getByRole('link').first()).toContainText(`技術主管${tag}`)
  const box = page.getByRole('main', { name: '團隊地圖' }).getByRole('group', { name: `工程${tag}` })
  await expect(box.getByTestId('map-node')).toHaveCount(2)
  await expect(box.getByTestId('map-node').first()).toContainText('待命')
  // 同名群組也開好了
  await expect(nav.getByRole('link', { name: new RegExp(`工程${tag}`) })).toBeVisible()
})

const now = Date.now()
const DAY = 86_400_000

async function company(page: Page) {
  const m = new MockServer(page)
  const xm = m.addBot({ name: '小秘', title: '幕僚長', color: 'blue', shape: 'cursor' })
  const zhe = m.addBot({ name: '阿哲', title: '選品研究員', color: 'orange', shape: 'blob' })
  const bian = m.addBot({ name: '小編', title: '社群小編', color: 'pink', shape: 'drop' })
  const kai = m.addBot({ name: '阿凱', title: '業務開發', color: 'green', shape: 'squircle' })
  const zf = m.addBot({ name: '帳房', title: '費用管理', color: 'yellow', shape: 'hexagon' })
  const cat = m.addBot({ name: '數據喵', title: '數據分析師', color: 'purple', shape: 'capsule' })
  m.addBot({ name: '抓蟲', title: 'Bug 重現', color: 'red', shape: 'diamond' })
  const ops = { id: 't1', name: '營運部', chief: xm, members: [zhe, bian, kai] }
  const back = { id: 't2', name: '後勤', chief: zf, members: [cat] }
  const teams = [ops, back].map((t, i) => ({
    id: t.id, name: t.name, about: '', chief_id: t.chief.id, member_ids: [t.chief.id, ...t.members.map((b) => b.id)], memory: [], room_id: '', template: '', position: i + 1, created_by: 'owner', created_at: now,
  }))
  for (const t of [ops, back]) {
    Object.assign(t.chief, { team_id: t.id })
    for (const b of t.members) Object.assign(b, { team_id: t.id, reports_to: t.chief.id })
  }
  const state: Record<string, string> = { [xm.id]: 'working', [zhe.id]: 'working', [bian.id]: 'waiting', [cat.id]: 'queued' }
  const models: Record<string, string> = { [xm.id]: 'Sonnet 5.5', [zhe.id]: 'Hermes', [bian.id]: 'Codex', [kai.id]: 'Codex', [zf.id]: 'Hermes', [cat.id]: 'Gemini' }
  const chartBots = m.bots.map((b) => {
    const t = [ops, back].find((x) => x.chief.id === b.id || x.members.some((y) => y.id === b.id))
    const boss = t && t.chief.id !== b.id ? t.chief : undefined
    return {
      id: b.id, name: b.name, title: b.title, color: b.color, shape: b.shape, model: models[b.id] ?? '', reports_to: boss?.name ?? '使用者', reports_to_id: boss?.id ?? '',
      team: t?.name ?? '', team_id: t?.id ?? '', chief: t?.chief.id === b.id, primary: b.id === xm.id, can_manage: b.id === xm.id, state: state[b.id] ?? 'idle', queued: 0,
      approvals: b.id === zf.id ? [{ subject: '10 月廣告費付款', since: now }] : [], recent: [],
    }
  })
  m.override['teams.list'] = () => ({ teams })
  m.override['teams.chart'] = () => ({ bots: chartBots, teams, bots_total: m.bots.length, bots_max: 50, team_max: 10 })
  m.override['teams.approval_items'] = () => ({ items: ['新商品定價', '新增支出', '簽約', '上架', '重要對客承諾'], max_items: 20, max_chars: 40 })
  m.override['teams.templates'] = () => ({
    templates: [
      { key: 'marketing', name: '行銷團隊', about: '行銷主管帶內容、社群、廣告與數據分析。', lead_manage: false, lead: { name: '行銷主管', title: '行銷團隊主管', persona: '' }, members: ['內容企劃', '社群經營', '廣告投放', '數據分析'].map((n) => ({ name: n, title: n, persona: '' })) },
      { key: 'engineering', name: '工程團隊', about: '技術主管帶前端、後端、QA、DBA 與 DevOps，從需求到上線。', lead_manage: false, lead: { name: '技術主管', title: '工程團隊主管', persona: '' }, members: ['前端工程師', '後端工程師', 'QA 工程師', 'DBA', 'DevOps 工程師'].map((n) => ({ name: n, title: n, persona: '' })) },
      { key: 'company', name: '公司組織', about: 'AI 執行長統籌七個平行職能，重大事項由你核定。', lead_manage: true, lead: { name: '執行長', title: 'AI 執行長／總經理', persona: '' }, members: ['營運管理', '市場研究', '業務行銷', '軟體研發', '財務成本', '創意總監', '品質驗收'].map((n) => ({ name: n, title: n, persona: '' })) },
    ],
    team_max: 10, bots_max: 50, bots_total: m.bots.length,
  })
  const p1 = { id: 'ag1', bot_id: xm.id, bot_name: '小秘', kind: 'propose', subject: '每週一上午 9:00 自動產出銷售週報，發到「每週一早會」', details: '內容：上週營收、退貨率、三家競品價格變化。數據喵整理數字，小秘寫摘要，約 8 行。', due_at: now + 3 * DAY - 60_000, state: 'open', note: '', created_at: now - 2 * 3600_000 }
  const d1 = { id: 'ag2', bot_id: zf.id, bot_name: '帳房', kind: 'discuss', subject: '11 月廣告預算要不要加碼', details: '10 月廣告花了 NT$18,400，帶來 NT$96,000 營收。雙 11 前想問你：維持還是加碼？', due_at: now + DAY, state: 'open', note: '', created_at: now - DAY }
  const p2 = { ...p1, id: 'ag3', bot_id: zhe.id, bot_name: '阿哲', subject: '雙 11 前把競品價格巡檢改成每天', details: '對手這兩週調價 4 次，每週三次會漏掉。到 11 月 12 日為止，之後自動改回。', due_at: now + DAY - 60_000, created_at: now - 3600_000 }
  m.override['agenda.list'] = () => ({
    from: now, to: now + 7 * DAY, open_max: 3, pending: [p1, d1, p2],
    decided: [
      { ...p1, id: 'x1', subject: '週末加發一篇開箱貼文', state: 'accepted', decided_at: now - 2 * DAY },
      { ...p1, id: 'x2', subject: '退貨率改用 30 天滾動計算', state: 'vetoed', decided_at: now - 4 * DAY },
    ],
    calendar: [p2, d1, p1].map((x) => ({ id: 'agenda:' + x.id, at: x.due_at, kind: x.kind, title: x.subject, bot_id: x.bot_id, bot_name: x.bot_name, agenda_id: x.id, state: 'open' })),
    approvals: [{ id: 'ap1', bot_id: zf.id, bot_name: '帳房', subject: '10 月廣告費付款', created_at: now }, { id: 'ap2', bot_id: kai.id, bot_name: '阿凱', subject: '簽約', created_at: now }],
  })
  m.override['agenda.decide'] = (i) => ({ ...p1, id: i.id, state: i.decision === 'veto' ? 'vetoed' : i.decision === 'answer' ? 'answered' : 'accepted' })
  for (const x of [p1, d1]) {
    m.inbox.push({
      id: 'in-' + x.id, kind: x.kind === 'propose' ? 'proposal' : 'discussion', bot_id: x.bot_id, task_id: '', thread_id: '', ref: x.id, title: `${x.bot_name} ${x.kind === 'propose' ? '提案' : '想找你討論'}：${x.subject}`,
      body: x.details, payload: { agenda_id: x.id, kind: x.kind, due_at: x.due_at, subject: x.subject }, status: 'open', resolution: '', created_at: x.created_at, read_at: 0, expires_at: 0,
    })
  }
  await m.install()
  return m
}

test('團隊地圖（F04）與側欄團隊分區', async ({ page }, info) => {
  await company(page)
  await page.goto('/teams')
  const map = page.getByRole('main', { name: '團隊地圖' })
  await expect(map.getByRole('group', { name: '營運部' }).getByTestId('map-node')).toHaveCount(4)
  await expect(map.getByRole('group', { name: '後勤' }).getByTestId('map-node').first()).toContainText('等你允許')
  await expect(map.getByRole('group', { name: '未分區' })).toContainText('抓蟲')
  if (info.project.name === 'desktop') {
    const nav = page.getByRole('navigation', { name: '側欄' })
    await expect(nav.getByRole('group', { name: '營運部' }).getByRole('link').first()).toContainText('小秘')
    await settle(page)
    await page.screenshot({ path: shot('desktop-F04-team-map') })
    await nav.screenshot({ path: shot('desktop-sidebar-teams') })
  } else {
    await settle(page)
    await page.screenshot({ path: shot('mobile-F04-team-map'), fullPage: true })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
})

test('建立團隊（F02/F03）：範本預覽、進階設定', async ({ page }, info) => {
  const m = await company(page)
  await page.goto('/teams')
  await page.getByRole('main', { name: '團隊地圖' }).getByRole('button', { name: '建立團隊' }).click()
  const dlg = page.getByRole('dialog', { name: /建立團隊/ })
  await expect(dlg.getByRole('radio', { name: /行銷團隊/ })).toHaveAttribute('aria-checked', 'true')
  await expect(dlg.getByRole('button', { name: '建立 5 個 Bot' })).toBeVisible()
  await expect(dlg.getByTestId('team-total')).toHaveText('建立後共 12 個 Bot（上限 50）')
  await settle(page)
  await dlg.screenshot({ path: shot(`${info.project.name}-F02-team-builder`) })
  await dlg.getByRole('button', { name: /進階設定/ }).click()
  await settle(page)
  await dlg.screenshot({ path: shot(`${info.project.name}-F03-team-builder-advanced`) })
  m.override['teams.create'] = () => ({ team: { id: 't9', name: '行銷團隊', about: '', chief_id: 'x', member_ids: [], memory: [], room_id: '', template: 'marketing', position: 3, created_by: 'owner', created_at: now }, lead_id: 'x', member_ids: [], created: [], note: '' })
  await dlg.getByRole('button', { name: '建立 5 個 Bot' }).click()
  await expect(dlg).toBeHidden()
  expect(m.calls.find((c) => c.op === 'teams.create')?.input).toMatchObject({ template: 'marketing', group_chat: true })
})

test('組織架構（F01）', async ({ page }, info) => {
  await company(page)
  await page.goto('/org')
  await expect(page.getByTestId('org-row')).toHaveCount(7)
  await expect(page.getByTestId('approval-items')).toContainText('重要對客承諾')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-F01-org`), fullPage: info.project.name === 'mobile' })
})

test('提案與討論：待我處理的卡、等你的事（H04）', async ({ page }, info) => {
  const m = await company(page)
  await page.goto('/')
  const cards = page.getByTestId('agenda-item')
  await expect(cards).toHaveCount(2)
  await expect(cards.first()).toContainText('3 天後生效')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-inbox-agenda`), fullPage: info.project.name === 'mobile' })
  await cards.first().getByRole('button', { name: '現在就同意' }).click()
  await expect(cards.first().getByRole('status')).toContainText('你同意了')
  expect(m.calls.find((c) => c.op === 'agenda.decide')?.input).toEqual({ id: 'ag1', decision: 'accept' })

  await page.goto('/agenda')
  const main = page.getByRole('main', { name: '等你的事' })
  await expect(main.getByTestId('agenda-item')).toHaveCount(3)
  await expect(main.getByRole('link', { name: /2 件待你核定/ })).toBeVisible()
  await expect(page.getByTestId('agenda-calendar')).toHaveCount(3)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-H04-agenda`), fullPage: true })
})
