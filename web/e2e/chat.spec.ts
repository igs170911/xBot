import { expect, test, type Page } from '@playwright/test'
import { MockServer, opError, settle } from './mock'

// W02 對話畫面：用瀏覽器層的假服務（mock.ts）確定地測流程，並截圖和 C01／C02／C03 畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

async function openSidebarIfMobile(page: Page, mobile: boolean) {
  if (mobile) await page.getByRole('button', { name: '打開側欄' }).click()
}

test('建 Bot → 送訊息 → 看到串流回覆與工作步驟', async ({ page }, info) => {
  const mobile = info.project.name === 'mobile'
  const api = new MockServer(page)
  await api.install()
  await page.goto('/')
  await openSidebarIfMobile(page, mobile)
  const nav = page.getByRole('navigation', { name: '側欄' })
  await nav.getByRole('button', { name: '新增 Bot' }).first().click()

  const dlg = page.getByRole('dialog', { name: '新增 Bot' })
  await dlg.getByLabel('名字').fill('抓蟲')
  await dlg.getByLabel('它負責什麼（一句話）').fill('照你的描述重做一次，錄下步驟和截圖')
  await dlg.getByRole('radio', { name: '盾牌' }).click()
  await dlg.getByRole('radio', { name: '紅', exact: true }).click()
  await settle(page)
  await dlg.screenshot({ path: shot(`${info.project.name}-new-bot`) })
  await dlg.getByRole('button', { name: '建立「抓蟲」' }).click()

  await expect(page).toHaveURL(/\/bots\/bot1$/)
  expect(api.calls.find((c) => c.op === 'bots.create')?.input).toMatchObject({ name: '抓蟲', shape: 'shield', color: 'red' })
  const main = page.getByRole('main', { name: '和抓蟲的對話' })
  await expect(main.getByTestId('chat-empty')).toContainText('抓蟲')
  await expect(main.getByTestId('chat-empty')).toContainText('先問我')
  if (mobile) await page.getByRole('button', { name: '打開側欄' }).click()
  await expect(nav.getByRole('link', { name: /抓蟲/ })).toBeVisible()
  if (mobile) await page.keyboard.press('Escape')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-empty`) })

  const box = main.getByRole('textbox')
  await box.fill('結帳頁按了沒反應，幫我重現一次')
  await box.press('Enter')
  await expect(page).toHaveURL(/\/bots\/bot1\/threads\/th\d+$/)
  const s = api.lastSend!
  expect(s.text).toBe('結帳頁按了沒反應，幫我重現一次')
  const base = { bot_id: s.botId, thread_id: s.threadId, task_id: s.taskId }
  const owner = api.messages[s.threadId][0]

  await api.emit({ ...base, kind: 'message.created', payload: api.storeMessage(s.threadId, owner) }, { ...base, kind: 'task.state', payload: { state: 'waiting_box' } })
  await expect(main.getByTestId('msg-owner')).toHaveText('結帳頁按了沒反應，幫我重現一次')
  await expect(main.getByText('電腦開機中')).toBeVisible()
  await expect(main.getByRole('button', { name: '停止抓蟲' })).toBeVisible()

  const reply = api.addMessage(s.threadId, { sender: 'bot:' + s.botId, content: '', task_id: s.taskId, streaming: true })
  await api.emit(
    { ...base, kind: 'message.created', payload: api.storeMessage(s.threadId, reply) },
    { ...base, kind: 'task.state', payload: { state: 'running' } },
    { ...base, kind: 'chat.thinking', persist: false, payload: { message_id: reply.id, text: '…' } },
  )
  await expect(main.getByText('思考中')).toBeVisible()
  await api.emit({ ...base, kind: 'chat.tool_call', payload: { type: 'tool_call', tool: { id: 'x1', name: 'browser_open', context: '打開結帳頁' } } })
  await expect(main.getByText('打開結帳頁')).toBeVisible()
  await api.emit({ ...base, kind: 'chat.tool_result', payload: { type: 'tool_result', tool: { id: 'x1', ok: true } } })

  // 串流：一段一段接上去
  for (const piece of ['重現了。', '按下「結帳」後', '購物車 API 回 500，', '畫面沒有顯示錯誤。']) {
    await api.emit({ ...base, kind: 'chat.text', persist: false, payload: { message_id: reply.id, text: piece } })
  }
  await expect(main.getByTestId('msg-bot')).toHaveText('重現了。按下「結帳」後購物車 API 回 500，畫面沒有顯示錯誤。')

  await api.emit(
    { ...base, kind: 'task.state', payload: { state: 'done', verdict: 'answer' } },
    { ...base, kind: 'chat.done', payload: { message_id: reply.id, status: 'complete', verdict: 'answer', text: '重現了。按下「結帳」後購物車 API 回 500，畫面沒有顯示錯誤。' } },
  )
  await expect(main.getByRole('button', { name: /工作了 .* · 1 步/ })).toBeVisible()
  await expect(main.getByRole('button', { name: '停止抓蟲' })).toBeHidden()
  await expect(main.getByTestId('bot-status')).toHaveText('待命')
})

// ── 截圖：照 C01／C02／C03 的情境準備資料 ─────────────────

async function seedXiaomi(api: MockServer) {
  const bot = api.addBot({ name: '小秘', title: '幕僚長', description: '每日重點與協調', shape: 'cursor', color: 'blue' })
  const t = api.addThread(bot.id, '比較蝦皮三家競品定價')
  const now = Date.now()
  api.addMessage(t.id, { sender: 'bot:' + bot.id, content: '早安。今天有 3 件事等你：帳房要付晨光廣告的 10 月廣告費 NT$1,280、小編發新品貼文需要你輸入簡訊驗證碼、阿凱的 3 封開發信草稿寫好了。', status: 'complete' }, now - 5 * 3600_000)
  api.addMessage(t.id, { sender: 'owner', content: '幫我比較蝦皮上三家競品的定價，整理成表格，告訴我哪些該調價。', task_id: 'task-c1' }, now - 72_000)
  const reply = api.addMessage(t.id, { sender: 'bot:' + bot.id, content: '收到。我請阿哲去抓三家的價格，我這邊先對齊我們自己的 12 項主力商品，好了整理成表格給你。', task_id: 'task-c1', streaming: true }, now - 70_000)
  return { bot, t, reply }
}

test('截圖 C01：工作中（步驟展開）與停止', async ({ page }, info) => {
  const api = new MockServer(page)
  await api.install()
  const { bot, t, reply } = await seedXiaomi(api)
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  await expect(main.getByTestId('msg-bot').last()).toContainText('收到。')
  const base = { bot_id: bot.id, thread_id: t.id, task_id: 'task-c1' }
  const started = Date.now() - 72_000
  const step = (id: string, name: string, context: string, ok = true) => [
    { ...base, kind: 'chat.tool_call', at: started, payload: { type: 'tool_call', tool: { id, name, context } } },
    ...(ok ? [{ ...base, kind: 'chat.tool_result', at: started, payload: { type: 'tool_result', tool: { id, ok: true } } }] : []),
  ]
  await api.emit(
    { ...base, kind: 'task.state', at: started, payload: { state: 'running' } },
    ...step('s1', 'read_file', '讀取自家 12 項主力商品現價'),
    ...step('s2', 'browser_open', '打開蝦皮搜尋「保溫瓶」'),
    ...step('s3', 'python', '對齊容量規格（500 ml／750 ml）'),
    ...step('s4', 'browser_click', '補第 3 家「日日家居」的價格', false),
  )
  await expect(main.getByText('4 個步驟', { exact: false })).toBeVisible()
  await expect(main.getByRole('button', { name: '停止小秘' })).toBeVisible()
  await expect(main.getByText('小秘工作中 · 要改做別的事，先按停止')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-working`) })

  // 停止：送 chat.stop，輸入框變「正在停止…」
  await main.getByRole('button', { name: '停止小秘' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'chat.stop')?.input).toEqual({ bot_id: bot.id, thread_id: t.id })
  await expect(main.getByRole('button', { name: '正在停止' })).toBeDisabled()
  await api.emit({ ...base, kind: 'task.state', payload: { state: 'stopping' } })
  await expect(main.getByText('正在停止…').first()).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-stopping`) })
  await api.emit(
    { ...base, kind: 'task.state', payload: { state: 'done', verdict: 'cancelled' } },
    { ...base, kind: 'chat.done', payload: { message_id: reply.id, status: 'interrupted', verdict: 'cancelled', text: reply.content } },
  )
  await expect(main.getByText('已停止')).toBeVisible()
})

test('截圖 C02：一輪完成（步驟摺疊）、工作未完成、已停止', async ({ page }, info) => {
  const api = new MockServer(page)
  await api.install()
  const bot = api.addBot({ name: '小秘', title: '幕僚長', shape: 'cursor', color: 'blue' })
  const t = api.addThread(bot.id, '比價')
  const y = Date.now() - 86_400_000
  api.addMessage(t.id, { sender: 'owner', content: '把昨天的訂單從蝦皮賣家中心匯出來。', task_id: 'k0' }, y)
  api.addMessage(t.id, { sender: 'bot:' + bot.id, content: '蝦皮賣家中心的登入過期了，我進不去後台，昨天的訂單沒辦法匯出。', task_id: 'k0', status: 'error' }, y + 60_000)
  api.addMessage(t.id, { sender: 'owner', content: '整理 10 月的出貨單。', task_id: 'k00' }, y + 120_000)
  api.addMessage(t.id, { sender: 'bot:' + bot.id, content: '正在整理 10 月的出貨單，已經做完 40 筆…', task_id: 'k00', status: 'interrupted' }, y + 180_000)
  const owner = api.addMessage(t.id, { sender: 'owner', content: '幫我比較蝦皮上三家競品的定價，整理成表格，告訴我哪些該調價。', task_id: 'k1' })
  const reply = api.addMessage(t.id, { sender: 'bot:' + bot.id, content: '', task_id: 'k1', streaming: true })
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  await expect(main.getByTestId('msg-owner').last()).toContainText('三家競品')
  const base = { bot_id: bot.id, thread_id: t.id, task_id: 'k1' }
  const t0 = owner.created_at
  const evs: Array<Record<string, unknown> & { kind: string }> = [{ ...base, kind: 'task.state', at: t0, payload: { state: 'running' } }]
  const names = ['read_file', 'browser_open', 'browser_open', 'browser_open', 'python', 'python', 'write_file', 'write_file', 'summarize']
  names.forEach((n, i) => {
    evs.push({ ...base, kind: 'chat.tool_call', at: t0 + i * 1000, payload: { type: 'tool_call', tool: { id: 'k1s' + i, name: n } } })
    evs.push({ ...base, kind: 'chat.tool_result', at: t0 + i * 1000 + 500, payload: { type: 'tool_result', tool: { id: 'k1s' + i, ok: true } } })
  })
  const answer = '三家都比完了。12 項主力商品裡有 3 項比三家都貴，建議調價；其他 9 項價差在 5% 以內，先不動。\n\n調價要你核定，我已經放進待你核定。完整 12 項在試算表裡。'
  evs.push({ ...base, kind: 'chat.text', persist: false, payload: { message_id: reply.id, text: answer } })
  evs.push({ ...base, kind: 'task.state', at: t0 + 160_000, payload: { state: 'done', verdict: 'answer' } })
  evs.push({ ...base, kind: 'chat.done', at: t0 + 160_000, payload: { message_id: reply.id, status: 'complete', verdict: 'answer', text: answer } })
  await api.emit(...evs)
  const summary = main.getByRole('button', { name: '工作了 3 分鐘 · 9 步' })
  await expect(summary).toBeVisible()
  await expect(main.getByText('工作未完成')).toBeVisible()
  await expect(main.getByText('已停止')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-answer`) })
  await summary.click()
  await expect(main.getByText('summarize')).toBeVisible()
})

