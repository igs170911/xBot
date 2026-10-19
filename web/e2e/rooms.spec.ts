import { expect, test, type Page } from './fixtures'
import { MockServer, settle } from './mock'

// X02：群組（D01 群組對話、D03 新增群組、D04 開場建議）與兩個 Bot 的往來（C13）。
// 截圖存在 e2e-results/screens，和 design/ 的畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

async function team(page: Page) {
  const m = new MockServer(page)
  const chief = m.addBot({ name: '小秘', title: '幕僚長', color: 'blue', shape: 'cursor' })
  const zhe = m.addBot({ name: '阿哲', title: '選品研究員', color: 'orange', shape: 'blob' })
  const bian = m.addBot({ name: '小編', title: '社群小編', color: 'pink', shape: 'drop' })
  m.addBot({ name: '帳房', title: '費用管理', color: 'yellow', shape: 'hexagon' })
  await m.install()
  return { m, chief, zhe, bian }
}

test('新增群組（D03）：勾 2 個以上才能建立，建好進到開場建議（D04）', async ({ page }, info) => {
  const { m } = await team(page)
  await page.goto('/')
  if (info.project.name === 'mobile') {
    await page.goto('/chats')
    await page.getByRole('button', { name: '打開側欄' }).click()
  }
  await page.getByRole('navigation', { name: '側欄' }).getByRole('button', { name: '新增群組' }).click()
  const dialog = page.getByRole('dialog', { name: '新增群組對話' })
  await expect(dialog).toBeVisible()
  const create = dialog.getByRole('button', { name: /建立群組/ })
  await dialog.getByRole('checkbox', { name: /小秘/ }).click()
  await expect(create).toBeDisabled()
  await expect(dialog.getByText('至少要 2 個 Bot 才能開群組')).toBeVisible()
  await dialog.getByRole('checkbox', { name: /阿哲/ }).click()
  await dialog.getByRole('checkbox', { name: /小編/ }).click()
  await expect(create).toHaveText('建立群組（3 位）')
  await expect(dialog.getByTestId('room-name-preview')).toHaveText('名稱：小秘、阿哲、小編')
  await dialog.getByRole('button', { name: '進階設定' }).click()
  await expect(dialog.getByRole('radio', { name: /自動挑一位/ })).toHaveAttribute('aria-checked', 'true')
  await settle(page)
  await dialog.screenshot({ path: shot(`${info.project.name}-D03-room-dialog`) })
  await create.click()
  const call = m.calls.find((c) => c.op === 'rooms.create')
  expect(call?.input).toMatchObject({ responder_rule: 'auto' })
  expect((call?.input.member_ids as string[]).length).toBe(3)
  await expect(page.getByRole('heading', { name: '小秘、阿哲、小編 都在這裡' })).toBeVisible()
  // 開場建議點了只帶進輸入框，不直接送出。
  await page.getByRole('button', { name: /各自用一句話/ }).click()
  await expect(page.getByRole('textbox', { name: '傳訊息到群組' })).toHaveValue(/@所有人/)
  expect(m.calls.some((c) => c.op === 'rooms.post')).toBe(false)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-D04-room-setup`) })
})

test('群組對話（D01）：交棒分隔線、輪數上限、成員狀態，你的發言送出', async ({ page }, info) => {
  const { m, chief, zhe, bian } = await team(page)
  const room = m.addRoom([chief.id, zhe.id, bian.id], '新品上架專案', 'auto', { [chief.id]: 'working', [bian.id]: 'waiting' })
  room.bulletin = '新品 10/20 上架。對外文案先給小秘看過；任何價格調整一律要我核准。'
  m.addRoomMessage(room.id, { sender: 'owner', content: '@阿哲 抓三家競品主力商品的價格，附上來源。@小編 寫新品貼文三版。', mentions: [zhe.id, bian.id], picked: [zhe.id, bian.id] })
  m.addRoomMessage(room.id, { sender: 'bot:' + zhe.id, speaker_name: '阿哲', content: '三家共 36 項主力商品已整理好，平均比我們便宜 7.8%。表格在 `shared/競品價格.xlsx`。', round: 0 })
  m.addRoomMessage(room.id, { sender: 'bot:' + bian.id, speaker_name: '小編', content: '三版寫好了：溫暖、俏皮、簡潔。@小秘 你比較懂這季主打，幫我挑一版？', mentions: [chief.id], round: 0, handoff: 1 })
  m.addRoomMessage(room.id, { sender: 'bot:' + chief.id, speaker_name: '小秘', content: '選俏皮那版。@小編 改一下標題，@阿哲 再補一家。', mentions: [bian.id, zhe.id], round: 1, handoff: 2 })
  m.addRoomMessage(room.id, { sender: 'bot:' + bian.id, speaker_name: '小編', content: '標題改好了。@小秘 再看一次', mentions: [chief.id], round: 4, capped: true })
  m.addRoomMessage(room.id, { sender: 'system', content: '這段群組討論已經來回 4 輪了，訊息照發但不再喚醒別人；需要的話請你接手。', notice: 'capped' })
  await page.goto(`/rooms/${room.id}`)
  await expect(page.getByTestId('room-status')).toHaveText('小秘 正在回覆…')
  await expect(page.getByText('第 1/4 輪 · 小編 → 小秘')).toBeVisible()
  await expect(page.getByText('第 2/4 輪 · 小秘 → 小編、阿哲')).toBeVisible()
  await expect(page.getByTestId('room-notice')).toContainText('已經來回 4 輪')
  await expect(page.getByText('小編 在自己的對話等你')).toBeVisible()
  await expect(page.getByRole('link', { name: '前往' })).toHaveAttribute('href', `/bots/${bian.id}/threads/seat-${bian.id}`)
  if (info.project.name !== 'mobile') await expect(page.getByRole('combobox', { name: '回應規則' })).toHaveValue('auto')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-D01-room`), fullPage: info.project.name === 'mobile' })
  if (info.project.name === 'mobile') {
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  }
  // 點名膠囊把 @名字 填進輸入框；送出。
  await page.getByRole('button', { name: '阿哲' }).first().click()
  const box = page.getByRole('textbox', { name: '傳訊息到群組' })
  await expect(box).toHaveValue('@阿哲 ')
  await box.fill('@阿哲 先停，改查 B 公司')
  await box.press('Enter')
  await expect.poll(() => m.calls.find((c) => c.op === 'rooms.post')?.input).toMatchObject({ room_id: room.id, text: '@阿哲 先停，改查 B 公司' })
  await expect(page.getByTestId('room-owner').last()).toHaveText('@阿哲 先停，改查 B 公司')
})

