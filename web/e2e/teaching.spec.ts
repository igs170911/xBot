import { expect, test } from '@playwright/test'
import { MockServer, opError, settle, type Bot } from './mock'
import { fakeVnc, loadFrame } from './rfb'

// B32 示範教學：開始示範（E05）→ 教學中（E04：教學列、即時步驟、補充、暫停、刪步驟）→ 草稿確認 → 存成技能、試做。
// 假服務＋假的 RFB 伺服器，截圖和 design/E04、E05 畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

type Lesson = Record<string, unknown> & { id: string; state: string; steps: Array<Record<string, unknown>>; goal: string }

function screenOf(bot: Bot, over: Record<string, unknown> = {}) {
  return {
    bot_id: bot.id, name: bot.name, host_id: 'local', host_name: '這台主機', box: 'running', host_status: 'online', working: false, paused: false,
    last_active_at: Date.now(), viewers: 1, you: false, vnc: `/api/v1/computers/vnc?bot=${bot.id}`, ...over,
  }
}

// 畫板 E04 的五步（抓蟲學「重現結帳運費歸零」）。
const STEPS = [
  { id: 'b1n', source: 'browser', kind: 'navigate', text: '打開測試站的「經典帆布托特包」商品頁', shot: 'shots/b-0001.jpg', at: 1 },
  { id: 'b2c', source: 'browser', kind: 'click', text: '點「加入購物車」，數量 1', shot: 'shots/b-0002.jpg', mark: [0.62, 0.55, 0.2, 0.08], at: 2 },
  { id: 'b3i', source: 'browser', kind: 'input', text: '在結帳頁輸入折扣碼 FALL10，按「套用」', shot: 'shots/b-0003.jpg', mark: [0.05, 0.6, 0.55, 0.07], at: 3 },
  { id: 'u4n', source: 'user', kind: 'note', text: '使用者補充：運費應該是 NT$60…', value: '運費應該是 NT$60，滿 NT$990 才免運；這裡變成 NT$0 就是 Bug', at: 4 },
  { id: 'b5c', source: 'browser', kind: 'click', text: '框出訂單摘要的「運費 NT$0」', shot: 'shots/b-0004.jpg', mark: [0.7, 0.42, 0.28, 0.06], at: 5 },
]

const DRAFT = {
  name: '重現結帳運費歸零',
  description: '要「重現結帳運費歸零」時使用（從你的示範學來，5 步）',
  instructions:
    '## 目的\n重現結帳運費歸零\n\n## 步驟\n1. 打開測試站的「經典帆布托特包」商品頁\n   - 網址：https://staging.chenguang-shop.tw/p/1\n   - 截圖：shots/b-0001.jpg\n' +
    '2. 點「加入購物車」，數量 1\n   - 定位：`#add-to-cart`\n   - 截圖：shots/b-0002.jpg\n3. 在結帳頁輸入折扣碼 FALL10，按「套用」\n   - 定位：`input[name="coupon"]`\n' +
    '   - 使用者補充：運費應該是 NT$60，滿 NT$990 才免運；這裡變成 NT$0 就是 Bug\n4. 框出訂單摘要的「運費 NT$0」\n   - 截圖：shots/b-0004.jpg\n\n## 注意\n- 示範裡的密碼只記成 ••••。\n',
  shots: ['shots/b-0001.jpg', 'shots/b-0002.jpg', 'shots/b-0004.jpg'],
  by: 'rules',
}

function wire(api: MockServer, bot: Bot, jpeg: string) {
  const screen = screenOf(bot)
  let lesson: Lesson | null = null
  Object.assign(api.override, {
    'computers.screen': () => screen,
    'computers.screens': () => ({ screens: [screen] }),
    'teaching.get': () => ({ lesson }),
    'teaching.start': (i: Record<string, unknown>) => {
      Object.assign(screen, { paused: true, you: true, takeover: { by: 'owner', by_name: '你', since: Date.now() } })
      lesson = { id: 'L1', bot_id: i.bot_id, goal: '', state: 'recording', started_at: Date.now(), steps: [], thread_id: i.thread_id }
      return lesson
    },
    'teaching.pause': (i: Record<string, unknown>) => Object.assign(lesson!, { state: i.paused ? 'paused' : 'recording' }),
    'teaching.note': (i: Record<string, unknown>) => {
      lesson!.steps = [...lesson!.steps, { id: 'u9n', source: 'user', kind: 'note', text: '使用者補充：' + i.text, value: i.text, at: 9 }]
      return lesson
    },
    'teaching.drop': (i: Record<string, unknown>) => {
      lesson!.steps = lesson!.steps.filter((s) => s.id !== i.step_id)
      return lesson
    },
    'teaching.stop': () => {
      delete (screen as Record<string, unknown>).takeover
      Object.assign(screen, { paused: false, you: false })
      return Object.assign(lesson!, { state: 'stopped', goal: '重現結帳運費歸零', draft: DRAFT })
    },
    'teaching.shot': () => ({ image: jpeg, media_type: 'image/jpeg' }),
    'teaching.save': (i: Record<string, unknown>) => {
      if (i.name === '已經有了') opError(409, 'conflict', '技能已經存在：已經有了')
      const th = api.addThread(bot.id, '試做：' + i.name)
      lesson = null
      return { lesson: { id: 'L1', state: 'saved' }, skill: { id: 'skill-abc', name: i.name, description: i.description, place: 'list', enabled: true, source: 'taught' }, task_id: 'task-trial', thread_id: th.id }
    },
    'teaching.discard': () => {
      lesson = null
      return { id: 'L1', state: 'discarded', steps: [] }
    },
  })
  return { set: (l: Lesson | null) => (lesson = l), get: () => lesson }
}

