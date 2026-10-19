import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { scanA11y } from './axe'
import { MockServer } from './mock'
import { ensureBot, pastOnboarding } from './real-helpers'

// v1.0 無障礙（PRD §8：WCAG AA 對比、鍵盤全操作、減少動態效果）：用 axe-core 掃主要頁面與對話框，
// 桌面與手機各一次，深色（預設）與淺色主題都掃；有 serious／critical 的違規就失敗，避免退步。
// 每頁的完整結果（含 moderate／minor）寫在 e2e-results/a11y/。其他規格截圖時也會掃（fixtures.ts）。
// 大部分頁面對著真的服務（make build 出來的執行檔、空的資料目錄）；要有對話內容、待我處理卡片、群組的頁面用假服務。

const scan = (page: Page, info: TestInfo, name: string) => scanA11y(page, info, name)

for (const theme of ['dark', 'light'] as const) {
  test.describe(`主題 ${theme}`, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((t) => window.localStorage.setItem('xbot.theme', t), theme)
    })

    test('真的服務：主要頁面', async ({ page, request }, info) => {
      test.setTimeout(180_000)
      await pastOnboarding(request)
      const bot = await ensureBot(request)
      const pages: Array<[string, string]> = [
        ['inbox', '/'],
        ['office', '/office'],
        ['agenda', '/agenda'],
        ['teams', '/teams'],
        ['org', '/org'],
        ['apps', '/apps'],
        ['apps-mcp', '/apps/mcp'],
        ['apps-logins', '/apps/logins'],
        ['files', '/files'],
        ['calendar', '/calendar'],
        ['routines', '/calendar/routines'],
        ['settings', '/settings'],
        ['settings-general', '/settings/general'],
        ['settings-models', '/settings/models'],
        ['settings-computers', '/settings/computers'],
        ['settings-computers-add', '/settings/computers/add'],
        ['settings-usage', '/settings/usage'],
        ['settings-advanced', '/settings/advanced'],
        ['settings-lab', '/settings/advanced/lab'],
        ['settings-network', '/settings/advanced/network'],
        ['settings-agents', '/settings/advanced/agents'],
        ['bot-settings', `/settings/bots/${bot.id}`],
        ['bot-soul', `/settings/bots/${bot.id}/soul`],
        ['bot-skills', `/settings/bots/${bot.id}/skills`],
        ['bot-memory', `/settings/bots/${bot.id}/memory`],
        ['bot-permissions', `/settings/bots/${bot.id}/permissions`],
        ['bot-advanced', `/settings/bots/${bot.id}/advanced`],
        ['chat-empty', `/bots/${bot.id}`],
        ['computer', `/bots/${bot.id}/computer`],
        ['bot-files', `/bots/${bot.id}/files`],
      ]
      if (info.project.name !== 'desktop')
        pages.push(['chats', '/chats'], ['notifications', '/notifications'], ['new-bot', '/new/bot'], ['new-room', '/new/room'], ['profile', `/bots/${bot.id}/profile`], ['share', '/share'], ['welcome', '/welcome'])
      for (const [name, path] of pages) {
        await test.step(name, async () => {
          await page.goto(path)
          await expect(page.locator('main, [role="main"]').first()).toBeVisible()
          await scan(page, info, `${theme}-${name}`)
        })
      }
    })

    test('真的服務：對話框', async ({ page, request }, info) => {
      test.skip(info.project.name !== 'desktop', '對話框在桌面掃（手機是同一個元件從底部拉出）')
      await pastOnboarding(request)
      await ensureBot(request)
      await page.goto('/')
      await page.getByRole('button', { name: /新增 Bot|新 Bot/ }).first().click()
      await expect(page.getByRole('dialog')).toBeVisible()
      await scan(page, info, `${theme}-dialog-new-bot`)
      await page.keyboard.press('Escape')
      await page.goto('/calendar/routines')
      await page.getByRole('button', { name: /新增定時任務|新增/ }).first().click()
      await expect(page.getByRole('dialog')).toBeVisible()
      await scan(page, info, `${theme}-dialog-new-routine`)
    })

    test('假服務：對話、待我處理、群組', async ({ page }, info) => {
      const api = new MockServer(page)
      const bot = api.addBot({ name: '小秘', title: '幕僚長' })
      const other = api.addBot({ name: '阿哲', shape: 'blob', color: 'orange' })
      const th = api.addThread(bot.id, '比價三家競品')
      api.addMessage(th.id, { sender: 'owner', content: '幫我比較三家競品的價格' })
      api.addMessage(th.id, { sender: `bot:${bot.id}`, content: '好的，以下是結果：\n\n| 店家 | 價格 |\n|---|---|\n| A | 100 |\n| B | 120 |\n\n```js\nconsole.log(1)\n```' })
      api.inbox.push(
        { id: 'in1', kind: 'approval', status: 'open', bot_id: bot.id, thread_id: th.id, title: '要寄出報價信', body: '寄給 3 位客戶', payload: { summary: '寄信', params: {} }, created_at: Date.now(), group: 'decide', read: false },
        { id: 'in2', kind: 'question', status: 'open', bot_id: other.id, title: '要用哪個帳號登入？', body: '', payload: {}, created_at: Date.now(), group: 'decide', read: false },
      )
      const room = api.addRoom([bot.id, other.id], '週會')
      api.addRoomMessage(room.id, { sender: 'owner', content: '大家早' })
      api.addRoomMessage(room.id, { sender: `bot:${other.id}`, content: '早安！' })
      await api.install()
      await page.goto('/')
      await expect(page.getByText('要寄出報價信')).toBeVisible()
      await scan(page, info, `${theme}-mock-inbox`)
      await page.goto(`/bots/${bot.id}/threads/${th.id}`)
      await expect(page.getByText('幫我比較三家競品的價格')).toBeVisible()
      await scan(page, info, `${theme}-mock-chat`)
      await page.goto(`/rooms/${room.id}`)
      await expect(page.getByText('早安！')).toBeVisible()
      await scan(page, info, `${theme}-mock-room`)
    })
  })
}
