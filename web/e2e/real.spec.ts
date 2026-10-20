import { expect, test, sidebarAdd } from './fixtures'
import { pastOnboarding } from './real-helpers'

// 對著真的執行檔（不攔截網路）：建 Bot 走真的 bots.create，側欄靠真的 SSE 事件更新；
// 沒設定模型時會看到提示卡，在網頁上設定好就消失。不會真的開 Bot 電腦（那是 make e2e-docker）。
test('真的服務：建 Bot、側欄即時出現、沒模型時引導設定', async ({ page, request }, info) => {
  test.skip(info.project.name !== 'desktop', '同一個服務只跑一次')
  await page.setViewportSize({ width: 1440, height: 960 })
  await pastOnboarding(request)
  await page.goto('/')
  const nav = page.getByRole('navigation', { name: '側欄' })
  await expect(nav).toBeVisible()

  // 別的地方（像 MCP 或指令列）建的 Bot 也會即時出現在側欄
  const name = `外面建的${Date.now() % 10000}`
  const res = await request.post('/api/v1/bots.create', { data: { name, color: 'teal', shape: 'drop' } })
  expect(res.ok()).toBe(true)
  await expect(nav.getByRole('link', { name: new RegExp(name) })).toBeVisible()

  await sidebarAdd(page, '新增 Bot')
  const dlg = page.getByRole('dialog', { name: '新增 Bot' })
  await dlg.getByLabel('名字').fill('網頁建的')
  await dlg.getByRole('button', { name: '建立「網頁建的」' }).click()
  await expect(page).toHaveURL(/\/bots\/[^/]+$/)
  const main = page.getByRole('main', { name: '和網頁建的的對話' })
  await expect(main.getByTestId('chat-empty')).toContainText('網頁建的')
  await expect(nav.getByRole('link', { name: /網頁建的/ }).first()).toHaveAttribute('aria-current', 'page')

  const models = await (await request.post('/api/v1/models.get', { data: {} })).json()
  if (!models.output.ready) {
    const card = main.getByRole('region', { name: '還沒設定模型' })
    await expect(card).toBeVisible()
    await card.getByRole('button', { name: '設定模型' }).click()
    const form = page.getByRole('dialog', { name: '設定模型' })
    await form.getByLabel('服務網址（OpenAI 相容）').fill('http://127.0.0.1:9/v1')
    await form.getByLabel('金鑰').fill('sk-e2e-not-real')
    await form.getByLabel('預設模型').fill('fake-model')
    await form.getByRole('button', { name: '儲存' }).click()
    await expect(form).toBeHidden()
    await expect(card).toBeHidden()
    const after = await (await request.post('/api/v1/models.get', { data: {} })).json()
    expect(after.output.ready).toBe(true)
  }
})
