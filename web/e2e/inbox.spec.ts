import { expect, test } from './fixtures'
import { MockServer, settle } from './mock'

// C12 待我處理與核准：用假服務測就地處理的流程，截圖和 Main／F08／C08～C10 畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

function seed(api: MockServer) {
  const zf = api.addBot({ name: '帳房', shape: 'hexagon', color: 'yellow' })
  const az = api.addBot({ name: '阿哲', shape: 'blob', color: 'orange' })
  const xb = api.addBot({ name: '小編', shape: 'circle', color: 'pink' })
  const bug = api.addBot({ name: '抓蟲', shape: 'shield', color: 'red' })
  const now = Date.now()
  api.inbox = [
    {
      id: 'ap',
      kind: 'approval',
      group: 'decide',
      bot_id: zf.id,
      ref: 'ap1',
      title: 'Claude Code 想要：寄出 10 月對帳單給晨光廣告',
      body: '要允許嗎？按允許才會執行。',
      payload: { op: 'xbot.mail.send' },
      status: 'open',
      read: false,
      created_at: now - 3 * 60_000,
    },
    {
      id: 'cmd',
      kind: 'approval',
      group: 'decide',
      bot_id: zf.id,
      task_id: 'k1',
      thread_id: 'th-x',
      title: '帳房想執行指令',
      body: '付款',
      payload: { prompt: '付款', command: 'python3 /workspace/pay.py --vendor 晨光廣告 --amount 1280', description: '付款 NT$1,280 給晨光廣告（10 月廣告費）', choices: ['once', 'always', 'deny'], thread_title: '10 月對帳', bot_name: '帳房' },
      status: 'open',
      read: false,
      expires_at: now + 7 * 60_000,
      created_at: now - 3 * 60_000,
    },
    {
      id: 'q',
      kind: 'question',
      group: 'decide',
      bot_id: az.id,
      task_id: 'k2',
      thread_id: 'th-y',
      title: '阿哲有問題問你',
      body: '競品比較要不要也放進日本賣場的價格？',
      payload: { prompt: '競品比較要不要也放進日本賣場的價格？', choices: ['要，一起比較日本賣場', '不用，台灣三家就好', '只比日本前 3 名的商品'], thread_title: '競品比價' },
      status: 'open',
      read: false,
      created_at: now - 8 * 60_000,
    },
    {
      id: 's',
      kind: 'secret',
      group: 'decide',
      bot_id: xb.id,
      task_id: 'k3',
      thread_id: 'th-z',
      title: '小編需要你輸入資料',
      body: '請提供蝦皮開放平台的 API 金鑰',
      payload: { prompt: '請提供蝦皮開放平台的 API 金鑰', method: 'secret', fields: [{ name: 'value', label: 'SHOPEE_API_KEY', kind: 'password' }], thread_title: '新品上架' },
      status: 'open',
      read: false,
      created_at: now - 20 * 60_000,
    },
    {
      id: 'b',
      kind: 'budget',
      group: 'decide',
      bot_id: bug.id,
      title: '「重現結帳失敗」這條工作鏈用完 $5 上限',
      body: '停在第 4 步。',
      payload: {},
      status: 'open',
      read: false,
      created_at: now - 60 * 60_000,
    },
  ]
}

