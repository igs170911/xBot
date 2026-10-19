import { expect, test } from './fixtures'
import { MockServer, settle } from './mock'

// 2026-10-19：Bot 記錯了一件事（「xbot 工具沒有刪除 Bot／團隊的能力」）一直拒絕你，你看不出它記了什麼。
// 對話步驟寫「記住了：…」＋「看記憶」；記憶頁每條顯示來歷，刪掉後可以開新對話串。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

const WRONG = 'xbot 工具沒有刪除 Bot／團隊的能力，要請使用者自己刪。'

test('對話裡看到 Bot 記了什麼，點「看記憶」改掉記錯的', async ({ page }, info) => {
  const api = new MockServer(page)
  const bot = api.addBot({ name: '水哥哥', shape: 'blob', color: 'teal' })
  const t = api.addThread(bot.id, '幫我整理團隊')
  api.addMessage(t.id, { sender: 'owner', content: '把沒在用的「行銷組」刪掉', task_id: 'task-m1' })
  const said = '我沒辦法刪除團隊，請你自己到設定刪。'
  const reply = api.addMessage(t.id, { sender: 'bot:' + bot.id, content: said, task_id: 'task-m1', status: 'complete' })
  const mem = {
    user: { entries: ['喜歡簡短回答，先講結論。'], used: 12, limit: 1375 },
    memory: { entries: ['專案資料放在 /workspace/shop', WRONG], used: 60, limit: 2200 },
  }
  const now = Date.now()
  let changes = [
    { id: 'c3', target: 'memory', actor: 'bot:' + bot.id, added: [WRONG], removed: [], created_at: now - 60_000, undoable: true, thread_id: t.id, thread_title: t.title },
    { id: 'c2', target: 'user', actor: 'bot:' + bot.id, added: [], removed: [], created_at: now - 86_400_000, undoable: false },
    { id: 'c1', target: 'memory', actor: 'owner', added: ['專案資料放在 /workspace/shop'], removed: ['專案資料放在 /workspace/old'], created_at: now - 3 * 86_400_000, undoable: false },
  ]
  Object.assign(api.override, {
    'memory.read': () => ({ ...mem, enabled: true, computer: true }),
    'memory.history': () => ({ changes }),
    'memory.write': (i: Record<string, unknown>) => {
      const before = mem.memory.entries
      mem.memory.entries = i.entries as string[]
      changes = [{ id: 'c4', target: 'memory', actor: 'owner', added: [], removed: before.filter((e) => !mem.memory.entries.includes(e)), created_at: Date.now(), undoable: true }, ...changes]
      return { ...mem, enabled: true, computer: true }
    },
  })
  await api.install()
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const main = page.getByRole('main', { name: '和水哥哥的對話' })
  await expect(main.getByText(said)).toBeVisible()
  const base = { bot_id: bot.id, thread_id: t.id, task_id: 'task-m1' }
  const args = { action: 'add', target: 'memory', content: WRONG }
  await api.emit(
    { ...base, kind: 'task.state', payload: { state: 'running' } },
    { ...base, kind: 'chat.tool_call', payload: { type: 'tool_call', tool: { id: 'k1', name: 'memory', context: '+memory: "xbot 工具沒有刪除…"', args } } },
    { ...base, kind: 'chat.tool_result', payload: { type: 'tool_result', tool: { id: 'k1', name: 'memory', ok: true, args } } },
    { ...base, kind: 'task.state', payload: { state: 'done', verdict: 'answer' } },
    { ...base, kind: 'chat.done', payload: { message_id: reply.id, status: 'complete', verdict: 'answer', text: said } },
  )
  // 做完預設收合：點開才看到步驟
  await main.getByRole('button', { name: /工作了 .* · 1 步/ }).click()
  await expect(main.getByText('記住了：xbot 工具沒有刪除 Bot／團隊的能力，要請…')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-memory-step`) })

  await main.getByRole('link', { name: '看記憶' }).click()
  await expect(page).toHaveURL(new RegExp(`/settings/bots/${bot.id}/abilities/memory$`))
  const facts = page.getByRole('group', { name: 'Bot 記住的事' })
  await expect(facts.getByRole('link', { name: '幫我整理團隊' })).toHaveAttribute('href', `/bots/${bot.id}/threads/${t.id}`)
  await expect(facts.getByText(/^你 · \d+\/\d+ 修改$/)).toBeVisible()
  const recent = page.getByRole('group', { name: '最近的變更' })
  await expect(recent.getByText('整理', { exact: true })).toBeVisible()
  await expect(recent.getByText('→ 專案資料放在 /workspace/shop')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-memory-origin`), fullPage: true })

  // 刪掉記錯的那條 → 提示新的對話串才會用到 → 開新對話串
  await facts.getByRole('button', { name: '刪除這一條' }).nth(1).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'memory.write')?.input.entries).toEqual(['專案資料放在 /workspace/shop'])
  await expect(page.getByText('已刪除一條。新的對話串才會用到')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-memory-deleted`) })
  await page.getByRole('button', { name: '開新對話串' }).click()
  await expect(page).toHaveURL(new RegExp(`/bots/${bot.id}/threads/new$`))
})
