import { expect, test, type Locator, type Page } from './fixtures'
import { MockServer } from './mock'

// v1.0 無障礙（PRD §8「鍵盤全操作」）：只用鍵盤（Tab／Shift+Tab／Enter／空白鍵／方向鍵／Esc）走過核心流程：
// 跳過側欄、建 Bot、對話、核准、接手、設定。用假服務，桌面尺寸。

test.skip(({ isMobile }) => isMobile, '鍵盤流程在桌面跑')
test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 960 })
})

/** 一直按 Tab（或 Shift+Tab）直到 target 拿到焦點；按太多次還沒到就失敗。 */
async function tabTo(page: Page, target: Locator, { back = false, max = 80 } = {}) {
  for (let i = 0; i < max; i++) {
    if (await target.evaluate((el) => el === document.activeElement).catch(() => false)) return
    await page.keyboard.press(back ? 'Shift+Tab' : 'Tab')
  }
  throw new Error(`按了 ${max} 次 Tab 還到不了 ${target}`)
}

/** 目前有焦點的元素看得到焦點框（:focus-visible 有外框）。 */
async function expectFocusRing(page: Page) {
  const outline = await page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null
    if (!el || el === document.body) return 'none'
    const cs = getComputedStyle(el)
    return el.matches(':focus-visible') ? `${cs.outlineStyle} ${cs.outlineWidth}` : 'not-focus-visible'
  })
  expect(outline).toMatch(/^solid [1-9]/)
}

function seed(api: MockServer) {
  const bot = api.addBot({ name: '小秘', title: '幕僚長' })
  const th = api.addThread(bot.id, '比價')
  api.addMessage(th.id, { sender: 'owner', content: '幫我比較三家的價格' }, Date.now() - 60_000)
  api.addMessage(th.id, { sender: `bot:${bot.id}`, content: '好的，我先去看看。' }, Date.now() - 50_000)
  api.inbox = [
    {
      id: 'ap',
      kind: 'approval',
      group: 'decide',
      bot_id: bot.id,
      ref: 'ap1',
      title: 'Claude Code 想要：寄出對帳單',
      body: '要允許嗎？按允許才會執行。',
      payload: { op: 'xbot.mail.send' },
      status: 'open',
      read: false,
      created_at: Date.now() - 60_000,
    },
  ]
  api.override['approvals.decide'] = (i) => {
    api.inbox = []
    return { approval: { id: 'ap1', op: 'xbot.mail.send', summary: '', params: {}, requested_by: 'agent:cc', status: i.decision === 'allow' ? 'approved' : 'denied', created_at: 1 }, settled: true, executed: i.decision === 'allow', failed: false }
  }
  return { bot, th }
}

test('跳到主要內容：第一個 Tab 就是它，按下焦點進到頁面內容', async ({ page }) => {
  const api = new MockServer(page)
  seed(api)
  await api.install()
  await page.goto('/')
  await expect(page.getByText('Claude Code 想要：寄出對帳單')).toBeVisible()
  await page.keyboard.press('Tab')
  const skip = page.getByRole('link', { name: '跳到主要內容' })
  await expect(skip).toBeFocused()
  await expect(skip).toBeVisible() // 聚焦時看得到
  await expectFocusRing(page)
  await page.keyboard.press('Enter')
  await expect(page.locator('main').first()).toBeFocused()
  // 再按 Tab 就在主要內容裡，不用再經過整個側欄
  await page.keyboard.press('Tab')
  expect(await page.evaluate(() => !!document.activeElement?.closest('main'))).toBe(true)
})

test('建 Bot：Tab 到側欄的＋ → Enter 打開選單 → Enter 選「新增 Bot」→ 打名字 → Enter 建立；Esc 關掉對話框時焦點回到＋', async ({ page }) => {
  const api = new MockServer(page)
  seed(api)
  await api.install()
  await page.goto('/')
  const nav = page.getByRole('navigation', { name: '側欄' })
  const open = nav.getByRole('button', { name: '新增', exact: true })
  await tabTo(page, open)
  await expectFocusRing(page)
  // 先試 Esc：對話框關掉，焦點回到原本的按鈕
  await page.keyboard.press('Enter')
  await expect(page.getByRole('menuitem', { name: '新增 Bot' })).toBeFocused()
  await page.keyboard.press('Enter')
  const dlg = page.getByRole('dialog', { name: '新增 Bot' })
  await expect(dlg).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dlg).toBeHidden()
  await expect(open).toBeFocused()
  // 再開一次，真的建
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter')
  await expect(dlg).toBeVisible()
  const name = dlg.getByLabel('名字')
  await tabTo(page, name)
  await page.keyboard.type('鍵盤建的')
  // Tab 在對話框裡循環，不會跑到後面的頁面
  for (let i = 0; i < 30; i++) {
    await page.keyboard.press('Tab')
    expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true)
  }
  await tabTo(page, dlg.getByRole('button', { name: /建立/ }))
  await page.keyboard.press('Enter')
  await expect.poll(() => api.calls.find((c) => c.op === 'bots.create')?.input).toMatchObject({ name: '鍵盤建的' })
})