test('待我處理：核准、指令、提問、密碼、預算卡都能就地處理，退回可以復原', async ({ page }, info) => {
  const api = new MockServer(page)
  seed(api)
  const drop = (id: string) => (api.inbox = api.inbox.filter((x) => x.id !== id))
  let removed: Record<string, unknown> | undefined
  api.override['approvals.decide'] = (i) => {
    removed = api.inbox.find((x) => x.id === 'ap')
    drop('ap')
    return { approval: { id: 'ap1', op: 'xbot.mail.send', summary: '', params: {}, requested_by: 'agent:cc', status: i.decision === 'allow' ? 'approved' : 'denied', created_at: 1 }, settled: true, executed: i.decision === 'allow', failed: false }
  }
  api.override['inbox.undo'] = () => {
    if (removed) api.inbox.unshift(removed as (typeof api.inbox)[number])
    return { item: removed }
  }
  api.override['chat.respond'] = (i) => {
    drop(i.inbox_id as string)
    return { inbox_id: i.inbox_id, task_id: 'k', thread_id: 't', status: 'resolved', resolution: 'answered' }
  }
  api.override['inbox.dismiss'] = (i) => {
    drop(i.inbox_id as string)
    return { item: {} }
  }
  await api.install()
  await page.goto('/')
  if (info.project.name === 'mobile') await expect(page.getByText('5 件需要你，其他 Bot 照常工作')).toBeVisible()
  else await expect(page.getByRole('heading', { level: 1, name: '有 5 件事等你' })).toBeVisible()
  await expect(page.getByTestId('ask-expiry')).toContainText('10 分鐘內沒回覆會過期')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-inbox-cards`), fullPage: true })

  // 退回 → 跳出「復原」→ 回來
  const ap = page.locator('[data-testid="inbox-item"][data-kind="approval"]').first()
  await ap.getByRole('button', { name: '退回' }).click()
  if (info.project.name === 'mobile') await expect(page.getByText('4 件需要你，其他 Bot 照常工作')).toBeVisible()
  else await expect(page.getByRole('heading', { level: 1, name: '有 4 件事等你' })).toBeVisible()
  const undo = page.getByRole('button', { name: '復原' })
  await expect(undo).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-inbox-undo-toast`) })
  await undo.click()
  if (info.project.name === 'mobile') await expect(page.getByText('5 件需要你，其他 Bot 照常工作')).toBeVisible()
  else await expect(page.getByRole('heading', { level: 1, name: '有 5 件事等你' })).toBeVisible()
  expect(api.calls.find((c) => c.op === 'inbox.undo')?.input).toEqual({ inbox_id: 'ap' })

  // Bot 的指令：允許這次（chat.respond）
  const cmd = page.locator('[data-kind="approval"] [data-testid="ask-card"]')
  await cmd.getByRole('button', { name: '允許這次' }).click()
  await expect(cmd).toHaveCount(0)
  expect(api.calls.find((c) => c.op === 'chat.respond')?.input).toEqual({ inbox_id: 'cmd', choice: 'once' })

  // 提問：點一個選項就送出
  await page.getByRole('button', { name: /不用，台灣三家就好/ }).click()
  await expect(page.locator('[data-kind="question"]')).toHaveCount(0)
  expect(api.calls.filter((c) => c.op === 'chat.respond')[1].input).toEqual({ inbox_id: 'q', text: '不用，台灣三家就好' })

  // 密碼：值只送給 chat.respond
  const sec = page.locator('[data-kind="secret"]')
  await sec.getByLabel('SHOPEE_API_KEY').fill('sk-shopee-123')
  await sec.getByRole('button', { name: '送出' }).click()
  await expect(sec).toHaveCount(0)
  expect(api.calls.filter((c) => c.op === 'chat.respond')[2].input).toEqual({ inbox_id: 's', value: 'sk-shopee-123' })

  // 預算卡：知道了
  await page.locator('[data-kind="budget"]').getByRole('button', { name: '知道了' }).click()
  if (info.project.name === 'mobile') await expect(page.getByText('1 件需要你，其他 Bot 照常工作')).toBeVisible()
  else await expect(page.getByRole('heading', { level: 1, name: '有 1 件事等你' })).toBeVisible()
})