test('教它一件事（E05→E04）：開始示範、即時步驟、補充、暫停、刪步驟、完成整理成草稿、存成技能並試做', async ({ page, context }, info) => {
  test.skip(info.project.name !== 'desktop', '電腦面板只在寬螢幕；手機另測')
  const api = new MockServer(page)
  const bot = api.addBot({ name: '抓蟲', shape: 'shield', color: 'red', title: '測試' })
  const th = api.addThread(bot.id, '結帳運費')
  api.addMessage(th.id, { sender: 'owner', content: '結帳頁套用折扣碼之後，運費好像會變成 0。我示範一次給你看，以後你自己檢查。' }, Date.now() - 3 * 60_000)
  api.addMessage(th.id, { sender: 'bot:' + bot.id, content: '好，我先在電腦上打開測試站。你按「開始示範」後直接操作就可以，不用解說每一步。' }, Date.now() - 2 * 60_000)
  const frame = await loadFrame(context, 'desktop.jpg')
  const lesson = wire(api, bot, frame.jpeg)
  await api.install()
  await fakeVnc(page, () => frame)

  await page.goto(`/bots/${bot.id}/threads/${th.id}`)
  await page.getByRole('button', { name: '更多' }).click()
  await page.getByRole('menuitem', { name: '教它一件事' }).click()
  const dialog = page.getByRole('dialog', { name: /教抓蟲一件事/ })
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('密碼欄位只會記成 ••••')
  await expect(dialog).toContainText('簡化：3 個欄位 → 1 步')
  await settle(page)
  await dialog.screenshot({ path: shot('desktop-e05-teach-start') })

  await dialog.getByRole('button', { name: '開始示範' }).click()
  await expect(dialog).toBeHidden()
  expect(api.calls.find((c) => c.op === 'teaching.start')?.input).toEqual({ bot_id: bot.id, thread_id: th.id })
  const panel = page.getByTestId('computer-panel')
  const bar = panel.getByTestId('teach-bar')
  await expect(bar).toContainText('教學中 · 第 0 步')
  await expect(page.getByTestId('teaching-pill')).toHaveText('教學中')
  await expect(panel.getByTestId('rec-label')).toHaveText('錄製中 · 密碼只會記成 ••••')
  // 交還的按鈕不出現：完成示範就是交還。
  await expect(panel.getByRole('button', { name: /完成，交還/ })).toHaveCount(0)

  // 錄製器記到的步驟經 teaching.updated 事件即時出現。
  const live = { ...lesson.get()!, goal: '重現結帳運費的問題', steps: STEPS }
  lesson.set(live)
  await api.emit({ kind: 'teaching.updated', bot_id: bot.id, payload: { bot_id: bot.id, lesson: live }, persist: false })
  await expect(bar).toContainText('教學中 · 第 5 步')
  await expect(bar.getByTestId('teach-step')).toHaveCount(5)
  await expect(bar.getByTestId('teach-step').nth(3)).toContainText('「運費應該是 NT$60')
  await expect(bar.getByTestId('teach-step').nth(4).getByLabel('記錄中')).toBeVisible()
  await expect(bar.getByTestId('shot-thumb').first().locator('img')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot('desktop-e04-teaching') })

  // 暫停 → 繼續。
  await bar.getByRole('button', { name: '暫停' }).click()
  await expect(bar).toContainText('已暫停 · 第 5 步')
  await expect(panel.getByTestId('rec-label')).toHaveText('已暫停錄製')
  await settle(page)
  await bar.screenshot({ path: shot('desktop-e04-paused') })
  await bar.getByRole('button', { name: '繼續' }).click()
  await expect(bar).toContainText('教學中')

  // 補充說明、刪掉做錯的一步（編號重排）。
  await bar.getByRole('textbox', { name: '補充說明' }).fill('每次都先清空購物車')
  await bar.getByRole('button', { name: '加入' }).click()
  await expect(bar.getByTestId('teach-step')).toHaveCount(6)
  expect(api.calls.find((c) => c.op === 'teaching.note')?.input).toEqual({ bot_id: bot.id, lesson_id: 'L1', text: '每次都先清空購物車' })
  await bar.getByRole('button', { name: '刪掉第 2 步' }).click()
  await expect(bar.getByTestId('teach-step')).toHaveCount(5)
  expect(api.calls.find((c) => c.op === 'teaching.drop')?.input).toEqual({ bot_id: bot.id, lesson_id: 'L1', step_id: 'b2c' })

  // 完成 → 草稿（E04「整理技能中」之後）。
  await bar.getByRole('button', { name: '完成，請它學起來' }).click()
  const draft = panel.getByTestId('teach-draft')
  await expect(draft).toBeVisible()
  await api.emit({ kind: 'computer.screen', bot_id: bot.id, payload: {} })
  await expect(panel.getByRole('button', { name: /完成，交還/ })).toHaveCount(0)
  await expect(draft.getByRole('textbox', { name: '技能名稱' })).toHaveValue('重現結帳運費歸零')
  await expect(draft.getByRole('checkbox', { name: '存好後請抓蟲試做一次' })).toBeChecked()
  await expect(draft.getByTestId('shot-thumb')).toHaveCount(3)
  await expect(page.getByTestId('teaching-pill')).toHaveCount(0)
  await settle(page)
  await panel.screenshot({ path: shot('desktop-e04-draft') })

  // 同名技能：留在草稿，顯示原因。
  await draft.getByRole('textbox', { name: '技能名稱' }).fill('已經有了')
  await draft.getByRole('button', { name: '存成技能' }).click()
  await expect(draft.getByRole('alert')).toContainText('技能已經存在')
  // 改內文（刪掉一張截圖的那行）後存檔：預設試做，跳到試做的對話串。
  await draft.getByRole('textbox', { name: '技能名稱' }).fill('檢查結帳運費')
  const text = DRAFT.instructions.replace('   - 截圖：shots/b-0004.jpg\n', '')
  await draft.getByRole('textbox', { name: '怎麼做（可以直接改）' }).fill(text)
  await expect(draft.getByTestId('shot-thumb')).toHaveCount(2)
  await draft.getByRole('button', { name: '存成技能' }).click()
  await expect(page.getByText('抓蟲學會了「檢查結帳運費」，正在試做一次')).toBeVisible()
  const save = api.calls.filter((c) => c.op === 'teaching.save').at(-1)?.input
  expect(save).toMatchObject({ bot_id: bot.id, lesson_id: 'L1', name: '檢查結帳運費', instructions: text, skip_trial: false })
  await expect(page).toHaveURL(/\/threads\/th\d+$/)
  await expect(panel.getByTestId('teach-draft')).toHaveCount(0)
})