test('對話：Tab 到 Bot → Enter 打開 → 輸入框打字 → Enter 送出', async ({ page }) => {
  const api = new MockServer(page)
  const { bot } = seed(api)
  await api.install()
  await page.goto('/')
  const link = page.getByRole('navigation', { name: '側欄' }).getByRole('link', { name: /小秘/ }).first()
  await tabTo(page, link)
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/bots/${bot.id}`))
  const box = page.getByRole('textbox', { name: /交代小秘|補充說明給小秘/ })
  await tabTo(page, box)
  await page.keyboard.type('用鍵盤送的訊息')
  await page.keyboard.press('Enter')
  await expect.poll(() => api.calls.find((c) => c.op === 'chat.send')?.input).toMatchObject({ bot_id: bot.id, text: '用鍵盤送的訊息' })
})

test('核准：待我處理的卡用 Tab 到「允許」，Enter 就執行', async ({ page }) => {
  const api = new MockServer(page)
  seed(api)
  await api.install()
  await page.goto('/')
  const card = page.getByTestId('inbox-item').filter({ hasText: '寄出對帳單' })
  await expect(card).toBeVisible()
  const allow = card.getByRole('button', { name: /^允許/ }).first()
  await tabTo(page, allow)
  await expectFocusRing(page)
  await page.keyboard.press('Enter')
  await expect.poll(() => api.calls.find((c) => c.op === 'approvals.decide')?.input).toMatchObject({ decision: 'allow' })
})

test('接手：對話頁打開電腦面板、Tab 到「接手操作」，Enter 接手', async ({ page }) => {
  const api = new MockServer(page)
  const { bot, th } = seed(api)
  const screen: Record<string, unknown> = {
    bot_id: bot.id, name: bot.name, host_id: 'local', host_name: '這台主機', box: 'running', host_status: 'online', working: true, paused: false,
    last_active_at: Date.now() - 60_000, viewers: 1, you: false, vnc: `/api/v1/computers/vnc?bot=${bot.id}`,
  }
  Object.assign(api.override, {
    'computers.screen': () => screen,
    'computers.screens': () => ({ screens: [screen] }),
    'computers.takeover': () => Object.assign(screen, { working: false, paused: true, you: true, takeover: { by: 'owner', by_name: '你', since: Date.now(), task_id: 'task-x' } }),
  })
  await api.install()
  await page.goto(`/bots/${bot.id}/threads/${th.id}`)
  const toggle = page.getByRole('button', { name: '小秘的電腦' })
  await tabTo(page, toggle)
  await page.keyboard.press('Enter')
  const panel = page.getByTestId('computer-panel')
  await expect(panel).toBeVisible()
  const take = panel.getByRole('button', { name: '接手操作' }).first()
  await tabTo(page, take)
  await expectFocusRing(page)
  await page.keyboard.press('Enter')
  await expect.poll(() => api.calls.some((c) => c.op === 'computers.takeover')).toBe(true)
  await expect(panel.getByRole('button', { name: '完成，交還給小秘' })).toBeVisible()
})

test('設定：Tab 到左下角「設定與更多」→ Enter → Enter 選「設定」；分類用 Tab 走、主題用方向鍵換成淺色', async ({ page }) => {
  const api = new MockServer(page)
  seed(api)
  api.override['settings.get'] = () => ({ name: '', value: null })
  await api.install()
  await page.goto('/')
  const settings = page.getByRole('navigation', { name: '側欄' }).getByRole('button', { name: '設定與更多' })
  await tabTo(page, settings)
  await page.keyboard.press('Enter')
  await expect(page.getByRole('menuitem', { name: '設定' })).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/settings/)
  const general = page.getByRole('navigation', { name: '設定分類' }).getByRole('link', { name: '一般' })
  await tabTo(page, general)
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/settings\/general/)
  const theme = page.getByRole('radiogroup', { name: /主題|外觀/ })
  const checked = theme.getByRole('radio', { checked: true })
  await tabTo(page, checked)
  await expectFocusRing(page)
  // 方向鍵在選項間移動，直到選到淺色
  for (let i = 0; i < 4 && (await page.evaluate(() => document.documentElement.getAttribute('data-theme'))) !== 'light'; i++) await page.keyboard.press('ArrowRight')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
})