test('對話裡回覆：核准卡、提問卡、密碼卡，送出後收成一行', async ({ page }, info) => {
  const api = new MockServer(page)
  const bot = api.addBot({ name: '帳房', shape: 'hexagon', color: 'yellow' })
  const t = api.addThread(bot.id, '10 月對帳')
  api.addMessage(t.id, { sender: 'owner', content: '幫我付 10 月的廣告費', task_id: 'k1' }, Date.now() - 60_000)
  api.override['chat.respond'] = (i) => ({ inbox_id: i.inbox_id, task_id: 'k1', thread_id: t.id, status: 'resolved', resolution: i.choice ?? 'answered' })
  await api.install()
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const main = page.getByRole('main', { name: '和帳房的對話' })
  await expect(main.getByTestId('msg-owner')).toBeVisible()
  const base = { bot_id: bot.id, thread_id: t.id, task_id: 'k1' }
  await api.emit(
    { ...base, kind: 'task.state', payload: { state: 'running' } },
    { ...base, kind: 'chat.tool_call', payload: { tool: { id: 'x1', name: 'terminal', context: '比對帳單和投放紀錄' } } },
    { ...base, kind: 'chat.tool_result', payload: { tool: { id: 'x1', name: 'terminal', ok: true } } },
    { ...base, kind: 'task.state', payload: { state: 'waiting_approval' } },
    {
      ...base,
      kind: 'chat.needs_approval',
      payload: {
        inbox_id: 'i1',
        request: { id: 'r1', kind: 'approval', method: 'approval', prompt: 'pay', choices: ['once', 'always', 'deny'], params: { command: 'python3 /workspace/pay.py --vendor 晨光廣告 --amount 1280', description: '付款 NT$1,280 給晨光廣告（10 月廣告費）' } },
      },
    },
  )
  const card = main.getByTestId('ask-card')
  await expect(card).toContainText('需要允許這項操作')
  await expect(main.getByTestId('bot-status')).toContainText('等你回覆')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-ask-approval`) })
  await card.getByRole('button', { name: '允許這次' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'chat.respond')?.input).toEqual({ inbox_id: 'i1', choice: 'once' })
  await api.emit({ ...base, kind: 'chat.answered', payload: { inbox_id: 'i1', kind: 'approval', resolution: 'once', text: '允許這次' } }, { ...base, kind: 'task.state', payload: { state: 'running' } })
  await expect(main.getByTestId('ask-answered')).toContainText('已允許這次')

  // 提問（C09）
  await api.emit(
    { ...base, kind: 'task.state', payload: { state: 'waiting_input' } },
    { ...base, kind: 'chat.needs_approval', payload: { inbox_id: 'i2', expires_at: Date.now() + 9 * 60_000, request: { id: 'r2', kind: 'clarify', prompt: '發票上的統編是 24578913 還是 24518913？', choices: ['24578913', '24518913'] } } },
  )
  await expect(main.getByTestId('ask-card')).toContainText('統編')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-ask-question`) })
  await main.getByTestId('ask-card').getByRole('button', { name: /24578913/ }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'chat.respond')[1]?.input).toEqual({ inbox_id: 'i2', text: '24578913' })
  await api.emit({ ...base, kind: 'chat.answered', payload: { inbox_id: 'i2', kind: 'question', resolution: 'answered', text: '24578913' } })

  // 密碼（C10）：帳號＋密碼
  await api.emit(
    { ...base, kind: 'task.state', payload: { state: 'waiting_input' } },
    { ...base, kind: 'chat.needs_approval', payload: { inbox_id: 'i3', request: { id: 'r3', kind: 'secret', method: 'vault.save_login', prompt: '帳房需要晨光記帳的登入' } } },
  )
  const sec = main.getByTestId('ask-card')
  await sec.getByLabel('帳號').fill('ops@chenguang.tw')
  await sec.getByLabel('密碼').fill('correct horse')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-ask-secret`) })
  await sec.getByRole('button', { name: '送出' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'chat.respond')[2]?.input).toEqual({ inbox_id: 'i3', values: { identifier: 'ops@chenguang.tw', password: 'correct horse' } })
  await api.emit({ ...base, kind: 'chat.answered', payload: { inbox_id: 'i3', kind: 'secret', resolution: 'provided', text: '已提供' } })
  await expect(main.getByTestId('ask-answered')).toHaveCount(3)
  await expect(main.getByTestId('ask-card')).toHaveCount(0)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-ask-answered`) })
})

// 2026-10-19：分享登入允許了兩次都失敗，卡片一閃就不見，修好後沒人再做一次。失敗的核准卡重新打開、寫原因，可以「再試一次」。
test('允許了但沒做成：卡片寫上原因，按「再試一次」再執行，「先不做了」收掉', async ({ page }) => {
  const api = new MockServer(page)
  const bot = api.addBot({ name: '研究總監', shape: 'blob', color: 'purple' })
  api.inbox = [
    {
      id: 'f1',
      kind: 'approval',
      group: 'decide',
      bot_id: bot.id,
      ref: 'apf',
      title: '研究總監 想要：請使用者把另一個 Bot 已登入的網站分享給你',
      body: '要允許嗎？按允許才會執行。',
      payload: { op: 'xbot.logins.request_share', actor: 'bot:' + bot.id, actor_name: '研究總監', failed: '讀不到這台電腦的瀏覽器登入' },
      status: 'open',
      created_at: Date.now() - 60_000,
    },
  ]
  api.override['approvals.decide'] = (i) => {
    api.inbox = []
    return { approval: { id: 'apf', op: 'xbot.logins.request_share', summary: '', params: {}, requested_by: 'bot:' + bot.id, status: 'approved', created_at: 1 }, settled: true, executed: i.decision === 'allow', failed: false }
  }
  await api.install()
  await page.goto('/')
  const card = page.getByTestId('inbox-item').filter({ hasText: '研究總監' })
  await expect(card).toContainText('沒做成')
  await expect(card).toContainText('讀不到這台電腦的瀏覽器登入')
  await expect(card.getByRole('button', { name: '退回' })).toHaveCount(0)
  await card.getByRole('button', { name: '再試一次' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'approvals.decide')?.input).toEqual({ approval_id: 'apf', decision: 'allow' })
  await expect(card).toHaveCount(0)
})