test('兩個 Bot 的往來（C13）：對話裡的往來行，打開看整條', async ({ page }, info) => {
  const { m, chief, zhe } = await team(page)
  const th = m.addThread(chief.id, '比價')
  m.addMessage(th.id, { sender: 'owner', content: '幫我比三家價格', task_id: 't1' })
  m.addMessage(th.id, { sender: 'bot:' + chief.id, kind: 'peer', content: '請抓蝦皮上三家競品的主力保溫瓶價格，做成 CSV。', peer: { event: 'message_out', peer_id: zhe.id, peer_name: '阿哲', intent: 'request', hop: 1, chain: 'c1' } })
  m.addMessage(th.id, { sender: 'bot:' + zhe.id, kind: 'peer', content: '完成。三家共 36 項商品，平均比我們便宜約 8%，價差表在 /workspace/from/阿哲/競品價格.csv', peer: { event: 'message_in', peer_id: zhe.id, peer_name: '阿哲', intent: 'result', hop: 2, chain: 'c1', files: 1 } })
  m.addMessage(th.id, { sender: 'bot:' + chief.id, content: '[silent]', silent: true })
  const now = Date.now()
  m.peerHistory = {
    a: chief.id,
    a_name: '小秘',
    b: zhe.id,
    b_name: '阿哲',
    max_hops: 6,
    hops_used: 3,
    chain_id: 'c1',
    open: true,
    messages: [
      { id: 'p1', from: chief.id, from_name: '小秘', to: zhe.id, to_name: '阿哲', intent: 'fyi', text: '明天開始我會每天 09:00 整理一次每日重點。', hop: 1, chain_id: 'c0', files: [], answered: true, created_at: now - 86_400_000 },
      { id: 'p2', from: chief.id, from_name: '小秘', to: zhe.id, to_name: '阿哲', intent: 'request', text: '請抓蝦皮上三家競品的主力保溫瓶價格，做成 CSV。', hop: 1, chain_id: 'c1', files: [], answered: false, deferred: true, created_at: now - 600_000 },
      { id: 'p3', from: zhe.id, from_name: '阿哲', to: chief.id, to_name: '小秘', intent: 'question', text: '第三家有兩個規格都叫 500ml，一個是不鏽鋼一個是陶瓷，都要算嗎？', hop: 2, chain_id: 'c1', files: [], answered: true, created_at: now - 500_000 },
      { id: 'p4', from: chief.id, from_name: '小秘', to: zhe.id, to_name: '阿哲', intent: 'result', text: '只算不鏽鋼。', hop: 3, chain_id: 'c1', files: [], answered: true, created_at: now - 400_000 },
      { id: 'p5', from: zhe.id, from_name: '阿哲', to: chief.id, to_name: '小秘', intent: 'result', text: '完成。三家共 36 項商品…\n\n…（完整回覆共 18240 字，太長放不進訊息，已存成檔案：/workspace/from/阿哲/result-20261018-141100.md）', hop: 4, chain_id: 'c1', files: ['/workspace/from/阿哲/競品價格.csv'], answered: true, created_at: now - 100_000 },
    ],
  }
  await page.goto(`/bots/${chief.id}/threads/${th.id}`)
  const lines = page.getByTestId('peer-line')
  await expect(lines).toHaveCount(2)
  await expect(lines.first()).toContainText('已傳訊息給 阿哲')
  await expect(lines.first()).toContainText('第 1 次')
  await expect(lines.last()).toContainText('來自 阿哲 的回覆')
  await expect(page.getByText('[silent]')).toHaveCount(0)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-C14-peer-lines`) })
  await lines.first().getByRole('button', { name: /看 小秘 ⇄ 阿哲 的對話/ }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('這條往來 3 / 6 次，到 6 次會先停下來回報你')
  await expect(dialog).toContainText('回覆不算次數')
  await expect(dialog).toContainText('太長放不進訊息')
  await expect(dialog).toContainText('等結果回來才一起回覆')
  await settle(page)
  await dialog.screenshot({ path: shot(`${info.project.name}-C13-peer-dialog`) })
  await dialog.getByRole('button', { name: '停止這條往來' }).click()
  await expect.poll(() => m.calls.find((c) => c.op === 'collab.stop_chain')?.input).toEqual({ chain_id: 'c1' })
})

test('側欄右鍵：編輯群組、刪除群組、刪除 Bot（都要先確認）', async ({ page }, info) => {
  test.skip(info.project.name === 'mobile', '手機用長按，桌面測右鍵就好')
  const { m, chief, zhe, bian } = await team(page)
  m.addRoom([chief.id, zhe.id], '上架小組', 'auto')
  await page.goto('/')
  const nav = page.getByRole('navigation', { name: '側欄' })
  await nav.getByRole('link', { name: /上架小組/ }).click({ button: 'right' })
  const menu = page.getByRole('menu', { name: '上架小組' })
  await expect(menu).toBeVisible()
  await menu.getByRole('menuitem', { name: '編輯群組' }).click()
  const dialog = page.getByRole('dialog', { name: '編輯群組' })
  await dialog.getByRole('textbox', { name: '名稱' }).fill('發表會小組')
  await dialog.getByRole('checkbox', { name: /小編/ }).click()
  await dialog.getByRole('button', { name: '儲存' }).click()
  await expect(dialog).toBeHidden()
  expect(m.calls.find((c) => c.op === 'rooms.update')?.input).toMatchObject({ name: '發表會小組', member_ids: [chief.id, zhe.id, bian.id] })

  await nav.getByRole('link', { name: /發表會小組/ }).click({ button: 'right' })
  await page.getByRole('menu', { name: '發表會小組' }).getByRole('menuitem', { name: '刪除群組' }).click()
  const confirm = page.getByRole('alertdialog', { name: '刪除群組「發表會小組」？' })
  await confirm.getByRole('button', { name: '取消' }).click()
  expect(m.calls.some((c) => c.op === 'rooms.delete')).toBe(false)
  await nav.getByRole('link', { name: /發表會小組/ }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: '刪除群組' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '刪除' }).click()
  await expect(nav.getByRole('link', { name: /發表會小組/ })).toHaveCount(0)

  await nav.getByRole('link', { name: '帳房' }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: '刪除 Bot' }).click()
  await page.getByRole('alertdialog', { name: '刪除 Bot「帳房」？' }).getByRole('button', { name: '刪除' }).click()
  await expect(nav.getByRole('link', { name: '帳房' })).toHaveCount(0)
  expect(m.calls.find((c) => c.op === 'bots.delete')?.input).toMatchObject({ id: expect.any(String) })
})

test('手機長按群組：叫出同一個選單，放開不會進群組', async ({ page }, info) => {
  test.skip(info.project.name !== 'mobile', '桌面用右鍵')
  const { m, chief, zhe } = await team(page)
  m.addRoom([chief.id, zhe.id], '上架小組', 'auto')
  await page.goto('/chats')
  const longPress = async (row: import('@playwright/test').Locator) => {
    const touch = (type: string) =>
    row.evaluate((el, type) => {
      const r = el.getBoundingClientRect()
      const t = { clientX: r.left + 20, clientY: r.top + 10, identifier: 1, target: el }
      const e = new Event(type, { bubbles: true, cancelable: true })
      Object.assign(e, { touches: type === 'touchend' ? [] : [t], changedTouches: [t], targetTouches: type === 'touchend' ? [] : [t] })
      el.dispatchEvent(e)
    }, type)
    await touch('touchstart')
    await page.waitForTimeout(650)
    await touch('touchend')
    await row.dispatchEvent('click')
  }
  // 「對話」分頁的列
  await longPress(page.getByRole('main', { name: '對話' }).getByRole('link', { name: /上架小組/ }))
  await expect(page.getByRole('menu', { name: '上架小組' }).getByRole('menuitem', { name: '刪除群組' })).toBeVisible()
  await expect(page).toHaveURL(/\/chats$/)
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu')).toHaveCount(0)
  await longPress(page.getByRole('main', { name: '對話' }).getByRole('link', { name: /帳房/ }))
  await expect(page.getByRole('menu', { name: '帳房' }).getByRole('menuitem', { name: '刪除 Bot' })).toBeVisible()
  await page.keyboard.press('Escape')
  // 側欄的列
  await page.getByRole('button', { name: '打開側欄' }).click()
  await longPress(page.getByRole('navigation', { name: '側欄' }).getByRole('link', { name: /上架小組/ }))
  await expect(page.getByRole('menu', { name: '上架小組' }).getByRole('menuitem', { name: '編輯群組' })).toBeVisible()
  await expect(page).toHaveURL(/\/chats$/)
})