test('錯誤狀態：沒設定模型 → 提示卡 → 設定後消失；Bot 在忙 → 提示可停止', async ({ page }, info) => {
  const api = new MockServer(page)
  await api.install()
  api.modelsReady = false
  const bot = api.addBot({ name: '阿哲', title: '選品研究員', shape: 'blob', color: 'orange' })
  await page.goto(`/bots/${bot.id}`)
  const main = page.getByRole('main', { name: '和阿哲的對話' })
  const card = main.getByRole('region', { name: '還沒設定模型' })
  await expect(card).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-no-model`) })
  await card.getByRole('button', { name: '設定模型' }).click()
  const dlg = page.getByRole('dialog', { name: '設定模型' })
  await dlg.getByLabel('金鑰').fill('sk-test')
  await dlg.getByLabel('預設模型').fill('gpt-4.1-mini')
  await dlg.getByRole('button', { name: '儲存' }).click()
  await expect(dlg).toBeHidden()
  await expect(card).toBeHidden()
  expect(api.calls.find((c) => c.op === 'models.set_provider')?.input).toMatchObject({ id: 'openai', api_key: 'sk-test', default_model: 'gpt-4.1-mini' })

  api.override['chat.send'] = () => opError(409, 'conflict', '這個 Bot 正在忙另一件事', true)
  const box = main.getByRole('textbox')
  await box.fill('再幫我看一下日本賣場')
  await box.press('Enter')
  const alert = main.getByRole('alert')
  await expect(alert).toContainText('阿哲正在忙另一件事')
  await expect(box).toHaveValue('再幫我看一下日本賣場')
  await alert.getByRole('button', { name: '停止它' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'chat.stop')?.input).toEqual({ bot_id: bot.id })
})
