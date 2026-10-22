import { expect, test } from './fixtures'
import { MockServer, settle } from './mock'

// UI 截圖：實際 1280x800 畫面，用於 docs/ui/ 和 PR 展示
const WIDTH = 1280
const HEIGHT = 800

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: WIDTH, height: HEIGHT })
})

test('capture UI screenshots with seeded data', async ({ page }) => {
  const api = new MockServer(page)
  await api.install()

  // Seed realistic data: Bot + Thread + Messages
  const xiaomi = api.addBot({ name: '小秘', title: '幕僚長', description: '每日重點與協調', shape: 'cursor', color: 'blue' })
  const thread = api.addThread(xiaomi.id, '比較蝦皮三家競品定價')
  const now = Date.now()

  api.addMessage(thread.id, {
    sender: 'bot:' + xiaomi.id,
    content: '早安。今天有 3 件事等你：帳房要付晨光廣告的 10 月廣告費 NT$1,280、小編發新品貼文需要你輸入簡訊驗證碼、阿凱的 3 封開發信草稿寫好了。',
    status: 'complete',
  }, now - 5 * 3600_000)

  api.addMessage(thread.id, {
    sender: 'you',
    content: '比較蝦皮三家競品定價',
  }, now - 3600_000)

  api.addMessage(thread.id, {
    sender: 'bot:' + xiaomi.id,
    content: '蝦皮上三家主要競品的定價如下：\n\n1. **品牌 A**：NT$ 890\n2. **品牌 B**：NT$ 1,200\n3. **品牌 C**：NT$ 750\n\n我們的定價 NT$ 980 位於中段，略高於品牌 C 但低於品牌 B。',
    status: 'complete',
    task_id: 'task1',
  }, now - 1800_000)

  await page.goto('/')
  await settle(page)

  // 1. Main chat view
  await page.goto(`/bots/${xiaomi.id}/threads/${thread.id}`)
  await settle(page)
  const main = page.getByRole('main')
  await expect(main).toBeVisible()
  await page.screenshot({ path: 'e2e-results/screens/ui-01-main-chat.png', fullPage: false })
  console.log('✓ Captured: 01-main-chat.png')

  // 2. Sidebar with 更多 (More) open
  const nav = page.getByRole('navigation', { name: '側欄' })
  await expect(nav).toBeVisible()

  // Try to find and click "更多" button
  const moreButton = page.locator('button:has-text("更多"), [aria-label*="更多"]').first()
  if (await moreButton.isVisible({ timeout: 2000 }).catch(() => false)) {
    await moreButton.click()
    await settle(page)
  }
  await page.screenshot({ path: 'e2e-results/screens/ui-02-sidebar-more-open.png', fullPage: false })
  console.log('✓ Captured: 02-sidebar-more-open.png')

  // 3. WorkSteps in progress
  // Start a new task to show work in progress
  const box = main.getByRole('textbox')
  await box.fill('整理今天的待辦清單')
  await box.press('Enter')
  await settle(page)

  // Emit task.state = running
  const lastSend = api.lastSend!
  const base = { bot_id: lastSend.botId, thread_id: lastSend.threadId, task_id: lastSend.taskId }

  api.addMessage(lastSend.threadId, {
    sender: 'bot:' + lastSend.botId,
    content: '',
    task_id: lastSend.taskId,
    streaming: true,
  })

  const replyMsg = api.addMessage(lastSend.threadId, {
    sender: 'bot:' + lastSend.botId,
    content: '',
    task_id: lastSend.taskId,
    streaming: true,
  })

  await api.emit(
    { ...base, kind: 'message.created', payload: api.storeMessage(lastSend.threadId, replyMsg) },
    { ...base, kind: 'task.state', payload: { state: 'running' } },
    { ...base, kind: 'chat.thinking', persist: false, payload: { message_id: replyMsg.id, text: '正在整理…' } },
  )

  await expect(page.getByText('正在整理')).toBeVisible({ timeout: 5000 }).catch(() => {})
  await settle(page)
  await page.screenshot({ path: 'e2e-results/screens/ui-03-worksteps-in-progress.png', fullPage: false })
  console.log('✓ Captured: 03-worksteps-in-progress.png')

  // 4. WorkSteps done
  await api.emit(
    { ...base, kind: 'chat.text', persist: false, payload: { message_id: replyMsg.id, text: '今天有三件待辦：廣告費用、驗證碼、郵件草稿。' } },
    { ...base, kind: 'task.state', payload: { state: 'done', verdict: 'answer' } },
    { ...base, kind: 'chat.done', payload: { message_id: replyMsg.id, status: 'complete', verdict: 'answer', text: '今天有三件待辦：廣告費用、驗證碼、郵件草稿。' } },
  )

  await expect(main.getByRole('button', { name: /工作了/ })).toBeVisible({ timeout: 5000 }).catch(() => {})
  await settle(page)
  await page.screenshot({ path: 'e2e-results/screens/ui-04-worksteps-done.png', fullPage: false })
  console.log('✓ Captured: 04-worksteps-done.png')
})
