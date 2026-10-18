import { expect, test, type Page } from '@playwright/test'

// C32 定時任務：對著真的服務（真的 routines.propose／create／calendar 與網址觸發），截圖和 H01、H02、H03、H06、L16、L17 畫板比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

async function ready(page: Page) {
  await page.evaluate(() => document.fonts.ready)
  await page.addStyleTag({ content: '*{animation:none!important;transition:none!important}' })
}

test('一句話建立定時任務、清單、行事曆、網址觸發', async ({ page, request }, info) => {
  const desktop = info.project.name === 'desktop'
  if (desktop) await page.setViewportSize({ width: 1440, height: 960 })
  const name = `排程喵${info.project.name === 'desktop' ? 'D' : 'M'}${Date.now() % 1000}`
  const bot = (await (await request.post('/api/v1/bots.create', { data: { name, color: 'purple', shape: 'star' } })).json()).output

  await page.goto('/calendar/routines')
  await expect(page.getByRole('heading', { level: 1, name: '定時任務' })).toBeVisible()
  await page.getByRole('button', { name: '新增定時任務' }).click()
  const dlg = page.getByRole('dialog', { name: '新增定時任務' })
  await expect(dlg).toBeVisible()

  // 沒寫時間：要問幾點，建立不能按。
  const say = dlg.getByLabel('用一句話說要它定時做什麼')
  await say.fill(`每天請${name}整理新聞給我`)
  await expect(dlg.getByText('沒看到時間，要幾點做？')).toBeVisible()
  await expect(dlg.getByRole('button', { name: '建立', exact: true })).toBeDisabled()

  // 寫了時間：確認卡即時更新，不出現 AM/PM。
  await say.fill(`每天早上 9 點請${name}整理新聞給我`)
  await expect(dlg.getByTestId('routine-when')).toHaveText('每天上午 9:00')
  await expect(dlg.getByRole('textbox', { name: '做什麼', exact: true })).toHaveValue('整理新聞給我')
  await expect(dlg.getByTestId('routine-next')).toContainText('上午 9:00')
  await expect(dlg).not.toContainText(/AM|PM/)
  await dlg.getByRole('button', { name: '進階設定' }).click()
  await expect(dlg.getByText('時間飄移', { exact: true }).first()).toBeVisible()
  await ready(page)
  await page.screenshot({ path: shot(`${info.project.name}-routine-new`) })
  await dlg.getByRole('switch', { name: '網址觸發' }).click()
  await dlg.getByRole('button', { name: '建立', exact: true }).click()
  await expect(dlg).toBeHidden()
  await expect(page.getByText(/已建立 · .*第一次跑/)).toBeVisible()

  // 清單：剛建的在裡面，有網址觸發。
  const card = page.getByTestId('routine-card').filter({ hasText: name }).filter({ hasText: '整理新聞給我' })
  await expect(card).toContainText('整理新聞給我')
  await expect(card).toContainText('每天上午 9:00')
  await expect(card).toContainText('/hooks/')

  // 也用 API 建一個每 2 小時的目標型、和一個平日的，畫面上看得到進度條。
  const mk = (data: object) => request.post('/api/v1/routines.create', { data: { bot_id: bot.id, ...data } })
  expect((await mk({ text: '平日下午三點整理客服信' })).ok()).toBe(true)
  expect((await mk({ prompt: '找 10 個高佣金新品', title: `找高佣金新品${name}`, schedule: { kind: 'interval', every_minutes: 120, time: '08:00' }, goal: { done_when: '找到 10 個佣金 15% 以上的新品', max_rounds: 10 } })).ok()).toBe(true)
  await page.reload()
  await expect(page.getByTestId('routine-card').filter({ hasText: `找高佣金新品${name}` })).toContainText('已跑 0 / 10 輪')

  // 抽屜：執行紀錄＋網址與金鑰；測試請求只記一筆。
  await card.getByRole('button', { name: '修改' }).click()
  const drawer = page.getByTestId('routine-drawer').locator('visible=true')
  await expect(drawer).toBeVisible()
  const list = (await (await request.post('/api/v1/routines.list', { data: { bot_id: bot.id } })).json()).output.routines
  const mine = list.find((r: { title: string }) => r.title === '整理新聞給我')
  const hook = await request.post(mine.hook.path, { headers: { Authorization: `Bearer ${mine.hook.secret}`, 'X-XBot-Test': '1' }, data: 'ping' })
  expect(hook.status()).toBe(200)
  expect((await request.post(mine.hook.path, { headers: { Authorization: 'Bearer nope' }, data: 'x' })).status()).toBe(401)
  await page.reload()
  await expect(drawer.getByTestId('run-list')).toContainText('測試')
  await expect(drawer.getByTestId('hook-secret')).not.toContainText(mine.hook.secret)
  await drawer.getByRole('button', { name: '顯示' }).click()
  await expect(drawer.getByTestId('hook-secret')).toContainText(mine.hook.secret)
  await drawer.getByRole('button', { name: '隱藏' }).click()
  await ready(page)
  await page.screenshot({ path: shot(`${info.project.name}-routine-list`), fullPage: !desktop })

  // 行事曆：這週看得到排程，Bot 篩選。
  await page.goto('/calendar')
  await expect(page.getByRole('heading', { level: 1, name: '行事曆' })).toBeVisible()
  await expect(page.getByTestId('cal-event').first()).toBeVisible()
  await ready(page)
  await page.screenshot({ path: shot(`${info.project.name}-calendar`), fullPage: !desktop })
  await page.getByRole('radio', { name: '清單' }).click()
  await expect(page.getByTestId('cal-list')).toContainText('整理新聞給我')
  await page.screenshot({ path: shot(`${info.project.name}-calendar-list`), fullPage: !desktop })

  // 點空格：帶著日期時間打開一句話建立。
  if (desktop) {
    await page.getByRole('radio', { name: '週' }).click()
    await page.getByTestId('cal-day').nth(3).click({ position: { x: 20, y: 30 } })
    await expect(page.getByRole('dialog', { name: '新增定時任務' }).getByLabel('用一句話說要它定時做什麼')).toHaveValue(/月.*日 07:30/)
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})