test('教它一件事（手機）：電腦頁開始、提示用電腦教比較方便；取消示範', async ({ page, context }, info) => {
  test.skip(info.project.name !== 'mobile', '手機版')
  const api = new MockServer(page)
  const bot = api.addBot({ name: '抓蟲', shape: 'shield', color: 'red' })
  const frame = await loadFrame(context, 'desktop.jpg')
  const lesson = wire(api, bot, frame.jpeg)
  await api.install()
  await fakeVnc(page, () => frame)

  await page.goto(`/bots/${bot.id}/computer`)
  await page.getByRole('button', { name: '教它一件事' }).click()
  const dialog = page.getByRole('dialog', { name: /教抓蟲一件事/ })
  await expect(dialog).toContainText('在電腦上教比較方便')
  await settle(page)
  await page.screenshot({ path: shot('mobile-e05-teach-start') })
  await dialog.getByRole('button', { name: '開始示範' }).click()
  const bar = page.getByTestId('teach-bar')
  await expect(bar).toContainText('教學中 · 第 0 步')
  lesson.set({ ...lesson.get()!, steps: STEPS.slice(0, 3) })
  await api.emit({ kind: 'teaching.updated', bot_id: bot.id, payload: { bot_id: bot.id, lesson: lesson.get() }, persist: false })
  await expect(bar.getByTestId('teach-step')).toHaveCount(3)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await settle(page)
  await page.screenshot({ path: shot('mobile-e04-teaching') })

  await bar.getByRole('button', { name: '取消這次示範' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '取消示範' }).click()
  await expect(bar).toHaveCount(0)
  expect(api.calls.some((c) => c.op === 'teaching.discard')).toBe(true)
})
